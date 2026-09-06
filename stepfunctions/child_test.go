package stepfunctions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Child executions through states:startExecution in its three patterns,
// against real machines in the same store — the one .sync integration that
// means something locally, since the thing waited for runs here.

const childDef = `{"StartAt":"Work","States":{"Work":{"Type":"Pass","Parameters":{"doubled.$":"States.MathAdd($.n, $.n)","by.$":"$"},"End":true}}}`

func parentDef(resource, extra string) string {
	return `{"StartAt":"Call","States":{"Call":{"Type":"Task","Resource":"` + resource + `",
	  "Parameters":{"StateMachineArn":"` + machineARN("child") + `","Name.$":"$.childName","Input":{"n.$":"$.n"}},
	  "ResultPath":"$.child",` + extra + `"End":true}}}`
}

func TestChildStartFireAndForget(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "child", childDef)
	createMachine(t, s, "parent", parentDef(childStartResource, ""))
	startExecInput(t, s, "parent", "run", `{"n":2,"childName":"kid-1"}`)
	out := waitSucceeded(t, s, "parent", "run")
	var v struct {
		Child struct{ ExecutionArn string }
	}
	json.Unmarshal([]byte(out), &v)
	if !strings.HasSuffix(v.Child.ExecutionArn, ":execution:child:kid-1") {
		t.Fatalf("result should carry the child's ARN, got %s", out)
	}
	if got := waitSucceeded(t, s, "child", "kid-1"); !strings.Contains(got, `"doubled":4`) {
		t.Errorf("child output = %s", got)
	}
}

func TestChildSyncWaitsAndCarriesTheOutcome(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "child", childDef)
	createMachine(t, s, "parent", parentDef(childStartResource+".sync", ""))
	createMachine(t, s, "parent2", parentDef(childStartResource+".sync:2", ""))

	startExecInput(t, s, "parent", "run", `{"n":3,"childName":"kid-sync"}`)
	out := waitSucceeded(t, s, "parent", "run")
	var v struct {
		Child struct {
			Status, Output, ExecutionArn string
		}
	}
	json.Unmarshal([]byte(out), &v)
	if v.Child.Status != "SUCCEEDED" || !strings.Contains(v.Child.Output, `"doubled":6`) {
		t.Fatalf(".sync result = %s", out)
	}
	// The child was told who started it.
	if !strings.Contains(v.Child.Output, `:execution:parent:run`) {
		t.Errorf("child input should name the parent execution, got %s", v.Child.Output)
	}

	startExecInput(t, s, "parent2", "run", `{"n":5,"childName":"kid-sync2"}`)
	out2 := waitSucceeded(t, s, "parent2", "run")
	var v2 struct {
		Child struct {
			Output map[string]any
		}
	}
	if err := json.Unmarshal([]byte(out2), &v2); err != nil || v2.Child.Output["doubled"] != 10.0 {
		t.Fatalf(".sync:2 should carry Output as a value, got %s (%v)", out2, err)
	}
}

func TestChildSyncFailureIsCatchable(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "child", `{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"Child.Bad","Cause":"nope"}}}`)
	createMachine(t, s, "parent", parentDef(childStartResource+".sync", ""))
	def := `{"StartAt":"Call","States":{"Call":{"Type":"Task","Resource":"` + childStartResource + `.sync",
	  "Parameters":{"StateMachineArn":"` + machineARN("child") + `","Name":"kid-fail"},
	  "Catch":[{"ErrorEquals":["States.TaskFailed"],"ResultPath":"$.err","Next":"Handled"}],"End":true},
	  "Handled":{"Type":"Pass","End":true}}}`
	createMachine(t, s, "parent-catch", def)
	startExec(t, s, "parent-catch", "run")
	out := waitSucceeded(t, s, "parent-catch", "run")
	var v struct {
		Err struct{ Error, Cause string } `json:"err"`
	}
	json.Unmarshal([]byte(out), &v)
	if v.Err.Error != "States.TaskFailed" || !strings.Contains(v.Err.Cause, `"Error":"Child.Bad"`) || !strings.Contains(v.Err.Cause, `"Status":"FAILED"`) {
		t.Errorf("caught child failure = %s", out)
	}
	// Uncaught, the parent fails the same way.
	startExecInput(t, s, "parent", "run", `{"n":1,"childName":"kid-fail-2"}`)
	if e := waitFailed(t, s, "parent", "run"); e.Error != "States.TaskFailed" {
		t.Errorf("parent error = %q", e.Error)
	}
}

func TestChildSyncOnExpress(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	if _, aerr := s.createStateMachine(context.Background(), map[string]any{
		"name": "child", "definition": childDef, "type": "EXPRESS",
		"roleArn": "arn:aws:iam::000000000000:role/x",
	}); aerr != nil {
		t.Fatal(aerr)
	}
	createMachine(t, s, "parent", parentDef(childStartResource+".sync:2", ""))
	startExecInput(t, s, "parent", "run", `{"n":4,"childName":"quick"}`)
	out := waitSucceeded(t, s, "parent", "run")
	if !strings.Contains(out, `"doubled":8`) || !strings.Contains(out, `:express:child:quick:`) {
		t.Errorf("Express child result = %s", out)
	}
}

func TestChildSyncSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, dir, clock)
	createMachine(t, s, "child", `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":30,"Next":"D"},"D":{"Type":"Pass","Result":"late","End":true}}}`)
	createMachine(t, s, "parent", parentDef(childStartResource+".sync:2", ""))
	startExecInput(t, s, "parent", "run", `{"n":1,"childName":"slow"}`)
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("parent", "run")
		return e != nil && e.Exec.Root().WaitExec != ""
	}, "the parent never parked on its child")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	clock.Advance(time.Minute)
	s2 := newTestServer(t, dir, clock)
	defer s2.Close()
	out := waitSucceeded(t, s2, "parent", "run")
	if !strings.Contains(out, `"Output":"late"`) {
		t.Errorf("after restart, parent result = %s", out)
	}
}

func TestChildWithTaskToken(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	// The child records the token it was handed; the test plays the worker.
	createMachine(t, s, "child", `{"StartAt":"P","States":{"P":{"Type":"Pass","End":true}}}`)
	createMachine(t, s, "parent", `{"StartAt":"Call","States":{"Call":{"Type":"Task",
	  "Resource":"`+childStartResource+`.waitForTaskToken",
	  "Parameters":{"StateMachineArn":"`+machineARN("child")+`","Name":"tok","Input":{"token.$":"$$.Task.Token"}},
	  "End":true}}}`)
	startExec(t, s, "parent", "run")
	token := parkedToken(t, s, "parent", "run")
	child, _ := s.store.GetExecution("child", "tok")
	if child == nil || !strings.Contains(child.Input, token) {
		t.Fatalf("the child should have been started with the token in its input: %+v", child)
	}
	if _, aerr := s.sendTaskSuccess(context.Background(), map[string]any{"taskToken": token, "output": `{"ok":true}`}); aerr != nil {
		t.Fatal(aerr)
	}
	if out := waitSucceeded(t, s, "parent", "run"); out != `{"ok":true}` {
		t.Errorf("parent output = %s", out)
	}
}

func TestChildRefusals(t *testing.T) {
	for _, r := range []string{
		"arn:aws:states:::ecs:runTask.sync",
		"arn:aws:states:::states:startExecution.sync.waitForTaskToken",
	} {
		if _, err := ParseResource(r); err == nil {
			t.Errorf("%s should be refused", r)
		}
	}
	for _, r := range []string{
		childStartResource, childStartResource + ".sync", childStartResource + ".sync:2", childStartResource + ".waitForTaskToken",
	} {
		if tt, err := ParseResource(r); err != nil || tt.Kind != taskChild {
			t.Errorf("%s: %v / %+v", r, err, tt)
		}
	}
}
