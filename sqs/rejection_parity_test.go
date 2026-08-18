package sqs

// Rejection parity: the inputs real SQS refuses, and the error code it refuses
// them with.
//
// The rest of the suite proves the positive direction — a valid SDK call
// works. That leaves the more dangerous direction untested. An emulator can be
// wrong two ways, and they are not symmetric: too strict fails loudly, here,
// now, and gets fixed; too permissive passes here and fails on deploy, which
// is the one place the cost is real. Every case below is something this store
// used to accept and AWS does not.
//
// Adding a case is a row. It is deliberately not a full transcription of the
// SQS validation rules — it is the ones worth the bytes, growable as more are
// found.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

// redrive renders a RedrivePolicy with the target given as a bare queue name.
func redrive(target string, maxReceive string) string {
	return `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:` + target +
		`","maxReceiveCount":` + maxReceive + `}`
}

func TestSetQueueAttributesRejectsWhatAWSRejects(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	// One fixture set for every case: a standard source and DLQ, and the FIFO
	// pair, so type-mismatch has something real on both sides.
	urls := map[string]string{}
	for _, q := range []string{"src", "dlq", "src.fifo", "dlq.fifo"} {
		in := &awssqs.CreateQueueInput{QueueName: aws.String(q)}
		if q == "src.fifo" || q == "dlq.fifo" {
			in.Attributes = map[string]string{"FifoQueue": "true"}
		}
		out, err := c.CreateQueue(ctx, in)
		if err != nil {
			t.Fatalf("CreateQueue %s: %v", q, err)
		}
		urls[q] = aws.ToString(out.QueueUrl)
	}

	cases := []struct {
		name     string
		queue    string
		attrs    map[string]string
		wantCode string
	}{{
		name:  "dead-letter target does not exist",
		queue: "src",
		// The one that started this: any string was accepted, the policy read
		// back as applied, and nothing was ever delivered to it.
		attrs:    map[string]string{"RedrivePolicy": redrive("no-such-queue", `"5"`)},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "standard source with a FIFO dead-letter queue",
		queue:    "src",
		attrs:    map[string]string{"RedrivePolicy": redrive("dlq.fifo", `"5"`)},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "FIFO source with a standard dead-letter queue",
		queue:    "src.fifo",
		attrs:    map[string]string{"RedrivePolicy": redrive("dlq", `"5"`)},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "queue as its own dead-letter queue",
		queue:    "src",
		attrs:    map[string]string{"RedrivePolicy": redrive("src", `"5"`)},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "maxReceiveCount of zero",
		queue:    "src",
		attrs:    map[string]string{"RedrivePolicy": redrive("dlq", `"0"`)},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "maxReceiveCount above 1000",
		queue:    "src",
		attrs:    map[string]string{"RedrivePolicy": redrive("dlq", `"1001"`)},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "maxReceiveCount not a number",
		queue:    "src",
		attrs:    map[string]string{"RedrivePolicy": redrive("dlq", `"many"`)},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "VisibilityTimeout above 12 hours",
		queue:    "src",
		attrs:    map[string]string{"VisibilityTimeout": "43201"},
		wantCode: "InvalidAttributeValue",
	}, {
		// Previously the parse failed, the error was discarded, and the queue
		// kept its old value behind a 200.
		name:     "VisibilityTimeout not a number",
		queue:    "src",
		attrs:    map[string]string{"VisibilityTimeout": "banana"},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "DelaySeconds above 15 minutes",
		queue:    "src",
		attrs:    map[string]string{"DelaySeconds": "901"},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "MessageRetentionPeriod below one minute",
		queue:    "src",
		attrs:    map[string]string{"MessageRetentionPeriod": "59"},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "MaximumMessageSize above 256 KiB",
		queue:    "src",
		attrs:    map[string]string{"MaximumMessageSize": "262145"},
		wantCode: "InvalidAttributeValue",
	}, {
		name:     "ReceiveMessageWaitTimeSeconds above 20",
		queue:    "src",
		attrs:    map[string]string{"ReceiveMessageWaitTimeSeconds": "21"},
		wantCode: "InvalidAttributeValue",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
				QueueUrl: aws.String(urls[tc.queue]), Attributes: tc.attrs,
			})
			if err == nil {
				t.Fatalf("SetQueueAttributes(%v) was accepted; AWS refuses it with %s",
					tc.attrs, tc.wantCode)
			}
			var ae smithy.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("error is not an APIError: %v", err)
			}
			if ae.ErrorCode() != tc.wantCode {
				t.Fatalf("code = %s, want %s (%v)", ae.ErrorCode(), tc.wantCode, err)
			}
		})
	}
}

// TestValidRedrivePolicyStillApplies guards the other side of the fix: the
// checks above must not have made a legitimate dead-letter queue unusable, and
// the policy has to read back the way an SDK expects.
func TestValidRedrivePolicyStillApplies(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	src, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders-dlq")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl:   src.QueueUrl,
		Attributes: map[string]string{"RedrivePolicy": redrive("orders-dlq", `"5"`)},
	}); err != nil {
		t.Fatalf("a valid redrive policy was refused: %v", err)
	}

	got, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: src.QueueUrl, AttributeNames: []types.QueueAttributeName{"RedrivePolicy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Attributes["RedrivePolicy"] == "" {
		t.Fatal("RedrivePolicy did not read back")
	}
}

// TestRedriveAcceptsUnquotedMaxReceiveCount: AWS writes maxReceiveCount as a
// number and accepts it quoted. Terraform sends the quoted form and the
// console the bare one, so refusing either would break a real caller.
func TestRedriveAcceptsUnquotedMaxReceiveCount(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	src, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("a")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl:   src.QueueUrl,
		Attributes: map[string]string{"RedrivePolicy": redrive("b", "5")},
	}); err != nil {
		t.Fatalf("bare-number maxReceiveCount refused: %v", err)
	}
}

// TestQueueNameCharset closes the gap docs/api-support/sqs.md carried as
// "❌ `bad name!` is accepted". SQS's model states no pattern or length for
// QueueName — the rule is prose only, which is exactly why nothing had ever
// cross-checked it — so these expectations are hand-derived from the API
// reference and asserted by the error CODE an SDK branches on.
func TestQueueNameCharset(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	cases := []struct {
		name  string
		queue string
		ok    bool
	}{
		{"plain", "orders", true},
		{"hyphen and underscore", "my-orders_2", true},
		{"fifo suffix", "ordersq.fifo", true},
		{"at the length limit", strings.Repeat("a", 80), true},
		{"space", "bad name", false},
		{"bang", "bad!", false},
		{"slash", "a/b", false},
		{"a period that is not the fifo suffix", "orders.v2", false},
		{"one over the length limit", strings.Repeat("a", 81), false},
		{"fifo whose suffix pushes it over", strings.Repeat("a", 76) + ".fifo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &awssqs.CreateQueueInput{QueueName: aws.String(tc.queue)}
			if strings.HasSuffix(tc.queue, ".fifo") {
				in.Attributes = map[string]string{"FifoQueue": "true"}
			}
			_, err := c.CreateQueue(ctx, in)
			if tc.ok {
				if err != nil {
					t.Fatalf("CreateQueue(%q) = %v, want accepted", tc.queue, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CreateQueue(%q) was accepted; AWS refuses it", tc.queue)
			}
			var ae smithy.APIError
			if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidParameterValue" {
				t.Errorf("code = %v, want InvalidParameterValue", err)
			}
		})
	}
}

// TestDelayedIsNotInFlight separates two states doze-aws used to collapse.
//
// A message can be invisible because a consumer is working on it, or because
// it has never been delivered and is still serving DelaySeconds. SQS counts
// those separately, and the difference is not cosmetic: anything scaling
// consumers off ApproximateNumberOfMessagesNotVisible would scale up for work
// nobody is doing.
func TestDelayedIsNotInFlight(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	out, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("states")})
	if err != nil {
		t.Fatal(err)
	}
	url := out.QueueUrl

	// One available, one delayed, one in flight.
	for _, body := range []string{"available", "inflight"} {
		if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{
			QueueUrl: url, MessageBody: aws.String(body),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: url, MessageBody: aws.String("delayed"), DelaySeconds: 900,
	}); err != nil {
		t.Fatal(err)
	}
	// Receive exactly one, and hold it: that is the only in-flight message.
	got, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: url, MaxNumberOfMessages: 1, VisibilityTimeout: 300,
	})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("ReceiveMessage = %d messages, err %v", len(got.Messages), err)
	}

	attrs, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: url, AttributeNames: []types.QueueAttributeName{"All"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, want string }{
		{"ApproximateNumberOfMessages", "1"},
		{"ApproximateNumberOfMessagesNotVisible", "1"},
		{"ApproximateNumberOfMessagesDelayed", "1"},
	} {
		if got := attrs.Attributes[c.name]; got != c.want {
			t.Errorf("%s = %q, want %q\n  all: %v", c.name, got, c.want, attrs.Attributes)
		}
	}
}

// TestSystemAttributesStayOutOfMessageAttributes covers the property that lets
// a trace header ride on a message without the application noticing.
//
// SQS keeps message SYSTEM attributes apart from the user's: they come back
// with SentTimestamp rather than in MessageAttributes, and — the part that
// would break a client if it were wrong — they are excluded from
// MD5OfMessageAttributes. An SDK verifies that digest, so folding the header in
// with the user's attributes would make every traced message look corrupted.
func TestSystemAttributesStayOutOfMessageAttributes(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	out, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("traced")})
	if err != nil {
		t.Fatal(err)
	}

	userAttrs := map[string]types.MessageAttributeValue{
		"kind": {DataType: aws.String("String"), StringValue: aws.String("order")},
	}
	// Send the same user attributes twice, once with a system attribute, and
	// compare the digests: the header must not move them.
	plain, err := c.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: out.QueueUrl, MessageBody: aws.String("a"), MessageAttributes: userAttrs,
	})
	if err != nil {
		t.Fatal(err)
	}
	traced, err := c.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: out.QueueUrl, MessageBody: aws.String("b"), MessageAttributes: userAttrs,
		MessageSystemAttributes: map[string]types.MessageSystemAttributeValue{
			"AWSTraceHeader": {DataType: aws.String("String"), StringValue: aws.String("Root=r;Parent=9")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(plain.MD5OfMessageAttributes) != aws.ToString(traced.MD5OfMessageAttributes) {
		t.Errorf("the system attribute changed MD5OfMessageAttributes: %q vs %q\n"+
			"  an SDK verifies that digest, so every traced message would look corrupted",
			aws.ToString(plain.MD5OfMessageAttributes), aws.ToString(traced.MD5OfMessageAttributes))
	}

	// And it comes back where AWS puts it: with the system attributes.
	got, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: out.QueueUrl, MaxNumberOfMessages: 10, VisibilityTimeout: 0,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{"All"},
		MessageAttributeNames:       []string{"All"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, m := range got.Messages {
		if aws.ToString(m.Body) != "b" {
			continue
		}
		seen = true
		if h := m.Attributes["AWSTraceHeader"]; h != "Root=r;Parent=9" {
			t.Errorf("AWSTraceHeader came back as %q, want the value that was sent", h)
		}
		if _, leaked := m.MessageAttributes["AWSTraceHeader"]; leaked {
			t.Error("the trace header leaked into the application's MessageAttributes")
		}
	}
	if !seen {
		t.Fatal("the traced message was never received")
	}
}

// TestModernSDKGetsSystemAttributes guards the rename that broke them.
//
// AWS deprecated AttributeNames in favour of MessageSystemAttributeNames, and
// doze-aws read only the old spelling — so a current aws-sdk-go-v2 client
// asking for "All" received no system attributes at all. Not the trace header:
// ApproximateReceiveCount and SentTimestamp too, which is the sort of thing a
// consumer's retry logic quietly depends on.
func TestModernSDKGetsSystemAttributes(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	out, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("sysattrs")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: out.QueueUrl, MessageBody: aws.String("x"),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:                    out.QueueUrl,
		MaxNumberOfMessages:         1,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{"All"},
	})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("ReceiveMessage = %d messages, err %v", len(got.Messages), err)
	}
	for _, want := range []string{"ApproximateReceiveCount", "SentTimestamp"} {
		if got.Messages[0].Attributes[want] == "" {
			t.Errorf("%s is missing when asked for via MessageSystemAttributeNames\n  got: %v",
				want, got.Messages[0].Attributes)
		}
	}
}
