package sqs

import (
	"context"
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
