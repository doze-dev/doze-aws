package dozeaws_test

// An SQS trigger under another account and region (LAM-36).
//
// SQS refuses a queue URL that names an account that is not its own, and the
// helpers the poller uses built every URL from the default account, so under
// --account-id a trigger received nothing. The helpers are covered by
// TestPeerHelpersReachAQueueUnderAnotherAccount; this is the poller from end to
// end: the message reaches the function once, the event says where it came
// from, and the message is gone from the queue afterwards.

import (
	"context"
	"encoding/json"
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
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestAnSQSTriggerDeliversUnderAnotherAccountAndRegion(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
	}
	ctx := context.Background()
	id := awsident.Identity{Region: "eu-west-1", AccountID: "111122223333"}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	cfg := aws.Config{Region: id.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	marker := filepath.Join(t.TempDir(), "invocations.log")
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("worker"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String(id.GlobalARN("iam", "role/r")),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatal(err)
	}
	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("jobs")})
	if err != nil {
		t.Fatal(err)
	}
	// The queue's ARN is read from the queue, not built here: it is the value a
	// user pastes into the mapping.
	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	if err != nil {
		t.Fatal(err)
	}
	queueARN := attrs.Attributes["QueueArn"]
	if want := "arn:aws:sqs:eu-west-1:111122223333:jobs"; queueARN != want {
		t.Fatalf("QueueArn = %q, want %q", queueARN, want)
	}

	sent, err := sqs.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("one job")})
	if err != nil {
		t.Fatal(err)
	}
	esm, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName: aws.String("worker"), EventSourceArn: aws.String(queueARN),
		BatchSize: aws.Int32(10), Enabled: aws.Bool(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The mapping names the function it invokes, in this account and region.
	const wantFn = "arn:aws:lambda:eu-west-1:111122223333:function:worker"
	if got := aws.ToString(esm.FunctionArn); got != wantFn {
		t.Errorf("CreateEventSourceMapping: FunctionArn = %q, want %q", got, wantFn)
	}
	got, err := lam.GetEventSourceMapping(ctx, &awslambda.GetEventSourceMappingInput{UUID: esm.UUID})
	if err != nil {
		t.Fatal(err)
	}
	if fn := aws.ToString(got.FunctionArn); fn != wantFn {
		t.Errorf("GetEventSourceMapping: FunctionArn = %q, want %q", fn, wantFn)
	}
	if src := aws.ToString(got.EventSourceArn); src != queueARN {
		t.Errorf("GetEventSourceMapping: EventSourceArn = %q, want %q", src, queueARN)
	}

	waitForMarker(t, marker, aws.ToString(sent.MessageId))

	// Deleted after the invocation succeeded: the queue holds nothing, visible
	// or in flight. Polled, because the delete follows the function's reply.
	deadline := time.Now().Add(10 * time.Second)
	for {
		a, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
			AttributeNames: []sqstypes.QueueAttributeName{
				sqstypes.QueueAttributeNameApproximateNumberOfMessages,
				sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible}})
		if err != nil {
			t.Fatal(err)
		}
		if a.Attributes["ApproximateNumberOfMessages"] == "0" && a.Attributes["ApproximateNumberOfMessagesNotVisible"] == "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message was not deleted after the function took it: %v", a.Attributes)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Exactly one invocation, of exactly that message. Read after the delete,
	// and after a wait a second delivery would have landed in.
	time.Sleep(time.Second)
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one invocation, got %d:\n%s", len(lines), raw)
	}
	var event struct {
		Records []struct {
			MessageID      string `json:"messageId"`
			Body           string `json:"body"`
			EventSource    string `json:"eventSource"`
			EventSourceARN string `json:"eventSourceARN"`
			AWSRegion      string `json:"awsRegion"`
		}
	}
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("%v: %s", err, lines[0])
	}
	if len(event.Records) != 1 {
		t.Fatalf("want one record, got %d: %s", len(event.Records), lines[0])
	}
	r := event.Records[0]
	if r.MessageID != aws.ToString(sent.MessageId) || r.Body != "one job" {
		t.Errorf("the record is not the message sent: %+v", r)
	}
	if r.AWSRegion != "eu-west-1" {
		t.Errorf("awsRegion = %q, want eu-west-1", r.AWSRegion)
	}
	if r.EventSourceARN != queueARN {
		t.Errorf("eventSourceARN = %q, want the queue's own ARN %q", r.EventSourceARN, queueARN)
	}
	if r.EventSource != "aws:sqs" {
		t.Errorf("eventSource = %q", r.EventSource)
	}
}
