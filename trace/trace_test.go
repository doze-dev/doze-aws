package trace

import (
	"context"
	"errors"
	"fmt"
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

type answered struct{ status int }

func (a answered) Error() string   { return "answered" }
func (a answered) HTTPStatus() int { return a.status }

// A failed hop carries the status its target answered, found through any
// wrapping, so the wire can show a denial as a denial.
func TestAFailedStepRecordsTheStatusItWasAnswered(t *testing.T) {
	sink := &fakeSink{run: "r"}
	ctx := With(context.Background(), sink, Cause(1))

	_ = Step(ctx, Event{Service: "sns", Action: "Publish"}, func(context.Context) error {
		return fmt.Errorf("publishing: %w", answered{403})
	})
	_ = Step(ctx, Event{Service: "sqs", Action: "SendMessage"}, func(context.Context) error {
		return errors.New("no sqs peer wired")
	})
	_ = Step(ctx, Event{Service: "sqs", Action: "SendMessage"}, func(context.Context) error { return nil })

	if got := sink.events[0].Status; got != 403 {
		t.Errorf("wrapped answer: status = %d, want 403", got)
	}
	if got := sink.events[1].Status; got != 0 {
		t.Errorf("an error with no status recorded %d, want 0", got)
	}
	if e := sink.events[2]; e.Status != 0 || e.Err != "" {
		t.Errorf("a success recorded %+v", e)
	}
}

type denied struct{}

func (denied) Error() string    { return "denied" }
func (denied) HTTPStatus() int  { return 403 }
func (denied) DeniedBy() string { return "iam" }

func TestAFailedStepRecordsWhoDeniedIt(t *testing.T) {
	sink := &fakeSink{run: "r"}
	ctx := With(context.Background(), sink, Cause(1))
	_ = Step(ctx, Event{Service: "sns", Action: "Publish"}, func(context.Context) error { return fmt.Errorf("x: %w", denied{}) })
	if e := sink.events[0]; e.Denied != "iam" || e.Status != 403 {
		t.Errorf("recorded %+v, want status 403 denied by iam", e)
	}
}

// StepDetail is Step for work that leaves something to read; a failure in it is
// recorded with the same status and denial, and the detail still rides along.
func TestAFailedStepDetailRecordsStatusDenialAndWhatItLeftBehind(t *testing.T) {
	sink := &fakeSink{run: "r"}
	ctx := With(context.Background(), sink, Cause(1))
	err := StepDetail(ctx, Event{Service: "lambda", Action: "Invoke"}, func(context.Context) (string, string, error) {
		return "tail of the log", "/lambda/fn?tab=logs", fmt.Errorf("invoke: %w", denied{})
	})
	if err == nil {
		t.Fatal("the error was swallowed")
	}
	e := sink.events[0]
	if e.Status != 403 || e.Denied != "iam" || e.Detail != "tail of the log" || e.DetailURL != "/lambda/fn?tab=logs" || e.Err == "" {
		t.Errorf("recorded %+v", e)
	}
	// Untraced: the function runs and its error is returned as is.
	if got := StepDetail(context.Background(), Event{}, func(context.Context) (string, string, error) { return "", "", errors.New("x") }); got == nil {
		t.Error("an untraced StepDetail dropped its error")
	}
}

// A caller that knows what emitted a delivery (S3, for a notification) says so
// on the context, and the step it makes carries it: the wire reads "via
// s3:ObjectCreated:Put" under the put that caused it. The step names its own
// via when it has one (an SNS fan-out), and the work inside the step does not
// inherit the caller's: a queue send made by a topic's delivery is the topic's.
func TestAStepCarriesTheViaItsCallerSetAndPassesItNoFurther(t *testing.T) {
	sink := &fakeSink{run: "r"}
	ctx := WithVia(With(context.Background(), sink, 1), "s3:ObjectCreated:Put")

	_ = Step(ctx, Event{Service: "sns", Action: "Publish", Resource: "t"}, func(ctx context.Context) error {
		// Inside the publish, a send with no via of its own is not "via s3".
		return Step(ctx, Event{Service: "sqs", Action: "SendMessage", Resource: "q"}, func(context.Context) error { return nil })
	})
	_ = Step(ctx, Event{Service: "sqs", Action: "SendMessage", Resource: "q2", Via: "sns:t"}, func(context.Context) error { return nil })

	byRes := map[string]Event{}
	for _, e := range sink.events {
		byRes[e.Resource] = e
	}
	if got := byRes["t"].Via; got != "s3:ObjectCreated:Put" {
		t.Errorf("the publish's via = %q, want the caller's", got)
	}
	if got := byRes["q"].Via; got != "" {
		t.Errorf("the send inside the publish inherited via %q; the caller's via goes no further", got)
	}
	if got := byRes["q2"].Via; got != "sns:t" {
		t.Errorf("a step's own via was replaced: %q", got)
	}
}
