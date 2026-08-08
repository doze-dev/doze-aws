package sqs

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestChangeMessageVisibilityBatch covers the operation the botocore diff found
// missing: the docs claimed tier F while the dispatch table had no entry.
func TestChangeMessageVisibilityBatch(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)

	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("batchvis")})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{
			QueueUrl: q.QueueUrl, MessageBody: aws.String(fmt.Sprintf("m%d", i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	recv, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, MaxNumberOfMessages: 3,
	})
	if err != nil || len(recv.Messages) == 0 {
		t.Fatalf("ReceiveMessage = %v, %v", recv, err)
	}

	entries := make([]sqstypes.ChangeMessageVisibilityBatchRequestEntry, 0, len(recv.Messages)+1)
	for i, m := range recv.Messages {
		entries = append(entries, sqstypes.ChangeMessageVisibilityBatchRequestEntry{
			Id: aws.String(fmt.Sprintf("e%d", i)), ReceiptHandle: m.ReceiptHandle,
			VisibilityTimeout: 60,
		})
	}
	// One deliberately bad handle, to prove failures are reported per entry
	// rather than failing the whole call.
	entries = append(entries, sqstypes.ChangeMessageVisibilityBatchRequestEntry{
		Id: aws.String("bogus"), ReceiptHandle: aws.String("not-a-handle"), VisibilityTimeout: 60,
	})

	out, err := c.ChangeMessageVisibilityBatch(ctx, &awssqs.ChangeMessageVisibilityBatchInput{
		QueueUrl: q.QueueUrl, Entries: entries,
	})
	if err != nil {
		t.Fatalf("ChangeMessageVisibilityBatch: %v", err)
	}
	if len(out.Successful) != len(recv.Messages) {
		t.Fatalf("Successful = %d, want %d", len(out.Successful), len(recv.Messages))
	}
	if len(out.Failed) != 1 || aws.ToString(out.Failed[0].Id) != "bogus" {
		t.Fatalf("Failed = %+v, want one entry for the bad handle", out.Failed)
	}
}

// TestRedrivePolicyMaxReceiveCountIsNumber is a Terraform regression.
//
// AWS returns maxReceiveCount as a JSON NUMBER. Terraform writes the redrive
// policy then polls GetQueueAttributes until what it reads back equals what it
// wrote — a stringified count never compares equal, so the resource spun for
// three minutes and then failed.
func TestRedrivePolicyMaxReceiveCountIsNumber(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dlq")})
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String("main"),
		Attributes: map[string]string{
			"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:dlq","maxReceiveCount":4}`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		DeadLetterTargetArn string          `json:"deadLetterTargetArn"`
		MaxReceiveCount     json.RawMessage `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(out.Attributes["RedrivePolicy"]), &got); err != nil {
		t.Fatalf("RedrivePolicy is not JSON: %v", err)
	}
	if string(got.MaxReceiveCount) != "4" {
		t.Fatalf("maxReceiveCount = %s, want the bare number 4 (a quoted string breaks Terraform)",
			got.MaxReceiveCount)
	}
	if got.DeadLetterTargetArn == "" {
		t.Fatal("deadLetterTargetArn missing")
	}
}
