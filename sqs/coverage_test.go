// Contract tests for the operations added in the doze-aws port: move tasks,
// dead-letter source discovery, tags over the JSON protocol.
package sqs

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestSDKMessageMoveTask(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	dlq := mustQueue(t, ctx, c, "move-dlq", nil)
	dest := mustQueue(t, ctx, c, "move-dest", nil)
	_ = dest
	for _, body := range []string{"m1", "m2", "m3"} {
		if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: dlq, MessageBody: aws.String(body)}); err != nil {
			t.Fatal(err)
		}
	}

	start, err := c.StartMessageMoveTask(ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn:      aws.String("arn:aws:sqs:us-east-1:000000000000:move-dlq"),
		DestinationArn: aws.String("arn:aws:sqs:us-east-1:000000000000:move-dest"),
	})
	if err != nil {
		t.Fatalf("StartMessageMoveTask: %v", err)
	}
	if aws.ToString(start.TaskHandle) == "" {
		t.Fatal("empty task handle")
	}

	// Everything moved: source empty, destination holds all three.
	if out, _ := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: dlq}); len(out.Messages) != 0 {
		t.Fatalf("source still has %d messages", len(out.Messages))
	}
	got, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: dest, MaxNumberOfMessages: 10})
	if err != nil || len(got.Messages) != 3 {
		t.Fatalf("destination: %v, %d messages", err, len(got.Messages))
	}

	list, err := c.ListMessageMoveTasks(ctx, &awssqs.ListMessageMoveTasksInput{
		SourceArn:  aws.String("arn:aws:sqs:us-east-1:000000000000:move-dlq"),
		MaxResults: aws.Int32(10),
	})
	if err != nil || len(list.Results) != 1 {
		t.Fatalf("ListMessageMoveTasks: %v, %d results", err, len(list.Results))
	}
	r := list.Results[0]
	if aws.ToString(r.Status) != "COMPLETED" || r.ApproximateNumberOfMessagesMoved != 3 {
		t.Errorf("task = %+v", r)
	}
}

func TestSDKListDeadLetterSourceQueues(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	dlq := mustQueue(t, ctx, c, "the-dlq", nil)
	rp := `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:the-dlq","maxReceiveCount":"3"}`
	mustQueue(t, ctx, c, "src-a", map[string]string{"RedrivePolicy": rp})
	mustQueue(t, ctx, c, "src-b", map[string]string{"RedrivePolicy": rp})
	mustQueue(t, ctx, c, "unrelated", nil)

	out, err := c.ListDeadLetterSourceQueues(ctx, &awssqs.ListDeadLetterSourceQueuesInput{QueueUrl: dlq})
	if err != nil {
		t.Fatalf("ListDeadLetterSourceQueues: %v", err)
	}
	if len(out.QueueUrls) != 2 {
		t.Fatalf("sources = %v, want 2", out.QueueUrls)
	}
}
