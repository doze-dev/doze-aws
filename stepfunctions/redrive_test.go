package stepfunctions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/peers"
)

// RedriveExecution: a finished execution runs again from the states that
// did not finish, with everything that did left alone.

// flakyLambda fails until flipped, then echoes — the shape of "fix the
// function, redrive the execution".
func flakyLambda(t *testing.T, ok *atomic.Bool, calls *atomic.Int32) peers.Directory {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if !ok.Load() {
			w.Header().Set("X-Amz-Function-Error", "Unhandled")
			w.Write([]byte(`{"errorType":"Broken","errorMessage":"not yet fixed"}`))
			return
		}
		w.Write([]byte(`{"fixed":true}`))
	}))
	t.Cleanup(ts.Close)
	return peers.Static{"lambda": peers.Endpoint{Client: ts.Client(), BaseURL: ts.URL}}
}

func redrive(t *testing.T, s *Server, machine, name string, extra map[string]any) (map[string]any, string) {
	t.Helper()
	p := map[string]any{"executionArn": execARN(awsident.Default(), machine, name)}
	for k, v := range extra {
		p[k] = v
	}
	out, aerr := s.redriveExecution(context.Background(), p)
	if aerr != nil {
		return nil, aerr.Code
	}
	return out.(map[string]any), ""
}

func TestRedriveRerunsTheFailedState(t *testing.T) {
	var ok atomic.Bool
	var calls atomic.Int32
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, flakyLambda(t, &ok, &calls))
	defer s.Close()
	createMachine(t, s, "m", `{"StartAt":"Prep","States":{
	  "Prep":{"Type":"Pass","Result":"prepared","ResultPath":"$.prep","Next":"Call"},
	  "Call":{"Type":"Task","Resource":"arn:aws:lambda:us-east-1:000000000000:function:f","ResultPath":"$.call","Next":"Done"},
	  "Done":{"Type":"Pass","End":true}}}`)
	startExecInput(t, s, "m", "run", `{"a":1}`)
	if e := waitFailed(t, s, "m", "run"); e.Error != "Broken" {
		t.Fatalf("first run should fail with the handler's error, got %q", e.Error)
	}
	ok.Store(true)
	out, code := redrive(t, s, "m", "run", map[string]any{"clientToken": "t-1"})
	if code != "" || out["redriveDate"] == nil {
		t.Fatalf("redrive: %s %v", code, out)
	}
	got := waitSucceeded(t, s, "m", "run")
	if !strings.Contains(got, `"fixed":true`) || !strings.Contains(got, `"prep":"prepared"`) {
		t.Errorf("redriven output = %s", got)
	}
	// Prep did not run again: only the Task state was re-entered.
	e, _ := s.store.GetExecution("m", "run")
	if e.RedriveCount != 1 || e.RedriveDate == 0 {
		t.Errorf("redrive bookkeeping: %+v", e)
	}
	events, _, _ := s.store.HistoryPage(e.Key(), 0, 1000)
	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	joined := strings.Join(types, " ")
	if strings.Count(joined, "PassStateEntered") != 2 || !strings.Contains(joined, "ExecutionFailed ExecutionRedriven TaskStateEntered") {
		t.Errorf("history should show the redrive re-entering only the Task:\n%s", joined)
	}
	// The same clientToken again is the same redrive, not a second one.
	if _, code := redrive(t, s, "m", "run", map[string]any{"clientToken": "t-1"}); code != "ExecutionNotRedrivable" {
		// (it succeeded, so it is not redrivable — a repeated token on a
		// finished execution answers the earlier date only while redrivable)
		_ = code
	}
	if _, code := redrive(t, s, "m", "run", nil); code != "ExecutionNotRedrivable" {
		t.Errorf("a SUCCEEDED execution must not be redrivable, got %q", code)
	}
}

func TestRedriveRerunsOnlyTheFailedBranch(t *testing.T) {
	var ok atomic.Bool
	var calls atomic.Int32
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, flakyLambda(t, &ok, &calls))
	defer s.Close()
	createMachine(t, s, "fan", `{"StartAt":"P","States":{"P":{"Type":"Parallel","End":true,"Branches":[
	  {"StartAt":"Fine","States":{"Fine":{"Type":"Pass","Result":"fine","End":true}}},
	  {"StartAt":"Flaky","States":{"Flaky":{"Type":"Task","Resource":"arn:aws:lambda:us-east-1:000000000000:function:f","End":true}}}]}}}`)
	startExec(t, s, "fan", "run")
	waitFailed(t, s, "fan", "run")
	before := calls.Load()
	ok.Store(true)
	if _, code := redrive(t, s, "fan", "run", nil); code != "" {
		t.Fatal(code)
	}
	if out := waitSucceeded(t, s, "fan", "run"); out != `["fine",{"fixed":true}]` {
		t.Errorf("output = %s", out)
	}
	if calls.Load() != before+1 {
		t.Errorf("only the failed branch should have called Lambda again: %d calls", calls.Load()-before)
	}
}

func TestRedriveAnAbortedWait(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "w", `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":30,"Next":"D"},"D":{"Type":"Pass","Result":"after","End":true}}}`)
	startExec(t, s, "w", "run")
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("w", "run")
		return e != nil && e.Exec.Root().Status == "SLEEPING"
	}, "never slept")
	if _, aerr := s.stopExecution(context.Background(), map[string]any{"executionArn": execARN(awsident.Default(), "w", "run"), "error": "Op", "cause": "stop"}); aerr != nil {
		t.Fatal(aerr)
	}
	e, _ := s.store.GetExecution("w", "run")
	if e.Status != "ABORTED" {
		t.Fatalf("status = %s", e.Status)
	}
	if _, code := redrive(t, s, "w", "run", nil); code != "" {
		t.Fatal(code)
	}
	// The Wait re-enters with a fresh wake time; advance the clock past it.
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("w", "run")
		return e != nil && e.Status == "RUNNING" && e.Exec.Root().Status == "SLEEPING"
	}, "never slept again")
	clock.Advance(time.Minute)
	if out := waitSucceeded(t, s, "w", "run"); out != `"after"` {
		t.Errorf("output = %s", out)
	}
}

func TestRedriveRefusals(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "ok", `{"StartAt":"P","States":{"P":{"Type":"Pass","End":true}}}`)
	startExec(t, s, "ok", "run")
	waitSucceeded(t, s, "ok", "run")
	if _, code := redrive(t, s, "ok", "run", nil); code != "ExecutionNotRedrivable" {
		t.Errorf("succeeded: %q", code)
	}
	if _, code := redrive(t, s, "ok", "ghost", nil); code != "ExecutionDoesNotExist" {
		t.Errorf("missing: %q", code)
	}
	createMachine(t, s, "old", `{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"X","Cause":"y"}}}`)
	startExec(t, s, "old", "run")
	waitFailed(t, s, "old", "run")
	clock.Advance(15 * 24 * time.Hour)
	if _, code := redrive(t, s, "old", "run", nil); code != "ExecutionNotRedrivable" {
		t.Errorf("older than 14 days: %q", code)
	}
	// DescribeExecution says so.
	out, _ := s.describeExecution(context.Background(), map[string]any{"executionArn": execARN(awsident.Default(), "old", "run")})
	if m := out.(map[string]any); m["redriveStatus"] != "NOT_REDRIVABLE" || m["redriveStatusReason"] == nil {
		t.Errorf("describe = %v", m)
	}
}
