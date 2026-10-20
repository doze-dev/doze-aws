package dozeaws_test

// The wire's cascades, through the stack and the console's recorder as the
// binary wires them: what a function does under an event source mapping, and
// what each row says it came of.
//
// Three things the wire page draws that a unit test of either half cannot show:
//
//   - a function that raises under a mapping is "it threw", as it is when it is
//     invoked directly, and says what it raised;
//   - a message an SDK sent (it carries no trace header) is still the cause of
//     the invoke that reads it, so "What it caused" has the invoke in it;
//   - a bucket's notification to a queue is a child of the put, via the event.

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
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// throwerBootstrap answers every invocation with a runtime error, the way a
// handler that raised does.
const throwerBootstrap = `package main

import (
	"io"
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
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+id+"/error", "application/json",
			strings.NewReader(` + "`" + `{"errorMessage":"'unit_price'","errorType":"KeyError"}` + "`" + `))
	}
}
`

func buildBootstrap(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fn\n\ngo 1.26\n"), 0o644)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap: %v\n%s", err, out)
	}
	return dir
}

type wireStack struct {
	rec *console.Recorder
	lam *awslambda.Client
	sqs *awssqs.Client
	s3  *awss3.Client
}

// newWireStack is a stack behind the recorder, as cmd/doze-aws builds it, with
// SDK clients that call through the recorder.
func newWireStack(t *testing.T) *wireStack {
	t.Helper()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	rec := console.NewRecorder(stack.Handler(), awsident.Default())
	rec.SetOpResolver(dozeaws.OperationResolvers())
	stack.SetTraceSink(rec)
	ts := httptest.NewServer(rec)
	t.Cleanup(ts.Close)

	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	return &wireStack{
		rec: rec,
		lam: awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		sqs: awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		s3:  awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true }),
	}
}

// find waits for a recorded entry the match accepts.
func (w *wireStack) find(t *testing.T, what string, match func(console.TrafficEntry) bool) console.TrafficEntry {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range w.rec.Entries(0) {
			if match(e) {
				return e
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	var seen []string
	for _, e := range w.rec.Entries(0) {
		seen = append(seen, strings.TrimSpace(e.Service+" "+e.Action+" "+e.Resource))
	}
	t.Fatalf("no %s on the wire; it holds %q", what, seen)
	return console.TrafficEntry{}
}

func (w *wireStack) function(t *testing.T, name, dir string) {
	t.Helper()
	if _, err := w.lam.CreateFunction(context.Background(), &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role: aws.String("arn:aws:iam::000000000000:role/r"),
		Code: &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(dir)},
	}); err != nil {
		t.Fatal(err)
	}
}

func (w *wireStack) mapping(t *testing.T, fn, queue string) {
	t.Helper()
	if _, err := w.lam.CreateEventSourceMapping(context.Background(), &awslambda.CreateEventSourceMappingInput{
		FunctionName: aws.String(fn), EventSourceArn: aws.String(awsident.Default().ARN("sqs", queue)),
		BatchSize: aws.Int32(1), Enabled: aws.Bool(true),
	}); err != nil {
		t.Fatal(err)
	}
}

// A function that raises under an event source mapping is a served invoke whose
// function threw. The row carried a 500 and the word "failed" (a bug in
// doze-aws, by the wire's own definition) because the cascade event had no way
// to say the function had answered.
func TestAFunctionErrorUnderAMappingIsRecordedAsTheFunctionThrowing(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
	}
	w := newWireStack(t)
	ctx := context.Background()
	w.function(t, "thrower", buildBootstrap(t, throwerBootstrap))
	q, err := w.sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}
	w.mapping(t, "thrower", "orders")
	if _, err := w.sqs.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String(`{"orderId":"1"}`)}); err != nil {
		t.Fatal(err)
	}

	inv := w.find(t, "invoke of thrower", func(e console.TrafficEntry) bool {
		return e.Action == "Invoke (event source)" && e.Resource == "thrower"
	})
	if inv.Status != 200 || inv.FnError != "Unhandled" {
		t.Errorf("the invoke was recorded as status %d, function error %q; want a served 200 flagged Unhandled", inv.Status, inv.FnError)
	}
	f := inv.Failure()
	if f == nil || f.Code != "KeyError" || f.Message != "'unit_price'" {
		t.Errorf("the invoke says it raised %+v, want KeyError and 'unit_price'", f)
	}
}

// A message an SDK sent carries no trace header, and the invoke that reads it is
// still a child of the send: the queue remembers, in memory, which call put
// each message on it, and the mapping asks.
func TestTheInvokeThatReadsAnSDKsMessageIsAChildOfTheSend(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
	}
	w := newWireStack(t)
	ctx := context.Background()
	w.function(t, "validator", buildRecorder(t))
	q, err := w.sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}
	w.mapping(t, "validator", "orders")
	if _, err := w.sqs.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String(`{"orderId":"1"}`)}); err != nil {
		t.Fatal(err)
	}

	send := w.find(t, "SendMessage", func(e console.TrafficEntry) bool { return e.Action == "SendMessage" && e.Resource == "orders" })
	inv := w.find(t, "invoke of validator", func(e console.TrafficEntry) bool {
		return e.Action == "Invoke (event source)" && e.Resource == "validator"
	})
	if send.IsCascade() {
		t.Fatalf("the SDK's send is itself a cascade row: %+v", send)
	}
	if inv.Parent != send.Seq {
		t.Errorf("the invoke's parent is call %d, want the SDK's SendMessage, call %d", inv.Parent, send.Seq)
	}
	if inv.Via != "sqs:orders" {
		t.Errorf("the invoke came %q, want sqs:orders", inv.Via)
	}
}

// A bucket's notification is a child of
// the put that caused it, saying which event it came of, and the mapping that
// reads it is under that send in turn.
func TestABucketNotificationIsAChildOfThePutViaTheEventAndTheMappingNestsUnderIt(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda across the stack")
	}
	w := newWireStack(t)
	ctx := context.Background()
	w.function(t, "dispatcher", buildRecorder(t))
	if _, err := w.sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("dispatch")}); err != nil {
		t.Fatal(err)
	}
	w.mapping(t, "dispatcher", "dispatch")
	if _, err := w.s3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("receipts")}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s3.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String("receipts"),
		NotificationConfiguration: &s3types.NotificationConfiguration{
			QueueConfigurations: []s3types.QueueConfiguration{{
				QueueArn: aws.String(awsident.Default().ARN("sqs", "dispatch")),
				Events:   []s3types.Event{s3types.EventS3ObjectCreated},
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s3.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("receipts"), Key: aws.String("2026/r-1.pdf"), Body: strings.NewReader("receipt")}); err != nil {
		t.Fatal(err)
	}

	put := w.find(t, "PutObject", func(e console.TrafficEntry) bool { return e.Action == "PutObject" && !e.IsCascade() })
	send := w.find(t, "the notification's SendMessage", func(e console.TrafficEntry) bool {
		return e.Action == "SendMessage" && e.Resource == "dispatch"
	})
	if send.Parent != put.Seq || send.Via != "s3:ObjectCreated:Put" {
		t.Errorf("the notification is parent %d via %q, want the put (%d) via s3:ObjectCreated:Put", send.Parent, send.Via, put.Seq)
	}
	// And the mapping that reads it is under the notification's send, not beside it.
	inv := w.find(t, "invoke of dispatcher", func(e console.TrafficEntry) bool {
		return e.Action == "Invoke (event source)" && e.Resource == "dispatcher"
	})
	if inv.Parent != send.Seq {
		t.Errorf("the invoke's parent is call %d, want the notification's send, call %d", inv.Parent, send.Seq)
	}
}
