package stepfunctions

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A frame that runs two Parallel/Map states in sequence keeps the first
// state's settled branches in its frame list — they are history, and a
// restart needs them. The second state's join must not see them. Before the
// spawn generation stamp it did, and a Map after a Parallel joined with
// trailing nulls; found by the JS SDK verification pass.
func TestSequentialFanoutsDoNotShareChildren(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "twice", `{
	  "StartAt": "P1",
	  "States": {
	    "P1": {"Type": "Parallel", "ResultPath": "$.p1", "Next": "Each", "Branches": [
	      {"StartAt": "A", "States": {"A": {"Type": "Pass", "Result": "a1", "End": true}}},
	      {"StartAt": "B", "States": {"B": {"Type": "Pass", "Result": "b1", "End": true}}}]},
	    "Each": {"Type": "Map", "ItemsPath": "$.items", "ResultPath": "$.each", "Next": "P2",
	      "ItemProcessor": {"StartAt": "I", "States": {"I": {"Type": "Pass", "End": true}}}},
	    "P2": {"Type": "Parallel", "ResultPath": "$.p2", "End": true, "Branches": [
	      {"StartAt": "C", "States": {"C": {"Type": "Pass", "Result": "c2", "End": true}}}]}
	  }}`)
	startExecInput(t, s, "twice", "run", `{"items": [1, 2, 3]}`)
	out := waitSucceeded(t, s, "twice", "run")
	want := `{"each":[1,2,3],"items":[1,2,3],"p1":["a1","b1"],"p2":["c2"]}`
	if out != want {
		t.Errorf("output = %s\n want = %s", out, want)
	}
}

// A Parallel whose branch failure is caught, followed by a Map: the Map must
// not fail with the caught branch's error.
func TestCaughtBranchFailureDoesNotLeakIntoTheNextJoin(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "leak", `{
	  "StartAt": "P",
	  "States": {
	    "P": {"Type": "Parallel", "Next": "Each", "ResultPath": "$.p",
	      "Catch": [{"ErrorEquals": ["Branch.Bad"], "ResultPath": "$.caught", "Next": "Each"}],
	      "Branches": [
	        {"StartAt": "Ok", "States": {"Ok": {"Type": "Pass", "End": true}}},
	        {"StartAt": "Bad", "States": {"Bad": {"Type": "Fail", "Error": "Branch.Bad", "Cause": "x"}}}]},
	    "Each": {"Type": "Map", "ItemsPath": "$.items", "ResultPath": "$.each", "End": true,
	      "ItemProcessor": {"StartAt": "I", "States": {"I": {"Type": "Pass", "End": true}}}}
	  }}`)
	startExecInput(t, s, "leak", "run", `{"items": [7]}`)
	out := waitSucceeded(t, s, "leak", "run")
	if !strings.Contains(out, `"each":[7]`) || !strings.Contains(out, `"Error":"Branch.Bad"`) {
		t.Errorf("output = %s", out)
	}
}

// Map iteration events land when the iteration runs, chained on the
// iteration: an item held by MaxConcurrency has no MapIterationStarted until
// a sibling settles, and its Succeeded points at its own last event while the
// parent's chain stays at MapStateStarted.
func TestMapIterationEventsFollowTheIteration(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "iter", `{
	  "StartAt": "Each",
	  "States": {"Each": {"Type": "Map", "MaxConcurrency": 1, "End": true,
	    "ItemProcessor": {"StartAt": "I", "States": {"I": {"Type": "Pass", "End": true}}}}}}`)
	startExecInput(t, s, "iter", "run", `[1, 2]`)
	waitSucceeded(t, s, "iter", "run")
	e, _ := s.store.GetExecution("iter", "run")
	events, _, err := s.store.HistoryPage(e.Key(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]histEvent{}
	var types []string
	for _, ev := range events {
		byID[ev.ID] = ev
		types = append(types, fmt.Sprintf("%d:%s(%d)", ev.ID, ev.Type, ev.PrevID))
	}
	find := func(typ string, n int) histEvent {
		t.Helper()
		seen := 0
		for _, ev := range events {
			if ev.Type == typ {
				if seen == n {
					return ev
				}
				seen++
			}
		}
		t.Fatalf("no %s #%d in\n%s", typ, n, strings.Join(types, "\n"))
		return histEvent{}
	}
	started := find("MapStateStarted", 0)
	it0, it1 := find("MapIterationStarted", 0), find("MapIterationStarted", 1)
	ok0, ok1 := find("MapIterationSucceeded", 0), find("MapIterationSucceeded", 1)
	if it0.PrevID != started.ID || it1.PrevID != started.ID {
		t.Errorf("iterations should start from MapStateStarted:\n%s", strings.Join(types, "\n"))
	}
	if it1.ID < ok0.ID {
		t.Errorf("iteration 1 started (#%d) before iteration 0 succeeded (#%d) under MaxConcurrency 1:\n%s",
			it1.ID, ok0.ID, strings.Join(types, "\n"))
	}
	// Each Succeeded chains to the item's last event, a PassStateExited.
	for _, ok := range []histEvent{ok0, ok1} {
		if prev := byID[ok.PrevID]; prev.Type != "PassStateExited" {
			t.Errorf("%d:MapIterationSucceeded chains to %d:%s, want the item's PassStateExited:\n%s",
				ok.ID, prev.ID, prev.Type, strings.Join(types, "\n"))
		}
	}
	if done := find("MapStateSucceeded", 0); done.PrevID != started.ID {
		t.Errorf("MapStateSucceeded chains to %d, want MapStateStarted %d:\n%s", done.PrevID, started.ID, strings.Join(types, "\n"))
	}
}
