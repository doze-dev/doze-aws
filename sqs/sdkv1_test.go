// SDK v1 contract tests: the legacy aws-sdk-go (v1) client speaks the Query/XML
// protocol against SQS — the wire format modern SDKs no longer exercise.
package sqs

import (
	"net/http/httptest"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	sqsv1 "github.com/aws/aws-sdk-go/service/sqs"
)

func sdkV1Client(t *testing.T) *sqsv1.SQS {
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
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion("us-east-1").
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials("test", "test", "")))
	if err != nil {
		t.Fatal(err)
	}
	return sqsv1.New(sess)
}

// TestSDKV1RoundTrip drives create/send/receive/delete over the legacy Query
// protocol. The v1 SDK validates MD5OfBody and MD5OfMessageAttributes
// client-side, so a clean receive proves checksum compatibility on this wire
// format too.
func TestSDKV1RoundTrip(t *testing.T) {
	c := sdkV1Client(t)

	q, err := c.CreateQueue(&sqsv1.CreateQueueInput{QueueName: awsv1.String("legacy")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	_, err = c.SendMessage(&sqsv1.SendMessageInput{
		QueueUrl:    q.QueueUrl,
		MessageBody: awsv1.String("from-v1"),
		MessageAttributes: map[string]*sqsv1.MessageAttributeValue{
			"origin": {DataType: awsv1.String("String"), StringValue: awsv1.String("sdk-v1")},
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	out, err := c.ReceiveMessage(&sqsv1.ReceiveMessageInput{
		QueueUrl:              q.QueueUrl,
		MaxNumberOfMessages:   awsv1.Int64(1),
		MessageAttributeNames: []*string{awsv1.String("All")},
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err) // fails on MD5 mismatch too
	}
	if len(out.Messages) != 1 || awsv1.StringValue(out.Messages[0].Body) != "from-v1" {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if av := out.Messages[0].MessageAttributes["origin"]; av == nil || awsv1.StringValue(av.StringValue) != "sdk-v1" {
		t.Fatalf("attributes = %+v", out.Messages[0].MessageAttributes)
	}
	if _, err := c.DeleteMessage(&sqsv1.DeleteMessageInput{
		QueueUrl: q.QueueUrl, ReceiptHandle: out.Messages[0].ReceiptHandle,
	}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
}

func TestSDKV1QueueAttributesAndTags(t *testing.T) {
	c := sdkV1Client(t)

	q, err := c.CreateQueue(&sqsv1.CreateQueueInput{
		QueueName:  awsv1.String("tagged"),
		Attributes: map[string]*string{"VisibilityTimeout": awsv1.String("7")},
		Tags:       map[string]*string{"env": awsv1.String("dev")},
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	attrs, err := c.GetQueueAttributes(&sqsv1.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []*string{awsv1.String("All")},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}
	if got := awsv1.StringValue(attrs.Attributes["VisibilityTimeout"]); got != "7" {
		t.Errorf("VisibilityTimeout = %q", got)
	}
	if got := awsv1.StringValue(attrs.Attributes["QueueArn"]); got != "arn:aws:sqs:us-east-1:000000000000:tagged" {
		t.Errorf("QueueArn = %q", got)
	}

	tags, err := c.ListQueueTags(&sqsv1.ListQueueTagsInput{QueueUrl: q.QueueUrl})
	if err != nil {
		t.Fatalf("ListQueueTags: %v", err)
	}
	if got := awsv1.StringValue(tags.Tags["env"]); got != "dev" {
		t.Errorf("tag env = %q", got)
	}
	if _, err := c.TagQueue(&sqsv1.TagQueueInput{
		QueueUrl: q.QueueUrl, Tags: map[string]*string{"team": awsv1.String("core")},
	}); err != nil {
		t.Fatalf("TagQueue: %v", err)
	}
	if _, err := c.UntagQueue(&sqsv1.UntagQueueInput{
		QueueUrl: q.QueueUrl, TagKeys: []*string{awsv1.String("env")},
	}); err != nil {
		t.Fatalf("UntagQueue: %v", err)
	}
	tags, _ = c.ListQueueTags(&sqsv1.ListQueueTagsInput{QueueUrl: q.QueueUrl})
	if _, still := tags.Tags["env"]; still || awsv1.StringValue(tags.Tags["team"]) != "core" {
		t.Errorf("tags after mutation = %+v", tags.Tags)
	}
}

func TestSDKV1ErrorEnvelope(t *testing.T) {
	c := sdkV1Client(t)
	_, err := c.GetQueueUrl(&sqsv1.GetQueueUrlInput{QueueName: awsv1.String("does-not-exist")})
	if err == nil {
		t.Fatal("want error for missing queue")
	}
	// The v1 SDK must parse the XML error envelope into a coded awserr.
	if !containsCode(err, "AWS.SimpleQueueService.NonExistentQueue") {
		t.Errorf("error = %v", err)
	}
}

func containsCode(err error, code string) bool {
	type coder interface{ Code() string }
	if c, ok := err.(coder); ok {
		return c.Code() == code
	}
	return false
}
