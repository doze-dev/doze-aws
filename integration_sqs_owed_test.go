package dozeaws_test

// What plan/services/sqs.md section 9 ("Owed to integration") asks of the
// services around SQS, as tests across the stack. Each says what its evidence
// is (model, doc, memory) because that decides what a failure is allowed to
// change: nothing is fixed on memory alone, so a memory test is written, run,
// and skipped with its reason until a recording settles it.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// owedStack is a stack with every service, and clients for the ones these tests use.
func owedStack(t *testing.T) (aws.Config, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	return aws.Config{Region: awsident.Region, Credentials: creds}, ts.URL
}

func lambdaClient(cfg aws.Config, url string) *awslambda.Client {
	return awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) })
}

func sqsClient(cfg aws.Config, url string) *awssqs.Client {
	return awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })
}

func snsClient(cfg aws.Config, url string) *awssns.Client {
	return awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(url) })
}

// recorderFunction creates a function that appends each event to marker.
func recorderFunction(t *testing.T, lam *awslambda.Client, name string, timeout int32, marker string) {
	t.Helper()
	if _, err := lam.CreateFunction(context.Background(), &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String("arn:aws:iam::000000000000:role/r"),
		Timeout:     aws.Int32(timeout),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatalf("CreateFunction(%s): %v", name, err)
	}
}

// refusedAs reports the API error code and HTTP status of a refused call.
func refusedAs(t *testing.T, err error) (code string, status int) {
	t.Helper()
	if err == nil {
		return "", 0
	}
	var api smithy.APIError
	if !errors.As(err, &api) {
		t.Fatalf("not an API error: %v", err)
	}
	var re *awshttp.ResponseError
	if errors.As(err, &re) {
		status = re.HTTPStatusCode()
	}
	return api.ErrorCode(), status
}

// Evidence: doc. "Your function's timeout must be less than or equal to the
// queue's visibility timeout. Lambda validates this requirement when you create
// or update an event source mapping and returns an error if the function timeout
// exceeds the queue's visibility timeout." (Creating and configuring an Amazon
// SQS event source mapping; LAM-5 in plan/services/lambda.md). The Lambda model
// lists InvalidParameterValueException as the one fitting client error, so the
// code and status are model-consistent; the message is memory and is not asserted.
// Direction when it fails: more forgiving than AWS.
func TestATriggerWhoseQueueHidesMessagesForLessThanTheFunctionRunsIsRefused(t *testing.T) {
	ctx := context.Background()
	cfg, url := owedStack(t)
	lam, sqs := lambdaClient(cfg, url), sqsClient(cfg, url)
	marker := filepath.Join(t.TempDir(), "invocations.log")
	recorderFunction(t, lam, "slow", 30, marker)
	recorderFunction(t, lam, "quick", 3, marker)

	mk := func(queue, visibility string) string {
		t.Helper()
		if _, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(queue),
			Attributes: map[string]string{"VisibilityTimeout": visibility}}); err != nil {
			t.Fatal(err)
		}
		return awsident.Default().ARN("sqs", queue)
	}
	create := func(fn, arn string) (*awslambda.CreateEventSourceMappingOutput, error) {
		return lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
			FunctionName: aws.String(fn), EventSourceArn: aws.String(arn), BatchSize: aws.Int32(1), Enabled: aws.Bool(true)})
	}

	// 30 s function, queue hides a message for 2 s: refused, and no mapping is left.
	short := mk("hides-2", "2")
	_, err := create("slow", short)
	if code, status := refusedAs(t, err); code != "InvalidParameterValueException" || status != http.StatusBadRequest {
		t.Fatalf("a trigger whose queue hides messages for 2 s and whose function may run 30 s: %v (code %q, status %d), want InvalidParameterValueException 400", err, code, status)
	}
	listed, err := lam.ListEventSourceMappings(ctx, &awslambda.ListEventSourceMappingsInput{EventSourceArn: aws.String(short)})
	if err != nil || len(listed.EventSourceMappings) != 0 {
		t.Errorf("the refused trigger left a mapping behind: %v %+v", err, listed)
	}

	// "Less than or equal": exactly the function's timeout is accepted, and so is more.
	if _, err := create("slow", mk("hides-30", "30")); err != nil {
		t.Errorf("a queue that hides for as long as the function may run: %v", err)
	}
	if _, err := create("slow", mk("hides-31", "31")); err != nil {
		t.Errorf("a queue that hides for longer than the function may run: %v", err)
	}

	// Update validates too. A mapping made while it was true is refused its next
	// update once the function's timeout has been raised past the queue's.
	made, err := create("quick", mk("hides-10", "10"))
	if err != nil {
		t.Fatalf("a 3 s function on a queue that hides for 10 s: %v", err)
	}
	if _, err := lam.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("quick"), Timeout: aws.Int32(60)}); err != nil {
		t.Fatal(err)
	}
	_, err = lam.UpdateEventSourceMapping(ctx, &awslambda.UpdateEventSourceMappingInput{UUID: made.UUID, BatchSize: aws.Int32(5)})
	if code, status := refusedAs(t, err); code != "InvalidParameterValueException" || status != http.StatusBadRequest {
		t.Fatalf("an update of a mapping whose function may now run 60 s on a queue that hides for 10 s: %v (code %q, status %d)", err, code, status)
	}
}

// Evidence: memory. The model's DeleteFunction says "To delete Lambda event
// source mappings that invoke a function, use DeleteEventSourceMapping" (so the
// mapping outlives the function) and nothing of what its poller then does. That
// it stops reading the queue, rather than receiving messages it can hand to
// no one, is the plan's expectation and is not established. Written, run, and
// skipped: see the recording queue (plan/services/sqs.md section 8,
// test_lambda::test_a_deleted_functions_mapping).
func TestADeletedFunctionsMappingDoesNotReadItsQueue(t *testing.T) {
	ctx := context.Background()
	cfg, url := owedStack(t)
	lam, sqs := lambdaClient(cfg, url), sqsClient(cfg, url)
	marker := filepath.Join(t.TempDir(), "invocations.log")
	recorderFunction(t, lam, "gone", 3, marker)
	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orphaned"),
		Attributes: map[string]string{"VisibilityTimeout": "30"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName: aws.String("gone"), EventSourceArn: aws.String(awsident.Default().ARN("sqs", "orphaned")),
		BatchSize: aws.Int32(1), Enabled: aws.Bool(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := lam.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{FunctionName: aws.String("gone")}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // a poller mid-receive when the function went
	if _, err := sqs.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("for nobody")}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)

	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible"}})
	if err != nil {
		t.Fatal(err)
	}
	if attrs.Attributes["ApproximateNumberOfMessages"] != "1" || attrs.Attributes["ApproximateNumberOfMessagesNotVisible"] != "0" {
		t.Skipf("memory, not established (recording queue: test_lambda::test_a_deleted_functions_mapping): the mapping of a deleted function still receives the queue's messages and holds them (waiting %s, in flight %s)",
			attrs.Attributes["ApproximateNumberOfMessages"], attrs.Attributes["ApproximateNumberOfMessagesNotVisible"])
	}
}

// Evidence: doc. "Amazon SQS FIFO queues aren't supported" as an S3 event
// notification destination (S3 developer guide, "Amazon S3 Event Notifications",
// as read for S3-5/S3-26 in plan/services/s3.md; the S3 model says nothing of
// it). The error code is memory (the same InvalidArgument that S3 gives a
// destination it cannot validate) and its text is not asserted.
// Direction when it fails: more forgiving than AWS.
func TestAFIFOQueueIsNotAnS3NotificationDestination(t *testing.T) {
	ctx := context.Background()
	cfg, url := owedStack(t)
	sqs := sqsClient(cfg, url)
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true })
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("uploads")}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("events.fifo"),
		Attributes: map[string]string{"FifoQueue": "true"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("events")}); err != nil {
		t.Fatal(err)
	}
	put := func(queueARN string) error {
		_, err := s3c.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{
			Bucket: aws.String("uploads"),
			NotificationConfiguration: &s3types.NotificationConfiguration{
				QueueConfigurations: []s3types.QueueConfiguration{{QueueArn: aws.String(queueARN), Events: []s3types.Event{s3types.EventS3ObjectCreated}}},
			},
		})
		return err
	}
	if err := put(awsident.Default().ARN("sqs", "events")); err != nil {
		t.Fatalf("a standard queue is a destination: %v", err)
	}
	err := put(awsident.Default().ARN("sqs", "events.fifo"))
	if _, status := refusedAs(t, err); err == nil || status != http.StatusBadRequest {
		t.Fatalf("a FIFO queue as a notification destination: %v (status %d), want a 400 refusal", err, status)
	}
	// The refused put changed nothing: the standard queue's configuration stands.
	got, err := s3c.GetBucketNotificationConfiguration(ctx, &awss3.GetBucketNotificationConfigurationInput{Bucket: aws.String("uploads")})
	if err != nil || len(got.QueueConfigurations) != 1 || !strings.HasSuffix(aws.ToString(got.QueueConfigurations[0].QueueArn), ":events") {
		t.Errorf("the refused put changed the configuration: %v %+v", err, got)
	}
}

// Evidence: memory. The SNS model says nothing of a FIFO queue on a standard
// topic (SNS-12 in plan/services/sns.md: "memory: AWS refuses the subscription
// with InvalidParameter"). Written, run, and skipped: recording queue
// test_sns::test_a_fifo_queue_on_a_standard_topic.
func TestAFIFOQueueCannotBeSubscribedToAStandardTopic(t *testing.T) {
	ctx := context.Background()
	cfg, url := owedStack(t)
	sns, sqs := snsClient(cfg, url), sqsClient(cfg, url)
	top, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("standard")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("fan.fifo"),
		Attributes: map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "true"}}); err != nil {
		t.Fatal(err)
	}
	_, err = sns.Subscribe(ctx, &awssns.SubscribeInput{TopicArn: top.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String(awsident.Default().ARN("sqs", "fan.fifo")), ReturnSubscriptionArn: true})
	if err == nil {
		t.Skip("memory, not established (recording queue: test_sns::test_a_fifo_queue_on_a_standard_topic): a FIFO queue is subscribed to a standard topic and every delivery is then dropped (SNS-12)")
	}
	if code, _ := refusedAs(t, err); code != "InvalidParameter" {
		t.Errorf("the refusal's code = %q, want InvalidParameter", code)
	}
}

// Evidence: doc, for the queue policy (a service principal needs a statement of
// its own: the SQS developer guide, and the model's AddPermission "an account
// grant delegates to the account's identity policies"), and the same rule the
// standard-queue test next to it holds (TestEnforceSNSToSQSNeedsQueuePolicy).
// Here the fan-out is a FIFO topic into a FIFO queue, which carries a group id
// and a deduplication id and has its own path in SNS's delivery.
func TestEnforceAFIFOTopicIntoAFIFOQueueNeedsTheQueuePolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-service chain")
	}
	ctx := context.Background()
	cfg, url := enforceStack(t, "iam", "sns", "sqs")
	sns, sqs := snsClient(cfg, url), sqsClient(cfg, url)

	top, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("orders.fifo"),
		Attributes: map[string]string{"FifoTopic": "true", "ContentBasedDeduplication": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders-q.fifo"),
		Attributes: map[string]string{"FifoQueue": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	queueARN := awsident.Default().ARN("sqs", "orders-q.fifo")
	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{TopicArn: top.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String(queueARN), ReturnSubscriptionArn: true}); err != nil {
		t.Fatalf("a FIFO queue on a FIFO topic: %v", err)
	}
	publish := func(msg string) {
		t.Helper()
		if _, err := sns.Publish(ctx, &awssns.PublishInput{TopicArn: top.TopicArn, Message: aws.String(msg), MessageGroupId: aws.String("g")}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	receive := func(wait int32) (bodies string, groups []string) {
		out, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, WaitTimeSeconds: wait, MaxNumberOfMessages: 10,
			MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{"MessageGroupId"}})
		if err != nil {
			t.Fatalf("ReceiveMessage: %v", err)
		}
		var b []string
		for _, m := range out.Messages {
			b = append(b, aws.ToString(m.Body))
			groups = append(groups, m.Attributes["MessageGroupId"])
		}
		return strings.Join(b, "\n"), groups
	}

	publish("before-policy")
	if got, _ := receive(2); got != "" {
		t.Fatalf("the FIFO topic reached the FIFO queue without a queue policy:\n%s", got)
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
		got, groups := receive(1)
		if strings.Contains(got, "with-policy") {
			if strings.Contains(got, "before-policy") {
				t.Fatalf("the refused message must not be replayed:\n%s", got)
			}
			if len(groups) == 0 || groups[0] != "g" {
				t.Errorf("the group id did not reach the FIFO queue: %v", groups)
			}
			return
		}
	}
	t.Fatal("a FIFO topic never delivered into a FIFO queue once the queue policy admitted it")
}
