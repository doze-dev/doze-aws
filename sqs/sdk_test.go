package sqs

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func sdkClient(t *testing.T) *awssqs.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awssqs.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

// TestSQSTypedError proves the JSON protocol emits an error the aws-sdk-go-v2
// typed-error machinery matches: errors.As(err, &types.QueueDoesNotExist{}) must
// succeed, the way it does against real AWS (it failed when __type carried the
// legacy Query code instead of the modern shape name).
func TestSQSTypedError(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	_, err := c.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("nope")})
	if err == nil {
		t.Fatal("GetQueueUrl on a missing queue should error")
	}
	var qdne *types.QueueDoesNotExist
	if !errors.As(err, &qdne) {
		t.Fatalf("want typed QueueDoesNotExist, got %T: %v", err, err)
	}
}

// TestSQSSDK drives the real aws-sdk-go-v2 SQS client (JSON protocol) against the
// server. The SDK validates MD5 of body and attributes itself, so a successful
// receive proves checksum compatibility.
func TestSQSSDK(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	t.Run("send/receive/delete with attributes", func(t *testing.T) {
		q := mustQueue(t, ctx, c, "jobs", nil)
		_, err := c.SendMessage(ctx, &awssqs.SendMessageInput{
			QueueUrl: q, MessageBody: aws.String("hi"),
			MessageAttributes: map[string]types.MessageAttributeValue{
				"color": {DataType: aws.String("String"), StringValue: aws.String("blue")},
			},
		})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		out, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl: q, MaxNumberOfMessages: 1, MessageAttributeNames: []string{"All"},
		})
		if err != nil {
			t.Fatalf("ReceiveMessage: %v", err) // also fails on MD5 mismatch
		}
		if len(out.Messages) != 1 || aws.ToString(out.Messages[0].Body) != "hi" {
			t.Fatalf("unexpected messages: %+v", out.Messages)
		}
		if av := out.Messages[0].MessageAttributes["color"]; aws.ToString(av.StringValue) != "blue" {
			t.Fatalf("attribute round-trip failed: %+v", out.Messages[0].MessageAttributes)
		}
		if _, err := c.DeleteMessage(ctx, &awssqs.DeleteMessageInput{QueueUrl: q, ReceiptHandle: out.Messages[0].ReceiptHandle}); err != nil {
			t.Fatalf("DeleteMessage: %v", err)
		}
		if r, _ := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q}); len(r.Messages) != 0 {
			t.Fatalf("message not deleted")
		}
	})

	t.Run("batch send and delete", func(t *testing.T) {
		q := mustQueue(t, ctx, c, "batch", nil)
		_, err := c.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{QueueUrl: q, Entries: []types.SendMessageBatchRequestEntry{
			{Id: aws.String("1"), MessageBody: aws.String("a")},
			{Id: aws.String("2"), MessageBody: aws.String("b")},
		}})
		if err != nil {
			t.Fatalf("SendMessageBatch: %v", err)
		}
		out, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q, MaxNumberOfMessages: 10})
		if err != nil || len(out.Messages) != 2 {
			t.Fatalf("receive batch: %v %d", err, len(out.Messages))
		}
		var del []types.DeleteMessageBatchRequestEntry
		for i, m := range out.Messages {
			del = append(del, types.DeleteMessageBatchRequestEntry{Id: aws.String(string(rune('a' + i))), ReceiptHandle: m.ReceiptHandle})
		}
		res, err := c.DeleteMessageBatch(ctx, &awssqs.DeleteMessageBatchInput{QueueUrl: q, Entries: del})
		if err != nil || len(res.Successful) != 2 {
			t.Fatalf("DeleteMessageBatch: %v %+v", err, res)
		}
	})

	t.Run("long poll wakes on send", func(t *testing.T) {
		q := mustQueue(t, ctx, c, "lp", nil)
		go func() {
			time.Sleep(300 * time.Millisecond)
			_, _ = c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q, MessageBody: aws.String("late")})
		}()
		start := time.Now()
		out, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q, WaitTimeSeconds: 5})
		if err != nil || len(out.Messages) != 1 {
			t.Fatalf("long poll: %v %d", err, len(out.Messages))
		}
		if d := time.Since(start); d < 200*time.Millisecond || d > 4*time.Second {
			t.Fatalf("long poll timing off: %v", d)
		}
	})

	t.Run("FIFO dedup and ordering", func(t *testing.T) {
		q := mustQueue(t, ctx, c, "orders.fifo", map[string]string{
			"FifoQueue": "true", "ContentBasedDeduplication": "true",
		})
		for _, body := range []string{"o1", "o1"} { // second is a content dup
			if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q, MessageBody: aws.String(body), MessageGroupId: aws.String("g")}); err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
		}
		out, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q, MaxNumberOfMessages: 10})
		if err != nil || len(out.Messages) != 1 {
			t.Fatalf("FIFO dedup: %v, got %d messages", err, len(out.Messages))
		}
	})

	t.Run("DLQ redrive", func(t *testing.T) {
		mustQueue(t, ctx, c, "dlq", nil)
		rp := `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:dlq","maxReceiveCount":"2"}`
		main := mustQueue(t, ctx, c, "main", map[string]string{"VisibilityTimeout": "0", "RedrivePolicy": rp})
		if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: main, MessageBody: aws.String("poison")}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ { // receive up to maxReceiveCount
			if out, _ := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: main}); len(out.Messages) != 1 {
				t.Fatalf("receive %d: expected the message", i)
			}
		}
		if out, _ := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: main}); len(out.Messages) != 0 {
			t.Fatalf("expected redrive (empty main)")
		}
		dlqURL := mustQueue(t, ctx, c, "dlq", nil)
		out, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: dlqURL})
		if err != nil || len(out.Messages) != 1 || aws.ToString(out.Messages[0].Body) != "poison" {
			t.Fatalf("expected poison in DLQ: %v %+v", err, out.Messages)
		}
	})
}

func mustQueue(t *testing.T, ctx context.Context, c *awssqs.Client, name string, attrs map[string]string) *string {
	t.Helper()
	out, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name), Attributes: attrs})
	if err != nil {
		t.Fatalf("CreateQueue %s: %v", name, err)
	}
	return out.QueueUrl
}
