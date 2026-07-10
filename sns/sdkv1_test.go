// SDK v1 contract tests: the legacy aws-sdk-go (v1) SNS client (Query/XML with
// SigV4) against the full stack, including cross-service fanout to SQS.
package sns_test

import (
	"net/http/httptest"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	snsv1 "github.com/aws/aws-sdk-go/service/sns"
	sqsv1 "github.com/aws/aws-sdk-go/service/sqs"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func startStackURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestSDKV1PublishFanout(t *testing.T) {
	url := startStackURL(t)
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(url).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	snsC := snsv1.New(sess)
	sqsC := sqsv1.New(sess)

	q, err := sqsC.CreateQueue(&sqsv1.CreateQueueInput{QueueName: awsv1.String("v1-fan")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	topicOut, err := snsC.CreateTopic(&snsv1.CreateTopicInput{Name: awsv1.String("v1-topic")})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if _, err := snsC.Subscribe(&snsv1.SubscribeInput{
		TopicArn: topicOut.TopicArn,
		Protocol: awsv1.String("sqs"),
		Endpoint: awsv1.String("arn:aws:sqs:us-east-1:000000000000:v1-fan"),
		Attributes: map[string]*string{
			"RawMessageDelivery": awsv1.String("true"),
		},
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := snsC.Publish(&snsv1.PublishInput{
		TopicArn: topicOut.TopicArn, Message: awsv1.String("v1 says hi"),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	out, err := sqsC.ReceiveMessage(&sqsv1.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, WaitTimeSeconds: awsv1.Int64(3),
	})
	if err != nil || len(out.Messages) != 1 || awsv1.StringValue(out.Messages[0].Body) != "v1 says hi" {
		t.Fatalf("fanout via v1: %v %+v", err, out.Messages)
	}

	// List/attribute surface parses in the v1 SDK too.
	topics, err := snsC.ListTopics(&snsv1.ListTopicsInput{})
	if err != nil || len(topics.Topics) != 1 {
		t.Fatalf("ListTopics: %v %+v", err, topics)
	}
	subs, err := snsC.ListSubscriptionsByTopic(&snsv1.ListSubscriptionsByTopicInput{TopicArn: topicOut.TopicArn})
	if err != nil || len(subs.Subscriptions) != 1 || awsv1.StringValue(subs.Subscriptions[0].Protocol) != "sqs" {
		t.Fatalf("ListSubscriptionsByTopic: %v %+v", err, subs)
	}
}

// TestSDKV1MobilePushIsHonestStub verifies Tier S ops answer with a parseable
// coded error rather than pretending.
func TestSDKV1MobilePushIsHonestStub(t *testing.T) {
	url := startStackURL(t)
	sess, _ := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(url).
		WithCredentials(credsv1.NewStaticCredentials("test", "test", "")))
	snsC := snsv1.New(sess)

	_, err := snsC.CreatePlatformApplication(&snsv1.CreatePlatformApplicationInput{
		Name: awsv1.String("app"), Platform: awsv1.String("GCM"),
		Attributes: map[string]*string{},
	})
	if err == nil {
		t.Fatal("want honest error for mobile push")
	}
	type coder interface{ Code() string }
	if c, ok := err.(coder); !ok || c.Code() != "InvalidAction" {
		t.Errorf("error = %v", err)
	}
}
