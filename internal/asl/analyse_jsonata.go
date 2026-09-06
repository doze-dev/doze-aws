package asl

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Static analysis of the JSONata dialect: which fields a JSONata state may
// carry, and whether every `{% … %}` it holds compiles.
//
// AWS refuses a JSONata state that uses a JSONPath field (InputPath,
// Parameters, ResultPath, …) and a JSONPath state that uses a JSONata one
// (Arguments, Output, Items, Condition) with SCHEMA_VALIDATION_FAILED, naming
// the field. It also refuses an expression that does not parse, and one that
// reads $states.result or $states.errorOutput where that value does not exist
// yet — both are caught here rather than at run time, because a definition
// accepted locally and refused on deploy is the failure this package exists to
// prevent.

// jsonataFieldsByType is fieldsByType for the JSONata dialect.
var jsonataFieldsByType = map[StateType]map[string]bool{
	Pass:     {"Result": true, "Output": true, "Assign": true},
	Task:     {"Resource": true, "Arguments": true, "Output": true, "Assign": true, "Retry": true, "Catch": true, "TimeoutSeconds": true, "HeartbeatSeconds": true, "Credentials": true},
	Choice:   {"Choices": true, "Default": true, "Output": true, "Assign": true},
	Wait:     {"Seconds": true, "Timestamp": true, "Output": true, "Assign": true},
	Succeed:  {"Output": true},
	Fail:     {"Error": true, "Cause": true},
	Parallel: {"Branches": true, "Arguments": true, "Output": true, "Assign": true, "Retry": true, "Catch": true},
	Map:      {"ItemProcessor": true, "Iterator": true, "Items": true, "ItemSelector": true, "MaxConcurrency": true, "ItemReader": true, "ItemBatcher": true, "ResultWriter": true, "Output": true, "Assign": true, "Retry": true, "Catch": true, "ToleratedFailureCount": true, "ToleratedFailurePercentage": true},
}

// jsonpathOnlyFields never appear on a JSONata state, whatever its type.
var jsonpathOnlyFields = map[string]bool{
	"InputPath": true, "OutputPath": true, "Parameters": true, "ResultSelector": true, "ResultPath": true,
	"ItemsPath": true, "SecondsPath": true, "TimestampPath": true, "TimeoutSecondsPath": true,
	"HeartbeatSecondsPath": true, "MaxConcurrencyPath": true, "ErrorPath": true, "CausePath": true,
}

// jsonataOnlyFields never appear on a JSONPath state.
var jsonataOnlyFields = map[string]bool{
	"Arguments": true, "Output": true, "Items": true,
}

// notSupported is AWS's wording for a field from the other dialect.
func notSupported(field string, lang QueryLanguage) string {
	return fmt.Sprintf("Field '%s' is not supported when QueryLanguage is %s", field, lang)
}

// exprPhase says which of the optional $states members an expression may
// read: result exists once a task, branch set or item set has completed, and
// errorOutput only inside a Catch.
type exprPhase int

const (
	phaseEntry  exprPhase = iota // input and context only
	phaseResult                  // plus $states.result
	phaseCatch                   // plus $states.errorOutput
)

// checkJSONataState runs the dialect's own rules for one state. The shared
// checks (transitions, Retry, Catch shape) have already run.
func checkJSONataState(d *Definition, s *State, at string, r *Report) {
	if s.QueryLanguage != "" && s.QueryLanguage != JSONPath && s.QueryLanguage != JSONata {
		r.addf(at, "QueryLanguage must be JSONPath or JSONata, got %q", s.QueryLanguage)
	}

	// The exit phase: Output and Assign see the result on the states that
	// produce one (and on a Pass that carries a literal Result).
	exit := phaseEntry
	switch s.Type {
	case Task, Parallel, Map:
		exit = phaseResult
	case Pass:
		if len(s.Result) > 0 {
			exit = phaseResult
		}
	}
	checkExprField(s.Output, at+".Output", exit, r)
	checkExprField(s.Assign, at+".Assign", exit, r)
	checkAssign(s.Assign, at+".Assign", r)

	checkExprField(s.Arguments, at+".Arguments", phaseEntry, r)
	checkExprField(s.Items, at+".Items", phaseEntry, r)
	checkExprField(s.ItemSelector, at+".ItemSelector", phaseEntry, r)
	for _, f := range []struct{ name, val string }{
		{"TimeoutSeconds", s.TimeoutSecondsExpr},
		{"HeartbeatSeconds", s.HeartbeatSecondsExpr},
		{"Seconds", s.SecondsExpr},
		{"MaxConcurrency", s.MaxConcurrencyExpr},
		{"Timestamp", s.Timestamp},
		{"Error", s.Error},
		{"Cause", s.Cause},
	} {
		if f.val != "" {
			checkExprString(f.val, at+"."+f.name, phaseEntry, r)
		}
	}
	for _, f := range []struct{ name, val string }{
		{"TimeoutSeconds", s.TimeoutSecondsExpr},
		{"HeartbeatSeconds", s.HeartbeatSecondsExpr},
		{"Seconds", s.SecondsExpr},
		{"MaxConcurrency", s.MaxConcurrencyExpr},
	} {
		if _, ok := jsonataExpr(f.val); f.val != "" && !ok {
			r.addf(at+"."+f.name, "must be a number or a JSONata expression, got %q", f.val)
		}
	}

	for i, rule := range s.Choices {
		where := fmt.Sprintf("%s.Choices[%d]", at, i)
		if len(rule.Condition) == 0 {
			r.addf(where, "a JSONata choice rule requires Condition")
		}
		if rule.Next == "" {
			r.addf(where, "a top-level choice rule requires Next")
		} else {
			checkTarget(d, rule.Next, where+".Next", r)
		}
		checkExprField(rule.Condition, where+".Condition", phaseEntry, r)
		checkExprField(rule.Assign, where+".Assign", phaseEntry, r)
		checkAssign(rule.Assign, where+".Assign", r)
		for _, f := range []struct {
			name    string
			present bool
		}{
			{"Variable", rule.Variable != ""},
			{"And", len(rule.And) > 0},
			{"Or", len(rule.Or) > 0},
			{"Not", rule.Not != nil},
		} {
			if f.present {
				r.addf(where, "%s", notSupported(f.name, JSONata))
			}
		}
		if rule.Comparison != nil {
			r.addf(where, "%s", notSupported(rule.Comparison.Op, JSONata))
		}
	}

	for i, c := range s.Catch {
		where := fmt.Sprintf("%s.Catch[%d]", at, i)
		if c.ResultPath != nil {
			r.addf(where, "%s", notSupported("ResultPath", JSONata))
		}
		checkExprField(c.Output, where+".Output", phaseCatch, r)
		checkExprField(c.Assign, where+".Assign", phaseCatch, r)
		checkAssign(c.Assign, where+".Assign", r)
	}
}

// checkJSONPathDialect refuses the JSONata-only things on a JSONPath state:
// the fields, an expression string where a number belongs, a Condition rule,
// a Catch Output.
func checkJSONPathDialect(s *State, at string, r *Report) {
	for _, f := range []struct{ name, val string }{
		{"TimeoutSeconds", s.TimeoutSecondsExpr},
		{"HeartbeatSeconds", s.HeartbeatSecondsExpr},
		{"Seconds", s.SecondsExpr},
		{"MaxConcurrency", s.MaxConcurrencyExpr},
	} {
		if f.val != "" {
			r.addf(at+"."+f.name, "must be a number, got %q", f.val)
		}
	}
	for i, rule := range s.Choices {
		if len(rule.Condition) > 0 {
			r.addf(fmt.Sprintf("%s.Choices[%d]", at, i), "%s", notSupported("Condition", JSONPath))
		}
	}
	for i, c := range s.Catch {
		if len(c.Output) > 0 {
			r.addf(fmt.Sprintf("%s.Catch[%d]", at, i), "%s", notSupported("Output", JSONPath))
		}
	}
}

// checkExprField walks a raw JSON field for expression strings.
func checkExprField(raw json.RawMessage, at string, phase exprPhase, r *Report) {
	if len(raw) == 0 {
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		r.addf(at, "is not valid JSON")
		return
	}
	walkStrings(v, func(s string) { checkExprString(s, at, phase, r) })
}

func walkStrings(v any, visit func(string)) {
	switch n := v.(type) {
	case string:
		visit(n)
	case map[string]any:
		for _, child := range n {
			walkStrings(child, visit)
		}
	case []any:
		for _, child := range n {
			walkStrings(child, visit)
		}
	}
}

// checkExprString compiles one string if it is an expression, and refuses a
// $states member the phase does not provide. The member check is textual —
// `$states.result` anywhere in the source — which is what AWS's own check
// amounts to, and cheap enough to run on every validate.
func checkExprString(s, at string, phase exprPhase, r *Report) {
	if err := checkJSONata(s); err != nil {
		r.addf(at, "%v", err)
		return
	}
	src, ok := jsonataExpr(s)
	if !ok {
		return
	}
	if phase < phaseResult && strings.Contains(src, "$states.result") {
		r.addf(at, "$states.result is not available here: it exists only after a Task, Parallel or Map has completed")
	}
	if phase < phaseCatch && strings.Contains(src, "$states.errorOutput") {
		r.addf(at, "$states.errorOutput is only available in a Catch's Output and Assign")
	}
}
