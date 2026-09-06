package asl

import "testing"

// Variables in the JSONPath dialect: Assign writes them, $name reads them —
// in Parameters, in Choice comparisons, in Wait paths — and a Task's Assign
// sees the raw result.
func TestJSONPathVariables(t *testing.T) {
	d := mustDef(t, `{"StartAt": "Remember", "States": {
		"Remember": {"Type": "Pass", "Assign": {"customer.$": "$.name", "limit": 3, "ctx.$": "$$.State.Name"}, "Next": "Use"},
		"Use": {"Type": "Pass", "Parameters": {"who.$": "$customer", "cap.$": "$limit", "from.$": "$ctx", "n.$": "$.n"}, "Next": "Route"},
		"Route": {"Type": "Choice", "Choices": [
			{"Variable": "$.n", "NumericGreaterThanPath": "$limit", "Assign": {"over": true}, "Next": "Over"}],
			"Default": "Under"},
		"Over": {"Type": "Pass", "Parameters": {"over.$": "$over", "who.$": "$customer"}, "End": true},
		"Under": {"Type": "Pass", "Parameters": {"under": true, "who.$": "$customer"}, "End": true}
	}}`)
	wantDone(t, run(t, d, newExec(`{"name": "ada", "n": 5}`)), `{"over": true, "who": "ada"}`)
	wantDone(t, run(t, d, newExec(`{"name": "ada", "n": 1}`)), `{"under": true, "who": "ada"}`)
}

func TestJSONPathVariablesReadTheParametersOutput(t *testing.T) {
	d := mustDef(t, `{"StartAt": "A", "States": {
		"A": {"Type": "Pass", "Parameters": {"shaped": true}, "Assign": {"seen.$": "$"}, "Next": "B"},
		"B": {"Type": "Pass", "Parameters": {"v.$": "$seen.shaped"}, "End": true}}}`)
	// Assign on a Pass sees the value after Parameters, as AWS documents.
	wantDone(t, run(t, d, newExec(`{"x": 1}`)), `{"v": true}`)
}

func TestJSONPathVariableNamesAreChecked(t *testing.T) {
	d := mustDef(t, `{"StartAt": "A", "States": {
		"A": {"Type": "Pass", "Assign": {"states": 1}, "End": true}}}`)
	wantFail(t, run(t, d, newExec(`{}`)), ErrRuntime)
}

func TestJSONPathVariablePathIsNotATarget(t *testing.T) {
	if err := ValidateReferencePath("$v.field"); err == nil {
		t.Error("a variable path should be refused as a reference-path target")
	}
	p, err := ParsePath("$order.items[0]")
	if err != nil || p.Root != RootVariable || p.Variable != "order" || len(p.Segments) != 2 {
		t.Errorf("ParsePath($order.items[0]) = %+v, %v", p, err)
	}
	if p, _ := ParsePath("$.plain"); p.Root != RootInput {
		t.Error("$.plain is the input, not a variable")
	}
	if p, _ := ParsePath("$$.Execution"); p.Root != RootContext {
		t.Error("$$.Execution is the context")
	}
}
