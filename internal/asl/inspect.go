package asl

import (
	"encoding/json"
	"time"
)

// Inspection is what TestState's DEBUG level reports: the state's input at
// each stage of the JSONPath data-flow pipeline. It re-runs the pipeline the
// way Advance did, on the same values, and records what each step produced
// — cheaper and more honest than instrumenting the interpreter with
// side-channels it does not otherwise need.
type Inspection struct {
	Input               json.RawMessage
	AfterInputPath      json.RawMessage
	AfterParameters     json.RawMessage
	Result              json.RawMessage
	AfterResultSelector json.RawMessage
	AfterResultPath     json.RawMessage
}

// Inspect walks the JSONPath pipeline for one state. result is the task
// result the state received (nil for states without one), and the stages
// after it are only filled when there was one. A stage that fails leaves
// the later ones empty, as the state itself would have.
func Inspect(d *Definition, stateName string, input, result json.RawMessage, now time.Time) Inspection {
	ins := Inspection{Input: input}
	s := d.States[stateName]
	if s == nil {
		return ins
	}
	ex := StartExec("arn:aws:states:us-east-1:000000000000:execution:TestState:inspect", "inspect",
		"arn:aws:states:us-east-1:000000000000:stateMachine:TestState", "TestState", "", input, now)
	f := ex.Root()
	f.State, f.EnteredAt = stateName, now.UnixMilli()
	ctxObj := buildContext(ex, f, "")
	doc := decodeDoc(input)

	eff, fail := selectPath(doc, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return ins
	}
	ins.AfterInputPath = encodeDoc(eff)
	if len(s.Parameters) > 0 {
		eff, fail = evalTemplate(s.Parameters, eff, ctxObj, Env{Now: now})
		if fail != nil {
			return ins
		}
	}
	ins.AfterParameters = encodeDoc(eff)
	if result == nil {
		return ins
	}
	ins.Result = result
	res := decodeDoc(result)
	if len(s.ResultSelector) > 0 {
		res, fail = evalTemplate(s.ResultSelector, res, ctxObj, Env{Now: now})
		if fail != nil {
			return ins
		}
	}
	ins.AfterResultSelector = encodeDoc(res)
	merged, fail := injectPath(doc, res, s.ResultPath)
	if fail != nil {
		return ins
	}
	ins.AfterResultPath = encodeDoc(merged)
	return ins
}
