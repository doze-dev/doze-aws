package msgcause

import (
	"context"
	"strconv"
	"testing"

	"github.com/doze-dev/doze-aws/trace"
)

type fakeSink struct{ Table }

func (f *fakeSink) ReserveCascade() trace.Cause { return 1 }
func (f *fakeSink) EmitCascade(trace.Event)     {}
func (f *fakeSink) LinkMessage(queue, id string, cause trace.Cause) {
	f.Put(queue, id, cause)
}
func (f *fakeSink) MessageCause(queue, id string) trace.Cause { return f.Get(queue, id) }

// A queue notes the id of each message it accepts under the call that sent it,
// and the poller asks by queue and id.
func TestAQueueNotesWhoSentAMessageAndThePollerAsks(t *testing.T) {
	sink := &fakeSink{}
	ctx := With(trace.With(context.Background(), sink, 42), sink)

	note := Noter(ctx, "orders")
	if note == nil {
		t.Fatal("a recorded request got no noter")
	}
	note("m-1")

	if got := Of(sink, "orders", "m-1"); got != 42 {
		t.Errorf("the message came of call %d, want 42", got)
	}
	// Queue and id together: the same id on another queue is another message.
	if got := Of(sink, "other", "m-1"); got != 0 {
		t.Errorf("another queue's message answered %d", got)
	}
	if got := Of(sink, "orders", "m-2"); got != 0 {
		t.Errorf("an unknown message answered %d", got)
	}
}

// Nothing to remember costs nothing: no noter without a recorder, or for a
// request no recorded call caused, and a sink that keeps no links says 0.
func TestWithNothingToRememberThereIsNoNoter(t *testing.T) {
	sink := &fakeSink{}
	if Noter(context.Background(), "q") != nil {
		t.Error("an unrecorded request got a noter")
	}
	if Noter(With(context.Background(), sink), "q") != nil {
		t.Error("a request with no causing call got a noter")
	}
	if Noter(trace.With(context.Background(), sink, 7), "q") != nil {
		t.Error("a request with no linker got a noter")
	}
	if got := Of(nopSink{}, "q", "m"); got != 0 {
		t.Errorf("a sink with no links answered %d", got)
	}
}

type nopSink struct{}

func (nopSink) ReserveCascade() trace.Cause { return 1 }
func (nopSink) EmitCascade(trace.Event)     {}

// The table is bounded and forgets the least recently used first: a message a
// failing function keeps being handed stays linked while newer ones push the
// idle ones out.
func TestTheTableIsBoundedAndForgetsTheLeastRecentlyUsed(t *testing.T) {
	var tb Table
	tb.Put("q", "kept", 1)
	for i := 0; i < Capacity+10; i++ {
		tb.Put("q", "m"+strconv.Itoa(i), trace.Cause(100+i))
		if tb.Get("q", "kept") != 1 {
			t.Fatalf("a message in use was forgotten after %d newer ones", i)
		}
	}
	if got := tb.Get("q", "m0"); got != 0 {
		t.Errorf("the oldest idle message is still linked (cause %d)", got)
	}
	if got := tb.Get("q", "m"+strconv.Itoa(Capacity+9)); got == 0 {
		t.Error("the newest message was forgotten")
	}
	if tb.lru.Len() > Capacity || len(tb.m) > Capacity {
		t.Errorf("the table holds %d links, over its bound of %d", tb.lru.Len(), Capacity)
	}
}

// A table nobody has put into holds nothing.
func TestAnUnusedTableAllocatesNothing(t *testing.T) {
	var tb Table
	if tb.Get("q", "m") != 0 || tb.m != nil || tb.lru != nil {
		t.Error("reading an empty table allocated")
	}
}
