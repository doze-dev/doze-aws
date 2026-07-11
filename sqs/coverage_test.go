package sqs

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestSDKQueueAdmin(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	q1, _ := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("q1")})
	c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("q2")})

	// ListQueues.
	lq, err := c.ListQueues(ctx, &awssqs.ListQueuesInput{})
	if err != nil || len(lq.QueueUrls) != 2 {
		t.Fatalf("ListQueues = %d err=%v", len(lq.QueueUrls), err)
	}

	// SetQueueAttributes.
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl:   q1.QueueUrl,
		Attributes: map[string]string{"VisibilityTimeout": "45"},
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	// Send, receive, change visibility, purge.
	c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q1.QueueUrl, MessageBody: aws.String("m")})
	rc, _ := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q1.QueueUrl})
	if len(rc.Messages) == 1 {
		if _, err := c.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
			QueueUrl: q1.QueueUrl, ReceiptHandle: rc.Messages[0].ReceiptHandle, VisibilityTimeout: 0,
		}); err != nil {
			t.Fatalf("ChangeMessageVisibility: %v", err)
		}
	}
	if _, err := c.PurgeQueue(ctx, &awssqs.PurgeQueueInput{QueueUrl: q1.QueueUrl}); err != nil {
		t.Fatalf("PurgeQueue: %v", err)
	}

	// Permissions round-trip.
	if _, err := c.AddPermission(ctx, &awssqs.AddPermissionInput{
		QueueUrl: q1.QueueUrl, Label: aws.String("p"),
		AWSAccountIds: []string{"000000000000"}, Actions: []string{"SendMessage"},
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	if _, err := c.RemovePermission(ctx, &awssqs.RemovePermissionInput{QueueUrl: q1.QueueUrl, Label: aws.String("p")}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}

	// DeleteQueue.
	if _, err := c.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: q1.QueueUrl}); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	_ = sqstypes.QueueAttributeName("")
}

func TestSDKMessageMoveTasks(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	// A DLQ and a main queue whose redrive policy names it — so the DLQ can
	// report its source queue and messages can be redriven back.
	dlq, _ := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("mmt-dlq")})
	dlqArn, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: dlq.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatal(err)
	}
	arn := dlqArn.Attributes["QueueArn"]
	main, _ := c.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("mmt-main"),
		Attributes: map[string]string{
			"RedrivePolicy": `{"deadLetterTargetArn":"` + arn + `","maxReceiveCount":"1"}`,
		},
	})

	// The DLQ now lists the main queue as a dead-letter source.
	src, err := c.ListDeadLetterSourceQueues(ctx, &awssqs.ListDeadLetterSourceQueuesInput{QueueUrl: dlq.QueueUrl})
	if err != nil || len(src.QueueUrls) == 0 {
		t.Fatalf("ListDeadLetterSourceQueues = %+v err=%v", src, err)
	}

	// Park a message in the DLQ, then redrive it back to the main queue.
	c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: dlq.QueueUrl, MessageBody: aws.String("parked")})
	mainArn := "arn:aws:sqs:us-east-1:000000000000:mmt-main"
	start, err := c.StartMessageMoveTask(ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(arn), DestinationArn: aws.String(mainArn),
	})
	if err != nil || aws.ToString(start.TaskHandle) == "" {
		t.Fatalf("StartMessageMoveTask = %+v err=%v", start, err)
	}
	list, err := c.ListMessageMoveTasks(ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(arn)})
	if err != nil || len(list.Results) == 0 {
		t.Fatalf("ListMessageMoveTasks = %+v err=%v", list, err)
	}
	// Cancel is best-effort (task may already be complete); it must not error on a live handle.
	c.CancelMessageMoveTask(ctx, &awssqs.CancelMessageMoveTaskInput{TaskHandle: start.TaskHandle})
	_ = main
}

// hDozePeek — the doze dashboard's non-destructive queue peek extension. It is
// not an SDK operation, so drive it as a raw JSON1.0 action.
func TestDozePeekExtension(t *testing.T) {
	ts := testServer(t)
	qurl := ts.URL + "/000000000000/peekq"
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"peekq"}`)
	jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+qurl+`","MessageBody":"m1"}`)

	// Peek twice; it must not consume, so the second call still sees the message
	// and neither bumps the receive count.
	out := jsonCall(t, ts.URL, "DozePeek", `{"QueueUrl":"`+qurl+`"}`)
	if !strings.Contains(out, "m1") {
		t.Fatalf("DozePeek missing message: %s", out)
	}
	if out2 := jsonCall(t, ts.URL, "DozePeek", `{"QueueUrl":"`+qurl+`"}`); !strings.Contains(out2, "m1") {
		t.Fatalf("DozePeek consumed the message (not idempotent): %s", out2)
	}
}
