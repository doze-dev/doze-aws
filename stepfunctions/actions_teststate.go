package stepfunctions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// TestState runs one state, for real, and reports where it would go next.
// The definition may be a lone state or a whole machine with stateName; a
// Task calls its integration unless a mock stands in. It runs as a volatile
// execution on the ordinary driver, stopped at the first state boundary by
// the hooks in express.go — which is what makes Task, Map and Parallel
// testable with the same code that runs them.

// TestSpec is what a TestState run carries on its execution record for the
// driver to find: the state under test and the mock, if any.
type TestSpec struct {
	State     string          `json:"state"`
	MockOut   json.RawMessage `json:"mock_out,omitempty"`
	MockError string          `json:"mock_error,omitempty"`
	MockCause string          `json:"mock_cause,omitempty"`
	HasMock   bool            `json:"has_mock,omitempty"`
}

func (t *TestSpec) run() *testRun {
	tr := &testRun{state: t.State}
	if t.HasMock {
		res := asl.TaskResult{Output: t.MockOut}
		if t.MockError != "" {
			res = asl.TaskResult{Failure: &asl.Failure{Name: t.MockError, Cause: t.MockCause}}
		}
		tr.mock = &res
	}
	return tr
}

func (s *Server) testState(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	raw := awsjson.Str(p, "definition")
	if raw == "" {
		return nil, errValidation("1 validation error detected: Value null at 'definition' failed to satisfy constraint: Member must not be null")
	}
	stateName := awsjson.Str(p, "stateName")
	def, frozen, stateName, aerr := testDefinition(raw, stateName)
	if aerr != nil {
		return nil, aerr
	}
	state := def.States[stateName]

	input := awsjson.Str(p, "input")
	if input == "" {
		input = "{}"
	}
	var probe any
	if err := json.Unmarshal([]byte(input), &probe); err != nil {
		return nil, errInvalidExecutionInput(err.Error())
	}
	level := awsjson.Str(p, "inspectionLevel")
	if level == "" {
		level = "INFO"
	}

	spec := &TestSpec{State: stateName}
	if mock, ok := p["mock"].(map[string]any); ok {
		switch state.Type {
		case asl.Task, asl.Map, asl.Parallel:
		default:
			return nil, errValidation("a mock can only be specified for a Task, Map or Parallel state; %q is a %s", stateName, state.Type)
		}
		spec.HasMock = true
		if out := awsjson.Str(mock, "result"); out != "" {
			if !json.Valid([]byte(out)) {
				return nil, errValidation("mock.result must be a JSON string")
			}
			spec.MockOut = json.RawMessage(out)
		} else if eo, ok := mock["errorOutput"].(map[string]any); ok {
			spec.MockError, spec.MockCause = awsjson.Str(eo, "error"), awsjson.Str(eo, "cause")
		}
		if spec.MockOut == nil && spec.MockError == "" {
			spec.MockOut = json.RawMessage(`{}`)
		}
	}

	// The definition text the run freezes is the wrapped one — the analyser
	// accepted it, and Sub/States lookups need the stubs to exist.
	now := s.store.clock()
	id := newToken()[2:18]
	arn := expressExecARN("TestState", stateName, id)
	e := &Execution{
		ARN: arn, MachineARN: machineARN("TestState"), Name: stateName,
		Definition: frozen, RoleARN: awsjson.Str(p, "roleArn"), Type: "STANDARD",
		Status: "RUNNING", StartedAt: now.UnixMilli(), Input: input,
		Volatile: true, Test: spec,
		Exec: asl.StartExec(arn, stateName, machineARN("TestState"), "TestState", awsjson.Str(p, "roleArn"),
			json.RawMessage(input), now),
		NextEventID: 2,
	}
	e.Deadline = e.StartedAt + expressMaxDuration.Milliseconds()
	root := e.Exec.Root()
	root.State, root.EnteredAt, root.PrevEventID = stateName, e.StartedAt, 1

	ch, err := s.startVolatile(e)
	if err != nil {
		return nil, asAPIError(err)
	}
	done, ok := s.awaitVolatile(ctx, e.Key(), ch)
	if !ok {
		return nil, awshttp.Errf(500, "InternalFailure", "the state did not finish before the request ended")
	}
	return testStateOutput(def, stateName, done, level), nil
}

// testDefinition parses the definition TestState was given. A lone state
// (no StartAt) is wrapped in a machine whose other states are terminal Pass
// stubs for whatever it names as Next, so the analyser's cross-reference
// rules hold and the run stops at the boundary anyway.
func testDefinition(raw, stateName string) (*asl.Definition, string, string, *awshttp.APIError) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, "", "", errInvalidDefinition(err.Error())
	}
	if _, whole := probe["States"]; !whole {
		// A lone state. Give it a name and stub its neighbours.
		var st map[string]any
		if err := json.Unmarshal([]byte(raw), &st); err != nil || st["Type"] == nil {
			return nil, "", "", errInvalidDefinition("the definition is neither a state machine nor a state")
		}
		if stateName == "" {
			stateName = "TestState"
		}
		states := map[string]any{stateName: st}
		for _, n := range nextTargets(st) {
			if _, exists := states[n]; !exists {
				states[n] = map[string]any{"Type": "Pass", "End": true}
			}
		}
		wrapped, _ := json.Marshal(map[string]any{"StartAt": stateName, "States": states})
		raw = string(wrapped)
	} else if stateName == "" {
		return nil, "", "", errValidation("stateName is required when the definition is a whole state machine")
	}
	def, rep := asl.ValidateDefinition([]byte(raw))
	if !rep.OK() {
		return nil, "", "", errInvalidDefinition(rep.Error())
	}
	if def.States[stateName] == nil {
		return nil, "", "", errValidation("stateName %q is not a top-level state of the definition", stateName)
	}
	return def, raw, stateName, nil
}

// nextTargets lists the states a raw state object can transition to: Next,
// Default, each Choice rule's Next, each Catch's Next.
func nextTargets(st map[string]any) []string {
	var out []string
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	add(st["Next"])
	add(st["Default"])
	for _, key := range []string{"Choices", "Catch"} {
		if list, ok := st[key].([]any); ok {
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					add(m["Next"])
				}
			}
		}
	}
	return out
}

// testStateOutput renders the API's answer from the finished run.
func testStateOutput(def *asl.Definition, stateName string, e *Execution, level string) map[string]any {
	out := map[string]any{}
	status, next, errName, cause := e.Status, "", e.Error, e.Cause
	var result, taskInput json.RawMessage
	if t := e.TestOutcome; t != nil {
		status, next, errName, cause = t.Status, t.NextState, t.Error, t.Cause
		result, taskInput = t.Result, t.TaskInput
	} else if status == "TIMED_OUT" || status == "ABORTED" {
		status = "FAILED"
	}
	out["status"] = status
	if next != "" {
		out["nextState"] = next
	}
	if e.Output != nil && status != "FAILED" {
		out["output"] = string(e.Output)
	}
	if errName != "" {
		out["error"] = errName
		out["cause"] = cause
	}
	if level == "INFO" {
		return out
	}
	ins := asl.Inspect(def, stateName, json.RawMessage(e.Input), result, e.Exec.StartTime)
	data := map[string]any{}
	put := func(k string, v json.RawMessage) {
		if v != nil {
			data[k] = string(v)
		}
	}
	put("input", ins.Input)
	put("afterInputPath", ins.AfterInputPath)
	put("afterParameters", ins.AfterParameters)
	put("result", ins.Result)
	put("afterResultSelector", ins.AfterResultSelector)
	put("afterResultPath", ins.AfterResultPath)
	if level == "TRACE" {
		if taskInput != nil {
			data["request"] = map[string]any{"body": string(taskInput)}
		}
		if result != nil {
			data["response"] = map[string]any{"body": string(result)}
		} else if errName != "" {
			data["response"] = map[string]any{"body": fmt.Sprintf(`{"Error":%q,"Cause":%q}`, errName, cause)}
		}
	}
	out["inspectionData"] = data
	return out
}
