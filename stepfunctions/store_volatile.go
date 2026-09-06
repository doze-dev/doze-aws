package stepfunctions

import (
	"encoding/json"
	"strings"
	"sync"
)

// Volatile executions: Express workflows and TestState runs. Both live only
// in memory — an Express execution is not durable on AWS either (it has no
// DescribeExecution, no history, no restart), and a TestState run exists for
// exactly one API call. Keeping them out of bbolt is what makes Express
// cheap, and it is also what makes the same engine serve both kinds: the
// store branches on Execution.Volatile at the one write it does per
// transition, and the driver never knows the difference.
//
// The map holds marshalled bytes, not the engine's pointer, so a read from a
// handler goroutine sees a consistent snapshot exactly as a bbolt read would.
type volatile struct {
	mu     sync.Mutex
	execs  map[string][]byte
	events map[string][]histEvent
}

func newVolatile() *volatile {
	return &volatile{execs: map[string][]byte{}, events: map[string][]histEvent{}}
}

func (v *volatile) save(key string, raw []byte, events []histEvent) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.execs[key] = raw
	if len(events) > 0 {
		v.events[key] = append(v.events[key], events...)
	}
}

func (v *volatile) get(key string) (*Execution, bool) {
	v.mu.Lock()
	raw, ok := v.execs[key]
	v.mu.Unlock()
	if !ok {
		return nil, false
	}
	var e Execution
	if json.Unmarshal(raw, &e) != nil {
		return nil, false
	}
	return &e, true
}

func (v *volatile) history(key string, afterID int64, limit int) (events []histEvent, more, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	all, ok := v.events[key]
	if !ok {
		return nil, false, false
	}
	var out []histEvent
	for _, ev := range all {
		if ev.ID > afterID {
			out = append(out, ev)
		}
	}
	more = len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, true
}

func (v *volatile) drop(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.execs, key)
	delete(v.events, key)
}

// isExpressARN reports whether an execution ARN is an Express one:
// arn:aws:states:<r>:<a>:express:<machine>:<name>:<id>, the form AWS uses,
// which the Standard-only operations refuse by shape.
func isExpressARN(arn string) bool {
	parts := strings.SplitN(arn, ":", 8)
	return len(parts) >= 6 && parts[5] == "express"
}

// DropVolatile forgets a volatile execution once its caller has read it.
func (s *Store) DropVolatile(key string) { s.vol.drop(key) }
