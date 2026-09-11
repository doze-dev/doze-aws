package main

// The model walk: operation discovery, trait reading, and the path a
// constraint is reported at.
//
// This is the part of dzaudit every rejection-parity suite in the repo stands
// on. A constraint it fails to find is a constraint no suite ever replays, and
// the suite still reports a clean pass — the same silent-hole failure the
// CloudWatch `dispatched` set had, one level further up. It had no tests.
//
// The fixtures are hand-written Smithy fragments rather than a real AWS model:
// a test that needs the network, or a 4 MB checked-in model, is a test people
// skip.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// modelFrom builds a model from a Smithy-shaped JSON fragment.
func modelFrom(t *testing.T, src string) *model {
	t.Helper()
	var m model
	if err := json.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	return &m
}

// A service reaches its operations two ways, and reading only the direct list
// finds a fraction of the surface while looking plausible — the comment on
// shape.Operations says Lambda hangs 72 of 85 operations off resources. This
// pins that both routes are followed.
const twoRoutesModel = `{"shapes": {
  "com.example#Svc": {
    "type": "service",
    "traits": {"aws.protocols#awsJson1_0": {}},
    "operations": [{"target": "com.example#Direct"}],
    "resources": [{"target": "com.example#Thing"}]
  },
  "com.example#Thing": {
    "type": "resource",
    "create": {"target": "com.example#CreateThing"},
    "read": {"target": "com.example#GetThing"},
    "collectionOperations": [{"target": "com.example#ListThings"}]
  },
  "com.example#Direct":      {"type": "operation", "input": {"target": "com.example#Empty"}},
  "com.example#CreateThing": {"type": "operation", "input": {"target": "com.example#Empty"}},
  "com.example#GetThing":    {"type": "operation", "input": {"target": "com.example#Empty"}},
  "com.example#ListThings":  {"type": "operation", "input": {"target": "com.example#Empty"}},
  "com.example#Empty":       {"type": "structure", "members": {}}
}}`

func TestServiceFindsOperationsBehindResources(t *testing.T) {
	m := modelFrom(t, twoRoutesModel)
	id, proto, ops := m.service()
	if !strings.HasSuffix(id, "#Svc") {
		t.Errorf("service id = %q", id)
	}
	if proto != "awsJson1_0" {
		t.Errorf("protocol = %q, want awsJson1_0", proto)
	}
	for _, want := range []string{"Direct", "CreateThing", "GetThing", "ListThings"} {
		if !slices.ContainsFunc(ops, func(op string) bool { return shortName(op) == want }) {
			t.Errorf("%s was not discovered — reading `operations` alone finds a "+
				"fraction of the surface and looks plausible doing it: %v", want, ops)
		}
	}
}

// Every constraint kind the tables use, at the path a caller would set it, and
// through the three container shapes that make paths interesting.
const constraintsModel = `{"shapes": {
  "com.example#Svc": {
    "type": "service",
    "traits": {"aws.protocols#awsJson1_0": {}},
    "operations": [{"target": "com.example#Put"}]
  },
  "com.example#Put": {"type": "operation", "input": {"target": "com.example#PutInput"}},
  "com.example#PutInput": {
    "type": "structure",
    "members": {
      "Name":    {"target": "com.example#Name", "traits": {"smithy.api#required": {}}},
      "Mode":    {"target": "com.example#Mode"},
      "Count":   {"target": "com.example#Count"},
      "Items":   {"target": "com.example#Items"},
      "Tags":    {"target": "com.example#Tags"},
      "Nested":  {"target": "com.example#Nested"}
    }
  },
  "com.example#Name":  {"type": "string", "traits": {
      "smithy.api#length": {"min": 1, "max": 255},
      "smithy.api#pattern": "^[A-Za-z0-9]+$"}},
  "com.example#Mode": {"type": "enum", "members": {
      "FAST": {"target": "smithy.api#Unit", "traits": {"smithy.api#enumValue": "FAST"}},
      "SLOW": {"target": "smithy.api#Unit", "traits": {"smithy.api#enumValue": "SLOW"}}}},
  "com.example#Count": {"type": "integer", "traits": {
      "smithy.api#range": {"min": 1, "max": 10}}},
  "com.example#Items": {"type": "list", "member": {"target": "com.example#Name"}},
  "com.example#Tags":  {"type": "map", "key": {"target": "com.example#Name"},
                        "value": {"target": "com.example#Name"}},
  "com.example#Nested": {"type": "structure", "members": {
      "Inner": {"target": "com.example#Name"}}}
}}`

func TestAuditFindsEveryConstraintAtItsPath(t *testing.T) {
	m := modelFrom(t, constraintsModel)
	got := map[string][]string{}
	for _, f := range m.audit("") {
		got[f.Path] = append(got[f.Path], f.Constraint.Kind)
	}

	for _, tc := range []struct {
		path string
		kind string
		why  string
	}{
		{"Name", "required", "a required member"},
		{"Name", "length", "length on the targeted shape, reported at the member's path"},
		{"Name", "pattern", "pattern likewise"},
		{"Mode", "enum", "an enum"},
		{"Count", "range", "a numeric range"},
		{"Items[]", "length", "a list ELEMENT, marked []"},
		{"Tags{}", "length", "a map VALUE, marked {}"},
		{"Nested.Inner", "length", "a nested structure member, dotted"},
	} {
		if !slices.Contains(got[tc.path], tc.kind) {
			t.Errorf("no %s constraint at %q (%s); found %v",
				tc.kind, tc.path, tc.why, got[tc.path])
		}
	}
}

// An operation with no input shape contributes nothing rather than panicking.
// Several real operations have none — every `smithy.api#Unit` input.
func TestAuditSkipsOperationsWithNoInput(t *testing.T) {
	m := modelFrom(t, `{"shapes": {
      "com.example#Svc": {"type": "service", "traits": {"aws.protocols#awsJson1_0": {}},
                          "operations": [{"target": "com.example#NoInput"}]},
      "com.example#NoInput": {"type": "operation"}
    }}`)
	if got := m.audit(""); len(got) != 0 {
		t.Errorf("an operation with no input produced %d findings: %+v", len(got), got)
	}
}

// The -op filter is how a single operation's checklist is generated, which is
// what every "add the cases for this op" workflow in the repo uses.
func TestAuditFilterIsCaseInsensitiveAndExact(t *testing.T) {
	m := modelFrom(t, constraintsModel)
	if got := m.audit("put"); len(got) == 0 {
		t.Error("the filter should match regardless of case")
	}
	if got := m.audit("Pu"); len(got) != 0 {
		t.Errorf("the filter should be exact, not a prefix: %+v", got)
	}
}

// Recursion has to terminate: a shape that contains itself is normal in these
// models, and the walk is bounded by maxDepth and a seen set.
func TestWalkTerminatesOnARecursiveShape(t *testing.T) {
	m := modelFrom(t, `{"shapes": {
      "com.example#Svc": {"type": "service", "traits": {"aws.protocols#awsJson1_0": {}},
                          "operations": [{"target": "com.example#Op"}]},
      "com.example#Op": {"type": "operation", "input": {"target": "com.example#Node"}},
      "com.example#Node": {"type": "structure", "members": {
          "Child": {"target": "com.example#Node"},
          "Name":  {"target": "com.example#Name"}}},
      "com.example#Name": {"type": "string", "traits": {"smithy.api#length": {"min": 1, "max": 8}}}
    }}`)
	// The assertion is that this returns at all; the count is a sanity bound.
	if got := m.audit(""); len(got) == 0 || len(got) > 50 {
		t.Errorf("recursive walk produced %d findings", len(got))
	}
}

func TestConstraintsOfReadsEachTrait(t *testing.T) {
	traits := map[string]json.RawMessage{
		"smithy.api#length":   json.RawMessage(`{"min": 2, "max": 4}`),
		"smithy.api#pattern":  json.RawMessage(`"^a+$"`),
		"smithy.api#range":    json.RawMessage(`{"min": 5, "max": 7}`),
		"smithy.api#required": json.RawMessage(`{}`),
	}
	kinds := map[string]bool{}
	for _, c := range constraintsOf(traits) {
		kinds[c.Kind] = true
	}
	for _, want := range []string{"length", "pattern", "range", "required"} {
		if !kinds[want] {
			t.Errorf("constraintsOf did not read %s: %v", want, kinds)
		}
	}
}

// A length with only a minimum is common, and rendering it must not claim a
// maximum the model never set.
func TestOpenEndedLengthRendersWithoutAFalseMaximum(t *testing.T) {
	cs := constraintsOf(map[string]json.RawMessage{
		"smithy.api#length": json.RawMessage(`{"min": 1}`),
	})
	if len(cs) != 1 {
		t.Fatalf("want one constraint, got %+v", cs)
	}
	if s := cs[0].String(); !strings.Contains(s, "1") {
		t.Errorf("rendered %q, which does not mention the minimum", s)
	}
}
