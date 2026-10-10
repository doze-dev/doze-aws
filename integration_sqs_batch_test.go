package dozeaws_test

// A trigger's batch size above ten still receives. SQS refuses a receive of
// more than ten messages, so a poller that asked for its whole BatchSize in one
// call would stop receiving the moment that refusal was enforced (LAM-6, closed
// together with SQS-5). It receives in calls of at most ten and hands the
// function one batch of up to BatchSize.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func TestATriggerWithABatchSizeOfTwentyReceivesInCallsOfTen(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	marker := filepath.Join(t.TempDir(), "invocations.log")
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("batcher"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role:        aws.String("arn:aws:iam::000000000000:role/r"),
		Code:        &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{"MARKER_FILE": marker}},
	}); err != nil {
		t.Fatal(err)
	}
	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("twenty")})
	if err != nil {
		t.Fatal(err)
	}
	// Twenty messages are waiting before the trigger exists, so the first
	// receive can see all of them.
	for i := 0; i < 2; i++ {
		var entries []sqstypes.SendMessageBatchRequestEntry
		for j := 0; j < 10; j++ {
			n := strconv.Itoa(i*10 + j)
			entries = append(entries, sqstypes.SendMessageBatchRequestEntry{Id: aws.String("m" + n), MessageBody: aws.String("msg-" + n)})
		}
		if _, err := sqs.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{QueueUrl: q.QueueUrl, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName: aws.String("batcher"), EventSourceArn: aws.String(awsident.Default().ARN("sqs", "twenty")),
		BatchSize: aws.Int32(20), Enabled: aws.Bool(true),
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(marker)
		if strings.Count(string(b), `"messageId"`) >= 20 {
			if first := strings.SplitN(string(b), "\n", 2)[0]; strings.Count(first, `"messageId"`) != 20 {
				t.Errorf("the first invocation carried %d of the 20 messages waiting; a batch size of 20 means one batch of 20",
					strings.Count(first, `"messageId"`))
			}
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	b, _ := os.ReadFile(marker)
	t.Fatalf("only %d of 20 messages reached the function", strings.Count(string(b), `"messageId"`))
}
