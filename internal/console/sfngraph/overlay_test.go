package sfngraph

import "testing"

// A synthetic history in the engine's own vocabulary: Prep passes, Review
// fails once and is retried, the Parallel's Ship branch fails again and is
// caught, the Map runs its processor three times, and a Wait is still going
// when the snapshot is taken.
func history() []Event {
	evs := []Event{
		{Type: "ExecutionStarted"},
		{Type: "PassStateEntered", State: "Prep"},
		{Type: "PassStateExited", State: "Prep"},
		{Type: "ChoiceStateEntered", State: "Route"},
		{Type: "ChoiceStateExited", State: "Route"},
		{Type: "TaskStateEntered", State: "Review"},
		{Type: "TaskScheduled"},
		{Type: "TaskStarted"},
		{Type: "TaskFailed"},
		{Type: "TaskScheduled"}, // the Retry
		{Type: "TaskStarted"},
		{Type: "TaskSucceeded"},
		{Type: "TaskStateExited", State: "Review"},
		{Type: "ParallelStateEntered", State: "Fan"},
		{Type: "TaskStateEntered", State: "Ship"},
		{Type: "PassStateEntered", State: "Bill"},
		{Type: "PassStateExited", State: "Bill"},
		{Type: "PassStateEntered", State: "Notify"},
		{Type: "PassStateExited", State: "Notify"},
		{Type: "LambdaFunctionScheduled"},
		{Type: "LambdaFunctionStarted"},
		{Type: "LambdaFunctionFailed"},
		{Type: "TaskStateExited", State: "Ship"}, // caught, not retried
		{Type: "ParallelStateExited", State: "Fan"},
		{Type: "MapStateEntered", State: "Each"},
	}
	for i := 0; i < 3; i++ {
		evs = append(evs,
			Event{Type: "ParallelStateEntered", State: "Inner"},
			Event{Type: "PassStateEntered", State: "A"}, Event{Type: "PassStateExited", State: "A"},
			Event{Type: "PassStateEntered", State: "B"}, Event{Type: "PassStateExited", State: "B"},
			Event{Type: "ParallelStateExited", State: "Inner"},
		)
	}
	evs = append(evs,
		Event{Type: "MapStateExited", State: "Each"},
		Event{Type: "WaitStateEntered", State: "Settle"},
	)
	// Ids in order, each chained to the one before: the shape a single
	// frame's history has. Attribution by id and by open-state fallback
	// must agree on it.
	for i := range evs {
		evs[i].ID = int64(i + 1)
		evs[i].PrevID = int64(i)
	}
	return evs
}

func graphFor(t *testing.T) *Graph {
	t.Helper()
	src := `{"StartAt":"Prep","States":{
	  "Prep":{"Type":"Pass","Next":"Route"},
	  "Route":{"Type":"Choice","Choices":[{"Variable":"$.x","IsPresent":true,"Next":"Review"}],"Default":"Fan"},
	  "Review":{"Type":"Task","Resource":"arn:aws:states:::lambda:invoke","Retry":[{"ErrorEquals":["States.ALL"]}],"Next":"Fan"},
	  "Fan":{"Type":"Parallel","Branches":[
	    {"StartAt":"Ship","States":{"Ship":{"Type":"Task","Resource":"arn:aws:lambda:us-east-1:1:function:ship","Catch":[{"ErrorEquals":["States.ALL"],"Next":"Sorry"}],"End":true},"Sorry":{"Type":"Pass","End":true}}},
	    {"StartAt":"Bill","States":{"Bill":{"Type":"Pass","Next":"Notify"},"Notify":{"Type":"Pass","End":true}}}
	  ],"Next":"Each"},
	  "Each":{"Type":"Map","ItemProcessor":{"StartAt":"Inner","States":{"Inner":{"Type":"Parallel","Branches":[
	    {"StartAt":"A","States":{"A":{"Type":"Pass","End":true}}},
	    {"StartAt":"B","States":{"B":{"Type":"Pass","End":true}}}],"End":true}}},"Next":"Settle"},
	  "Settle":{"Type":"Wait","Seconds":30,"Next":"Done"},
	  "Done":{"Type":"Succeed"}}}`
	return Layout(parse(t, src))
}

func TestOverlayStatuses(t *testing.T) {
	g := graphFor(t)
	Overlay(g, history(), true)
	want := map[string]string{
		"Prep": "succeeded", "Route": "succeeded", "Review": "succeeded", "Fan": "succeeded",
		"Ship": "caught", "Sorry": "", "Bill": "succeeded", "Notify": "succeeded",
		"Each": "succeeded", "Inner": "succeeded", "A": "succeeded", "B": "succeeded",
		"Settle": "running", "Done": "",
	}
	for name, status := range want {
		if n := nodeByName(g, name); n.Status != status {
			t.Errorf("%s: status %q, want %q", name, n.Status, status)
		}
	}
	if n := nodeByName(g, "Review"); n.Retries != 1 || n.Entered != 1 {
		t.Errorf("Review should record one retry: %+v", n)
	}
	if n := nodeByName(g, "Ship"); n.Retries != 0 {
		t.Errorf("a caught failure is not a retry: %+v", n)
	}
	for _, name := range []string{"Inner", "A", "B"} {
		if n := nodeByName(g, name); n.Entered != 3 || n.Exited != 3 {
			t.Errorf("Map processor state %s should be entered 3×: %+v", name, n)
		}
	}
}

// The same history once the execution has stopped: what was running is now
// cancelled, and an abort paints nothing red.
func TestOverlayTerminal(t *testing.T) {
	g := graphFor(t)
	evs := append(history(), Event{ID: 100, PrevID: 99, Type: "ExecutionAborted"})
	Overlay(g, evs, false)
	if n := nodeByName(g, "Settle"); n.Status != "cancelled" {
		t.Errorf("an aborted Wait is cancelled, got %q", n.Status)
	}
	if n := nodeByName(g, "Prep"); n.Status != "succeeded" {
		t.Errorf("finished states keep their outcome, got %q", n.Status)
	}

	// An uncaught task failure fails its state, its Parallel, and the run;
	// the other branch's states are cut off, not blamed.
	g = graphFor(t)
	evs = []Event{
		{ID: 1, Type: "ExecutionStarted"},
		{ID: 2, PrevID: 1, Type: "PassStateEntered", State: "Prep"},
		{ID: 3, PrevID: 2, Type: "PassStateExited", State: "Prep"},
		{ID: 4, PrevID: 3, Type: "ParallelStateEntered", State: "Fan"},
		{ID: 5, PrevID: 4, Type: "TaskStateEntered", State: "Ship"},
		{ID: 6, PrevID: 4, Type: "PassStateEntered", State: "Bill"},
		{ID: 7, PrevID: 5, Type: "LambdaFunctionScheduled"},
		{ID: 8, PrevID: 6, Type: "PassStateExited", State: "Bill"},
		{ID: 9, PrevID: 8, Type: "PassStateEntered", State: "Notify"},
		{ID: 10, PrevID: 7, Type: "LambdaFunctionFailed"},
		{ID: 11, PrevID: 4, Type: "ExecutionFailed"},
	}
	Overlay(g, evs, false)
	for name, status := range map[string]string{"Ship": "failed", "Fan": "failed", "Bill": "succeeded", "Notify": "cancelled", "Prep": "succeeded", "Each": ""} {
		if n := nodeByName(g, name); n.Status != status {
			t.Errorf("%s: status %q, want %q", name, n.Status, status)
		}
	}

	// A Fail state ends the execution without a task event to blame.
	g = graphFor(t)
	evs = []Event{
		{ID: 1, Type: "ExecutionStarted"},
		{ID: 2, PrevID: 1, Type: "PassStateEntered", State: "Prep"},
		{ID: 3, PrevID: 2, Type: "PassStateExited", State: "Prep"},
		{ID: 4, PrevID: 3, Type: "FailStateEntered", State: "Done"},
		{ID: 5, PrevID: 4, Type: "ExecutionFailed"},
	}
	Overlay(g, evs, false)
	if n := nodeByName(g, "Done"); n.Status != "failed" {
		t.Errorf("a Fail state is painted failed, got %q", n.Status)
	}
}

// Without ids the overlay falls back to the open-state stack, and on a
// single-frame history the answer is the same.
func TestOverlayWithoutIDs(t *testing.T) {
	g := graphFor(t)
	evs := history()
	for i := range evs {
		evs[i].ID, evs[i].PrevID = 0, 0
	}
	Overlay(g, evs, true)
	if n := nodeByName(g, "Review"); n.Status != "succeeded" || n.Retries != 1 {
		t.Errorf("Review without ids: %+v", n)
	}
	if n := nodeByName(g, "Ship"); n.Status != "caught" {
		t.Errorf("Ship without ids: %+v", n)
	}
}
