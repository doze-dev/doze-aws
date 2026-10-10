package sqs

// ChangeMessageVisibility on a message that is not in flight is refused, and a
// receipt handle that is not one is refused. Both were accepted: the first
// quietly re-hid a visible message, the second returned 200 for nothing.
//
// doc: the ChangeMessageVisibility API reference (errors MessageNotInflight and
// ReceiptHandleIsInvalid) and the developer guide's visibility timeout page.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

func apiCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}
	return ""
}

func TestChangeVisibilityNeedsAMessageInFlight(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, _ := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("vis")})
	if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("m")}); err != nil {
		t.Fatal(err)
	}
	r, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, VisibilityTimeout: 30})
	if err != nil || len(r.Messages) != 1 {
		t.Fatalf("receive: %v %v", r, err)
	}
	handle := r.Messages[0].ReceiptHandle

	// In flight: fine, and 0 makes it visible again.
	if _, err := c.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
		QueueUrl: q.QueueUrl, ReceiptHandle: handle, VisibilityTimeout: 0}); err != nil {
		t.Fatalf("a message in flight: %v", err)
	}
	// Now it is visible, so it is not in flight.
	_, err = c.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
		QueueUrl: q.QueueUrl, ReceiptHandle: handle, VisibilityTimeout: 60})
	if err == nil {
		t.Fatal("a visible message was re-hidden by a visibility change")
	}
	if code := apiCode(err); code != "MessageNotInflight" && code != "AWS.SimpleQueueService.MessageNotInflight" && code != "InvalidParameterValue" {
		t.Errorf("refused with %q", code)
	}

	// A handle that is not one.
	if _, err := c.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
		QueueUrl: q.QueueUrl, ReceiptHandle: aws.String("nonsense"), VisibilityTimeout: 5}); err == nil {
		t.Error("a malformed receipt handle was accepted")
	}

	// A message that was deleted has no handle that works.
	r2, _ := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, VisibilityTimeout: 30})
	if len(r2.Messages) != 1 {
		t.Fatal("expected the message back")
	}
	if _, err := c.DeleteMessage(ctx, &awssqs.DeleteMessageInput{QueueUrl: q.QueueUrl, ReceiptHandle: r2.Messages[0].ReceiptHandle}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
		QueueUrl: q.QueueUrl, ReceiptHandle: r2.Messages[0].ReceiptHandle, VisibilityTimeout: 5}); err == nil {
		t.Error("a deleted message's handle changed a visibility")
	}

	// A batch fails per entry, with the same codes.
	out, err := c.ChangeMessageVisibilityBatch(ctx, &awssqs.ChangeMessageVisibilityBatchInput{QueueUrl: q.QueueUrl,
		Entries: []types.ChangeMessageVisibilityBatchRequestEntry{
			{Id: aws.String("gone"), ReceiptHandle: r2.Messages[0].ReceiptHandle, VisibilityTimeout: 5},
			{Id: aws.String("junk"), ReceiptHandle: aws.String("nonsense"), VisibilityTimeout: 5},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Failed) != 2 || len(out.Successful) != 0 {
		t.Errorf("both entries should fail alone: %+v", out)
	}
}

// The console's "what changes" says a changed queue visibility timeout leaves
// messages already held on the timeout they were received with, and applies to
// receives from then on. That sentence is backed here.
func TestAQueueVisibilityChangeLeavesHeldMessagesAlone(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	if _, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "30"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"held", "later"} {
		if _, err := s.Send("q", b, nil, -1, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	held, _ := s.Receive("q", 1, 0, -1) // hidden until now+30s
	if len(held) != 1 {
		t.Fatalf("receive: %v", held)
	}
	if err := s.SetAttributes("q", map[string]string{"VisibilityTimeout": "5"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second) // past the new 5s, short of the old 30s
	if got, _ := s.Receive("q", 10, 0, -1); len(got) != 1 || got[0].Body != "later" {
		t.Fatalf("10s on, the held message %v: want only the one never received, still hidden for 30s", got)
	}
	// And the one received now is hidden for the new 5s.
	now = now.Add(6 * time.Second)
	got, _ := s.Receive("q", 10, 0, -1)
	bodies := map[string]bool{}
	for _, m := range got {
		bodies[m.Body] = true
	}
	if !bodies["later"] {
		t.Errorf("a message received under the new timeout was hidden for longer than 5s: %v", bodies)
	}
}
