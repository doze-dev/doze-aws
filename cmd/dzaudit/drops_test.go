package main

import (
	"bytes"
	"strings"
	"testing"
)

// A constraint the generator builds no case for is named, never dropped in
// silence: the operation, the member, the rule and the reason. Every way it
// can leave something out is in this model once.
const droppingModel = `{"shapes": {
  "x#Svc": {"type": "service", "traits": {"aws.protocols#awsJson1_1": {}}, "operations": [{"target": "x#Op"}]},
  "x#Op": {"type": "operation", "input": {"target": "x#In"}},
  "x#In": {"type": "structure", "members": {
    "Anything": {"target": "x#Anything"},
    "Big":      {"target": "x#Big"},
    "Wrapped":  {"target": "x#Wrapped"},
    "A":        {"target": "x#L1"}
  }},
  "x#Anything": {"type": "string", "traits": {"smithy.api#pattern": "^[\\s\\S]*$"}},
  "x#Big": {"type": "list", "member": {"target": "smithy.api#String"},
    "traits": {"smithy.api#length": {"min": 1, "max": 5000}}},
  "x#Rec": {"type": "structure", "members": {"V": {"target": "smithy.api#String"}}},
  "x#Wrapped": {"type": "list", "member": {"target": "x#Rec"},
    "traits": {"smithy.api#length": {"min": 1, "max": 3}}},
  "x#L1": {"type": "structure", "members": {"B": {"target": "x#L2"}}},
  "x#L2": {"type": "structure", "members": {"C": {"target": "x#L3"}}},
  "x#L3": {"type": "structure", "members": {"D": {"target": "x#L4"}}},
  "x#L4": {"type": "structure", "members": {"E": {"target": "x#L5"}}},
  "x#L5": {"type": "structure", "members": {"F": {"target": "x#L6"}}},
  "x#L6": {"type": "structure", "members": {"G": {"target": "smithy.api#String"}}}
}}`

func TestWhatTheGeneratorLeavesOutIsListed(t *testing.T) {
	m := modelFrom(t, droppingModel)
	var out bytes.Buffer
	was := diagnostics
	diagnostics = &out
	defer func() { diagnostics = was }()
	if err := emitCases(&bytes.Buffer{}, m, m.audit(""), ""); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		// A pattern nothing on hand breaks: not emitted as a "violation" that is valid.
		"Op Anything [pattern ^[\\s\\S]*$]: no candidate value on hand breaks the pattern",
		// The minimum's case stands, the maximum's is above the cap, and it says so.
		"Op Big [list length 1..5000]: the maximum of 5000 is above the cap of 200",
		// A list of structures: nothing valid on its own to build around.
		"Op Wrapped [list length 1..3]: no case: the element is itself constrained",
		// The walk stops at maxDepth, and names where.
		"member(s) nest deeper than 4 levels",
		"Op A.B.C.D.E",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not say %q:\n%s", want, got)
		}
	}
	// And it is stable: the same model twice gives the same list.
	var again bytes.Buffer
	diagnostics = &again
	m2 := modelFrom(t, droppingModel)
	if err := emitCases(&bytes.Buffer{}, m2, m2.audit(""), ""); err != nil {
		t.Fatal(err)
	}
	if again.String() != got {
		t.Errorf("the report differs between runs:\n%s\n---\n%s", got, again.String())
	}
}

// A model with nothing to drop prints nothing: silence is information too.
func TestNothingDroppedSaysNothing(t *testing.T) {
	const clean = `{"shapes": {
	  "x#Svc": {"type": "service", "traits": {"aws.protocols#awsJson1_1": {}}, "operations": [{"target": "x#Op"}]},
	  "x#Op": {"type": "operation", "input": {"target": "x#In"}},
	  "x#In": {"type": "structure", "members": {"N": {"target": "x#N"}}},
	  "x#N": {"type": "integer", "traits": {"smithy.api#range": {"min": 1, "max": 9}}}
	}}`
	m := modelFrom(t, clean)
	var out bytes.Buffer
	was := diagnostics
	diagnostics = &out
	defer func() { diagnostics = was }()
	if err := emitCases(&bytes.Buffer{}, m, m.audit(""), ""); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("said %q about a model that drops nothing", out.String())
	}
}
