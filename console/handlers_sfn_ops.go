package console

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ---- Step Functions: Express, TestState, redrive, heartbeat, Map Runs ----
//
// The panels that render in place. An Express run has no execution page to
// land on, a state test has no execution at all, and a Map Run is a panel
// under the history it belongs to — so these post and re-render a partial
// rather than redirect, the way the task-result form does.

// sfnStartSync runs an Express machine synchronously and renders the
// outcome where the form was. StartSyncExecution IS the Express value
// proposition — a workflow behind a request — and the panel says so.
func (c *Console) sfnStartSync(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	res, err := c.be.StartSyncExecution(r.Context(), startTargetOf(r, name), r.FormValue("name"), r.FormValue("input"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sfn_sync_result", map[string]any{"Name": name, "Result": res})
}

// sfnTestState runs one state of the definition — the editor's current
// text, so an unsaved edit can be tried before it is saved — and renders
// where it would go next. The mock is optional and only a Task, Map or
// Parallel can take one; the service refuses the rest by name.
func (c *Console) sfnTestState(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	definition := r.FormValue("definition")
	if strings.TrimSpace(definition) == "" {
		sm, err := c.be.DescribeStateMachine(r.Context(), stateMachineARNOf(name))
		if err != nil {
			c.fail(w, err)
			return
		}
		definition = sm.Definition
	}
	var mock *TestMock
	switch r.FormValue("mock") {
	case "result":
		mock = &TestMock{Result: strings.TrimSpace(r.FormValue("mock_result"))}
		if mock.Result == "" {
			mock.Result = "{}"
		}
	case "error":
		mock = &TestMock{Error: strings.TrimSpace(r.FormValue("mock_error")), Cause: strings.TrimSpace(r.FormValue("mock_cause"))}
	}
	level := r.FormValue("level")
	if level == "" {
		level = "INFO"
	}
	res, err := c.be.TestState(r.Context(), definition, r.FormValue("state"), r.FormValue("input"), level, mock)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sfn_test_result", map[string]any{"Name": name, "State": r.FormValue("state"), "Level": level, "Result": res})
}

// sfnRedrive restarts a finished execution from the states that did not
// finish. It redirects because the header changes — status back to RUNNING,
// the redrive count up by one — and the header is outside every live region.
func (c *Console) sfnRedrive(w http.ResponseWriter, r *http.Request) {
	machine, name := r.PathValue("machine"), r.PathValue("exec")
	if err := c.be.RedriveExecution(r.Context(), executionARNOf(machine, name)); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+machine+"/execution/"+name, "Execution redriven — the states that finished stay finished; the rest run again")
}

// sfnHeartbeat is the worker's "still here" for a token with HeartbeatSeconds.
// It sits on the task-result form because that form already holds the token,
// on the execution page and the activities page alike. Nothing on either
// page changes when it lands — AWS records no event for a heartbeat — so the
// answer is a toast and no swap; a token nobody holds any more is the
// refusal the wire gives.
func (c *Console) sfnHeartbeat(w http.ResponseWriter, r *http.Request) {
	if err := c.be.SendTaskHeartbeat(r.Context(), strings.TrimSpace(r.FormValue("token"))); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Heartbeat sent — the task's HeartbeatSeconds clock starts over")
	w.WriteHeader(http.StatusNoContent)
}

// sfnMapRunsFor is the Map Runs half of the history panel: every run the
// execution started, described. Hash parts ride along so the history's live
// region re-renders when a count moves, not only when an event lands.
func (c *Console) sfnMapRunsFor(r *http.Request, arn string) ([]MapRun, []string) {
	runs, _ := c.be.ListMapRuns(r.Context(), arn)
	parts := make([]string, 0, len(runs)*3)
	for _, mr := range runs {
		parts = append(parts, mr.Status, strconv.Itoa(mr.MaxConcurrency), fmt.Sprint(mr.Counts))
	}
	return runs, parts
}

// sfnMapRunUpdate is the inline "Set concurrency" form: UpdateMapRun, then
// the history panel again with the new number in it.
func (c *Console) sfnMapRunUpdate(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("max")))
	if err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.UpdateMapRun(r.Context(), r.FormValue("arn"), n); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Concurrency set to "+strconv.Itoa(n)+" — a raised limit takes effect at the next launch")
	c.partial(w, "sfn_history", c.sfnHistoryData(r, r.PathValue("machine"), r.PathValue("exec")))
}

// sfnMapRunChildren lists one run's child executions — the executions the
// machine's own list hides — into the slot under the Map Runs panel.
func (c *Console) sfnMapRunChildren(w http.ResponseWriter, r *http.Request) {
	arn := r.URL.Query().Get("arn")
	execs, err := c.be.ListMapRunExecutions(r.Context(), arn)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sfn_map_children", map[string]any{
		"Machine": r.PathValue("machine"), "Name": r.PathValue("exec"),
		"Label": mapRunLabel(arn), "Execs": execs,
	})
}
