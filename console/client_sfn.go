package console

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// ---- Step Functions (JSON 1.0, target AWSStepFunctions) ----
//
// The one service in the stack on JSON 1.0 with lowercase-initial members:
// stateMachineArn, not StateMachineArn. Every request and response struct
// below spells them the way the wire does, because a wrong case decodes to an
// empty struct with no error to say so.

// StateMachine is one row of the list pane, plus what Describe adds.
type StateMachine struct {
	Name       string
	ARN        string
	Type       string // STANDARD | EXPRESS
	Status     string // ACTIVE | DELETING
	Definition string // pretty-printed ASL; "" from the list
	RoleARN    string
	Created    string
	Updated    string
	Revision   string
	States     int // top-level states, counted from the definition
}

// Execution is one run of a machine.
type Execution struct {
	Name       string
	ARN        string
	MachineARN string
	Machine    string
	Status     string // RUNNING | SUCCEEDED | FAILED | TIMED_OUT | ABORTED
	Started    string
	Stopped    string
	Input      string
	Output     string
	Error      string
	Cause      string
}

// HistoryEvent is one GetExecutionHistory row, flattened for a table: the
// state name and the payload come from whichever *EventDetails the event
// carries, so the template never has to know the sixty detail-key names.
type HistoryEvent struct {
	ID      int64
	PrevID  int64
	When    string
	Type    string
	State   string // the state a StateEntered/Exited event names
	Payload string // input, output, parameters, or error+cause — whichever the event has
	Error   string
	Cause   string
	IsErr   bool
}

// sfnCall posts a Step Functions JSON 1.0 request.
func (b *backend) sfnCall(ctx context.Context, action string, in any) ([]byte, error) {
	return b.json10(ctx, "AWSStepFunctions", action, in)
}

func (b *backend) ListStateMachines(ctx context.Context) ([]StateMachine, error) {
	body, err := b.sfnCall(ctx, "ListStateMachines", map[string]any{"maxResults": 1000})
	if err != nil {
		return nil, err
	}
	var out struct {
		StateMachines []struct {
			Name         string  `json:"name"`
			ARN          string  `json:"stateMachineArn"`
			Type         string  `json:"type"`
			CreationDate float64 `json:"creationDate"`
		} `json:"stateMachines"`
	}
	json.Unmarshal(body, &out)
	sms := make([]StateMachine, 0, len(out.StateMachines))
	for _, m := range out.StateMachines {
		sms = append(sms, StateMachine{Name: m.Name, ARN: m.ARN, Type: m.Type, Created: epochToTime(m.CreationDate)})
	}
	sort.Slice(sms, func(i, j int) bool { return sms[i].Name < sms[j].Name })
	return sms, nil
}

// CountStateMachines is the cheap cardinality probe: one list call.
func (b *backend) CountStateMachines(ctx context.Context) (int, error) {
	sms, err := b.ListStateMachines(ctx)
	return len(sms), err
}

func (b *backend) DescribeStateMachine(ctx context.Context, arn string) (StateMachine, error) {
	body, err := b.sfnCall(ctx, "DescribeStateMachine", map[string]any{"stateMachineArn": arn})
	if err != nil {
		return StateMachine{}, err
	}
	var out struct {
		Name         string  `json:"name"`
		ARN          string  `json:"stateMachineArn"`
		Type         string  `json:"type"`
		Status       string  `json:"status"`
		Definition   string  `json:"definition"`
		RoleARN      string  `json:"roleArn"`
		CreationDate float64 `json:"creationDate"`
		UpdateDate   float64 `json:"updateDate"`
		RevisionID   string  `json:"revisionId"`
	}
	json.Unmarshal(body, &out)
	return StateMachine{
		Name: out.Name, ARN: out.ARN, Type: out.Type, Status: out.Status,
		Definition: prettyJSON(out.Definition), RoleARN: out.RoleARN,
		Created: epochToTime(out.CreationDate), Updated: epochToTime(max(out.UpdateDate, out.CreationDate)),
		Revision: out.RevisionID, States: countStates(out.Definition),
	}, nil
}

// countStates counts the top-level States of a definition, for the fact strip.
func countStates(definition string) int {
	var def struct {
		States map[string]json.RawMessage `json:"States"`
	}
	if json.Unmarshal([]byte(definition), &def) != nil {
		return 0
	}
	return len(def.States)
}

func (b *backend) CreateStateMachine(ctx context.Context, name, definition, roleARN, typ string) (string, error) {
	// roleArn is required by the model, as on AWS; the form carries a default
	// because nothing local evaluates it and a required field with nothing
	// sensible to type is a form that refuses for a reason nobody cares about.
	in := map[string]any{"name": name, "definition": definition, "roleArn": roleARN}
	if typ != "" {
		in["type"] = typ
	}
	body, err := b.sfnCall(ctx, "CreateStateMachine", in)
	if err != nil {
		return "", err
	}
	var out struct {
		ARN string `json:"stateMachineArn"`
	}
	json.Unmarshal(body, &out)
	return out.ARN, nil
}

func (b *backend) UpdateStateMachine(ctx context.Context, arn, definition, roleARN string) error {
	in := map[string]any{"stateMachineArn": arn}
	if definition != "" {
		in["definition"] = definition
	}
	if roleARN != "" {
		in["roleArn"] = roleARN
	}
	_, err := b.sfnCall(ctx, "UpdateStateMachine", in)
	return err
}

func (b *backend) DeleteStateMachine(ctx context.Context, arn string) error {
	_, err := b.sfnCall(ctx, "DeleteStateMachine", map[string]any{"stateMachineArn": arn})
	return err
}

// Diagnostic is one finding from ValidateStateMachineDefinition.
type Diagnostic struct {
	Severity string // ERROR | WARNING
	Code     string
	Message  string
	Location string
}

// ValidateDefinition runs the analyser without creating anything — the check a
// create form can run live, the way the EventBridge form tests a pattern.
func (b *backend) ValidateDefinition(ctx context.Context, definition, typ string) ([]Diagnostic, error) {
	in := map[string]any{"definition": definition}
	if typ != "" {
		in["type"] = typ
	}
	body, err := b.sfnCall(ctx, "ValidateStateMachineDefinition", in)
	if err != nil {
		return nil, err
	}
	var out struct {
		Diagnostics []struct {
			Severity string `json:"severity"`
			Code     string `json:"code"`
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"diagnostics"`
	}
	json.Unmarshal(body, &out)
	ds := make([]Diagnostic, 0, len(out.Diagnostics))
	for _, d := range out.Diagnostics {
		ds = append(ds, Diagnostic{Severity: d.Severity, Code: d.Code, Message: d.Message, Location: d.Location})
	}
	return ds, nil
}

func (b *backend) StartExecution(ctx context.Context, machineARN, name, input string) (string, error) {
	in := map[string]any{"stateMachineArn": machineARN}
	if name != "" {
		in["name"] = name
	}
	if strings.TrimSpace(input) != "" {
		in["input"] = input
	}
	body, err := b.sfnCall(ctx, "StartExecution", in)
	if err != nil {
		return "", err
	}
	var out struct {
		ARN string `json:"executionArn"`
	}
	json.Unmarshal(body, &out)
	return out.ARN, nil
}

func (b *backend) StopExecution(ctx context.Context, arn, errName, cause string) error {
	in := map[string]any{"executionArn": arn}
	if errName != "" {
		in["error"] = errName
	}
	if cause != "" {
		in["cause"] = cause
	}
	_, err := b.sfnCall(ctx, "StopExecution", in)
	return err
}

func (b *backend) ListExecutions(ctx context.Context, machineARN, status string) ([]Execution, error) {
	in := map[string]any{"stateMachineArn": machineARN, "maxResults": 1000}
	if status != "" {
		in["statusFilter"] = status
	}
	body, err := b.sfnCall(ctx, "ListExecutions", in)
	if err != nil {
		return nil, err
	}
	var out struct {
		Executions []struct {
			ARN        string  `json:"executionArn"`
			MachineARN string  `json:"stateMachineArn"`
			Name       string  `json:"name"`
			Status     string  `json:"status"`
			StartDate  float64 `json:"startDate"`
			StopDate   float64 `json:"stopDate"`
		} `json:"executions"`
	}
	json.Unmarshal(body, &out)
	execs := make([]Execution, 0, len(out.Executions))
	for _, e := range out.Executions {
		execs = append(execs, Execution{
			Name: e.Name, ARN: e.ARN, MachineARN: e.MachineARN, Machine: arnLeaf(e.MachineARN),
			Status: e.Status, Started: epochToTime(e.StartDate), Stopped: epochToTime(e.StopDate),
		})
	}
	// Newest first: the one you just started is the one you are looking for.
	sort.SliceStable(execs, func(i, j int) bool { return execs[i].Started > execs[j].Started })
	return execs, nil
}

func (b *backend) DescribeExecution(ctx context.Context, arn string) (Execution, error) {
	body, err := b.sfnCall(ctx, "DescribeExecution", map[string]any{"executionArn": arn})
	if err != nil {
		return Execution{}, err
	}
	var out struct {
		ARN        string  `json:"executionArn"`
		MachineARN string  `json:"stateMachineArn"`
		Name       string  `json:"name"`
		Status     string  `json:"status"`
		StartDate  float64 `json:"startDate"`
		StopDate   float64 `json:"stopDate"`
		Input      string  `json:"input"`
		Output     string  `json:"output"`
		Error      string  `json:"error"`
		Cause      string  `json:"cause"`
	}
	json.Unmarshal(body, &out)
	return Execution{
		Name: out.Name, ARN: out.ARN, MachineARN: out.MachineARN, Machine: arnLeaf(out.MachineARN),
		Status: out.Status, Started: epochToTime(out.StartDate), Stopped: epochToTime(out.StopDate),
		Input: prettyJSON(out.Input), Output: prettyJSON(out.Output), Error: out.Error, Cause: out.Cause,
	}, nil
}

// ExecutionDefinition is the definition the execution is actually running —
// the frozen snapshot, not the machine's current one.
func (b *backend) ExecutionDefinition(ctx context.Context, arn string) (string, error) {
	body, err := b.sfnCall(ctx, "DescribeStateMachineForExecution", map[string]any{"executionArn": arn})
	if err != nil {
		return "", err
	}
	var out struct {
		Definition string `json:"definition"`
	}
	json.Unmarshal(body, &out)
	return prettyJSON(out.Definition), nil
}

func (b *backend) ExecutionHistory(ctx context.Context, arn string) ([]HistoryEvent, error) {
	body, err := b.sfnCall(ctx, "GetExecutionHistory", map[string]any{
		"executionArn": arn, "maxResults": 1000, "includeExecutionData": true,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	json.Unmarshal(body, &out)
	evs := make([]HistoryEvent, 0, len(out.Events))
	for _, raw := range out.Events {
		evs = append(evs, flattenEvent(raw))
	}
	return evs, nil
}

// flattenEvent lifts the one *EventDetails object an event carries into the
// flat row the table renders.
func flattenEvent(raw map[string]any) HistoryEvent {
	ev := HistoryEvent{Type: str(raw["type"])}
	if id, ok := raw["id"].(float64); ok {
		ev.ID = int64(id)
	}
	if id, ok := raw["previousEventId"].(float64); ok {
		ev.PrevID = int64(id)
	}
	if ts, ok := raw["timestamp"].(float64); ok {
		ev.When = epochToTime(ts)
	}
	for k, v := range raw {
		details, ok := v.(map[string]any)
		if !ok || !strings.HasSuffix(k, "EventDetails") {
			continue
		}
		ev.State = str(details["name"])
		if r := str(details["resource"]); r != "" && ev.State == "" {
			ev.State = arnLeaf(r)
		}
		for _, key := range []string{"input", "output", "parameters"} {
			if p := str(details[key]); p != "" {
				ev.Payload = prettyJSON(p)
				break
			}
		}
		ev.Error, ev.Cause = str(details["error"]), str(details["cause"])
	}
	ev.IsErr = ev.Error != "" || strings.HasSuffix(ev.Type, "Failed") ||
		strings.HasSuffix(ev.Type, "TimedOut") || strings.HasSuffix(ev.Type, "Aborted")
	return ev
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// SendTaskResult redeems a task token from the console — the callback a
// worker would make, for the case where you are the worker.
func (b *backend) SendTaskResult(ctx context.Context, token string, success bool, output, errName, cause string) error {
	if success {
		if strings.TrimSpace(output) == "" {
			output = "{}"
		}
		_, err := b.sfnCall(ctx, "SendTaskSuccess", map[string]any{"taskToken": token, "output": output})
		return err
	}
	in := map[string]any{"taskToken": token}
	if errName != "" {
		in["error"] = errName
	}
	if cause != "" {
		in["cause"] = cause
	}
	_, err := b.sfnCall(ctx, "SendTaskFailure", in)
	return err
}

// stateMachineARNOf rebuilds a machine ARN from its console path segment.
func stateMachineARNOf(name string) string { return awsident.ARN("states", "stateMachine:"+name) }

// executionARNOf rebuilds an execution ARN from the two path segments.
func executionARNOf(machine, name string) string {
	return awsident.ARN("states", "execution:"+machine+":"+name)
}
