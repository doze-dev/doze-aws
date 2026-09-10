// Package logship carries log events from a service to the logs service off
// the caller's path: Step Functions history, API Gateway access and
// execution logs, and EventBridge deliveries to a log group all go through
// one Shipper. Events are batched per stream and sent from a worker
// goroutine, so a slow or absent logs peer costs the caller nothing; when
// the logs service is not enabled the shipper says so once and drops the
// rest, which is what "logging off" looks like locally.
package logship

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// Event is one log line: peercall.LogEvent under a shorter name.
type Event = peercall.LogEvent

// flushAfter bounds how long an event waits before it is shipped.
const flushAfter = 250 * time.Millisecond

type key struct{ group, stream string }

// Shipper batches and ships events for one producer.
type Shipper struct {
	name  string // the producer, for the one line said when logs is absent
	peers peers.Directory
	logf  func(string, ...any)

	mu       sync.Mutex
	pending  map[key][]Event
	ensured  map[key]bool
	timer    *time.Timer
	disabled bool
	closed   bool
	work     chan map[key][]Event
	// quit tells the worker to drain and stop. The work channel is never
	// closed, because Flush runs on a timer goroutine that can be between its
	// unlock and its send at the moment Close is called — closing the channel
	// underneath it is a panic, and a flag cannot prevent it.
	quit chan struct{}
	done chan struct{}
	once sync.Once
}

// New starts a shipper. name appears in the "logs service is not enabled"
// line, which is said once per shipper.
func New(name string, dir peers.Directory, logf func(string, ...any)) *Shipper {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Shipper{
		name: name, peers: dir, logf: logf,
		pending: map[key][]Event{}, ensured: map[key]bool{},
		work: make(chan map[key][]Event, 64),
		quit: make(chan struct{}), done: make(chan struct{}),
	}
	go s.worker()
	return s
}

// Put queues events for a stream; they are shipped within flushAfter, or
// sooner on Flush. Safe from any goroutine.
func (s *Shipper) Put(group, stream string, events ...Event) {
	if len(events) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled || s.closed {
		return
	}
	k := key{group, stream}
	s.pending[k] = append(s.pending[k], events...)
	if s.timer == nil {
		s.timer = time.AfterFunc(flushAfter, s.Flush)
	}
}

// Flush hands everything pending to the worker.
func (s *Shipper) Flush() {
	s.send(s.take())
}

// take detaches what is pending and disarms the timer.
func (s *Shipper) take() map[key][]Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.pending
	s.pending = map[key][]Event{}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	return batch
}

// send queues a batch, giving up if the worker has already stopped — which is
// what a Flush racing Close does, and it costs at most the events that were
// pending at the instant of shutdown.
func (s *Shipper) send(batch map[key][]Event) {
	if len(batch) == 0 {
		return
	}
	select {
	case s.work <- batch:
	case <-s.done:
	}
}

// Close flushes and stops the worker. It waits for what was pending to be
// sent, bounded by a short deadline, so a test can read what it wrote.
func (s *Shipper) Close() {
	s.once.Do(func() {
		batch := s.take()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.send(batch)
		close(s.quit)
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
	})
}

func (s *Shipper) worker() {
	defer close(s.done)
	for {
		select {
		case batch := <-s.work:
			s.shipAll(batch)
		case <-s.quit:
			// Everything already queued still goes out: Close waits for this,
			// and a test that writes then reads back depends on it.
			for {
				select {
				case batch := <-s.work:
					s.shipAll(batch)
				default:
					return
				}
			}
		}
	}
}

func (s *Shipper) shipAll(batch map[key][]Event) {
	for k, events := range batch {
		s.ship(k, events)
	}
}

func (s *Shipper) ship(k key, events []Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !s.ensured[k] {
		if err := peercall.LogsEnsure(ctx, s.peers, k.group, k.stream); err != nil {
			s.fail(err)
			return
		}
		s.ensured[k] = true
	}
	if err := peercall.LogsPut(ctx, s.peers, k.group, k.stream, events); err != nil {
		s.fail(err)
	}
}

// fail records a shipping failure: a missing logs service turns the shipper
// off after one line; anything else is one line per failure.
func (s *Shipper) fail(err error) {
	if errors.Is(err, peercall.ErrNoLogs) {
		s.mu.Lock()
		already := s.disabled
		s.disabled = true
		s.pending = map[key][]Event{}
		s.mu.Unlock()
		if !already {
			s.logf("%s: the logs service is not enabled; log lines are dropped", s.name)
		}
		return
	}
	s.logf("%s: shipping logs: %v", s.name, err)
}
