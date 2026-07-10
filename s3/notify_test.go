package s3_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// TestS3EventNotificationToSQS drives PutBucketNotificationConfiguration and a
// PutObject through a full Stack, asserting the object-created event lands in
// the configured SQS queue with the right shape and honoring a suffix filter.
func TestS3EventNotificationToSQS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	s3c := awss3.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds},
		func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })
	sqsc := awssqs.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds},
		func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	q, err := sqsc.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("s3-events")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("uploads")}); err != nil {
		t.Fatal(err)
	}

	// Notify the queue on object-created for .jpg keys only.
	if _, err := s3c.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String("uploads"),
		NotificationConfiguration: &s3types.NotificationConfiguration{
			QueueConfigurations: []s3types.QueueConfiguration{{
				QueueArn: aws.String("arn:aws:sqs:us-east-1:000000000000:s3-events"),
				Events:   []s3types.Event{"s3:ObjectCreated:*"},
				Filter: &s3types.NotificationConfigurationFilter{
					Key: &s3types.S3KeyFilter{FilterRules: []s3types.FilterRule{
						{Name: s3types.FilterRuleNameSuffix, Value: aws.String(".jpg")},
					}},
				},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketNotificationConfiguration: %v", err)
	}

	// A .txt upload does NOT notify (filtered out); a .jpg does.
	s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("uploads"), Key: aws.String("notes.txt"), Body: strings.NewReader("x")})
	s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("uploads"), Key: aws.String("photo.jpg"), Body: strings.NewReader("img")})

	recv, err := sqsc.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, WaitTimeSeconds: 3, MaxNumberOfMessages: 10,
	})
	if err != nil || len(recv.Messages) != 1 {
		t.Fatalf("ReceiveMessage: %v, %d messages (want 1)", err, len(recv.Messages))
	}
	var envelope struct {
		Records []struct {
			EventName string `json:"eventName"`
			S3        struct {
				Bucket struct{ Name string } `json:"bucket"`
				Object struct{ Key string }  `json:"object"`
			} `json:"s3"`
		} `json:"Records"`
	}
	if err := json.Unmarshal([]byte(aws.ToString(recv.Messages[0].Body)), &envelope); err != nil {
		t.Fatalf("notification body: %v\n%s", err, aws.ToString(recv.Messages[0].Body))
	}
	if len(envelope.Records) != 1 {
		t.Fatalf("records = %d", len(envelope.Records))
	}
	rec := envelope.Records[0]
	if !strings.HasPrefix(rec.EventName, "ObjectCreated:") || rec.S3.Bucket.Name != "uploads" || rec.S3.Object.Key != "photo.jpg" {
		t.Fatalf("event record = %+v", rec)
	}

}
