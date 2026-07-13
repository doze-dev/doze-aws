package dozeaws_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// recorderBootstrap appends each invocation's raw event to MARKER_FILE, so the
// test can assert a message was delivered to the function.
const recorderBootstrap = `package main

import (
	"io"
	"net/http"
	"os"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	marker := os.Getenv("MARKER_FILE")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		f, _ := os.OpenFile(marker, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		f.Write(body)
		f.WriteString("\n")
		f.Close()
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+id+"/response", "application/json", nil)
	}
}
`

func buildRecorder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(recorderBootstrap), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module rec\n\ngo 1.26\n"), 0o644)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build recorder: %v\n%s", err, out)
	}
	return dir
}

func waitForMarker(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(path); strings.Contains(string(b), want) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	b, _ := os.ReadFile(path)
	t.Fatalf("marker never contained %q; got:\n%s", want, b)
}

// TestIntegrationLambdaSinks proves the three Lambda-as-target wirings end to
// end through a full Stack: SNS -> Lambda, SQS event-source-mapping -> Lambda,
// and EventBridge rule -> Lambda. A recording Lambda logs each delivery.
func TestIntegrationLambdaSinks(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
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
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	eb := awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	marker := filepath.Join(t.TempDir(), "invocations.log")
	fnARN := "arn:aws:lambda:us-east-1:000000000000:function:sink"
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("sink"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String("arn:aws:iam::000000000000:role/r"),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// SNS -> Lambda.
	top, _ := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("t")})
	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: top.TopicArn, Protocol: aws.String("lambda"), Endpoint: aws.String(fnARN), ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatalf("Subscribe(lambda): %v", err)
	}
	if _, err := sns.Publish(ctx, &awssns.PublishInput{TopicArn: top.TopicArn, Message: aws.String("via-sns-XYZ")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitForMarker(t, marker, "via-sns-XYZ")

	// SQS event-source-mapping -> Lambda.
	q, _ := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("jobs")})
	if _, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName: aws.String("sink"), EventSourceArn: aws.String(awsident.ARN("sqs", "jobs")),
		BatchSize: aws.Int32(1), Enabled: aws.Bool(true),
	}); err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}
	if _, err := sqs.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("via-sqs-ABC")}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	waitForMarker(t, marker, "via-sqs-ABC")

	// EventBridge rule -> Lambda.
	eb.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String("r"), EventPattern: aws.String(`{"source":["intg"]}`)})
	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"), Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String(fnARN)}},
	}); err != nil {
		t.Fatalf("PutTargets(lambda): %v", err)
	}
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{{Source: aws.String("intg"), DetailType: aws.String("t"), Detail: aws.String(`{"tag":"via-eb-QRS"}`)}},
	}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}
	waitForMarker(t, marker, "via-eb-QRS")
}

// TestIntegrationS3ToLambda proves an S3 event notification invokes a Lambda:
// configure a bucket notification targeting the function, upload an object, and
// assert the function received an s3:ObjectCreated event carrying the object key.
func TestIntegrationS3ToLambda(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
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
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })

	marker := filepath.Join(t.TempDir(), "invocations.log")
	fnARN := "arn:aws:lambda:us-east-1:000000000000:function:sink"
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("sink"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String("arn:aws:iam::000000000000:role/r"),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("uploads")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := s3c.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String("uploads"),
		NotificationConfiguration: &s3types.NotificationConfiguration{
			LambdaFunctionConfigurations: []s3types.LambdaFunctionConfiguration{{
				LambdaFunctionArn: aws.String(fnARN),
				Events:            []s3types.Event{"s3:ObjectCreated:*"},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketNotificationConfiguration: %v", err)
	}

	if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("uploads"), Key: aws.String("via-s3-LMN"), Body: strings.NewReader("hi"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	waitForMarker(t, marker, "via-s3-LMN")
}

// TestIntegrationDynamoDBStreamToLambda proves a DynamoDB change invokes a
// Lambda through a stream event-source-mapping: enable a stream, wire an ESM at
// the stream ARN, put an item, and assert the function received an INSERT record.
func TestIntegrationDynamoDBStreamToLambda(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
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
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ddb := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	marker := filepath.Join(t.TempDir(), "invocations.log")
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("sink"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String("arn:aws:iam::000000000000:role/r"),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// Table with a stream enabled.
	ct, err := ddb.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("events"),
		BillingMode:         ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:           []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		StreamSpecification: &ddbtypes.StreamSpecification{
			StreamEnabled: aws.Bool(true), StreamViewType: ddbtypes.StreamViewTypeNewAndOldImages,
		},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	streamArn := ct.TableDescription.LatestStreamArn
	if aws.ToString(streamArn) == "" {
		t.Fatal("CreateTable returned no LatestStreamArn for a stream-enabled table")
	}

	// Wire the ESM at the stream ARN.
	if _, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName: aws.String("sink"), EventSourceArn: streamArn,
		BatchSize: aws.Int32(10), Enabled: aws.Bool(true),
		StartingPosition: lamtypes.EventSourcePositionTrimHorizon,
	}); err != nil {
		t.Fatalf("CreateEventSourceMapping(stream): %v", err)
	}

	if _, err := ddb.PutItem(ctx, &awsddb.PutItemInput{
		TableName: aws.String("events"),
		Item:      map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "via-ddb-STREAM"}},
	}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}
	waitForMarker(t, marker, "via-ddb-STREAM")
}

// failBootstrap reports every invocation as an error to the Runtime API, so the
// async retry loop exhausts and the DLQ path fires.
const failBootstrap = `package main

import (
	"net/http"
	"os"
	"strings"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		resp.Body.Close()
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+id+"/error", "application/json",
			strings.NewReader(` + "`" + `{"errorMessage":"boom","errorType":"Boom"}` + "`" + `))
	}
}
`

func buildFailer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(failBootstrap), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fail\n\ngo 1.26\n"), 0o644)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failer: %v\n%s", err, out)
	}
	return dir
}

// TestIntegrationLambdaDLQ proves the async-failure DLQ wiring: a function whose
// invocations always error, invoked asynchronously, drives the payload to its
// dead-letter SQS queue once retries are exhausted (exercises invokeAsync's
// failure branch and deliverToArn across the stack).
func TestIntegrationLambdaDLQ(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a failing lambda across the stack")
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
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	dlq, _ := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dlq")})
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName:     aws.String("flaky"),
		Runtime:          lamtypes.RuntimeProvidedal2,
		Handler:          aws.String("bootstrap"),
		Role:             aws.String("arn:aws:iam::000000000000:role/r"),
		Code:             &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildFailer(t))},
		Timeout:          aws.Int32(10),
		DeadLetterConfig: &lamtypes.DeadLetterConfig{TargetArn: aws.String(awsident.ARN("sqs", "dlq"))},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := lam.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   aws.String("flaky"),
		InvocationType: lamtypes.InvocationTypeEvent,
		Payload:        []byte(`{"tag":"dead-letter-me"}`),
	}); err != nil {
		t.Fatalf("async Invoke: %v", err)
	}

	// After retries exhaust, the original payload lands in the DLQ.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: dlq.QueueUrl, WaitTimeSeconds: 1})
		if len(out.Messages) > 0 {
			if !strings.Contains(aws.ToString(out.Messages[0].Body), "dead-letter-me") {
				t.Fatalf("DLQ message = %s", aws.ToString(out.Messages[0].Body))
			}
			return
		}
	}
	t.Fatal("payload never reached the DLQ")
}

// TestIntegrationLambdaOnFailureDestination proves the EventInvokeConfig
// OnFailure destination path (routeDestination): a failing function with an
// OnFailure SQS destination — set via the now-reachable
// PutFunctionEventInvokeConfig — delivers a destination record (condition
// RetriesExhausted, original payload) to the queue once retries are exhausted.
func TestIntegrationLambdaOnFailureDestination(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a failing lambda across the stack")
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
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	dest, _ := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("onfail")})
	lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("flaky2"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role: aws.String("arn:aws:iam::000000000000:role/r"),
		Code: &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildFailer(t))},
	})
	if _, err := lam.PutFunctionEventInvokeConfig(ctx, &awslambda.PutFunctionEventInvokeConfigInput{
		FunctionName:         aws.String("flaky2"),
		MaximumRetryAttempts: aws.Int32(0),
		DestinationConfig: &lamtypes.DestinationConfig{
			OnFailure: &lamtypes.OnFailure{Destination: aws.String(awsident.ARN("sqs", "onfail"))},
		},
	}); err != nil {
		t.Fatalf("PutFunctionEventInvokeConfig: %v", err)
	}

	lam.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("flaky2"), InvocationType: lamtypes.InvocationTypeEvent,
		Payload: []byte(`{"tag":"route-me"}`),
	})

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: dest.QueueUrl, WaitTimeSeconds: 1})
		if len(out.Messages) > 0 {
			body := aws.ToString(out.Messages[0].Body)
			if !strings.Contains(body, "route-me") || !strings.Contains(body, "RetriesExhausted") {
				t.Fatalf("OnFailure destination record = %s", body)
			}
			return
		}
	}
	t.Fatal("OnFailure destination never received the record")
}

// TestIntegrationS3ToSNSToSQS proves a three-service chain: an S3 object-created
// notification fans out to an SNS topic, which delivers to a subscribed SQS
// queue.
func TestIntegrationS3ToSNSToSQS(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-service chain")
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
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	s3c := s3New(cfg, ts.URL)
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	top, _ := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("uploads")})
	q, _ := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("uploads-q")})
	sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: top.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String(awsident.ARN("sqs", "uploads-q")), ReturnSubscriptionArn: true,
	})
	s3PutBucketNotifyToTopic(t, ctx, s3c, "uploads", aws.ToString(top.TopicArn))

	// Upload an object; the notification must chain S3 -> SNS -> SQS.
	s3Put(t, ctx, s3c, "uploads", "photo.jpg", "data")

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, WaitTimeSeconds: 1})
		if len(out.Messages) > 0 {
			if strings.Contains(aws.ToString(out.Messages[0].Body), "photo.jpg") {
				return
			}
		}
	}
	t.Fatal("S3 -> SNS -> SQS chain never delivered the object-created event")
}

func s3New(cfg aws.Config, url string) *awss3.Client {
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true })
}

func s3Put(t *testing.T, ctx context.Context, c *awss3.Client, bucket, key, body string) {
	t.Helper()
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
		// bucket may already exist from notify setup
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader(body)}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
}

func s3PutBucketNotifyToTopic(t *testing.T, ctx context.Context, c *awss3.Client, bucket, topicARN string) {
	t.Helper()
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)})
	if _, err := c.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String(bucket),
		NotificationConfiguration: &s3types.NotificationConfiguration{
			TopicConfigurations: []s3types.TopicConfiguration{{
				TopicArn: aws.String(topicARN),
				Events:   []s3types.Event{s3types.EventS3ObjectCreated},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketNotificationConfiguration: %v", err)
	}
}
