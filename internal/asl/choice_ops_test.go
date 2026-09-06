package asl

import (
	"fmt"
	"testing"
)

// Every Choice operator, evaluated. The operator table in choice.go is what
// the parser, analyser and evaluator agree on; this pins what each one
// actually decides at runtime, including the two rules that are AWS's and not
// obvious — a present variable of the wrong type is false, not an error, and
// the Path twins compare against another field of the input.
func TestChoiceEveryOperator(t *testing.T) {
	input := `{
		"s": "mango", "s2": "apple", "n": 5, "n2": 10, "b": true,
		"t": "2024-06-01T12:00:00Z", "t2": "2024-06-02T12:00:00Z",
		"nul": null, "obj": {"k": 1}, "arr": [1]
	}`
	type tc struct {
		rule string
		want bool
	}
	cases := []tc{
		// strings order lexically
		{`{"Variable": "$.s", "StringEquals": "mango"}`, true},
		{`{"Variable": "$.s", "StringEquals": "Mango"}`, false},
		{`{"Variable": "$.s2", "StringLessThan": "mango"}`, true},
		{`{"Variable": "$.s", "StringLessThan": "apple"}`, false},
		{`{"Variable": "$.s", "StringGreaterThan": "apple"}`, true},
		{`{"Variable": "$.s", "StringLessThanEquals": "mango"}`, true},
		{`{"Variable": "$.s", "StringGreaterThanEquals": "mango"}`, true},
		{`{"Variable": "$.s", "StringGreaterThanEquals": "zebra"}`, false},
		{`{"Variable": "$.s", "StringMatches": "m*o"}`, true},
		{`{"Variable": "$.s", "StringMatches": "m*x"}`, false},
		// numbers
		{`{"Variable": "$.n", "NumericEquals": 5}`, true},
		{`{"Variable": "$.n", "NumericEquals": 5.0}`, true},
		{`{"Variable": "$.n", "NumericLessThan": 6}`, true},
		{`{"Variable": "$.n", "NumericLessThan": 5}`, false},
		{`{"Variable": "$.n", "NumericLessThanEquals": 5}`, true},
		{`{"Variable": "$.n", "NumericGreaterThan": 4.5}`, true},
		{`{"Variable": "$.n", "NumericGreaterThanEquals": 5}`, true},
		{`{"Variable": "$.n", "NumericGreaterThanEquals": 6}`, false},
		// booleans
		{`{"Variable": "$.b", "BooleanEquals": true}`, true},
		{`{"Variable": "$.b", "BooleanEquals": false}`, false},
		// timestamps compare as instants, not strings
		{`{"Variable": "$.t", "TimestampEquals": "2024-06-01T12:00:00Z"}`, true},
		{`{"Variable": "$.t", "TimestampEquals": "2024-06-01T14:00:00+02:00"}`, true},
		{`{"Variable": "$.t", "TimestampLessThan": "2024-06-02T00:00:00Z"}`, true},
		{`{"Variable": "$.t", "TimestampGreaterThan": "2024-06-02T00:00:00Z"}`, false},
		{`{"Variable": "$.t", "TimestampLessThanEquals": "2024-06-01T12:00:00Z"}`, true},
		{`{"Variable": "$.t", "TimestampGreaterThanEquals": "2024-06-01T12:00:00Z"}`, true},
		// type probes
		{`{"Variable": "$.s", "IsString": true}`, true},
		{`{"Variable": "$.n", "IsString": true}`, false},
		{`{"Variable": "$.n", "IsString": false}`, true},
		{`{"Variable": "$.n", "IsNumeric": true}`, true},
		{`{"Variable": "$.b", "IsBoolean": true}`, true},
		{`{"Variable": "$.nul", "IsNull": true}`, true},
		{`{"Variable": "$.s", "IsNull": true}`, false},
		{`{"Variable": "$.nul", "IsPresent": true}`, true},
		{`{"Variable": "$.missing", "IsPresent": true}`, false},
		{`{"Variable": "$.missing", "IsPresent": false}`, true},
		{`{"Variable": "$.t", "IsTimestamp": true}`, true},
		{`{"Variable": "$.s", "IsTimestamp": true}`, false},
		// wrong type is false, never an error
		{`{"Variable": "$.s", "NumericEquals": 5}`, false},
		{`{"Variable": "$.n", "StringEquals": "5"}`, false},
		{`{"Variable": "$.s", "TimestampEquals": "2024-06-01T12:00:00Z"}`, false},
		{`{"Variable": "$.obj", "BooleanEquals": true}`, false},
		{`{"Variable": "$.arr", "StringLessThan": "z"}`, false},
		// Path twins compare two fields of the input
		{`{"Variable": "$.s", "StringEqualsPath": "$.s"}`, true},
		{`{"Variable": "$.s", "StringEqualsPath": "$.s2"}`, false},
		{`{"Variable": "$.s2", "StringLessThanPath": "$.s"}`, true},
		{`{"Variable": "$.n", "NumericLessThanPath": "$.n2"}`, true},
		{`{"Variable": "$.n2", "NumericGreaterThanEqualsPath": "$.n"}`, true},
		{`{"Variable": "$.b", "BooleanEqualsPath": "$.b"}`, true},
		{`{"Variable": "$.t", "TimestampLessThanPath": "$.t2"}`, true},
		{`{"Variable": "$.t2", "TimestampGreaterThanEqualsPath": "$.t"}`, true},
		// combinators
		{`{"Not": {"Variable": "$.n", "NumericEquals": 5}}`, false},
		{`{"Not": {"Variable": "$.n", "NumericEquals": 6}}`, true},
		{`{"Or": [{"Variable": "$.n", "NumericEquals": 1}, {"Variable": "$.s", "StringEquals": "mango"}]}`, true},
		{`{"Or": [{"Variable": "$.n", "NumericEquals": 1}, {"Variable": "$.s", "StringEquals": "kiwi"}]}`, false},
		{`{"And": [{"Variable": "$.n", "NumericEquals": 5}, {"Not": {"Variable": "$.b", "BooleanEquals": false}}]}`, true},
		{`{"And": [{"Variable": "$.n", "NumericEquals": 5}, {"Variable": "$.b", "BooleanEquals": false}]}`, false},
	}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			// The rule carries Next; a nested rule (inside And/Or/Not) may not.
			def := fmt.Sprintf(`{"StartAt": "C", "States": {
				"C": {"Type": "Choice", "Choices": [%s], "Default": "No"},
				"Yes": {"Type": "Pass", "Result": "yes", "End": true},
				"No": {"Type": "Pass", "Result": "no", "End": true}}}`, withNext(c.rule))
			d := mustDef(t, def)
			eff := run(t, d, newExec(input))
			want := `"no"`
			if c.want {
				want = `"yes"`
			}
			if fail, ok := eff.(EffFail); ok {
				t.Fatalf("rule %s: failed with %s: %s", c.rule, fail.Failure.Name, fail.Failure.Cause)
			}
			wantDone(t, eff, want)
		})
	}
}

// withNext appends "Next": "Yes" to a top-level rule object.
func withNext(rule string) string {
	return rule[:len(rule)-1] + `, "Next": "Yes"}`
}

// A comparison against a Path twin whose path selects nothing is
// States.Runtime, the same as a missing Variable.
func TestChoicePathTwinMissingIsRuntime(t *testing.T) {
	d := mustDef(t, `{"StartAt": "C", "States": {
		"C": {"Type": "Choice", "Choices": [{"Variable": "$.n", "NumericEqualsPath": "$.absent", "Next": "Y"}], "Default": "Y"},
		"Y": {"Type": "Succeed"}}}`)
	eff := run(t, d, newExec(`{"n": 1}`))
	wantFail(t, eff, ErrRuntime)
}

// A Path twin that selects a value of the wrong type is false, like a
// literal comparand of the wrong type.
func TestChoicePathTwinWrongTypeIsFalse(t *testing.T) {
	d := mustDef(t, `{"StartAt": "C", "States": {
		"C": {"Type": "Choice", "Choices": [{"Variable": "$.n", "NumericEqualsPath": "$.s", "Next": "Y"}], "Default": "N"},
		"Y": {"Type": "Pass", "Result": "yes", "End": true},
		"N": {"Type": "Pass", "Result": "no", "End": true}}}`)
	wantDone(t, run(t, d, newExec(`{"n": 1, "s": "1"}`)), `"no"`)
}
