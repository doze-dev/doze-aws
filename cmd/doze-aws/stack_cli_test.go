package main

// `doze-aws apply` and `doze-aws export` had no tests.
//
// split_test.go covers splitApplyArgs and flagTakesValue — the argument
// parsing — and stops one step short of everything the arguments are for.
// runApply, runExport, loadTemplate, printTranspileReport, printReport,
// gatewayFor and proxyHandler were all 0%, and the binary tests in
// main_test.go, which do build and run the real executable, never pass
// `apply` or `export` at all: cmd/doze-aws contributed 0.0% of statements to
// provision's coverage.
//
// That is the first thing a developer does with this tool. It is also the
// path where a failure is silent in the worst way — a template that maps no
// resources still exits 0, and the only warning is a line on stderr that
// nothing checked was printed.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
)

const applyTemplate = `AWSTemplateFormatVersion: '2010-09-09'
Parameters:
  Stage:
    Type: String
    Default: dev
Resources:
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub '${Stage}-orders'
      VisibilityTimeout: 45
  Alerts:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: !Sub '${Stage}-alerts'
  SkippedRole:
    Type: AWS::IAM::Role
    Properties:
      AssumeRolePolicyDocument: {}
Outputs:
  QueueName:
    Value: !GetAtt Orders.QueueName
  Zebra:
    Value: last-alphabetically
`

// inDir runs fn with the process working directory moved, since runApply
// discovers templates relative to it.
func inDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { os.Chdir(prev) }()
	fn()
}

// captureStderrStdout runs fn with both streams redirected and returns them.
func captureStderrStdout(t *testing.T, fn func()) (stderr, stdout string) {
	t.Helper()
	er, ew, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	or, ow, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prevErr, prevOut := os.Stderr, os.Stdout
	os.Stderr, os.Stdout = ew, ow

	done := make(chan [2]string, 1)
	go func() {
		eb := make([]byte, 0, 1<<16)
		ob := make([]byte, 0, 1<<16)
		buf := make([]byte, 4096)
		for {
			n, err := er.Read(buf)
			eb = append(eb, buf[:n]...)
			if err != nil {
				break
			}
		}
		for {
			n, err := or.Read(buf)
			ob = append(ob, buf[:n]...)
			if err != nil {
				break
			}
		}
		done <- [2]string{string(eb), string(ob)}
	}()

	fn()

	ew.Close()
	ow.Close()
	os.Stderr, os.Stdout = prevErr, prevOut
	got := <-done
	return got[0], got[1]
}

// TestApplyCommandCreatesResources is the first-run path: a template on disk,
// no server listening, resources in the data dir afterwards.
func TestApplyCommandCreatesResources(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.WriteFile(filepath.Join(dir, "template.yaml"), []byte(applyTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	stderr, _ := captureStderrStdout(t, func() {
		inDir(t, dir, func() {
			// No file argument: the template is discovered by name, which is
			// the whole point of DefaultTemplateFiles and was never tested.
			code = runApply([]string{"--data-dir", data, "--listen", "127.0.0.1:0"})
		})
	})
	if code != 0 {
		t.Fatalf("apply exited %d\n%s", code, stderr)
	}

	// What the user reads.
	if !strings.Contains(stderr, "✓ converged:") {
		t.Errorf("no convergence line:\n%s", stderr)
	}
	if !strings.Contains(stderr, "no server running") {
		t.Errorf("apply should say it went to the data dir:\n%s", stderr)
	}
	// printTranspileReport must name what it did NOT map — the silent skip is
	// the failure the transpiler exists to avoid.
	if !strings.Contains(stderr, "AWS::IAM::Role") || !strings.Contains(stderr, "≈ SkippedRole") {
		t.Errorf("a skipped resource must be reported with its reason:\n%s", stderr)
	}
	if !strings.Contains(stderr, "+ queue/dev-orders") {
		t.Errorf("printReport should mark the queue created:\n%s", stderr)
	}
	// Outputs, in sorted order.
	qi, zi := strings.Index(stderr, "QueueName ="), strings.Index(stderr, "Zebra =")
	if qi < 0 || zi < 0 || qi > zi {
		t.Errorf("outputs missing or unsorted:\n%s", stderr)
	}

	// And the resources are really there.
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: data, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := newHTTPTest(t, stack)
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	sqsC := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts) })
	if _, err := sqsC.GetQueueUrl(context.Background(), &awssqs.GetQueueUrlInput{
		QueueName: aws.String("dev-orders")}); err != nil {
		t.Errorf("the queue apply reported is not there: %v", err)
	}
	snsC := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts) })
	topics, err := snsC.ListTopics(context.Background(), &awssns.ListTopicsInput{})
	if err != nil || len(topics.Topics) != 1 {
		t.Errorf("the topic is not there: %+v %v", topics, err)
	}
}

// TestApplyCommandVarReachesTheTranspiler: --var is the template-parameter
// channel. splitApplyArgs was tested up to producing the map and no further,
// so nothing proved the map arrives.
func TestApplyCommandVarReachesTheTranspiler(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	tmpl := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(tmpl, []byte(applyTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	stderr, _ := captureStderrStdout(t, func() {
		code = runApply([]string{"--var", "Stage=prod", "--data-dir", data, "--listen", "127.0.0.1:0", tmpl})
	})
	if code != 0 {
		t.Fatalf("apply exited %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "prod-orders") {
		t.Fatalf("--var Stage=prod did not reach the transpiler:\n%s", stderr)
	}
	if strings.Contains(stderr, "dev-orders") {
		t.Errorf("the default parameter was used despite --var:\n%s", stderr)
	}
}

// TestApplyCommandExitCodes: 2 for the caller's mistakes, 1 for the
// template's. Nothing asserted any exit code.
func TestApplyCommandExitCodes(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")

	t.Run("no template and none discoverable", func(t *testing.T) {
		empty := t.TempDir()
		var code int
		stderr, _ := captureStderrStdout(t, func() {
			inDir(t, empty, func() { code = runApply([]string{"--data-dir", data}) })
		})
		if code != 2 {
			t.Errorf("exit = %d, want 2", code)
		}
		if !strings.Contains(stderr, "template.yaml") {
			t.Errorf("the message should name what it looked for:\n%s", stderr)
		}
	})

	t.Run("unreadable file", func(t *testing.T) {
		var code int
		stderr, _ := captureStderrStdout(t, func() {
			code = runApply([]string{"--data-dir", data, filepath.Join(dir, "nope.yaml")})
		})
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.HasPrefix(stderr, "apply:") {
			t.Errorf("the error should be prefixed:\n%s", stderr)
		}
	})

	t.Run("unparseable template", func(t *testing.T) {
		bad := filepath.Join(dir, "bad.yaml")
		if err := os.WriteFile(bad, []byte("{{{ not yaml"), 0o644); err != nil {
			t.Fatal(err)
		}
		var code int
		captureStderrStdout(t, func() { code = runApply([]string{"--data-dir", data, bad}) })
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})

	t.Run("a bad --var", func(t *testing.T) {
		var code int
		captureStderrStdout(t, func() { code = runApply([]string{"--var", "novalue"}) })
		if code != 2 {
			t.Errorf("exit = %d, want 2", code)
		}
	})
}

// TestExportCommandEmitsATemplate: not one byte of `doze-aws export` output
// was asserted anywhere, and Export is called from nothing else in the
// module — the CLI is its only caller.
func TestExportCommandEmitsATemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	tmpl := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(tmpl, []byte(applyTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	captureStderrStdout(t, func() {
		runApply([]string{"--data-dir", data, "--listen", "127.0.0.1:0", tmpl})
	})

	var code int
	stderr, stdout := captureStderrStdout(t, func() {
		code = runExport([]string{"--data-dir", data, "--listen", "127.0.0.1:0"})
	})
	if code != 0 {
		t.Fatalf("export exited %d\n%s", code, stderr)
	}
	if stdout == "" {
		t.Fatal("export wrote nothing to stdout")
	}
	// The contract is that what comes out goes back in.
	parsed, err := cloudformation.Parse([]byte(stdout))
	if err != nil {
		t.Fatalf("export output is not a template we can read back: %v\n%s", err, stdout)
	}
	back, _, err := cloudformation.Transpile(parsed, cloudformation.TranspileOptions{StackName: "round"})
	if err != nil {
		t.Fatalf("export output does not transpile: %v\n%s", err, stdout)
	}
	if _, ok := back.Queues["dev-orders"]; !ok {
		t.Errorf("the exported template lost the queue: %+v", back.Queues)
	}
	if len(back.Topics) != 1 {
		t.Errorf("the exported template lost the topic: %+v", back.Topics)
	}
}

// TestProxyHandlerRelaysBothWays covers the adapter that lets apply converge
// a RUNNING server — bbolt is single-writer, so this is the only path when
// the user has doze-aws up in another terminal, which is the common case.
func TestProxyHandlerRelaysBothWays(t *testing.T) {
	upstream, base := newEchoServer(t)
	defer upstream()

	code, body, hdr := doThroughProxy(t, proxyHandler{base: base}, "POST", "/x?y=1", "hello",
		map[string]string{"X-Sent": "up"})
	if code != 207 {
		t.Errorf("status not relayed: %d", code)
	}
	if body != "POST /x?y=1 hello up" {
		t.Errorf("request not relayed faithfully: %q", body)
	}
	if hdr.Get("X-Came-Back") != "yes" {
		t.Errorf("response headers not relayed: %+v", hdr)
	}
}

// TestProxyHandlerAnswers502WhenNothingListens: the failure a user hits when
// the server they were talking to has gone away.
func TestProxyHandlerAnswers502WhenNothingListens(t *testing.T) {
	code, _, _ := doThroughProxy(t, proxyHandler{base: "http://127.0.0.1:1"}, "GET", "/", "", nil)
	if code != 502 {
		t.Errorf("status = %d, want 502", code)
	}
}

// TestGatewayForPrefersARunningServer: the live/embedded decision, and that
// the embedded branch releases the bbolt lock when it is done.
func TestGatewayForPrefersARunningServer(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	data := t.TempDir()

	// Nothing listening: an embedded stack, and closer() must free the lock.
	cfg := configFor(data, "127.0.0.1:1")
	h, closer, live, err := gatewayFor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Error("nothing is listening, so live must be false")
	}
	if h == nil {
		t.Fatal("no handler")
	}
	closer()
	// If the lock was not released this fails, which is the whole reason the
	// closer exists.
	second, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: data, Logf: t.Logf})
	if err != nil {
		t.Fatalf("closer() did not release the data dir: %v", err)
	}
	second.Close()

	// Something listening: a proxy, and no lock taken at all.
	stop, addr := newListener(t)
	defer stop()
	h, closer, live, err = gatewayFor(configFor(data, addr))
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	if !live {
		t.Error("a listening server must be detected")
	}
	if _, ok := h.(proxyHandler); !ok {
		t.Errorf("a live server must be reached through proxyHandler, got %T", h)
	}
}
