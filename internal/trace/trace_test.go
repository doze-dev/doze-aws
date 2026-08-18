package trace

import (
	"context"
	"testing"
)

type fakeSink struct {
	run    string
	events []Event
	next   Cause
}

func (f *fakeSink) RunID() string { return f.run }
func (f *fakeSink) ReserveCascade() Cause {
	f.next++
	return f.next
}
func (f *fakeSink) EmitCascade(e Event) { f.events = append(f.events, e) }

// TestHeaderCarriesTheCauseAcrossAQueue covers the round trip a message makes:
// a header is written from a traced context and read back into an equivalent
// one, which is what keeps S3 → SQS → Lambda a single chain.
func TestHeaderCarriesTheCauseAcrossAQueue(t *testing.T) {
	sink := &fakeSink{run: "abc123"}
	ctx := With(context.Background(), sink, Cause(42))

	h := Header(ctx)
	if h != "Root=abc123;Parent=42" {
		t.Fatalf("Header() = %q", h)
	}

	got := Continue(context.Background(), sink, h)
	if c := CauseOf(got); c != 42 {
		t.Errorf("Continue() gave cause %d, want 42", c)
	}
}

// TestHeaderFromAnotherRunIsIgnored is the one that matters for correctness.
//
// Sequence numbers restart at zero when doze-aws does, so a message that
// outlived a restart carries a parent id that now belongs to a completely
// different call. Honouring it would draw a confident, wrong line between two
// unrelated things — worse than drawing none.
func TestHeaderFromAnotherRunIsIgnored(t *testing.T) {
	previous := &fakeSink{run: "old-run"}
	stale := Header(With(context.Background(), previous, Cause(7)))

	current := &fakeSink{run: "new-run"}
	got := Continue(context.Background(), current, stale)
	if c := CauseOf(got); c != 0 {
		t.Errorf("a header from run %q was honoured by run %q (cause %d)", "old-run", "new-run", c)
	}
}

func TestMalformedHeadersAreDropped(t *testing.T) {
	sink := &fakeSink{run: "r"}
	for _, h := range []string{"", "garbage", "Root=r", "Parent=1", "Root=r;Parent=", "Root=r;Parent=x", "Root=r;Parent=0"} {
		if c := CauseOf(Continue(context.Background(), sink, h)); c != 0 {
			t.Errorf("header %q produced cause %d, want none", h, c)
		}
	}
}

// TestStepReParentsItsChildren pins the property that makes depth work: the
// work inside a step descends from THAT step, not from whatever caused it.
func TestStepReParentsItsChildren(t *testing.T) {
	sink := &fakeSink{run: "r"}
	root := With(context.Background(), sink, Cause(100))

	var innerCause Cause
	err := Step(root, Event{Service: "sns", Action: "Publish"}, func(ctx context.Context) error {
		innerCause = CauseOf(ctx)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("got %d events, want 1", len(sink.events))
	}
	e := sink.events[0]
	if e.Cause != 100 {
		t.Errorf("the step's own cause is %d, want 100", e.Cause)
	}
	if innerCause != e.Self {
		t.Errorf("work inside the step saw cause %d, want the step's own id %d — "+
			"without this a two-hop chain flattens", innerCause, e.Self)
	}
}

// TestUntracedContextIsANoOp: instrumentation must be free to call anywhere.
func TestUntracedContextIsANoOp(t *testing.T) {
	ran := false
	err := Step(context.Background(), Event{Service: "sqs"}, func(ctx context.Context) error {
		ran = true
		return nil
	})
	if err != nil || !ran {
		t.Fatal("Step did not run fn without a sink")
	}
	if Header(context.Background()) != "" {
		t.Error("Header() invented a value without a sink")
	}
}
