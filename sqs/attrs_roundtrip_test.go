package sqs

// Attribute round-tripping. A queue that accepts an attribute and then reports
// it missing is worse than one that rejects it: Terraform sets the attribute,
// polls GetQueueAttributes until what it reads equals what it wrote, and never
// converges. These tests assert the read-back, not just that the write was
// accepted.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	stypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestQueueAttributesRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	url := aws.ToString(mustQueue(t, ctx, c, "attr-rt", nil))

	// The long tail SQS accepts and doze-aws has no behaviour for: the queue
	// policy that wires S3 and SNS notifications, the SSE pair, and the
	// redrive-source restriction.
	want := map[string]string{
		"KmsMasterKeyId":               "alias/aws/sqs",
		"KmsDataKeyReusePeriodSeconds": "600",
		"SqsManagedSseEnabled":         "true",
		"Policy":                       `{"Version":"2012-10-17","Statement":[]}`,
		"RedriveAllowPolicy":           `{"redrivePermission":"allowAll"}`,
	}
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl: aws.String(url), Attributes: want,
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	got, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(url), AttributeNames: []stypes.QueueAttributeName{stypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}
	for k, v := range want {
		if got.Attributes[k] != v {
			t.Errorf("%s = %q, want %q", k, got.Attributes[k], v)
		}
	}
	// LastModifiedTimestamp is reported by SQS and moves when attributes change.
	if got.Attributes["LastModifiedTimestamp"] == "" {
		t.Error("LastModifiedTimestamp missing")
	}
}

// A computed attribute is the queue's own bookkeeping and must not be
// overwritable by a caller who names it.
func TestComputedAttributesAreNotOverwritable(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	url := aws.ToString(mustQueue(t, ctx, c, "attr-computed", nil))

	_, _ = c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl: aws.String(url),
		Attributes: map[string]string{
			"ApproximateNumberOfMessages": "9999",
			"QueueArn":                    "arn:aws:sqs:us-east-1:000000000000:not-this",
		},
	})
	got, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(url), AttributeNames: []stypes.QueueAttributeName{stypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Attributes["ApproximateNumberOfMessages"] != "0" {
		t.Errorf("message count was overwritten: %q", got.Attributes["ApproximateNumberOfMessages"])
	}
	if got.Attributes["QueueArn"] == "arn:aws:sqs:us-east-1:000000000000:not-this" {
		t.Error("QueueArn was overwritten by the caller")
	}
}

// Clearing an attribute removes it rather than leaving the old value behind.
func TestClearingAnAttributeRemovesIt(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	url := aws.ToString(mustQueue(t, ctx, c, "attr-clear", nil))

	set := func(v string) {
		t.Helper()
		if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
			QueueUrl: aws.String(url), Attributes: map[string]string{"KmsMasterKeyId": v},
		}); err != nil {
			t.Fatal(err)
		}
	}
	set("alias/aws/sqs")
	set("")
	got, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(url), AttributeNames: []stypes.QueueAttributeName{stypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got.Attributes["KmsMasterKeyId"]; ok {
		t.Errorf("KmsMasterKeyId still present after clearing: %q", v)
	}
}
