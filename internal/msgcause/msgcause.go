// Package msgcause remembers which recorded call put a message on a queue, so
// the event source mapping that later reads it can say what caused its invoke.
//
// Work doze-aws starts itself (an S3 notification, an SNS fan-out) carries its
// cause ON the message, in the AWSTraceHeader system attribute (package trace,
// "Across a queue"). A message an SDK sends carries no such header, and the
// header cannot be added: it is stored with the message and comes back to the
// application in ReceiveMessage, which real AWS would not do for a sender that
// set none, and putting it there would be a storage change besides.
//
// So the link is kept beside the queue, in memory: the queue says, as it accepts
// a message, "this id came of that call", and the poller asks by queue and id.
// The table is a bounded LRU and is lost on a restart, and both are fine: a
// link outlives the call it names only until that call ages out of the
// recorder's 500-entry ring, and a restart empties the ring too. A message that
// is looked up and not found is a root on the wire, which is what it was
// before, and honest ("nothing told us what caused this").
//
// It is internal because trace is public API and this is plumbing between the
// queue, the recorder and the poller.
package msgcause

import (
	"container/list"
	"context"
	"sync"

	"github.com/doze-dev/doze-aws/trace"
)

// Capacity bounds a Table. The recorder keeps 500 calls; a message sent by one
// of them is worth remembering only while that call is still on the page, and a
// batch of ten sends counts as ten, so a table the size of the ring is enough.
const Capacity = 512

// Linker is what a recorder implements to keep the links. It is looked for on
// the request's context (With) by the queue and on the trace sink by the poller.
type Linker interface {
	// LinkMessage says that message id on queue came of the call cause.
	LinkMessage(queue, id string, cause trace.Cause)
	// MessageCause is the call that put the message on the queue, or 0.
	MessageCause(queue, id string) trace.Cause
}

type linkerKey struct{}

// With attaches the linker to a request's context. The recorder does this at
// the edge, beside trace.With, so a queue that accepts a message can find it.
func With(ctx context.Context, l Linker) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, linkerKey{}, l)
}

// Noter returns the function a queue calls with each message id it accepts on
// queue, or nil when there is nothing to remember: the request is not recorded,
// or no recorded call caused it. A nil Noter costs the caller nothing.
func Noter(ctx context.Context, queue string) func(id string) {
	if ctx == nil {
		return nil
	}
	l, ok := ctx.Value(linkerKey{}).(Linker)
	if !ok {
		return nil
	}
	cause := trace.CauseOf(ctx)
	if cause == 0 {
		return nil
	}
	return func(id string) { l.LinkMessage(queue, id, cause) }
}

// Of is the call that put a message on a queue, or 0 when the sink keeps no
// links or does not know this message.
func Of(sink trace.Sink, queue, id string) trace.Cause {
	if l, ok := sink.(Linker); ok {
		return l.MessageCause(queue, id)
	}
	return 0
}

// Table is a Linker's memory: a least-recently-used map from queue and message
// id to a cause, holding at most Capacity links. The zero value is ready, and
// allocates nothing until the first link, so a stack nobody sends to pays for
// two words.
type Table struct {
	mu  sync.Mutex
	m   map[string]*list.Element
	lru *list.List // front is the most recently used
}

type link struct {
	key   string
	cause trace.Cause
}

func keyOf(queue, id string) string { return queue + "\x00" + id }

// Put remembers a link, forgetting the least recently used when the table is
// full.
func (t *Table) Put(queue, id string, cause trace.Cause) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m, t.lru = make(map[string]*list.Element), list.New()
	}
	k := keyOf(queue, id)
	if el, ok := t.m[k]; ok {
		el.Value.(*link).cause = cause
		t.lru.MoveToFront(el)
		return
	}
	t.m[k] = t.lru.PushFront(&link{k, cause})
	if t.lru.Len() > Capacity {
		old := t.lru.Back()
		t.lru.Remove(old)
		delete(t.m, old.Value.(*link).key)
	}
}

// Get is the cause remembered for a message, or 0. A message that is read keeps
// its place: one a failing function is handed again and again stays linked while
// it is being retried.
func (t *Table) Get(queue, id string) trace.Cause {
	t.mu.Lock()
	defer t.mu.Unlock()
	el, ok := t.m[keyOf(queue, id)]
	if !ok {
		return 0
	}
	t.lru.MoveToFront(el)
	return el.Value.(*link).cause
}
