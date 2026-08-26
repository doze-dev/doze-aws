package asl

import (
	"strings"
	"testing"
)

// A realistic machine using most of the language: Choice with a combinator,
// Retry and Catch, Parallel, Map with the current spelling, Wait, and both
// terminal state types. If the analyser reports anything here it is
// over-rejecting, which breaks templates that deploy fine on AWS — the mirror
// image of the failure the analyser exists to prevent.
const realistic = `{
  "Comment": "order pipeline",
  "StartAt": "Validate",
  "TimeoutSeconds": 3600,
  "States": {
    "Validate": {
      "Type": "Task",
      "Resource": "arn:aws:lambda:us-east-1:000000000000:function:validate",
      "InputPath": "$.order",
      "ResultPath": "$.validation",
      "TimeoutSeconds": 30,
      "Retry": [
        {"ErrorEquals": ["Lambda.TooManyRequestsException"], "IntervalSeconds": 2, "MaxAttempts": 3, "BackoffRate": 2.0},
        {"ErrorEquals": ["States.ALL"], "MaxAttempts": 1}
      ],
      "Catch": [{"ErrorEquals": ["States.TaskFailed"], "Next": "Reject", "ResultPath": "$.error"}],
      "Next": "Branch"
    },
    "Branch": {
      "Type": "Choice",
      "Choices": [
        {"Variable": "$.validation.ok", "BooleanEquals": true, "Next": "Fanout"},
        {"And": [
          {"Variable": "$.order.total", "NumericGreaterThan": 1000},
          {"Not": {"Variable": "$.order.region", "StringEquals": "eu"}}
        ], "Next": "Hold"},
        {"Variable": "$.order.placedAt", "TimestampLessThan": "2020-01-01T00:00:00Z", "Next": "Reject"}
      ],
      "Default": "Reject"
    },
    "Hold": {"Type": "Wait", "Seconds": 60, "Next": "Fanout"},
    "Fanout": {
      "Type": "Parallel",
      "Branches": [
        {"StartAt": "Charge", "States": {"Charge": {"Type": "Task", "Resource": "arn:aws:states:::sqs:sendMessage", "End": true}}},
        {"StartAt": "Notify", "States": {"Notify": {"Type": "Task", "Resource": "arn:aws:states:::sns:publish", "End": true}}}
      ],
      "ResultPath": "$.fanout",
      "Next": "Items"
    },
    "Items": {
      "Type": "Map",
      "ItemsPath": "$.order.lines",
      "MaxConcurrency": 4,
      "ItemProcessor": {
        "StartAt": "Ship",
        "States": {"Ship": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:ship", "End": true}}
      },
      "Next": "Done"
    },
    "Done": {"Type": "Succeed"},
    "Reject": {"Type": "Fail", "Error": "OrderRejected", "Cause": "validation failed"}
  }
}`

func TestAnalyseAcceptsARealisticMachine(t *testing.T) {
	d, rep := ValidateDefinition([]byte(realistic))
	if d == nil {
		t.Fatalf("parse failed: %s", rep.Error())
	}
	if !rep.OK() {
		for _, diag := range rep.Diagnostics {
			t.Errorf("unexpected diagnostic: %s", diag)
		}
	}
}

// TestAnalyseReportsEveryProblemAtOnce — four mistakes should be four
// diagnostics, not four edit-and-retry cycles.
func TestAnalyseReportsEveryProblemAtOnce(t *testing.T) {
	const broken = `{
	  "StartAt": "A",
	  "States": {
	    "A": {"Type": "Task", "Next": "Nowhere"},
	    "B": {"Type": "Wait", "Seconds": 1, "Timestamp": "2020-01-01T00:00:00Z", "End": true}
	  }
	}`
	_, rep := ValidateDefinition([]byte(broken))
	if rep.OK() {
		t.Fatal("a definition with four problems was accepted")
	}
	// A: missing Resource, dangling Next. B: two Wait fields, unreachable.
	if len(rep.Diagnostics) < 4 {
		t.Errorf("got %d diagnostics, want at least 4:", len(rep.Diagnostics))
		for _, d := range rep.Diagnostics {
			t.Errorf("  %s", d)
		}
	}
}

func TestAnalyseRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  string
		want string
	}{
		{"missing StartAt", `{"States":{"A":{"Type":"Succeed"}}}`, "StartAt is required"},
		{"StartAt names nothing", `{"StartAt":"X","States":{"A":{"Type":"Succeed"}}}`, "not a state here"},
		{"no states", `{"StartAt":"A","States":{}}`, "at least one state"},
		{"unknown type", `{"StartAt":"A","States":{"A":{"Type":"Frobnicate"}}}`, "unknown state type"},
		{"Task without Resource", `{"StartAt":"A","States":{"A":{"Type":"Task","End":true}}}`, "Task requires Resource"},
		{"neither Next nor End", `{"StartAt":"A","States":{"A":{"Type":"Pass"}}}`, "needs Next, or End"},
		{"both Next and End", `{"StartAt":"A","States":{"A":{"Type":"Pass","Next":"B","End":true},"B":{"Type":"Succeed"}}}`, "not both"},
		{"Succeed with Next", `{"StartAt":"A","States":{"A":{"Type":"Succeed","Next":"B"},"B":{"Type":"Succeed"}}}`, "terminal"},
		{"Choice with Next", `{"StartAt":"A","States":{"A":{"Type":"Choice","Next":"B","Choices":[{"Variable":"$.x","StringEquals":"y","Next":"B"}]},"B":{"Type":"Succeed"}}}`, "transitions through its rules"},
		{"Choice with no rules", `{"StartAt":"A","States":{"A":{"Type":"Choice","Default":"B"},"B":{"Type":"Succeed"}}}`, "non-empty Choices"},
		{"Wait with nothing", `{"StartAt":"A","States":{"A":{"Type":"Wait","End":true}}}`, "exactly one of Seconds"},
		{"Parallel with no branches", `{"StartAt":"A","States":{"A":{"Type":"Parallel","End":true}}}`, "non-empty Branches"},
		{"Map with no processor", `{"StartAt":"A","States":{"A":{"Type":"Map","End":true}}}`, "requires ItemProcessor"},
		{"unreachable state", `{"StartAt":"A","States":{"A":{"Type":"Succeed"},"B":{"Type":"Succeed"}}}`, "no other state transitions here"},
		{"misplaced field", `{"StartAt":"A","States":{"A":{"Type":"Wait","Seconds":1,"Resource":"arn:x","End":true}}}`, "Resource is not a field of a Wait state"},
		{"bad ResultPath", `{"StartAt":"A","States":{"A":{"Type":"Pass","ResultPath":"$.a[*]","End":true}}}`, "wildcard"},
		{"Retry with empty ErrorEquals", `{"StartAt":"A","States":{"A":{"Type":"Task","Resource":"arn:x","Retry":[{"ErrorEquals":[]}],"End":true}}}`, "ErrorEquals is required"},
		{"States.ALL not alone", `{"StartAt":"A","States":{"A":{"Type":"Task","Resource":"arn:x","Retry":[{"ErrorEquals":["States.ALL","Other"]}],"End":true}}}`, "must be the only entry"},
		{"States.ALL not last", `{"StartAt":"A","States":{"A":{"Type":"Task","Resource":"arn:x","Retry":[{"ErrorEquals":["States.ALL"]},{"ErrorEquals":["X"]}],"End":true}}}`, "nothing after it can ever run"},
		{"invented States. error", `{"StartAt":"A","States":{"A":{"Type":"Task","Resource":"arn:x","Retry":[{"ErrorEquals":["States.Nope"]}],"End":true}}}`, "the States. prefix is reserved"},
		{"Catch without Next", `{"StartAt":"A","States":{"A":{"Type":"Task","Resource":"arn:x","Catch":[{"ErrorEquals":["States.ALL"]}],"End":true}}}`, "Next is required"},
		{"Retry on a Pass", `{"StartAt":"A","States":{"A":{"Type":"Pass","Retry":[{"ErrorEquals":["States.ALL"]}],"End":true}}}`, "only allowed on Task, Parallel and Map"},
		{"nested rule with Next", `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[{"And":[{"Variable":"$.x","StringEquals":"y","Next":"B"}],"Next":"B"}]},"B":{"Type":"Succeed"}}}`, "only a top-level choice rule"},
		{"rule with no comparison", `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[{"Variable":"$.x","Next":"B"}]},"B":{"Type":"Succeed"}}}`, "needs a comparison"},
		{"two operators in one rule", `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[{"Variable":"$.x","StringEquals":"y","NumericEquals":1,"Next":"B"}]},"B":{"Type":"Succeed"}}}`, "only one"},
		{"numeric op, string operand", `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[{"Variable":"$.x","NumericEquals":"one","Next":"B"}]},"B":{"Type":"Succeed"}}}`, "expects a numeric operand"},
		{"timestamp op, bad instant", `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[{"Variable":"$.x","TimestampEquals":"2020-01-01","Next":"B"}]},"B":{"Type":"Succeed"}}}`, "ISO-8601 timestamp"},
		{"presence op, non-boolean", `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[{"Variable":"$.x","IsString":"yes","Next":"B"}]},"B":{"Type":"Succeed"}}}`, "expects a boolean operand"},
		{"dangling Default", `{"StartAt":"A","States":{"A":{"Type":"Choice","Choices":[{"Variable":"$.x","StringEquals":"y","Next":"B"}],"Default":"Gone"},"B":{"Type":"Succeed"}}}`, "not a state here"},
		{"BackoffRate below 1", `{"StartAt":"A","States":{"A":{"Type":"Task","Resource":"arn:x","Retry":[{"ErrorEquals":["X"],"BackoffRate":0.5}],"End":true}}}`, "at least 1.0"},
		{"both ItemProcessor and Iterator", `{"StartAt":"A","States":{"A":{"Type":"Map","End":true,"ItemProcessor":{"StartAt":"I","States":{"I":{"Type":"Succeed"}}},"Iterator":{"StartAt":"I","States":{"I":{"Type":"Succeed"}}}}}}`, "set only one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rep := ValidateDefinition([]byte(tc.def))
			if rep.OK() {
				t.Fatalf("accepted, but it should report %q", tc.want)
			}
			var got []string
			for _, d := range rep.Diagnostics {
				got = append(got, d.String())
				if strings.Contains(d.Message, tc.want) {
					return
				}
			}
			t.Errorf("no diagnostic mentions %q; got:\n  %s", tc.want, strings.Join(got, "\n  "))
		})
	}
}

// TestAnalyseIsDeterministic — Go randomises map iteration, so a report built
// by ranging over States would reorder between runs and stop being diffable.
func TestAnalyseIsDeterministic(t *testing.T) {
	const def = `{"StartAt":"A","States":{
	  "A":{"Type":"Task","End":true},
	  "B":{"Type":"Task","End":true},
	  "C":{"Type":"Task","End":true},
	  "D":{"Type":"Task","End":true}
	}}`
	var first string
	for i := 0; i < 20; i++ {
		_, rep := ValidateDefinition([]byte(def))
		var b strings.Builder
		for _, d := range rep.Diagnostics {
			b.WriteString(d.String())
			b.WriteByte('\n')
		}
		if i == 0 {
			first = b.String()
			continue
		}
		if b.String() != first {
			t.Fatalf("diagnostics reordered between runs:\n--- first\n%s--- run %d\n%s", first, i, b.String())
		}
	}
}

// TestParseRejectsNonJSON — the parse error has to survive into the report,
// because CreateStateMachine turns it into InvalidDefinition.
func TestParseRejectsNonJSON(t *testing.T) {
	d, rep := ValidateDefinition([]byte(`{"StartAt": `))
	if d != nil || rep.OK() {
		t.Fatal("malformed JSON was accepted")
	}
	if !strings.Contains(rep.Error(), "not valid JSON") {
		t.Errorf("report = %q, want it to say the definition is not valid JSON", rep.Error())
	}
}
