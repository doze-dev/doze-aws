package console

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/console/sfngraph"
	"github.com/doze-dev/doze-aws/internal/asl"
)

// ---- Step Functions ----
//
// Two pages: a machine (its executions, its definition, its tags) and an
// execution (its history, its input and output, the definition it froze).
// Executions finish asynchronously — the engine steps them on its own
// goroutine — so the execution list and the history are live regions that
// poll while anything is RUNNING, the way the SQS message panel polls.

func (c *Console) sfnMachines(w http.ResponseWriter, r *http.Request) {
	sms, err := c.be.ListStateMachines(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	if len(sms) > 0 {
		r.SetPathValue("machine", sms[0].Name)
		c.sfnMachine(w, r)
		return
	}
	c.render(w, r, "sfn_home", map[string]any{"List": sms, "Title": "Step Functions"})
}

func (c *Console) sfnCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	_, err := c.be.CreateStateMachine(r.Context(), name, r.FormValue("definition"), r.FormValue("role"), r.FormValue("type"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+name, "State machine “"+name+"” created")
}

// sfnValidate is the create form's live check: the analyser's diagnostics,
// rendered next to the editor, without creating anything. It is the same
// shape as EventBridge's test-pattern partial.
func (c *Console) sfnValidate(w http.ResponseWriter, r *http.Request) {
	diags, err := c.be.ValidateDefinition(r.Context(), r.FormValue("definition"), r.FormValue("type"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sfn_diagnostics", map[string]any{"Diagnostics": diags, "Checked": true})
}

func (c *Console) sfnDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteStateMachine(r.Context(), stateMachineARNOf(r.PathValue("machine"))); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn", "State machine deleted")
}

func (c *Console) sfnUpdateDefinition(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	if err := c.be.UpdateStateMachine(r.Context(), stateMachineARNOf(name), r.FormValue("definition"), r.FormValue("role")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+name+"?tab=definition", "Definition updated — running executions keep the one they started with")
}

func (c *Console) sfnMachine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	sm, err := c.be.DescribeStateMachine(r.Context(), stateMachineARNOf(name))
	if err != nil {
		c.fail(w, err)
		return
	}
	sms, _ := c.be.ListStateMachines(r.Context())
	data := map[string]any{
		"Machine": sm, "Name": name, "ARN": sm.ARN, "List": sms,
		"Tab": tabOf(r, "executions"), "Title": name + " · Step Functions",
	}
	for k, v := range c.sfnExecutionsData(r, name) {
		data[k] = v
	}
	if data["Tab"] == "graph" {
		data["Graph"], data["GraphErr"] = graphOf(sm.Definition, nil, false)
	}
	c.render(w, r, "sfn_machine", data)
}

// sfnExecutionsData is what the executions panel renders, shared by the page
// and its poll. Running is what decides whether the region keeps polling.
func (c *Console) sfnExecutionsData(r *http.Request, name string) map[string]any {
	execs, _ := c.be.ListExecutions(r.Context(), stateMachineARNOf(name), r.URL.Query().Get("status"))
	running := 0
	parts := make([]string, 0, len(execs))
	for _, e := range execs {
		if e.Status == "RUNNING" {
			running++
		}
		parts = append(parts, e.Name, e.Status, e.Stopped)
	}
	return map[string]any{
		"Name": name, "Execs": execs, "Running": running,
		"Filter": r.URL.Query().Get("status"),
		"Hash":   contentHash(parts...),
	}
}

// sfnExecutions is the polled executions partial: 204 when unchanged.
func (c *Console) sfnExecutions(w http.ResponseWriter, r *http.Request) {
	data := c.sfnExecutionsData(r, r.PathValue("machine"))
	if liveUnchanged(w, r, data["Hash"].(string)) {
		return
	}
	c.partial(w, "sfn_executions", data)
}

func (c *Console) sfnStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	arn, err := c.be.StartExecution(r.Context(), stateMachineARNOf(name), r.FormValue("name"), r.FormValue("input"))
	if err != nil {
		c.fail(w, err)
		return
	}
	_, exec := parseExecutionARN(arn)
	c.redirect(w, r, c.prefix+"/sfn/"+name+"/execution/"+exec, "Execution “"+exec+"” started")
}

// parseExecutionARN splits arn:aws:states:<r>:<a>:execution:<machine>:<name>.
func parseExecutionARN(arn string) (machine, name string) {
	parts := strings.SplitN(arn, ":", 8)
	if len(parts) != 8 || parts[5] != "execution" {
		return "", arnLeaf(arn)
	}
	return parts[6], parts[7]
}

func (c *Console) sfnExecution(w http.ResponseWriter, r *http.Request) {
	machine, name := r.PathValue("machine"), r.PathValue("exec")
	arn := executionARNOf(machine, name)
	ex, err := c.be.DescribeExecution(r.Context(), arn)
	if err != nil {
		c.fail(w, err)
		return
	}
	sms, _ := c.be.ListStateMachines(r.Context())
	data := map[string]any{
		"Exec": ex, "Machine": machine, "Name": name, "ARN": arn, "List": sms,
		"Tab": tabOf(r, "history"), "Title": name + " · " + machine + " · Step Functions",
	}
	switch data["Tab"] {
	case "definition":
		data["Definition"], _ = c.be.ExecutionDefinition(r.Context(), arn)
	case "graph":
		for k, v := range c.sfnGraphData(r, machine, name) {
			data[k] = v
		}
	default:
		for k, v := range c.sfnHistoryData(r, machine, name) {
			data[k] = v
		}
	}
	c.render(w, r, "sfn_execution", data)
}

// sfnHistoryData is the history panel, shared by the page and its poll. The
// execution's status rides along so the panel can stop polling — and show the
// stop form — from the same fetch.
func (c *Console) sfnHistoryData(r *http.Request, machine, name string) map[string]any {
	arn := executionARNOf(machine, name)
	ex, _ := c.be.DescribeExecution(r.Context(), arn)
	evs, _ := c.be.ExecutionHistory(r.Context(), arn)
	parts := []string{ex.Status, strconv.Itoa(len(evs))}
	if n := len(evs); n > 0 {
		parts = append(parts, strconv.FormatInt(evs[n-1].ID, 10))
	}
	return map[string]any{
		"Machine": machine, "Name": name, "Exec": ex, "Events": evs,
		"Hash": contentHash(parts...),
	}
}

// sfnHistory is the polled history partial: 204 when unchanged.
func (c *Console) sfnHistory(w http.ResponseWriter, r *http.Request) {
	data := c.sfnHistoryData(r, r.PathValue("machine"), r.PathValue("exec"))
	if liveUnchanged(w, r, data["Hash"].(string)) {
		return
	}
	c.partial(w, "sfn_history", data)
}

func (c *Console) sfnStop(w http.ResponseWriter, r *http.Request) {
	machine, name := r.PathValue("machine"), r.PathValue("exec")
	if err := c.be.StopExecution(r.Context(), executionARNOf(machine, name), r.FormValue("error"), r.FormValue("cause")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+machine+"/execution/"+name, "Execution stopped")
}

// sfnTaskResult redeems a task token by hand. A state parked on
// .waitForTaskToken is waiting for a worker to call back; when the worker is
// you, this is the callback. The token itself is in the parameters the Task
// sent — the history shows them — and pasting it here is the honest shape:
// the console cannot know a token the service handed to someone else.
func (c *Console) sfnTaskResult(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.FormValue("token"))
	success := r.FormValue("outcome") != "failure"
	if err := c.be.SendTaskResult(r.Context(), token, success, r.FormValue("output"), r.FormValue("error"), r.FormValue("cause")); err != nil {
		c.fail(w, err)
		return
	}
	msg := "Task succeeded — the execution moves on"
	if !success {
		msg = "Task failed — Retry and Catch decide what happens next"
	}
	toast(w, msg)
	c.partial(w, "sfn_history", c.sfnHistoryData(r, r.PathValue("machine"), r.PathValue("exec")))
}

// ---- the graph ----
//
// The machine page draws its definition; the execution page draws the
// definition the execution froze and colours it with the history. Layout is
// sfngraph's — the handler only parses and hands over.

// graphOf lays out a definition and, given a history, overlays it. A
// definition that does not parse yields the error for the panel to show
// rather than a blank picture: the machine was accepted with it, so the
// person reading the page did not write it just now.
func graphOf(definition string, evs []HistoryEvent, running bool) (*sfngraph.Graph, string) {
	def, err := asl.Parse([]byte(definition))
	if err != nil {
		return nil, err.Error()
	}
	g := sfngraph.Layout(def)
	if evs != nil {
		events := make([]sfngraph.Event, 0, len(evs))
		for _, ev := range evs {
			events = append(events, sfngraph.Event{ID: ev.ID, PrevID: ev.PrevID, Type: ev.Type, State: ev.State})
		}
		sfngraph.Overlay(g, events, running)
	}
	return g, ""
}

// sfnGraphData is the execution graph panel plus the history under it — the
// graph tab shows both, because a node click lands on its history rows. The
// hash is the history's: the picture changes exactly when the events do.
func (c *Console) sfnGraphData(r *http.Request, machine, name string) map[string]any {
	data := c.sfnHistoryData(r, machine, name)
	definition, _ := c.be.ExecutionDefinition(r.Context(), executionARNOf(machine, name))
	ex := data["Exec"].(Execution)
	evs := data["Events"].([]HistoryEvent)
	if evs == nil {
		evs = []HistoryEvent{}
	}
	data["Graph"], data["GraphErr"] = graphOf(definition, evs, ex.Status == "RUNNING")
	return data
}

// sfnGraph is the polled graph partial: 204 when unchanged.
func (c *Console) sfnGraph(w http.ResponseWriter, r *http.Request) {
	data := c.sfnGraphData(r, r.PathValue("machine"), r.PathValue("exec"))
	if liveUnchanged(w, r, data["Hash"].(string)) {
		return
	}
	c.partial(w, "sfn_graph_live", data)
}
