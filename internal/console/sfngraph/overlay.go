package sfngraph

import (
	"sort"
	"strings"
)

// Event is the slice of a history event the overlay reads. The console's
// HistoryEvent carries payloads and timestamps too; this is the shape that
// keeps sfngraph from importing console for the sake of one struct.
type Event struct {
	ID     int64
	PrevID int64
	Type   string // the AWS event type: TaskStateEntered, TaskFailed, ExecutionAborted …
	State  string // the name a StateEntered/StateExited event carries; "" on the rest
}

// Overlay colours a laid-out graph with what one execution did, the way the
// AWS console does over its execution graph: green where a state exited,
// red where it failed, amber where it is still going, grey where an abort
// cut it off, and nothing where it was never entered.
//
// Task events name no state — a TaskFailed says which resource, not which
// Task — so each is attributed by following previousEventId back to the
// StateEntered that opened its frame. That chain is what keeps two branches
// of a Parallel from claiming each other's failures. A history without ids
// (a test's, say) falls back to the most recently entered state still open.
//
// running says whether the execution is still RUNNING: an entered-but-not-
// exited state is "running" then, and "cancelled" once the execution has
// stopped without it finishing.
func Overlay(g *Graph, events []Event, running bool) {
	if g == nil {
		return
	}
	sorted := make([]Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	type tally struct {
		entered, exited, open, retries int
		failed                         bool // a failure nothing has retried since
		status                         string
	}
	tallies := map[string]*tally{}
	at := func(name string) *tally {
		t := tallies[name]
		if t == nil {
			t = &tally{}
			tallies[name] = t
		}
		return t
	}
	byID := map[int64]Event{}
	var openStack []string // fallback attribution when ids are missing

	// owner finds the state a task event belongs to.
	owner := func(ev Event) string {
		for prev, ok := byID[ev.PrevID]; ok; prev, ok = byID[prev.PrevID] {
			if strings.HasSuffix(prev.Type, "StateEntered") {
				return prev.State
			}
			if strings.HasSuffix(prev.Type, "StateExited") || prev.PrevID >= prev.ID {
				break
			}
		}
		if n := len(openStack); n > 0 {
			return openStack[n-1]
		}
		return ""
	}
	// closeAll settles every open state when the execution stops: the ones
	// that failed are failed, the rest were cut off. Failure is blamed on the
	// states that reported one — or, when none did, on the last state
	// entered, which is how a Fail state ends an execution.
	closeAll := func(failed bool) {
		if failed {
			blamed := false
			for _, t := range tallies {
				blamed = blamed || (t.open > 0 && t.failed)
			}
			if n := len(openStack); !blamed && n > 0 {
				at(openStack[n-1]).failed = true
			}
		}
		for _, t := range tallies {
			if t.open > 0 {
				t.open = 0
				t.status = "cancelled"
				if t.failed {
					t.status = "failed"
				}
			}
		}
		openStack = nil
	}

	for _, ev := range sorted {
		byID[ev.ID] = ev
		switch {
		case strings.HasSuffix(ev.Type, "StateEntered"):
			t := at(ev.State)
			t.entered++
			t.open++
			t.failed = false
			openStack = append(openStack, ev.State)
		case strings.HasSuffix(ev.Type, "StateExited"):
			t := at(ev.State)
			t.exited++
			if t.open > 0 {
				t.open--
			}
			// A state that failed and still exited was caught: Catch sent
			// the execution onward. AWS paints that its own colour.
			if t.failed {
				t.status = "caught"
			} else {
				t.status = "succeeded"
			}
			t.failed = false
			for i := len(openStack) - 1; i >= 0; i-- {
				if openStack[i] == ev.State {
					openStack = append(openStack[:i], openStack[i+1:]...)
					break
				}
			}
		case isTaskFailure(ev.Type):
			if name := owner(ev); name != "" {
				at(name).failed = true
			}
		case strings.HasSuffix(ev.Type, "Scheduled"):
			// A dispatch after a failure, inside the same state, is a Retry.
			if name := owner(ev); name != "" {
				if t := at(name); t.failed {
					t.retries++
					t.failed = false
				}
			}
		case ev.Type == "ExecutionFailed", ev.Type == "ExecutionTimedOut":
			closeAll(true)
		case ev.Type == "ExecutionAborted":
			closeAll(false)
		}
	}

	for _, n := range g.Nodes {
		t := tallies[n.Name]
		if t == nil {
			n.Status, n.Entered, n.Exited, n.Retries = "", 0, 0, 0
			continue
		}
		n.Entered, n.Exited, n.Retries = t.entered, t.exited, t.retries
		switch {
		case t.open > 0 && t.failed:
			n.Status = "failed"
		case t.open > 0 && running:
			n.Status = "running"
		case t.open > 0:
			n.Status = "cancelled"
		default:
			n.Status = t.status
		}
	}
	// A container ends the way its contents did: a branch that failed fails
	// the Parallel around it, even though the abort reached the container as
	// a cut-off rather than as a failure of its own.
	for _, c := range g.Nodes {
		if !c.Container || c.Status != "cancelled" {
			continue
		}
		for _, n := range g.Nodes {
			if n.Status == "failed" && strings.HasPrefix(n.ID, c.ID+"/") {
				c.Status = "failed"
				break
			}
		}
	}
}

// isTaskFailure matches the failure events of both task families — the
// Lambda one and the optimized-integration one — and the timeouts, which are
// failures with a different name.
func isTaskFailure(typ string) bool {
	if !strings.HasPrefix(typ, "Task") && !strings.HasPrefix(typ, "LambdaFunction") {
		return false
	}
	return strings.HasSuffix(typ, "Failed") || strings.HasSuffix(typ, "TimedOut")
}
