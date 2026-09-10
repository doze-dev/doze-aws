package lambda

import (
	"time"

	"github.com/doze-dev/doze-aws/internal/emf"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/logship"
	"github.com/doze-dev/doze-aws/internal/metricship"
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
	fn      string
	group   string
	ship    *logship.Shipper
	metrics *metricship.Shipper
	logf    func(string, ...any)
	echo    bool
}

func newLogSink(fn string, ship *logship.Shipper, metrics *metricship.Shipper,
	logf func(string, ...any), echo bool) *logSink {
	return &logSink{fn: fn, group: lambdaruntime.LogGroupName(fn),
		ship: ship, metrics: metrics, logf: logf, echo: echo}
}

// Line implements lambdaruntime.LogSink.
func (s *logSink) Line(stream, requestID string, at time.Time, line []byte) {
	if s.echo {
		s.logf("lambda[%s] %s", s.fn, line)
	}
	s.ship.Put(s.group, stream, logship.Event{Timestamp: at.UnixMilli(), Message: string(line), RequestID: requestID})
	s.emit(line, at)
}

// emit publishes the metrics an EMF line carries. A line is still a log line
// either way: EMF is a log entry that is ALSO a publication, so this is in
// addition to shipping it, never instead.
//
// emf.Looks gates the parse, because this runs on every line a function
// prints and full-parsing all of them to find the few that are EMF would make
// printing expensive for everyone else.
func (s *logSink) emit(line []byte, at time.Time) {
	if s.metrics == nil || !emf.Looks(line) {
		return
	}
	byNamespace := map[string][]metricship.Datum{}
	for _, m := range emf.Parse(line) {
		ms := m.TimestampMs
		if ms == 0 {
			ms = at.UnixMilli()
		}
		byNamespace[m.Namespace] = append(byNamespace[m.Namespace], metricship.Datum{
			MetricName: m.Name, Value: m.Value, Unit: m.Unit,
			Dimensions: m.Dimensions, TimestampMs: ms,
			StorageResolution: m.StorageResolution,
		})
	}
	for ns, data := range byNamespace {
		s.metrics.Put(ns, data...)
	}
}

// Flush implements lambdaruntime.LogSink: an invocation ended, send now.
func (s *logSink) Flush() { s.ship.Flush() }

// Close sends what is pending; the shipper itself lives with the server.
func (s *logSink) Close() { s.ship.Flush() }
