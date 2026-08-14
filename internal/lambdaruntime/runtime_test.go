package lambdaruntime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeCommand(t *testing.T) {
	cases := []struct {
		runtime string
		handler string
		want    []string
		wantErr bool
	}{
		{"", "", []string{"./bootstrap"}, false},
		{"go", "main", []string{"./main"}, false},
		{"provided.al2", "bootstrap", []string{"./bootstrap"}, false},
		{"python3.12", "app.handler", []string{"python3", "-m", "awslambdaric", "app.handler"}, false},
		{"nodejs20.x", "index.handler", []string{"npx", "--yes", "aws-lambda-ric", "index.handler"}, false},
		{"java21", "example.Handler::run", []string{"java", "-cp", "./*:.", "com.amazonaws.services.lambda.runtime.api.client.AWSLambda", "example.Handler::run"}, false},
		{"ruby3.3", "app.LambdaFunction::Handler.process", []string{"aws_lambda_ric", "app.LambdaFunction::Handler.process"}, false},
		{"dotnet8", "Assembly::Type::Method", []string{"dotnet", "exec", "/opt/aws-lambda-ric.dll", "Assembly::Type::Method"}, false},
		{"cobol", "x", nil, true},
	}
	for _, c := range cases {
		got, err := runtimeCommand(c.runtime, c.handler)
		if c.wantErr {
			if err == nil {
				t.Errorf("runtime %q: expected error, got %v", c.runtime, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("runtime %q: unexpected error: %v", c.runtime, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("runtime %q: got %v, want %v", c.runtime, got, c.want)
		}
	}
}

// TestRelativeCodeDirResolvesAbsolutely is a regression from the API Gateway
// work. cmd.Dir is set to the code directory, and Go resolves a relative
// argv[0] against cmd.Dir — so a relative code dir made the child look for
// <dir>/<dir>/bootstrap and fail with "no such file or directory".
//
// It only bit code unpacked from a zip (every `sam deploy` / `cdk deploy`
// function) under a relative --data-dir, which is the default.
func TestRelativeCodeDirResolvesAbsolutely(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "bootstrap")
	if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Address the code dir RELATIVELY, as a default ./data deployment would.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, dir)
	if err != nil {
		t.Skipf("cannot express %s relative to %s", dir, wd)
	}

	r := &Runner{spec: Spec{
		Name: "fn", Handler: "bootstrap", Runtime: "provided.al2", Dir: rel,
	}, logf: func(string, ...any) {}}
	cmd, err := r.buildCommand("127.0.0.1:1")
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !filepath.IsAbs(cmd.Path) {
		t.Fatalf("argv[0] = %q; it must be absolute, because cmd.Dir=%q would "+
			"otherwise resolve it a second time against itself", cmd.Path, cmd.Dir)
	}
	if _, err := os.Stat(cmd.Path); err != nil {
		t.Fatalf("resolved argv[0] %q does not exist: %v", cmd.Path, err)
	}
}

// slowInitBootstrap sleeps before its FIRST poll for work, which is what a
// cold start looks like from the outside: process spawned, not yet asking for
// an invocation. It then answers immediately, reporting how much of the
// deadline it was given.
const slowInitBootstrap = `package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	time.Sleep(2 * time.Second) // the cold start
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		reqID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		dl, _ := strconv.ParseInt(resp.Header.Get("Lambda-Runtime-Deadline-Ms"), 10, 64)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		remaining := dl - time.Now().UnixMilli()
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+reqID+"/response",
			"application/json", bytes.NewReader([]byte(fmt.Sprintf(` + "`" + `{"remaining_ms":%d}` + "`" + `, remaining))))
	}
}
`

// TestColdStartIsNotChargedToTheFunctionTimeout pins the fix for a flake that
// only ever showed up under a full test run: the timeout clock used to start
// when Invoke QUEUED the work, so a slow process spawn ate the function's
// budget and the caller got "Task timed out" for a handler that had not yet
// been asked to do anything.
//
// The function here takes 2s to come up and answers instantly, with a 500ms
// timeout. Charging init to the function makes that impossible to pass; not
// charging it makes it reliable. The remaining-deadline assertion is the part
// that would catch a regression that merely widened some grace period.
func TestColdStartIsNotChargedToTheFunctionTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	dir := buildBootstrapSource(t, slowInitBootstrap)
	r := NewRunner(Spec{
		Name:    "slowstart",
		Command: []string{"./bootstrap"},
		Dir:     dir,
		Timeout: 500 * time.Millisecond,
	}, nil)
	defer r.Stop()

	start := time.Now()
	res, err := r.Invoke(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.FunctionErr != "" {
		t.Fatalf("FunctionErr = %q, payload %s\n"+
			"The cold start was charged to the function's 500ms timeout.",
			res.FunctionErr, res.Payload)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("returned in %v — the 2s cold start cannot have happened, so this "+
			"test is no longer testing what it claims", elapsed)
	}

	var out struct {
		RemainingMS int64 `json:"remaining_ms"`
	}
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		t.Fatalf("payload %s: %v", res.Payload, err)
	}
	// The function must be handed a deadline measured from when IT got the
	// work. A deadline set at queue time would already be ~1.5s in the past.
	if out.RemainingMS <= 0 || out.RemainingMS > 500 {
		t.Errorf("the function was given %dms of its 500ms timeout; want (0, 500]\n"+
			"A non-positive value means the deadline was set before the function was handed the work.",
			out.RemainingMS)
	}
}

// buildBootstrapSource compiles a Runtime API client from source into a temp
// dir and returns the dir, so a test can describe the function it needs as a
// program rather than mocking the protocol.
func buildBootstrapSource(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module bootstrap\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap: %v\n%s", err, out)
	}
	return dir
}

// TestMaxWaitOutlastsEveryInternalBound pins the contract that lambda/invoke.go
// depends on: a caller's backstop deadline must not be able to fire before the
// runtime's own waits have run their course.
//
// This is the invariant whose absence caused the second half of the
// TestDeployedAPIInvokesLambda flake. The runtime's clock was fixed to exclude
// cold start, but the caller still capped the whole thing at Timeout+5s
// measured from before the process existed — so under load the context expired
// first and a function timeout surfaced as a 500 transport error instead of the
// 200-with-error-payload AWS returns.
func TestMaxWaitOutlastsEveryInternalBound(t *testing.T) {
	for _, timeout := range []time.Duration{0, time.Second, 3 * time.Second, 30 * time.Second} {
		effective := timeout
		if effective <= 0 {
			effective = defaultTimeout
		}
		// The two waits Invoke can perform back to back.
		internal := initBudget + effective + graceAfterDeadline
		if got := MaxWait(timeout); got < internal {
			t.Errorf("MaxWait(%v) = %v, which is shorter than the runtime's own "+
				"worst case %v — a caller using it would race the runtime and win",
				timeout, got, internal)
		}
	}
}
