package dozeaws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// SQS compares the account in a queue URL with its own (SQS-23), so the
// helpers other services use to reach a queue must not name an account that is
// not this stack's. They did: peercall built every URL from the default account
// constant, and an EventBridge rule, an S3 notification or a Lambda trigger
// could not deliver to a queue under any other --account-id (SQS-30, LAM-36).
func TestPeerHelpersReachAQueueUnderAnotherAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	const account = "111122223333"
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t),
		Services: []string{"sqs"}, Identity: awsident.Identity{Region: "eu-west-1", AccountID: account}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)

	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	c := awssqs.NewFromConfig(aws.Config{Region: "eu-west-1", Credentials: creds},
		func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("jobs")})
	if err != nil {
		t.Fatal(err)
	}
	dir := peers.Static{"sqs": peers.Endpoint{Client: http.DefaultClient, BaseURL: ts.URL}}

	if err := peercall.SQSSend(ctx, dir, "jobs", "one", nil); err != nil {
		t.Errorf("SQSSend: %v", err)
	}
	if _, _, err := peercall.SQSSendDetailed(ctx, dir, "jobs", "two", nil); err != nil {
		t.Errorf("SQSSendDetailed: %v", err)
	}
	msgs, err := peercall.SQSReceive(ctx, dir, "jobs", 10, 0)
	if err != nil {
		t.Fatalf("SQSReceive: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("SQSReceive returned %d messages, want the two sent", len(msgs))
	}
	for _, m := range msgs {
		if err := peercall.SQSDelete(ctx, dir, "jobs", m.ReceiptHandle); err != nil {
			t.Errorf("SQSDelete: %v", err)
		}
	}
	// And the other side still holds: a URL that does name another account's
	// queue is not this one.
	other := *q.QueueUrl
	other = other[:len(other)-len(account)-len("/jobs")] + "999999999999/jobs"
	if _, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(other)}); err == nil {
		t.Errorf("a queue URL in another account was served")
	}
}

// The same, end to end: an SNS subscription, an EventBridge rule and an S3
// bucket notification each deliver to a queue in a stack under another account
// and Region. The helper test above proves the four calls; this proves the
// three services that make them.
func TestOtherServicesDeliverToAQueueUnderAnotherAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	const account, region = "111122223333", "eu-west-1"
	ctx := context.Background()
	id := awsident.Identity{Region: region, AccountID: account}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t),
		Services: []string{"sqs", "sns", "eventbridge", "s3"}, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	cfg := aws.Config{Region: region, Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	sqsc := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	snsc := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ebc := awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })

	queue := func(name string) (url, arn string) {
		q, err := sqsc.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name)})
		if err != nil {
			t.Fatal(err)
		}
		return *q.QueueUrl, id.ARN("sqs", name)
	}
	arrives := func(what, qurl string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			out, err := sqsc.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(qurl), WaitTimeSeconds: 1})
			if err != nil {
				t.Fatalf("%s: receive: %v", what, err)
			}
			if len(out.Messages) > 0 {
				return
			}
		}
		t.Errorf("%s: nothing reached the queue under account %s", what, account)
	}

	// SNS.
	snsURL, snsARN := queue("from-sns")
	topic, err := snsc.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("t")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snsc.Subscribe(ctx, &awssns.SubscribeInput{TopicArn: topic.TopicArn, Protocol: aws.String("sqs"), Endpoint: aws.String(snsARN)}); err != nil {
		t.Fatal(err)
	}
	if _, err := snsc.Publish(ctx, &awssns.PublishInput{TopicArn: topic.TopicArn, Message: aws.String("hello")}); err != nil {
		t.Fatal(err)
	}
	arrives("an SNS subscription", snsURL)

	// EventBridge.
	ebURL, ebARN := queue("from-eb")
	if _, err := ebc.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String("r"), EventPattern: aws.String(`{"source":["my.app"]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := ebc.PutTargets(ctx, &awseb.PutTargetsInput{Rule: aws.String("r"), Targets: []ebtypes.Target{{Id: aws.String("q"), Arn: aws.String(ebARN)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ebc.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{{Source: aws.String("my.app"), DetailType: aws.String("x"), Detail: aws.String("{}")}}}); err != nil {
		t.Fatal(err)
	}
	arrives("an EventBridge rule", ebURL)

	// S3.
	s3URL, s3ARN := queue("from-s3")
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("uploads"),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{LocationConstraint: s3types.BucketLocationConstraint(region)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{Bucket: aws.String("uploads"),
		NotificationConfiguration: &s3types.NotificationConfiguration{QueueConfigurations: []s3types.QueueConfiguration{
			{QueueArn: aws.String(s3ARN), Events: []s3types.Event{s3types.EventS3ObjectCreated}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("uploads"), Key: aws.String("k"), Body: strings.NewReader("x")}); err != nil {
		t.Fatal(err)
	}
	arrives("an S3 bucket notification", s3URL)
}
