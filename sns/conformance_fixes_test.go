package sns_test

// Three refusals the boto3 conformance suite found missing
// (conformance/tests/test_sns.py). The service model types all three inputs as
// bare strings, so the model-derived audit had nothing to derive.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

func wantInvalid(t *testing.T, what string, err error, message string) {
	t.Helper()
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "InvalidParameter" || api.ErrorMessage() != message {
		t.Errorf("%s: got %v, want InvalidParameter %q", what, err, message)
	}
}

func TestTopicNamesAreHeldToWhatSNSAccepts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	ctx := context.Background()
	c, _ := startStack(t)

	for _, name := range []string{"orders", "Orders_2-b", strings.Repeat("x", 256)} {
		if _, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String(name)}); err != nil {
			t.Errorf("CreateTopic(%.20q): %v", name, err)
		}
	}
	// ".fifo" and FifoTopic say the same thing, or the topic is refused.
	fifo := map[string]string{"FifoTopic": "true"}
	if _, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("orders.fifo"), Attributes: fifo}); err != nil {
		t.Errorf("CreateTopic(orders.fifo, FifoTopic): %v", err)
	}
	_, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("plain"), Attributes: fifo})
	wantInvalid(t, "FifoTopic without the suffix", err, "Invalid parameter: Topic Name")
	for _, name := range []string{"not a topic name!", "dots.in.it", "slash/ed", "suffix-only.fifo", strings.Repeat("x", 257)} {
		_, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String(name)})
		wantInvalid(t, "CreateTopic("+name[:8]+"…)", err, "Invalid parameter: Topic Name")
	}
}

func TestPublishRefusesAnEmptyMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	ctx := context.Background()
	c, _ := startStack(t)
	arn := createTopic(t, ctx, c, "empty")
	_, err := c.Publish(ctx, &awssns.PublishInput{TopicArn: aws.String(arn), Message: aws.String("")})
	wantInvalid(t, "Publish", err, "Invalid parameter: Empty message")
}

func TestSetTopicAttributesRefusesAnUnknownName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	ctx := context.Background()
	c, _ := startStack(t)
	arn := createTopic(t, ctx, c, "attrs")
	set := func(name string) error {
		_, err := c.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
			TopicArn: aws.String(arn), AttributeName: aws.String(name), AttributeValue: aws.String("x")})
		return err
	}
	for _, name := range []string{"DisplayName", "SQSSuccessFeedbackRoleArn", "LambdaFailureFeedbackRoleArn", "HTTPSuccessFeedbackSampleRate"} {
		if err := set(name); err != nil {
			t.Errorf("SetTopicAttributes(%s): %v", name, err)
		}
	}
	for _, name := range []string{"DisplyName", "FifoTopic", "SuccessFeedbackRoleArn", "KafkaSuccessFeedbackRoleArn"} {
		wantInvalid(t, "SetTopicAttributes("+name+")", set(name), "Invalid parameter: AttributeName")
	}
	// And the refused name was not stored.
	got, err := c.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Attributes["DisplyName"]; ok {
		t.Error("a refused attribute name was stored anyway")
	}
}

func TestPublishBatchIsRefusedWholeForItsShape(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	ctx := context.Background()
	c, _ := startStack(t)
	arn := createTopic(t, ctx, c, "batches")
	entry := func(id string) snstypes.PublishBatchRequestEntry {
		return snstypes.PublishBatchRequestEntry{Id: aws.String(id), Message: aws.String("x")}
	}
	eleven := make([]snstypes.PublishBatchRequestEntry, 11)
	for i := range eleven {
		eleven[i] = entry(string(rune('a' + i)))
	}
	for _, tc := range []struct {
		code    string
		entries []snstypes.PublishBatchRequestEntry
	}{
		{"EmptyBatchRequest", []snstypes.PublishBatchRequestEntry{}},
		{"TooManyEntriesInBatchRequest", eleven},
		{"BatchEntryIdsNotDistinct", []snstypes.PublishBatchRequestEntry{entry("a"), entry("a")}},
	} {
		_, err := c.PublishBatch(ctx, &awssns.PublishBatchInput{TopicArn: aws.String(arn), PublishBatchRequestEntries: tc.entries})
		var api smithy.APIError
		if !errors.As(err, &api) || api.ErrorCode() != tc.code {
			t.Errorf("got %v, want %s", err, tc.code)
		}
	}
}

// The arrangement FIFO topics exist for: a FIFO topic fanning out to a FIFO
// queue. Every call used to succeed and nothing was delivered.
func TestFIFOTopicDeliversToAFIFOQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	ctx := context.Background()
	c, q := startStack(t)
	topic, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("orders.fifo"),
		Attributes: map[string]string{"FifoTopic": "true", "ContentBasedDeduplication": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	made, err := q.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders.fifo"),
		Attributes: map[string]string{"FifoQueue": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	subscribe(t, ctx, c, aws.ToString(topic.TopicArn), "sqs", queueARNOf(t, ctx, q, aws.ToString(made.QueueUrl)),
		map[string]string{"RawMessageDelivery": "true"})

	_, err = c.Publish(ctx, &awssns.PublishInput{TopicArn: topic.TopicArn, Message: aws.String("x")})
	wantInvalid(t, "publish without a group", err, "Invalid parameter: The MessageGroupId parameter is required for FIFO topics")

	out, err := c.Publish(ctx, &awssns.PublishInput{TopicArn: topic.TopicArn, Message: aws.String("first"), MessageGroupId: aws.String("g")})
	if err != nil || len(aws.ToString(out.SequenceNumber)) != 20 {
		t.Fatalf("publish: %v, SequenceNumber %q", err, aws.ToString(out.SequenceNumber))
	}
	// The same content again is a duplicate, and the queue takes it once.
	if _, err := c.Publish(ctx, &awssns.PublishInput{TopicArn: topic.TopicArn, Message: aws.String("first"), MessageGroupId: aws.String("g")}); err != nil {
		t.Fatal(err)
	}
	got, err := q.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: made.QueueUrl, MaxNumberOfMessages: 10,
		WaitTimeSeconds: 2, MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{"MessageGroupId"}})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("received %d messages (%v), want the one", len(got.Messages), err)
	}
	if m := got.Messages[0]; aws.ToString(m.Body) != "first" || m.Attributes["MessageGroupId"] != "g" {
		t.Fatalf("delivered %q in group %q", aws.ToString(m.Body), m.Attributes["MessageGroupId"])
	}
}
