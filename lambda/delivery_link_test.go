package lambda

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/trace"
)

// msgs is a batch of one message that carries the given trace header.
func msgs(header string) []peercall.SQSMessage {
	m := peercall.SQSMessage{MessageID: "m-1"}
	if header != "" {
		m.Attributes = map[string]string{"AWSTraceHeader": header}
	}
	return []peercall.SQSMessage{m}
}

// linkingSink is a recorder that remembers which call sent which message.
type linkingSink struct {
	sinkRecorder
	links map[string]trace.Cause
}

func (l *linkingSink) LinkMessage(queue, id string, c trace.Cause) { l.links[queue+"/"+id] = c }
func (l *linkingSink) MessageCause(queue, id string) trace.Cause   { return l.links[queue+"/"+id] }

// A message an SDK sent has no header, but the recorder knows which call sent
// it: the delivery is a child of that call. The header, when there is one, is
// the more exact witness and wins; a batch takes the first message that either
// names a cause, because one invoke has one parent.
func TestAnSQSDeliveryOfAnSDKsMessageIsAChildOfTheSend(t *testing.T) {
	sink := &linkingSink{links: map[string]trace.Cause{"orders/m-1": 9, "orders/m-3": 30}}
	s := &Server{}
	s.SetTraceSink(sink)
	run := func(batch []peercall.SQSMessage) trace.Cause {
		return trace.CauseOf(s.deliveryContext("orders", batch))
	}

	if got := run(msgs("")); got != 9 {
		t.Errorf("a message with no header: cause %d, want 9 (the call that sent it)", got)
	}
	if got := run(msgs("Root=run;Parent=7")); got != 7 {
		t.Errorf("a message with a header: cause %d, want 7 (the header wins)", got)
	}
	// Nothing known: a root, which is on the wire and has no parent.
	if got := run([]peercall.SQSMessage{{MessageID: "unknown"}}); got != 0 {
		t.Errorf("an unknown message: cause %d, want 0 (a root)", got)
	}
	// The first message of a batch that names a cause is the one used.
	batch := []peercall.SQSMessage{{MessageID: "unknown"}, {MessageID: "m-3"}, {MessageID: "m-1"}}
	if got := run(batch); got != 30 {
		t.Errorf("a batch: cause %d, want 30 (the first message with a known cause)", got)
	}
}
