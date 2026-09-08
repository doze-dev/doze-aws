package lambda

import (
	"time"

	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/logship"
)

// Where a function's lines go: the terminal, and the logs service.
//
// The runtime hands over one line at a time, attributed. The sink echoes it
// to the terminal and hands it to the server's shipper (internal/logship),
// which batches per stream and sends from its own goroutine — never on the
// child's stdout copier and never on the Invoke goroutine, so a slow logs
// peer costs nothing on the invocation path. Flush, which the runtime calls
// when an invocation ends, sends what is pending at once rather than on the
// shipper's safety timer. One shipper serves every function, so a missing
// logs service is said once per server.

type logSink struct {
	fn    string
	group string
	ship  *logship.Shipper
	logf  func(string, ...any)
	echo  bool
}

func newLogSink(fn string, ship *logship.Shipper, logf func(string, ...any), echo bool) *logSink {
	return &logSink{fn: fn, group: lambdaruntime.LogGroupName(fn), ship: ship, logf: logf, echo: echo}
}

// Line implements lambdaruntime.LogSink.
func (s *logSink) Line(stream, requestID string, at time.Time, line []byte) {
	if s.echo {
		s.logf("lambda[%s] %s", s.fn, line)
	}
	s.ship.Put(s.group, stream, logship.Event{Timestamp: at.UnixMilli(), Message: string(line), RequestID: requestID})
}

// Flush implements lambdaruntime.LogSink: an invocation ended, send now.
func (s *logSink) Flush() { s.ship.Flush() }

// Close sends what is pending; the shipper itself lives with the server.
func (s *logSink) Close() { s.ship.Flush() }
