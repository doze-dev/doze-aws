package sqs

// Message system attributes (AWSTraceHeader) on every way of sending, and the
// receive-side name selection the model describes.
//
// The first pass read them on the single send alone, and on the Query wire
// under a spelling (`MessageSystemAttribute.entry.N`) a flattened list does not
// use. A batch entry's were dropped on both protocols, which is the path an SNS
// fan-out and a Lambda destination take, so the trace stopped there.
//
// doc: the model's SendMessageBatchRequestEntry ("MessageSystemAttributes ...
// the only supported message system attribute is AWSTraceHeader"), the
// SendMessageResult's MD5OfMessageSystemAttributes, and ReceiveMessage's
// MessageAttributeNames ("All or .*", "all message attributes starting with a
// prefix, for example bar.*").

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	awsv1 "github.com/aws/aws-sdk-go/aws"
	sqsv1 "github.com/aws/aws-sdk-go/service/sqs"
)

const traceA = "Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1"

func traceHeaders(t *testing.T, c *awssqs.Client, url *string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for i := 0; i < 3; i++ {
		r, err := c.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{QueueUrl: url,
			MaxNumberOfMessages: 10, VisibilityTimeout: 600,
			MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{"All"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range r.Messages {
			out[aws.ToString(m.Body)] = m.Attributes["AWSTraceHeader"]
		}
	}
	return out
}

func TestABatchEntryKeepsItsTraceHeader(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("traced")})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{QueueUrl: q.QueueUrl,
		Entries: []sqstypes.SendMessageBatchRequestEntry{
			{Id: aws.String("a"), MessageBody: aws.String("with"), MessageSystemAttributes: map[string]sqstypes.MessageSystemAttributeValue{
				"AWSTraceHeader": {DataType: aws.String("String"), StringValue: aws.String(traceA)}}},
			{Id: aws.String("b"), MessageBody: aws.String("without")},
		}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range res.Successful {
		switch aws.ToString(ok.Id) {
		case "a":
			if aws.ToString(ok.MD5OfMessageSystemAttributes) == "" {
				t.Error("a traced entry's result has no MD5OfMessageSystemAttributes")
			}
		case "b":
			if aws.ToString(ok.MD5OfMessageSystemAttributes) != "" {
				t.Error("an untraced entry's result has an MD5OfMessageSystemAttributes")
			}
		}
	}
	got := traceHeaders(t, c, q.QueueUrl)
	if got["with"] != traceA {
		t.Errorf("the batch entry's AWSTraceHeader came back as %q", got["with"])
	}
	if got["without"] != "" {
		t.Errorf("an untraced entry came back with %q", got["without"])
	}
}

func TestTheQuerySpellingOfSystemAttributesIsRead(t *testing.T) {
	c := sdkV1Client(t)
	q, err := c.CreateQueue(&sqsv1.CreateQueueInput{QueueName: awsv1.String("legacy-trace")})
	if err != nil {
		t.Fatal(err)
	}
	attr := map[string]*sqsv1.MessageSystemAttributeValue{
		"AWSTraceHeader": {DataType: awsv1.String("String"), StringValue: awsv1.String(traceA)},
	}
	single, err := c.SendMessage(&sqsv1.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: awsv1.String("single"),
		MessageSystemAttributes: attr})
	if err != nil {
		t.Fatal(err)
	}
	if awsv1.StringValue(single.MD5OfMessageSystemAttributes) == "" {
		t.Error("a traced single send has no MD5OfMessageSystemAttributes")
	}
	if _, err := c.SendMessageBatch(&sqsv1.SendMessageBatchInput{QueueUrl: q.QueueUrl,
		Entries: []*sqsv1.SendMessageBatchRequestEntry{
			{Id: awsv1.String("a"), MessageBody: awsv1.String("batched"), MessageSystemAttributes: attr}}}); err != nil {
		t.Fatal(err)
	}
	r, err := c.ReceiveMessage(&sqsv1.ReceiveMessageInput{QueueUrl: q.QueueUrl, MaxNumberOfMessages: awsv1.Int64(10),
		VisibilityTimeout: awsv1.Int64(600), AttributeNames: []*string{awsv1.String("All")}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range r.Messages {
		got[awsv1.StringValue(m.Body)] = awsv1.StringValue(m.Attributes["AWSTraceHeader"])
	}
	for _, body := range []string{"single", "batched"} {
		if got[body] != traceA {
			t.Errorf("%s: AWSTraceHeader came back as %q over the Query wire", body, got[body])
		}
	}
}

func TestMessageAttributeNamesTakeAPrefixWildcard(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("prefixed"),
		Attributes: map[string]string{"VisibilityTimeout": "0"}})
	if err != nil {
		t.Fatal(err)
	}
	str := func(v string) sqstypes.MessageAttributeValue {
		return sqstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}
	if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("m"),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{"bar.one": str("1"), "bar.two": str("2"), "baz": str("3")}}); err != nil {
		t.Fatal(err)
	}
	names := func(sel ...string) map[string]bool {
		r, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, MessageAttributeNames: sel})
		if err != nil || len(r.Messages) != 1 {
			t.Fatalf("receive %v: %v %v", sel, r, err)
		}
		out := map[string]bool{}
		for k := range r.Messages[0].MessageAttributes {
			out[k] = true
		}
		return out
	}
	if got := names("bar.*"); len(got) != 2 || !got["bar.one"] || !got["bar.two"] {
		t.Errorf("bar.* selected %v, want bar.one and bar.two", got)
	}
	if got := names(".*"); len(got) != 3 {
		t.Errorf(".* selected %v, want all three", got)
	}
	if got := names("baz"); len(got) != 1 || !got["baz"] {
		t.Errorf("baz selected %v", got)
	}
	if got := names(); len(got) != 0 {
		t.Errorf("no names selected %v, want none", got)
	}
}

// ReceiveRequestAttemptId: a retry with the same id gets the same messages and
// handles while none has been modified, and starts the timeout again.
func TestReceiveRequestAttemptIdReplaysAReceive(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("retry.fifo"),
		Attributes: map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"g1", "g2"} {
		if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("m-" + g),
			MessageGroupId: aws.String(g)}); err != nil {
			t.Fatal(err)
		}
	}
	recv := func(attempt string) []sqstypes.Message {
		in := &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, MaxNumberOfMessages: 10, VisibilityTimeout: 600}
		if attempt != "" {
			in.ReceiveRequestAttemptId = aws.String(attempt)
		}
		r, err := c.ReceiveMessage(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return r.Messages
	}
	handles := func(ms []sqstypes.Message) map[string]string {
		out := map[string]string{}
		for _, m := range ms {
			out[aws.ToString(m.Body)] = aws.ToString(m.ReceiptHandle)
		}
		return out
	}
	first := handles(recv("try-1"))
	if len(first) != 2 {
		t.Fatalf("first receive: %v", first)
	}
	// Without the id the messages are in flight and nothing comes back.
	if got := recv(""); len(got) != 0 {
		t.Fatalf("an unmatched receive returned %d messages while they were in flight", len(got))
	}
	again := handles(recv("try-1"))
	if len(again) != 2 {
		t.Fatalf("the retry returned %v, want the same two", again)
	}
	for body, h := range first {
		if again[body] != h {
			t.Errorf("%s: the retry's receipt handle %q differs from %q", body, again[body], h)
		}
	}
	// A different id is a different receive: nothing visible.
	if got := recv("try-2"); len(got) != 0 {
		t.Errorf("a new attempt id returned %d messages", len(got))
	}
	// Delete one: the retry no longer matches, and is a new receive of what is
	// visible — which is nothing, since the other is still in flight.
	if _, err := c.DeleteMessage(ctx, &awssqs.DeleteMessageInput{QueueUrl: q.QueueUrl, ReceiptHandle: aws.String(first["m-g1"])}); err != nil {
		t.Fatal(err)
	}
	if got := recv("try-1"); len(got) != 0 {
		t.Errorf("a retry after a delete returned %d messages", len(got))
	}
}

func TestReceiveRequestAttemptIdIsFIFOOnly(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("std-attempt")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl,
		ReceiveRequestAttemptId: aws.String("x")}); err == nil {
		t.Error("a standard queue took a ReceiveRequestAttemptId")
	}
}

// Five minutes after the receive the id no longer matches.
func TestAReceiveAttemptExpiresAfterFiveMinutes(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	if _, err := s.CreateQueue("q.fifo", map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "true", "VisibilityTimeout": "3600"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q.fifo", "m", nil, -1, "g", "", nil); err != nil {
		t.Fatal(err)
	}
	first, err := s.ReceiveAttempt("q.fifo", "a", 1, 0, -1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first: %v %v", first, err)
	}
	now = now.Add(4*time.Minute + 59*time.Second)
	if got, _ := s.ReceiveAttempt("q.fifo", "a", 1, 0, -1); len(got) != 1 {
		t.Errorf("inside the five minutes the retry returned %d", len(got))
	}
	now = now.Add(6 * time.Minute)
	if got, _ := s.ReceiveAttempt("q.fifo", "a", 1, 0, -1); len(got) != 0 {
		t.Errorf("after the five minutes the retry returned %d, want none: the message is still in flight", len(got))
	}
}
