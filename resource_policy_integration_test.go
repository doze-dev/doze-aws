package dozeaws_test

// Service-to-service triggers under IAM enforce: a peer call carries a
// service principal and no identity policy, so the target's resource policy
// is the only thing that can admit it — exactly as on AWS, where an S3
// notification needs a Lambda permission and an SNS subscription needs a
// queue policy.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

func enforceStack(t *testing.T, services ...string) (aws.Config, string) {
	t.Helper()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf, IAMMode: iam.ModeEnforce, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	return aws.Config{Region: awsident.Region, Credentials: creds}, ts.URL
}

// markerStaysEmpty asserts nothing reached the marker for a while.
func markerStaysEmpty(t *testing.T, path, unwanted string) {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	if b, _ := os.ReadFile(path); strings.Contains(string(b), unwanted) {
		t.Fatalf("delivery went through without a permission:\n%s", b)
	}
}

// TestEnforceS3ToLambdaNeedsPermission: the notification is refused until
// AddPermission admits s3.amazonaws.com for the bucket.
func TestEnforceS3ToLambdaNeedsPermission(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
	}
	ctx := context.Background()
	cfg, url := enforceStack(t, "iam", "s3", "lambda")
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) })
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true })

	marker := filepath.Join(t.TempDir(), "invocations.log")
	fnARN := awsident.ARN("lambda", "function:sink")
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("sink"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String("arn:aws:iam::000000000000:role/r"),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("uploads")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String("uploads"),
		NotificationConfiguration: &s3types.NotificationConfiguration{
			LambdaFunctionConfigurations: []s3types.LambdaFunctionConfiguration{{
				LambdaFunctionArn: aws.String(fnARN), Events: []s3types.Event{"s3:ObjectCreated:*"},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketNotificationConfiguration: %v", err)
	}

	put := func(key string) {
		t.Helper()
		if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("uploads"), Key: aws.String(key), Body: strings.NewReader("hi")}); err != nil {
			t.Fatalf("PutObject: %v", err)
		}
	}
	put("before-permission")
	markerStaysEmpty(t, marker, "before-permission")

	// A permission for another bucket does not admit this one.
	if _, err := lam.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("sink"), StatementId: aws.String("other-bucket"), Action: aws.String("lambda:InvokeFunction"),
		Principal: aws.String("s3.amazonaws.com"), SourceArn: aws.String("arn:aws:s3:::other"),
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	put("wrong-bucket")
	markerStaysEmpty(t, marker, "wrong-bucket")

	if _, err := lam.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("sink"), StatementId: aws.String("uploads"), Action: aws.String("lambda:InvokeFunction"),
		Principal: aws.String("s3.amazonaws.com"), SourceArn: aws.String("arn:aws:s3:::uploads"),
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	put("with-permission")
	waitForMarker(t, marker, "with-permission")

	// And the direct Invoke by the root identity was never gated.
	if _, err := lam.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("sink"), Payload: []byte(`"direct"`)}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	waitForMarker(t, marker, "direct")
}

// TestEnforceSNSToSQSNeedsQueuePolicy: the fan-out is refused until the
// queue policy admits sns.amazonaws.com for the topic.
func TestEnforceSNSToSQSNeedsQueuePolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-service chain")
	}
	ctx := context.Background()
	cfg, url := enforceStack(t, "iam", "sns", "sqs")
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(url) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })

	top, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("uploads")})
	if err != nil {
		t.Fatal(err)
	}
	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("uploads-q")})
	if err != nil {
		t.Fatal(err)
	}
	queueARN := awsident.ARN("sqs", "uploads-q")
	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: top.TopicArn, Protocol: aws.String("sqs"), Endpoint: aws.String(queueARN), ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatal(err)
	}
	publish := func(msg string) {
		t.Helper()
		if _, err := sns.Publish(ctx, &awssns.PublishInput{TopicArn: top.TopicArn, Message: aws.String(msg)}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	receive := func(wait int32) string {
		out, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, WaitTimeSeconds: wait, MaxNumberOfMessages: 10})
		if err != nil {
			t.Fatalf("ReceiveMessage: %v", err)
		}
		var bodies []string
		for _, m := range out.Messages {
			bodies = append(bodies, aws.ToString(m.Body))
		}
		return strings.Join(bodies, "\n")
	}

	publish("before-policy")
	if got := receive(2); got != "" {
		t.Fatalf("the topic reached the queue without a queue policy:\n%s", got)
	}

	if _, err := sqs.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl, Attributes: map[string]string{
		"Policy": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"sns.amazonaws.com"},"Action":"sqs:SendMessage","Resource":"` +
			queueARN + `","Condition":{"ArnEquals":{"aws:SourceArn":"` + aws.ToString(top.TopicArn) + `"}}}]}`,
	}}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}
	publish("with-policy")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := receive(1); strings.Contains(got, "with-policy") {
			if strings.Contains(got, "before-policy") {
				t.Fatalf("the refused message must not be replayed:\n%s", got)
			}
			return
		}
	}
	t.Fatal("SNS -> SQS never delivered once the queue policy admitted the topic")
}
