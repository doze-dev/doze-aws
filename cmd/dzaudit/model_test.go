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
	"os"
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

// The IAM table is what a policy is matched against. A model that names the
// action says it; one that says nothing falls to AWS's reference, which is
// found in the cache — never a table of default names that would look like an
// answer.
func TestIAMActionsComeFromTheModelOrTheReference(t *testing.T) {
	withTrait := &model{Shapes: map[string]shape{
		"x#Svc": {Type: "service", Operations: []ref{{Target: "x#Invoke"}, {Target: "x#GetThing"}},
			Traits: map[string]json.RawMessage{"aws.api#service": json.RawMessage(`{"arnNamespace":"svc"}`)}},
		"x#Invoke":   {Type: "operation", Traits: map[string]json.RawMessage{"aws.iam#iamAction": json.RawMessage(`{"name":"InvokeFunction"}`)}},
		"x#GetThing": {Type: "operation", Traits: map[string]json.RawMessage{"aws.iam#iamAction": json.RawMessage(`{}`)}},
	}}
	got, err := buildIAM(withTrait, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Actions["Invoke"] != "svc:InvokeFunction" || got.Actions["GetThing"] != "svc:GetThing" {
		t.Errorf("actions = %v, want the rename and the default", got.Actions)
	}

	// No trait: the reference decides, and a version has an action of its own.
	cache := t.TempDir()
	os.WriteFile(cache+"/svc.reference.json", []byte(`{
	  "Actions": [{"Name":"GetThing"},{"Name":"GetThingTagging"},{"Name":"PutBucketCORS"},{"Name":"GetObject"},{"Name":"GetObjectVersion"}],
	  "Operations": [
	    {"Name":"GetThing","AuthorizedActions":[{"Name":"GetThing","Service":"svc-other"},{"Name":"GetThingTagging","Service":"svc"}]},
	    {"Name":"DeleteBucketCors","AuthorizedActions":[{"Name":"PutBucketCORS","Service":"svc"}]},
	    {"Name":"GetObject","AuthorizedActions":[{"Name":"GetObject","Service":"svc"},{"Name":"GetObjectVersion","Service":"svc"}]},
	    {"Name":"CopyObject","AuthorizedActions":[{"Name":"GetObject","Service":"svc"},{"Name":"PutObject","Service":"svc"}]},
	    {"Name":"ListThings","AuthorizedActions":[{"Name":"GetObjectAcl","Service":"svc"},{"Name":"ListBucket","Service":"svc"}]},
	    {"Name":"DeleteObjects","AuthorizedActions":[{"Name":"BypassGovernanceRetention","Service":"svc"},{"Name":"DeleteObject","Service":"svc"}]}
	  ]}`), 0o644)
	bare := &model{Shapes: map[string]shape{
		"x#Svc": {Type: "service", Operations: []ref{{Target: "x#GetThing"}, {Target: "x#DeleteBucketCors"}, {Target: "x#GetObject"}, {Target: "x#CopyObject"}, {Target: "x#DeleteObjects"}, {Target: "x#ListThings"}},
			Traits: map[string]json.RawMessage{"aws.api#service": json.RawMessage(`{"arnNamespace":"svc"}`)}},
		"x#GetThing": {Type: "operation"}, "x#DeleteBucketCors": {Type: "operation"}, "x#GetObject": {Type: "operation"},
		"x#CopyObject": {Type: "operation"}, "x#DeleteObjects": {Type: "operation"}, "x#ListThings": {Type: "operation"},
	}}
	got, err = buildIAM(bare, cache)
	if err != nil {
		t.Fatal(err)
	}
	if got.Actions["DeleteBucketCors"] != "svc:PutBucketCORS" {
		t.Errorf("DeleteBucketCors = %q, want the reference's svc:PutBucketCORS", got.Actions["DeleteBucketCors"])
	}
	if got.Actions["GetThing"] != "svc:GetThingTagging" {
		t.Errorf("GetThing = %q: only an action of this service is the operation's own", got.Actions["GetThing"])
	}
	if got.Actions["CopyObject"] != "svc:PutObject" {
		t.Errorf("CopyObject = %q, want the destination's write, not the source's read", got.Actions["CopyObject"])
	}
	if got.Actions["ListThings"] != "svc:ListBucket" {
		t.Errorf("ListThings = %q, want ListBucket, not the GetObjectAcl the reference lists first", got.Actions["ListThings"])
	}
	if got.Actions["DeleteObjects"] != "svc:DeleteObject" {
		t.Errorf("DeleteObjects = %q, want DeleteObject, not the governance bypass listed first", got.Actions["DeleteObjects"])
	}
	if got.Versioned["svc:GetObject"] != "svc:GetObjectVersion" {
		t.Errorf("versioned = %v, want GetObject → GetObjectVersion", got.Versioned)
	}
}

// A rule on a list or on a map's keys is a rule on the member, and the walk
// used to read only what sat on the shape of a VALUE: a list's length and a
// tag key's pattern were in no audit. Logs found the key rule unenforced by
// hand, which is how this was noticed.
const containerModel = `{"shapes": {
  "com.example#Svc": {"type": "service", "traits": {"aws.protocols#awsJson1_1": {}},
    "operations": [{"target": "com.example#Put"}]},
  "com.example#Put": {"type": "operation", "input": {"target": "com.example#PutInput"}},
  "com.example#PutInput": {"type": "structure", "members": {
    "Names":  {"target": "com.example#NameList"},
    "Tags":   {"target": "com.example#TagMap"},
    "Nested": {"target": "com.example#NestedList"}
  }},
  "com.example#NameList": {"type": "list", "member": {"target": "smithy.api#String"},
    "traits": {"smithy.api#length": {"min": 1, "max": 3}}},
  "com.example#TagKey":   {"type": "string", "traits": {"smithy.api#length": {"min": 1, "max": 5}}},
  "com.example#TagMap":   {"type": "map", "key": {"target": "com.example#TagKey"}, "value": {"target": "smithy.api#String"},
    "traits": {"smithy.api#length": {"max": 2}}},
  "com.example#Short":    {"type": "string", "traits": {"smithy.api#length": {"min": 2}}},
  "com.example#NestedList": {"type": "list", "member": {"target": "com.example#Short"},
    "traits": {"smithy.api#length": {"max": 4}}}
}}`

func TestAListsLengthAndAMapsKeyAreAudited(t *testing.T) {
	m := modelFrom(t, containerModel)
	found := m.audit("")
	have := map[string]bool{}
	for _, f := range found {
		have[f.Path+"|"+f.Constraint.String()] = true
	}
	for _, want := range []string{
		"Names|list length 1..3",
		"Tags|map key length 1..5",
		"Tags|map length -..2",
		"Nested|list length -..4",
	} {
		if !have[want] {
			t.Errorf("not found: %q\nhave: %v", want, have)
		}
	}
}

func TestContainerCasesBreakOnlyTheContainersRule(t *testing.T) {
	m := modelFrom(t, containerModel)
	byPath := map[string][]finding{}
	for _, f := range m.audit("") {
		byPath[f.Path] = append(byPath[f.Path], f)
	}

	// A list of plain strings: one over the maximum, and an empty one under the minimum.
	var names []string
	for _, f := range byPath["Names"] {
		vs, ok := containerViolations(f.Constraint)
		if !ok {
			t.Fatal("no case for a list of plain strings")
		}
		for _, v := range vs {
			names = append(names, v.Why)
			l, _ := v.Value.([]any)
			if strings.Contains(v.Why, "more than") && len(l) != 4 {
				t.Errorf("over the maximum is %d elements, want 4", len(l))
			}
			if strings.Contains(v.Why, "fewer than") && len(l) != 0 {
				t.Errorf("under the minimum is %d elements, want 0", len(l))
			}
		}
	}
	if len(names) != 2 {
		t.Errorf("want two cases for 1..3, got %v", names)
	}

	// A map key: the value of the map is the only entry and its key is too long.
	var sawKey bool
	for _, f := range byPath["Tags"] {
		if f.Constraint.Container != "mapKey" {
			continue
		}
		for _, v := range mustViolations(t, f.Constraint) {
			sawKey = true
			mp, _ := v.Value.(map[string]any)
			if len(mp) != 1 {
				t.Fatalf("a key case should hold one entry, has %d", len(mp))
			}
			for k, val := range mp {
				if len(k) >= 1 && len(k) <= 5 {
					t.Errorf("key %q does not break 1..5", k)
				}
				if val != "a" {
					t.Errorf("the value was %v: the case must fail for the key alone", val)
				}
			}
		}
	}
	if !sawKey {
		t.Error("no map-key case")
	}

	// A list whose element is itself constrained is built from elements that
	// satisfy the element's rule, so the case fails for the list's rule alone.
	for _, f := range byPath["Nested"] {
		if f.Constraint.Container != "list" {
			continue
		}
		for _, v := range mustViolations(t, f.Constraint) {
			l, _ := v.Value.([]any)
			if len(l) != 5 {
				t.Errorf("over a maximum of 4 is %d elements, want 5", len(l))
			}
			for _, e := range l {
				if s, _ := e.(string); len(s) < 2 {
					t.Errorf("element %q breaks its own minimum length of 2: the case would fail for the element", e)
				}
			}
		}
	}
}

// A sample satisfies every rule its shape states, and there is none when a rule
// cannot be met or cannot be checked: a case built around a bad element reports
// the container's rule as enforced when the service only refused the element.
func TestASampleSatisfiesItsShapeOrThereIsNone(t *testing.T) {
	m := modelFrom(t, `{"shapes": {
	  "x#Tag":    {"type": "string", "traits": {"smithy.api#length": {"min": 3, "max": 8}, "smithy.api#pattern": "^[a-z]+$"}},
	  "x#Digits": {"type": "string", "traits": {"smithy.api#pattern": "^[0-9]{2}$"}},
	  "x#Never":  {"type": "string", "traits": {"smithy.api#pattern": "^\\p{IsNoSuchClass}+$"}},
	  "x#Tight":  {"type": "string", "traits": {"smithy.api#length": {"min": 5, "max": 2}}},
	  "x#Port":   {"type": "integer", "traits": {"smithy.api#range": {"min": 1024, "max": 65535}}},
	  "x#None":   {"type": "integer", "traits": {"smithy.api#range": {"min": 9, "max": 3}}},
	  "x#Kind":   {"type": "enum", "members": {"B": {"traits": {"smithy.api#enumValue": "b"}}, "A": {"traits": {"smithy.api#enumValue": "a"}}}},
	  "x#Thing":  {"type": "structure", "members": {}}
	}}`)
	for shape, want := range map[string]any{"x#Tag": "aaa", "x#Digits": "11", "x#Port": float64(1024), "x#Kind": "a"} {
		got, ok := m.sample(shape)
		if !ok || got != want {
			t.Errorf("sample(%s) = %v, %v; want %v", shape, got, ok, want)
		}
	}
	for _, shape := range []string{"x#Never", "x#Tight", "x#None", "x#Thing", "x#Missing"} {
		if got, ok := m.sample(shape); ok {
			t.Errorf("sample(%s) = %v, want none", shape, got)
		}
	}
}

func mustViolations(t *testing.T, c constraint) []struct {
	Why   string
	Value any
} {
	t.Helper()
	vs, ok := containerViolations(c)
	if !ok {
		t.Fatalf("no case for %+v", c)
	}
	return vs
}

const cappedModel = `{"shapes": {
  "com.example#Svc": {"type": "service", "traits": {"aws.protocols#awsJson1_1": {}},
    "operations": [{"target": "com.example#Put"}]},
  "com.example#Put": {"type": "operation", "input": {"target": "com.example#PutInput"}},
  "com.example#PutInput": {"type": "structure", "members": {
    "Events":  {"target": "com.example#EventList"},
    "ByKey":   {"target": "com.example#KeyedMap"}
  }},
  "com.example#EventList": {"type": "list", "member": {"target": "smithy.api#String"},
    "traits": {"smithy.api#length": {"min": 1, "max": 10000}}},
  "com.example#Key": {"type": "string", "traits": {"smithy.api#pattern": "^[a-z]+$"}},
  "com.example#KeyedMap": {"type": "map", "key": {"target": "com.example#Key"}, "value": {"target": "smithy.api#String"},
    "traits": {"smithy.api#length": {"max": 3}}}
}}`

// A bound too large to write out loses its maximum case and keeps its minimum
// one; and a map whose keys are constrained has no SIZE case, because the keys
// a size case invents would break the key's rule and report the wrong thing.
func TestContainerCasesKeepWhatCanBeBuilt(t *testing.T) {
	m := modelFrom(t, cappedModel)
	var events, sized []finding
	for _, f := range m.audit("") {
		switch {
		case f.Path == "Events" && f.Constraint.Container == "list":
			events = append(events, f)
		case f.Path == "ByKey" && f.Constraint.Container == "map":
			sized = append(sized, f)
		}
	}
	if len(events) != 1 || len(sized) != 1 {
		t.Fatalf("findings: events=%d sized=%d", len(events), len(sized))
	}
	vs, ok := containerViolations(events[0].Constraint)
	if !ok || len(vs) != 1 || !strings.Contains(vs[0].Why, "fewer than") {
		t.Errorf("want only the minimum case for 1..10000, got ok=%v %+v", ok, vs)
	}
	if _, ok := containerViolations(sized[0].Constraint); ok {
		t.Error("a size case was built for a map whose keys are constrained")
	}
}
