package shapecheck

import (
	"strings"
	"testing"
)

func TestSegmentsParsesThePathGrammar(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []segment
	}{
		{"Name", []segment{{segMember, "Name"}}},
		{"Table.Name", []segment{{segMember, "Table"}, {segMember, "Name"}}},
		{"Queues[]", []segment{{segMember, "Queues"}, {segElem, ""}}},
		{"Failed[].Code", []segment{{segMember, "Failed"}, {segElem, ""}, {segMember, "Code"}}},
		{"Attributes{}", []segment{{segMember, "Attributes"}, {segValue, ""}}},
		{"Tags{}.Key", []segment{{segMember, "Tags"}, {segValue, ""}, {segMember, "Key"}}},
		// A list of maps: element first, then the map's value. Getting this
		// order backwards resolves to nothing and reads as "member absent",
		// which would quietly turn every such check into a pass.
		{"Rows[]{}", []segment{{segMember, "Rows"}, {segElem, ""}, {segValue, ""}}},
	} {
		got := segments(tc.path)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %d segments, want %d (%+v)", tc.path, len(got), len(tc.want), got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: segment %d = %+v, want %+v", tc.path, i, got[i], tc.want[i])
			}
		}
	}
}

// A path naming many values must report each. Collapsing to the first would
// hide the case where element seventeen is the malformed one.
func TestEveryElementIsChecked(t *testing.T) {
	body := []byte(`{"Failed":[{"Code":"a"},{"Code":7},{"Code":"c"},{"Code":false}]}`)
	got := Check(body, Shape{Operation: "X", Members: []Member{
		{Path: "Failed", Type: "list"},
		{Path: "Failed[]", Type: "structure"},
		{Path: "Failed[].Code", Type: "string"},
	}})
	if len(got) != 2 {
		t.Fatalf("want 2 problems (the number and the boolean), got %d: %v", len(got), got)
	}
	for _, p := range got {
		if p.Path != "Failed[].Code" {
			t.Errorf("wrong path: %v", p)
		}
	}
}

func TestRequiredMembersMustBePresent(t *testing.T) {
	s := Shape{Operation: "X", Members: []Member{
		{Path: "QueueUrl", Type: "string", Required: true},
		{Path: "NextToken", Type: "string"},
	}}
	if got := Check([]byte(`{"QueueUrl":"http://x"}`), s); len(got) != 0 {
		t.Errorf("a response with the required member failed: %v", got)
	}
	got := Check([]byte(`{"NextToken":"t"}`), s)
	if len(got) != 1 || got[0].Path != "QueueUrl" {
		t.Fatalf("a missing required member was not reported: %v", got)
	}
	if !strings.Contains(got[0].Why, "required") {
		t.Errorf("the reason should say it was required: %q", got[0].Why)
	}
}

// The classic emulator bug, and one this repo has a hand-written test for
// elsewhere: a number rendered as a string.
func TestANumberRenderedAsAStringIsCaught(t *testing.T) {
	s := Shape{Operation: "X", Members: []Member{
		{Path: "ApproximateNumberOfMessages", Type: "integer"},
	}}
	if got := Check([]byte(`{"ApproximateNumberOfMessages":12}`), s); len(got) != 0 {
		t.Errorf("a number failed: %v", got)
	}
	got := Check([]byte(`{"ApproximateNumberOfMessages":"12"}`), s)
	if len(got) != 1 {
		t.Fatalf("a stringified number was not caught: %v", got)
	}
	if !strings.Contains(got[0].Why, "integer") {
		t.Errorf("the reason should name the expected type: %q", got[0].Why)
	}
}

func TestAFractionalIntegerIsCaught(t *testing.T) {
	got := Check([]byte(`{"Count":1.5}`), Shape{Members: []Member{{Path: "Count", Type: "integer"}}})
	if len(got) != 1 {
		t.Fatalf("1.5 passed as an integer: %v", got)
	}
}

func TestEnumValuesAreChecked(t *testing.T) {
	s := Shape{Members: []Member{
		{Path: "Status", Type: "enum", Enum: []string{"RUNNING", "SUCCEEDED", "FAILED"}},
	}}
	if got := Check([]byte(`{"Status":"SUCCEEDED"}`), s); len(got) != 0 {
		t.Errorf("a declared value failed: %v", got)
	}
	got := Check([]byte(`{"Status":"Succeeded"}`), s)
	if len(got) != 1 {
		t.Fatalf("a value outside the enum was not caught (case matters): %v", got)
	}
	if !strings.Contains(got[0].Why, "RUNNING") {
		t.Errorf("the reason should list the permitted values: %q", got[0].Why)
	}
}

// The types a JSON response cannot disagree about must not be checked, or the
// checker manufactures failures on correct wire output.
func TestUncheckableTypesArePassedOver(t *testing.T) {
	body := []byte(`{"When":1757000000,"Blob":"aGk=","Anything":{"x":1}}`)
	got := Check(body, Shape{Members: []Member{
		{Path: "When", Type: "timestamp"},
		{Path: "Blob", Type: "blob"},
		{Path: "Anything", Type: "any"},
	}})
	if len(got) != 0 {
		t.Errorf("a checker that cannot know reported anyway: %v", got)
	}
}

// An explicit null is an absence, not a type error. AWS omits rather than
// nulls, but reporting one as "the model says a string, the response has null"
// would be wrong about what went wrong.
func TestNullIsNotATypeError(t *testing.T) {
	got := Check([]byte(`{"Name":null}`), Shape{Members: []Member{{Path: "Name", Type: "string"}}})
	if len(got) != 0 {
		t.Errorf("an explicit null was reported as a type error: %v", got)
	}
}

// An empty response fails only the TOP-LEVEL required members. Reporting
// "Failed[].Code is missing" when there is no Failed list at all is noise that
// buries the one line that matters.
func TestAnEmptyResponseReportsOnlyTopLevelRequirements(t *testing.T) {
	got := Check(nil, Shape{Members: []Member{
		{Path: "Failed", Type: "list", Required: true},
		{Path: "Failed[].Code", Type: "string", Required: true},
	}})
	if len(got) != 1 || got[0].Path != "Failed" {
		t.Fatalf("want one problem naming Failed, got %v", got)
	}
}

func TestANonJSONResponseSaysSo(t *testing.T) {
	got := Check([]byte(`<Error><Code>x</Code></Error>`), Shape{Members: []Member{{Path: "A", Type: "string"}}})
	if len(got) != 1 || !strings.Contains(got[0].Why, "not JSON") {
		t.Fatalf("want a not-JSON problem, got %v", got)
	}
}

// A member the model declares and the response omits is fine unless required —
// AWS omits empty members constantly, and failing on that would make the
// checker unusable on the first operation it met.
func TestAnAbsentOptionalMemberIsFine(t *testing.T) {
	got := Check([]byte(`{}`), Shape{Members: []Member{
		{Path: "NextToken", Type: "string"},
		{Path: "Queues", Type: "list"},
	}})
	if len(got) != 0 {
		t.Errorf("absent optional members were reported: %v", got)
	}
}

// A required member under an ABSENT optional parent is not missing.
//
// The first version of Check resolved the whole path and called an empty result
// missing, so a correct DynamoDB CreateTable response produced nine findings:
// RestoreSummary is simply not in it, and its two required children were
// reported absent. They are not absent, they are not applicable. A checker that
// cannot tell those apart is one that gets switched off in its first week.
func TestRequiredUnderAnAbsentParentIsNotMissing(t *testing.T) {
	s := Shape{Members: []Member{
		{Path: "Table", Type: "structure"},
		{Path: "Table.RestoreSummary", Type: "structure"},
		{Path: "Table.RestoreSummary.RestoreInProgress", Type: "boolean", Required: true},
	}}
	// No RestoreSummary at all.
	if got := Check([]byte(`{"Table":{"TableName":"t"}}`), s); len(got) != 0 {
		t.Errorf("a required member under an absent parent was reported: %v", got)
	}
	// Present but incomplete IS a finding — the question now arises.
	got := Check([]byte(`{"Table":{"RestoreSummary":{}}}`), s)
	if len(got) != 1 || got[0].Path != "Table.RestoreSummary.RestoreInProgress" {
		t.Fatalf("a present parent missing its required child was not reported: %v", got)
	}
}

// An empty list names no elements, so a required member of its element type is
// not missing. Every empty List* response depends on this.
func TestAnEmptyListHasNoMissingMembers(t *testing.T) {
	s := Shape{Members: []Member{
		{Path: "Queues", Type: "list"},
		{Path: "Queues[]", Type: "structure"},
		{Path: "Queues[].Name", Type: "string", Required: true},
	}}
	if got := Check([]byte(`{"Queues":[]}`), s); len(got) != 0 {
		t.Errorf("an empty list produced findings: %v", got)
	}
	got := Check([]byte(`{"Queues":[{"Name":"a"},{}]}`), s)
	if len(got) != 1 || got[0].Path != "Queues[].Name" {
		t.Fatalf("the element missing its required member was not reported: %v", got)
	}
}
