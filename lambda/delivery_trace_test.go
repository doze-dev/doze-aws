package lambda

import (
	"context"
	"testing"

	"github.com/doze-dev/doze-aws/trace"
)

type sinkRecorder struct{ events []trace.Event }

func (r *sinkRecorder) ReserveCascade() trace.Cause { return trace.Cause(len(r.events) + 100) }
func (r *sinkRecorder) EmitCascade(e trace.Event)   { r.events = append(r.events, e) }
func (r *sinkRecorder) RunID() string               { return "run" }

// An SQS mapping's invoke of a message no header names is still work doze-aws
// did. It is recorded as a root, so it is on the wire. trace.Continue alone
// gives back a context with no sink for it, and the invoke was recorded
// nowhere: an app's SendMessage to a queue a function reads showed on the wire,
// and the function running because of it did not.
func TestAnSQSDeliveryWithNoHeaderIsARootOnTheWire(t *testing.T) {
	sink := &sinkRecorder{}
	s := &Server{}
	s.SetTraceSink(sink)

	ctx := s.deliveryContext("orders", msgs(""))
	err := trace.Step(ctx, trace.Event{Service: "lambda", Action: "Invoke (event source)", Resource: "validator", Via: "sqs:orders"},
		func(context.Context) error { return nil })
	_ = err
	if len(sink.events) != 1 {
		t.Fatalf("the delivery recorded %d events, want 1 (a root)", len(sink.events))
	}
	if e := sink.events[0]; e.Cause != 0 || e.Via != "sqs:orders" {
		t.Errorf("event = cause %d via %q, want a root (cause 0) via sqs:orders", e.Cause, e.Via)
	}
}

// A header that names a call of this run continues its chain: the delivery is a
// child of the call that sent the message.
func TestAnSQSDeliveryContinuesTheChainItsHeaderNames(t *testing.T) {
	sink := &sinkRecorder{}
	s := &Server{}
	s.SetTraceSink(sink)

	ctx := s.deliveryContext("orders", msgs("Root=run;Parent=7"))
	_ = trace.Step(ctx, trace.Event{Service: "lambda", Action: "Invoke (event source)", Via: "sqs:orders"}, func(context.Context) error { return nil })
	if len(sink.events) != 1 || sink.events[0].Cause != 7 {
		t.Fatalf("events = %+v, want one child of call 7", sink.events)
	}
}
