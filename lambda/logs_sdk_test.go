package lambda_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// A function's output reaches the logs service the way it reaches CloudWatch:
// under /aws/lambda/<name>, in a stream named for the process, each line
// carrying the request id of the invocation that printed it — whether the
// invoke was synchronous or fired by an event source.

const printingBootstrap = `package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	fmt.Println("cold start")
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		reqID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("handling %s\n", body)
		fmt.Fprintln(os.Stderr, "a warning")
		http.Post("http://" + api + "/2018-06-01/runtime/invocation/" + reqID + "/response",
			"application/json", bytes.NewReader([]byte("{}")))
	}
}
`

func buildPrinting(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(printingBootstrap), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module bootstrap\n\ngo 1.26\n"), 0o644)
	return buildIn(t, dir)
}

func TestFunctionOutputReachesTheLogsService(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	ctx := context.Background()
	var echoed []string
	logf := func(format string, args ...any) { echoed = append(echoed, sprintf(format, args...)) }
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := newTestServer(t, stack.Handler())
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	lc := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts) })
	logs := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts) })

	dir := buildPrinting(t)
	if _, err := lc.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("printer"), Runtime: lambdatypes.RuntimeProvidedal2023, Handler: aws.String("bootstrap"),
		Role: aws.String("arn:aws:iam::000000000000:role/x"),
		Code: &lambdatypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(dir)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("printer"), Payload: []byte(`{"n":1}`), LogType: lambdatypes.LogTypeTail}); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("printer"), InvocationType: lambdatypes.InvocationTypeEvent, Payload: []byte(`{"n":2}`)}); err != nil {
		t.Fatal(err)
	}

	// The async invocation lands on its own schedule; the sync one shipped at
	// REPORT. Poll for both.
	var events []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String("/aws/lambda/printer")})
		if err == nil {
			events = events[:0]
			for _, ev := range res.Events {
				events = append(events, aws.ToString(ev.Message))
			}
			if strings.Contains(strings.Join(events, "\n"), `handling {"n":2}`) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	all := strings.Join(events, "\n")
	for _, want := range []string{"cold start", `handling {"n":1}`, "a warning", `handling {"n":2}`, "START RequestId: ", "END RequestId: ", "REPORT RequestId: "} {
		if !strings.Contains(all, want) {
			t.Errorf("logs lack %q:\n%s", want, all)
		}
	}
	// Init output carries no request id; every line of an invocation carries
	// its own. The doze extension on FilterLogEvents selects one invocation.
	streams, err := logs.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{LogGroupName: aws.String("/aws/lambda/printer")})
	if err != nil || len(streams.LogStreams) == 0 {
		t.Fatalf("streams: %+v, %v", streams, err)
	}
	if name := aws.ToString(streams.LogStreams[0].LogStreamName); !strings.Contains(name, "/[$LATEST]") {
		t.Errorf("stream name %q is not AWS-shaped", name)
	}
	// The terminal saw the same lines, prefixed with the function.
	echo := strings.Join(echoed, "\n")
	if !strings.Contains(echo, "lambda[printer] cold start") || !strings.Contains(echo, `lambda[printer] handling {"n":1}`) {
		t.Errorf("function output was not echoed to the log:\n%s", echo)
	}
}

// TestInvokeWithoutTheLogsService: a stack started without logs still runs
// functions, echoes their output, and says once where the lines went.
func TestInvokeWithoutTheLogsService(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	ctx := context.Background()
	var lines []string
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Services: []string{"lambda"},
		Logf: func(format string, args ...any) { lines = append(lines, sprintf(format, args...)) }})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := newTestServer(t, stack.Handler())
	lc := awslambda.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")},
		func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts) })
	dir := buildPrinting(t)
	lc.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("lonely"), Runtime: lambdatypes.RuntimeProvidedal2023, Handler: aws.String("bootstrap"),
		Role: aws.String("arn:aws:iam::000000000000:role/x"),
		Code: &lambdatypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(dir)},
	})
	for i := 0; i < 2; i++ {
		if _, err := lc.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("lonely"), Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond) // the sink's worker reports on its own goroutine
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "lambda[lonely] handling {}") {
		t.Errorf("output should still be echoed:\n%s", all)
	}
	if n := strings.Count(all, "logs service is not enabled"); n != 1 {
		t.Errorf("the missing logs service should be reported exactly once, got %d:\n%s", n, all)
	}
}
