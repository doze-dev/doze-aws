package dozeaws_test

// What SQS keeps across a restart that is not a message body: the clock a
// receive started, the window a deduplication id holds, the record of a move
// task, and what a queue was configured with.
//
// TestPersistenceAcrossRestart proves a message is still there. The stage-1
// plan asks for more: "everything above survives a restart, including
// in-flight timers and dedup state". A message that came back visible, or a
// duplicate that was accepted because the window was forgotten, would pass the
// older test and be wrong.
//
// The values are not the defaults, so a stack that lost the record and
// re-seeded it would fail.

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestSQSStateSurvivesARestart(t *testing.T) {
	type written struct {
		flightURL, receipt string // a message in flight for an hour
		fifoURL, dedupID   string // a FIFO send whose window is open
		srcURL, dlqARN     string
	}
	persistsAcrossRestart(t,
		func(ctx context.Context, url string) written {
			c := clientsFor(url).sqs
			var w written
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}

			// A message received with an hour's visibility timeout.
			q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("flight")})
			must(err)
			w.flightURL = aws.ToString(q.QueueUrl)
			_, err = c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("m1")})
			must(err)
			rec, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, VisibilityTimeout: 3600})
			must(err)
			if len(rec.Messages) != 1 {
				t.Fatalf("received %d messages, want 1", len(rec.Messages))
			}
			w.receipt = aws.ToString(rec.Messages[0].ReceiptHandle)

			// A FIFO send, so its deduplication id is held.
			fq, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("window.fifo"),
				Attributes: map[string]string{"FifoQueue": "true"}})
			must(err)
			w.fifoURL = aws.ToString(fq.QueueUrl)
			sent, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: fq.QueueUrl, MessageBody: aws.String("a"),
				MessageGroupId: aws.String("g"), MessageDeduplicationId: aws.String("d1")})
			must(err)
			w.dedupID = aws.ToString(sent.MessageId)

			// A dead-letter pair with a message moved and then redriven, a
			// permission, and a tag.
			dlq, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dead")})
			must(err)
			dlqAttrs, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: dlq.QueueUrl,
				AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
			must(err)
			w.dlqARN = dlqAttrs.Attributes["QueueArn"]
			src, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("src"),
				Attributes: map[string]string{
					"VisibilityTimeout": "0",
					"RedrivePolicy":     `{"deadLetterTargetArn":"` + w.dlqARN + `","maxReceiveCount":1}`,
				},
				Tags: map[string]string{"team": "payments"}})
			must(err)
			w.srcURL = aws.ToString(src.QueueUrl)
			_, err = c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: src.QueueUrl, MessageBody: aws.String("poison")})
			must(err)
			// Received once (count 1 reaches the max), then the next receive
			// moves it to the dead-letter queue.
			for i := 0; i < 2; i++ {
				_, err = c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: src.QueueUrl})
				must(err)
			}
			_, err = c.AddPermission(ctx, &awssqs.AddPermissionInput{QueueUrl: src.QueueUrl, Label: aws.String("friend"),
				AWSAccountIds: []string{"111122223333"}, Actions: []string{"SendMessage"}})
			must(err)
			task, err := c.StartMessageMoveTask(ctx, &awssqs.StartMessageMoveTaskInput{SourceArn: aws.String(w.dlqARN)})
			must(err)
			if task.TaskHandle == nil {
				t.Fatal("StartMessageMoveTask returned no handle")
			}
			return w
		},
		func(ctx context.Context, url string, w written) {
			c := clientsFor(url).sqs

			// In flight still: nothing to receive, and the old receipt handle
			// is still honoured.
			rec, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(w.flightURL)})
			if err != nil {
				t.Fatal(err)
			}
			if len(rec.Messages) != 0 {
				t.Errorf("a message in flight for an hour was visible after a restart: %v", rec.Messages)
			}
			if _, err := c.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
				QueueUrl: aws.String(w.flightURL), ReceiptHandle: aws.String(w.receipt), VisibilityTimeout: 0}); err != nil {
				t.Errorf("the receipt handle did not survive the restart: %v", err)
			}
			rec, err = c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(w.flightURL),
				MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{sqstypes.MessageSystemAttributeNameApproximateReceiveCount}})
			if err != nil {
				t.Fatal(err)
			}
			if len(rec.Messages) != 1 || rec.Messages[0].Attributes["ApproximateReceiveCount"] != "2" {
				t.Errorf("after the timeout was cut the message was %+v, want it back with a receive count of 2", rec.Messages)
			}

			// The deduplication window is still open: the same id is the same
			// message, not a second one.
			again, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(w.fifoURL), MessageBody: aws.String("a"),
				MessageGroupId: aws.String("g"), MessageDeduplicationId: aws.String("d1")})
			if err != nil {
				t.Fatal(err)
			}
			if got := aws.ToString(again.MessageId); got != w.dedupID {
				t.Errorf("a duplicate sent after a restart got a new message id %s, want %s", got, w.dedupID)
			}
			attrs, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(w.fifoURL),
				AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages}})
			if err != nil {
				t.Fatal(err)
			}
			if got := attrs.Attributes["ApproximateNumberOfMessages"]; got != "1" {
				t.Errorf("the FIFO queue holds %s messages after a duplicate, want 1", got)
			}

			// The move task is on record, finished, with what it moved.
			tasks, err := c.ListMessageMoveTasks(ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(w.dlqARN)})
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks.Results) != 1 {
				t.Fatalf("%d move tasks on record after a restart, want 1", len(tasks.Results))
			}
			// A finished task lists no handle (doc: only a RUNNING one does).
			if r := tasks.Results[0]; r.TaskHandle != nil ||
				aws.ToString(r.Status) != "COMPLETED" || r.ApproximateNumberOfMessagesMoved != 1 {
				t.Errorf("the move task reads %+v after a restart", r)
			}

			// And what the queue was configured with.
			sa, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(w.srcURL),
				AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(sa.Attributes["RedrivePolicy"], w.dlqARN) {
				t.Errorf("RedrivePolicy after a restart: %q", sa.Attributes["RedrivePolicy"])
			}
			if !strings.Contains(sa.Attributes["Policy"], `"friend"`) {
				t.Errorf("the permission was lost: Policy = %q", sa.Attributes["Policy"])
			}
			tags, err := c.ListQueueTags(ctx, &awssqs.ListQueueTagsInput{QueueUrl: aws.String(w.srcURL)})
			if err != nil {
				t.Fatal(err)
			}
			if tags.Tags["team"] != "payments" {
				t.Errorf("tags after a restart: %v", tags.Tags)
			}
		})
}
