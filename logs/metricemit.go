package logs

// The metric-filter emitter.
//
// A sibling of fanout.go, and deliberately the same shape: a buffered channel,
// one worker, a per-group cache of compiled patterns, and forget() on a write.
// The reasoning is the same too — a batch arriving while the server closes is
// dropped with a line rather than blocking the writer, because PutLogEvents is
// on Lambda's invocation path and must not wait for anything downstream.
//
// The difference is what happens to a match: fanout ships the line onward,
// this turns it into a number.

import (
	"context"
	"sync"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/metricship"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

const metricBuffer = 1024

type metricBatch struct {
	ctx    context.Context
	group  string
	events []Stored
}

// compiledFilter is a metric filter with its pattern compiled once.
type compiledFilter struct {
	MetricFilter
	match matcher
}

type metricEmitter struct {
	store *Store
	ship  *metricship.Shipper
	logf  func(string, ...any)
	in    chan metricBatch
	done  chan struct{}

	mu     sync.Mutex
	cache  map[string][]compiledFilter
	closed bool
}

func newMetricEmitter(store *Store, dir peers.Directory, logf func(string, ...any)) *metricEmitter {
	m := &metricEmitter{
		store: store, ship: metricship.New("logs", dir, logf), logf: logf,
		in: make(chan metricBatch, metricBuffer), done: make(chan struct{}),
		cache: map[string][]compiledFilter{},
	}
	go m.run()
	return m
}

// enqueue hands a stored batch to the worker, detaching the request context
// so evaluation outlives the PutLogEvents that caused it.
func (m *metricEmitter) enqueue(ctx context.Context, group string, events []Stored) {
	if len(events) == 0 {
		return
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return
	}
	select {
	case m.in <- metricBatch{ctx: context.WithoutCancel(ctx), group: group, events: events}:
	default:
		m.logf("logs: metric-filter queue full; dropped a batch for %s", group)
	}
}

// forget drops a group's compiled filters after one changed.
func (m *metricEmitter) forget(group string) {
	m.mu.Lock()
	delete(m.cache, group)
	m.mu.Unlock()
}

func (m *metricEmitter) close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	close(m.in)
	<-m.done
	m.ship.Close()
}

func (m *metricEmitter) run() {
	defer close(m.done)
	for b := range m.in {
		m.evaluate(b)
	}
}

// filtersFor compiles a group's filters once and caches them, because a
// pattern is recompiled per batch otherwise and PutLogEvents is hot.
func (m *metricEmitter) filtersFor(group string) []compiledFilter {
	m.mu.Lock()
	if got, ok := m.cache[group]; ok {
		m.mu.Unlock()
		return got
	}
	m.mu.Unlock()

	found, err := m.store.MetricFilters(group, "")
	if err != nil {
		m.logf("logs: reading metric filters for %s: %v", group, err)
		return nil
	}
	out := make([]compiledFilter, 0, len(found))
	for _, f := range found {
		m2, err := compile(f.Pattern)
		if err != nil {
			// A pattern that does not compile matches nothing rather than
			// everything: publishing a metric for every line because the
			// filter was malformed is the worse failure.
			m.logf("logs: metric filter %s/%s has an unusable pattern: %v", f.Group, f.Name, err)
			continue
		}
		out = append(out, compiledFilter{MetricFilter: f, match: m2})
	}
	m.mu.Lock()
	m.cache[group] = out
	m.mu.Unlock()
	return out
}

// evaluate turns the matching lines of one batch into metrics.
func (m *metricEmitter) evaluate(b metricBatch) {
	filters := m.filtersFor(b.group)
	if len(filters) == 0 {
		return
	}
	// The principal is the logs service acting for the group, so a metric
	// published under enforcement is attributable the way AWS attributes it.
	ctx := peers.WithPrincipal(b.ctx, "logs",
		awsident.ARN("logs", "log-group:"+b.group+":*"))

	published := 0
	for _, f := range filters {
		for _, ev := range b.events {
			if !f.match(ev.Msg) {
				continue
			}
			for _, t := range f.Transformations {
				value, ok := metricValueOf(t, ev.Msg)
				if !ok {
					// A $.field that did not resolve and no defaultValue.
					// Publishing zero here would invent an observation.
					continue
				}
				m.ship.Put(t.Namespace, metricship.Datum{
					MetricName: t.MetricName, Value: value, Unit: t.Unit,
					Dimensions: t.Dimensions, TimestampMs: ev.TS,
				})
				published++
			}
		}
	}
	if published == 0 {
		return
	}
	// Traced so the console shows the cascade: a log line became a metric.
	_ = trace.Step(ctx, trace.Event{
		Service: "logs", Action: "MetricFilter", Resource: b.group,
		Via: "logs:PutLogEvents",
	}, func(context.Context) error {
		m.ship.Flush()
		return nil
	})
}
