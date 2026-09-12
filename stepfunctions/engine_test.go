package stepfunctions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/peers"
)

// Engine lifecycle tests: the close-race and restart-resume guarantees. These
// drive the handlers directly rather than through the SDK, because they need
// the fake clock and the store.

// testClock is a settable clock safe to read from the engine goroutine.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestServer(t *testing.T, dir string, clock *testClock) *Server {
	t.Helper()
	s, err := New(Options{DataDir: dir, Logf: t.Logf, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func createMachine(t *testing.T, s *Server, name, def string) {
	t.Helper()
	_, aerr := s.createStateMachine(context.Background(), map[string]any{
		"name": name, "definition": def,
		"roleArn": "arn:aws:iam::000000000000:role/StepFunctions",
	})
	if aerr != nil {
		t.Fatalf("createStateMachine: %v", aerr)
	}
}

func startExec(t *testing.T, s *Server, machine, name string) {
	t.Helper()
	_, aerr := s.startExecution(context.Background(), map[string]any{
		"stateMachineArn": machineARN(awsident.Default(), machine), "name": name,
	})
	if aerr != nil {
		t.Fatalf("startExecution: %v", aerr)
	}
}

const waitingDef = `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":5,"Next":"S"},"S":{"Type":"Pass","Result":"resumed","End":true}}}`

// TestCloseUnderLoad is the close-race exit criterion: many executions in
// flight, Close called immediately, and nothing may panic or leak — run under
// -race in the suite. The ordering being tested: the driver exits before the
// store closes, so no goroutine can write to a closed bbolt.
func TestCloseUnderLoad(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	createMachine(t, s, "racy", waitingDef)
	for i := 0; i < 50; i++ {
		startExec(t, s, "racy", fmt.Sprintf("burst-%d", i))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Closing twice must be as safe as closing once.
	s.engine.close()
}

// TestRestartResumesAWait is the restart exit criterion: an execution parked
// on a Wait survives a Close, and a reopened stack completes it once the
// clock has passed its wake time. Recovery is the same code path as steady
// state; this proves it.
func TestRestartResumesAWait(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{now: time.Now()}

	s := newTestServer(t, dir, clock)
	createMachine(t, s, "napper", waitingDef)
	startExec(t, s, "napper", "sleeper")

	// The frame must be persisted SLEEPING before the stack goes down.
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("napper", "sleeper")
		return e != nil && e.Exec.Root().WakeAt != 0
	}, "the Wait was never persisted")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The machine was off while the wait elapsed.
	clock.Advance(10 * time.Second)

	s2 := newTestServer(t, dir, clock)
	defer s2.Close()
	waitFor(t, func() bool {
		e, _ := s2.store.GetExecution("napper", "sleeper")
		return e != nil && e.Status == "SUCCEEDED"
	}, "the reopened stack never completed the parked execution")
	e, _ := s2.store.GetExecution("napper", "sleeper")
	if string(e.Output) != `"resumed"` {
		t.Errorf("output = %s", e.Output)
	}
}

// TestTickerWakesAWait: the live path rather than the restart path — the
// execution sleeps, the clock jumps, and the 1s ticker notices.
func TestTickerWakesAWait(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "ticky", waitingDef)
	startExec(t, s, "ticky", "dozing")

	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("ticky", "dozing")
		return e != nil && e.Exec.Root().WakeAt != 0
	}, "never went to sleep")
	clock.Advance(10 * time.Second)
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("ticky", "dozing")
		return e != nil && e.Status == "SUCCEEDED"
	}, "the ticker never woke the execution")
}

// TestMachineTimeoutFires: an execution-level TimeoutSeconds ends the
// execution as TIMED_OUT with States.Timeout.
func TestMachineTimeoutFires(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "slowpoke",
		`{"TimeoutSeconds":30,"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":300,"Next":"S"},"S":{"Type":"Succeed"}}}`)
	startExec(t, s, "slowpoke", "doomed")

	clock.Advance(60 * time.Second)
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("slowpoke", "doomed")
		return e != nil && e.Status == "TIMED_OUT"
	}, "the execution deadline never fired")
	e, _ := s.store.GetExecution("slowpoke", "doomed")
	if e.Error != "States.Timeout" {
		t.Errorf("error = %q, want States.Timeout", e.Error)
	}
}

// waitSucceeded polls until the execution succeeds and returns its output.
func waitSucceeded(t *testing.T, s *Server, machine, name string) string {
	t.Helper()
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution(machine, name)
		if e != nil && e.Status != "RUNNING" && e.Status != "SUCCEEDED" {
			t.Fatalf("execution settled as %s (%s: %s)", e.Status, e.Error, e.Cause)
		}
		return e != nil && e.Status == "SUCCEEDED"
	}, "execution never succeeded")
	e, _ := s.store.GetExecution(machine, name)
	return string(e.Output)
}

func startExecInput(t *testing.T, s *Server, machine, name, input string) {
	t.Helper()
	_, aerr := s.startExecution(context.Background(), map[string]any{
		"stateMachineArn": machineARN(awsident.Default(), machine), "name": name, "input": input,
	})
	if aerr != nil {
		t.Fatalf("startExecution: %v", aerr)
	}
}

// TestParallelBranches: branches run as sibling frames and join in branch
// order, whatever order they finish in.
func TestParallelBranches(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "fanout", `{
	  "StartAt": "Both",
	  "States": {"Both": {"Type": "Parallel", "Branches": [
	    {"StartAt": "A", "States": {"A": {"Type": "Pass", "Result": "first", "End": true}}},
	    {"StartAt": "B", "States": {"B": {"Type": "Wait", "Seconds": 0, "Next": "B2"},
	      "B2": {"Type": "Pass", "Result": "second", "End": true}}}
	  ], "End": true}}
	}`)
	startExec(t, s, "fanout", "run")
	if out := waitSucceeded(t, s, "fanout", "run"); out != `["first","second"]` {
		t.Errorf("output = %s", out)
	}
}

// TestParallelBranchFailureCaught: a branch failure fails the Parallel with
// the branch's own error name, which the parent's Catch routes.
func TestParallelBranchFailureCaught(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "brittle", `{
	  "StartAt": "Both",
	  "States": {
	    "Both": {"Type": "Parallel", "Branches": [
	      {"StartAt": "OK", "States": {"OK": {"Type": "Wait", "Seconds": 300, "Next": "N"}, "N": {"Type": "Succeed"}}},
	      {"StartAt": "Boom", "States": {"Boom": {"Type": "Fail", "Error": "Branch.Broke", "Cause": "x"}}}
	    ],
	    "Catch": [{"ErrorEquals": ["Branch.Broke"], "ResultPath": "$.err", "Next": "Saved"}],
	    "End": true},
	    "Saved": {"Type": "Pass", "Parameters": {"got.$": "$.err.Error"}, "End": true}
	  }
	}`)
	startExec(t, s, "brittle", "run")
	if out := waitSucceeded(t, s, "brittle", "run"); out != `{"got":"Branch.Broke"}` {
		t.Errorf("output = %s", out)
	}
}

// TestMapWithConcurrencyAndSelector: items run through the ItemProcessor with
// MaxConcurrency spawning the tail PENDING, ItemSelector shaping each item
// via $$.Map.Item, and outputs landing in item order.
func TestMapWithConcurrencyAndSelector(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "mapper", `{
	  "StartAt": "Each",
	  "States": {"Each": {"Type": "Map", "ItemsPath": "$.items", "MaxConcurrency": 1,
	    "ItemSelector": {"i.$": "$$.Map.Item.Index", "v.$": "$$.Map.Item.Value"},
	    "ItemProcessor": {"StartAt": "P", "States": {
	      "P": {"Type": "Pass", "Parameters": {"seen.$": "$.v", "at.$": "$.i"}, "End": true}}},
	    "End": true}}
	}`)
	startExecInput(t, s, "mapper", "run", `{"items": ["a", "b", "c"]}`)
	want := `[{"at":0,"seen":"a"},{"at":1,"seen":"b"},{"at":2,"seen":"c"}]`
	if out := waitSucceeded(t, s, "mapper", "run"); out != want {
		t.Errorf("output = %s\n want = %s", out, want)
	}
}

// TestMapOverEmptyArray joins immediately with an empty result.
func TestMapOverEmptyArray(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "empty", `{
	  "StartAt": "Each",
	  "States": {"Each": {"Type": "Map",
	    "ItemProcessor": {"StartAt": "P", "States": {"P": {"Type": "Succeed"}}},
	    "End": true}}
	}`)
	startExecInput(t, s, "empty", "run", `[]`)
	if out := waitSucceeded(t, s, "empty", "run"); out != `[]` {
		t.Errorf("output = %s", out)
	}
}

// TestRestartMidParallel: one branch done, one parked on a Wait; the whole
// execution survives a Close and completes on the reopened stack.
func TestRestartMidParallel(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, dir, clock)
	createMachine(t, s, "split", `{
	  "StartAt": "Both",
	  "States": {"Both": {"Type": "Parallel", "Branches": [
	    {"StartAt": "A", "States": {"A": {"Type": "Pass", "Result": 1, "End": true}}},
	    {"StartAt": "B", "States": {"B": {"Type": "Wait", "Seconds": 30, "Next": "B2"},
	      "B2": {"Type": "Pass", "Result": 2, "End": true}}}
	  ], "End": true}}
	}`)
	startExec(t, s, "split", "run")
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("split", "run")
		if e == nil {
			return false
		}
		for _, f := range e.Exec.Frames {
			if f.WakeAt != 0 {
				return true
			}
		}
		return false
	}, "the branch Wait was never persisted")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	clock.Advance(60 * time.Second)
	s2 := newTestServer(t, dir, clock)
	defer s2.Close()
	if out := waitSucceeded(t, s2, "split", "run"); out != `[1,2]` {
		t.Errorf("output = %s", out)
	}
}

// stubSQS answers every SendMessage with a fixed happy response, so a token
// task's send half succeeds without a real SQS.
func stubSQS(t *testing.T) peers.Directory {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.Write([]byte(`{"MessageId":"m-1","MD5OfMessageBody":"d41d8cd9"}`))
	}))
	t.Cleanup(ts.Close)
	return peers.Static{"sqs": peers.Endpoint{Client: ts.Client(), BaseURL: ts.URL}}
}

func newTestServerPeers(t *testing.T, dir string, clock *testClock, peers peers.Directory) *Server {
	t.Helper()
	s, err := New(Options{DataDir: dir, Logf: t.Logf, Clock: clock.Now, Peers: peers})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const tokenDef = `{"StartAt":"Ask","States":{"Ask":{"Type":"Task",
  "Resource":"arn:aws:states:::sqs:sendMessage.waitForTaskToken",
  "Parameters":{"QueueUrl":"http://q/000000000000/approvals","MessageBody":{"token.$":"$$.Task.Token"}},
  "End":true}}}`

// parkedToken waits until the execution is parked and returns its token.
func parkedToken(t *testing.T, s *Server, machine, name string) string {
	t.Helper()
	var token string
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution(machine, name)
		if e == nil {
			return false
		}
		token = e.Exec.Root().Token
		return e.Exec.Root().Status == "PARKED" && token != ""
	}, "the token task never parked")
	if ref, err := s.store.GetToken(token); err != nil || ref == nil {
		t.Fatalf("the parked token has no redeemable row (ref=%v err=%v)", ref, err)
	}
	return token
}

// TestTokenParkRestartRedeem is the callback pattern's whole promise: park on
// a task token, close the stack, reopen it, and the token still redeems.
func TestTokenParkRestartRedeem(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, dir, clock, stubSQS(t))
	createMachine(t, s, "approval", tokenDef)
	startExec(t, s, "approval", "req")
	token := parkedToken(t, s, "approval", "req")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := newTestServerPeers(t, dir, clock, stubSQS(t))
	defer s2.Close()
	if _, aerr := s2.sendTaskSuccess(context.Background(), map[string]any{
		"taskToken": token, "output": `{"approved": true}`,
	}); aerr != nil {
		t.Fatalf("SendTaskSuccess after restart: %v", aerr)
	}
	if out := waitSucceeded(t, s2, "approval", "req"); out != `{"approved":true}` {
		t.Errorf("output = %s", out)
	}
	// The redeemed token is spent.
	if _, aerr := s2.sendTaskSuccess(context.Background(), map[string]any{
		"taskToken": token, "output": `{}`,
	}); aerr == nil || aerr.Code != "TaskDoesNotExist" {
		t.Errorf("redeeming twice = %v, want TaskDoesNotExist", aerr)
	}
}

// TestTokenFailureRoutesThroughCatch: SendTaskFailure's error name is what
// the state's Catch matches.
func TestTokenFailureRoutesThroughCatch(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, stubSQS(t))
	defer s.Close()
	createMachine(t, s, "veto", `{"StartAt":"Ask","States":{
	  "Ask":{"Type":"Task","Resource":"arn:aws:states:::sqs:sendMessage.waitForTaskToken",
	    "Parameters":{"QueueUrl":"http://q/000000000000/approvals","MessageBody":{"token.$":"$$.Task.Token"}},
	    "Catch":[{"ErrorEquals":["Rejected"],"Next":"No"}],"End":true},
	  "No":{"Type":"Pass","Result":"vetoed","End":true}}}`)
	startExec(t, s, "veto", "req")
	token := parkedToken(t, s, "veto", "req")
	if _, aerr := s.sendTaskFailure(context.Background(), map[string]any{
		"taskToken": token, "error": "Rejected", "cause": "nope",
	}); aerr != nil {
		t.Fatal(aerr)
	}
	if out := waitSucceeded(t, s, "veto", "req"); out != `"vetoed"` {
		t.Errorf("output = %s", out)
	}
}

// TestHeartbeatTimeout: a parked task with HeartbeatSeconds dies of silence,
// and a heartbeat keeps it alive.
func TestHeartbeatTimeout(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, stubSQS(t))
	defer s.Close()
	createMachine(t, s, "pulse", `{"StartAt":"Ask","States":{
	  "Ask":{"Type":"Task","Resource":"arn:aws:states:::sqs:sendMessage.waitForTaskToken",
	    "HeartbeatSeconds": 30,
	    "Parameters":{"QueueUrl":"http://q/000000000000/x","MessageBody":{"token.$":"$$.Task.Token"}},
	    "End":true}}}`)
	startExec(t, s, "pulse", "kept")
	token := parkedToken(t, s, "pulse", "kept")

	// A heartbeat at +20s pushes the deadline; the task is still alive at
	// +40s, then dies at +55s of silence.
	clock.Advance(20 * time.Second)
	if _, aerr := s.sendTaskHeartbeat(context.Background(), map[string]any{"taskToken": token}); aerr != nil {
		t.Fatal(aerr)
	}
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("pulse", "kept")
		return e != nil && e.Exec.Root().HeartbeatAt >= clock.Now().UnixMilli()-1000
	}, "the heartbeat never landed")
	clock.Advance(35 * time.Second)
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("pulse", "kept")
		return e != nil && e.Status == "FAILED"
	}, "the heartbeat timeout never fired")
	e, _ := s.store.GetExecution("pulse", "kept")
	if e.Error != "States.HeartbeatTimeout" {
		t.Errorf("error = %q, want States.HeartbeatTimeout", e.Error)
	}
	// A worker that answers after the timeout hears TaskTimedOut — the
	// tombstone AWS keeps — not the TaskDoesNotExist of a token never minted.
	for _, call := range []func(map[string]any) *awshttp.APIError{
		func(p map[string]any) *awshttp.APIError { _, a := s.sendTaskSuccess(context.Background(), p); return a },
		func(p map[string]any) *awshttp.APIError { _, a := s.sendTaskFailure(context.Background(), p); return a },
		func(p map[string]any) *awshttp.APIError {
			_, a := s.sendTaskHeartbeat(context.Background(), p)
			return a
		},
	} {
		if aerr := call(map[string]any{"taskToken": token, "output": "{}"}); aerr == nil || aerr.Code != "TaskTimedOut" {
			t.Errorf("after the timeout, SendTask* = %v, want TaskTimedOut", aerr)
		}
	}
	// The tombstone is swept a day later, and the token is then unknown.
	clock.Advance(25 * time.Hour)
	startExec(t, s, "pulse", "later")
	later := parkedToken(t, s, "pulse", "later")
	clock.Advance(40 * time.Second) // times out too, which writes a tombstone and sweeps
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("pulse", "later")
		return e != nil && e.Status == "FAILED"
	}, "the second timeout never fired")
	if _, aerr := s.sendTaskSuccess(context.Background(), map[string]any{"taskToken": token, "output": "{}"}); aerr == nil || aerr.Code != "TaskDoesNotExist" {
		t.Errorf("a day-old tombstone should be swept: %v", aerr)
	}
	if _, aerr := s.sendTaskSuccess(context.Background(), map[string]any{"taskToken": later, "output": "{}"}); aerr == nil || aerr.Code != "TaskTimedOut" {
		t.Errorf("the fresh tombstone should stand: %v", aerr)
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
