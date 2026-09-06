package lambda

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// Where a function's lines go: the terminal, and the logs service.
//
// The runtime hands over one line at a time, attributed. This sink batches
// them per stream and ships a batch when the runtime says an invocation has
// finished (Flush) or, for init output and handlers that print without ever
// finishing, when the safety timer fires. Shipping happens on one worker
// goroutine — never under the lock, never on the child's stdout copier, and
// never on the Invoke goroutine — so a slow logs peer costs nothing on the
// invocation path.

// flushAfter bounds how long a line waits before it is shipped without an
// invocation ending — init output would otherwise sit until the first Flush.
const flushAfter = 250 * time.Millisecond

type logSink struct {
	fn    string
	group string
	peers peers.Directory
	logf  func(string, ...any)
	echo  bool

	mu       sync.Mutex
	pending  map[string][]peercall.LogEvent // stream → batch
	ensured  map[string]bool                // stream → group and stream exist
	timer    *time.Timer
	disabled bool // no logs peer: said once, then only the echo remains
	work     chan map[string][]peercall.LogEvent
	done     chan struct{}
	once     sync.Once
}

func newLogSink(fn string, dir peers.Directory, logf func(string, ...any), echo bool) *logSink {
	s := &logSink{
		fn: fn, group: lambdaruntime.LogGroupName(fn), peers: dir, logf: logf, echo: echo,
		pending: map[string][]peercall.LogEvent{}, ensured: map[string]bool{},
		work: make(chan map[string][]peercall.LogEvent, 64), done: make(chan struct{}),
	}
	go s.worker()
	return s
}

// Line implements lambdaruntime.LogSink.
func (s *logSink) Line(stream, requestID string, at time.Time, line []byte) {
	if s.echo {
		s.logf("lambda[%s] %s", s.fn, line)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		return
	}
	s.pending[stream] = append(s.pending[stream], peercall.LogEvent{
		Timestamp: at.UnixMilli(), Message: string(line), RequestID: requestID,
	})
	if s.timer == nil {
		s.timer = time.AfterFunc(flushAfter, s.Flush)
	}
}

// Flush implements lambdaruntime.LogSink: hand the batches to the worker.
func (s *logSink) Flush() {
	s.mu.Lock()
	batch := s.pending
	s.pending = map[string][]peercall.LogEvent{}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	select {
	case s.work <- batch:
	case <-s.done:
	}
}

func (s *logSink) worker() {
	for {
		select {
		case batch := <-s.work:
			s.ship(batch)
		case <-s.done:
			// Drain what is queued so a Close right after an invoke loses nothing.
			for {
				select {
				case batch := <-s.work:
					s.ship(batch)
				default:
					return
				}
			}
		}
	}
}

func (s *logSink) ship(batch map[string][]peercall.LogEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for stream, events := range batch {
		if !s.ensured[stream] { // worker-only field; no lock needed
			if err := peercall.LogsEnsure(ctx, s.peers, s.group, stream); err != nil {
				s.fail(err)
				return
			}
			s.ensured[stream] = true
		}
		if err := peercall.LogsPut(ctx, s.peers, s.group, stream, events); err != nil {
			s.fail(err)
			return
		}
	}
}

// fail is said once for a missing peer, every time for anything else.
func (s *logSink) fail(err error) {
	if errors.Is(err, peercall.ErrNoLogs) {
		s.mu.Lock()
		first := !s.disabled
		s.disabled = true
		s.pending = map[string][]peercall.LogEvent{}
		s.mu.Unlock()
		if first {
			s.logf("lambda[%s]: %v; function output goes to the terminal only", s.fn, err)
		}
		return
	}
	s.logf("lambda[%s]: shipping logs: %v", s.fn, err)
}

// Close ships what is pending and stops the worker.
func (s *logSink) Close() {
	s.once.Do(func() {
		s.Flush()
		close(s.done)
	})
}
