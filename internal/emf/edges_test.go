package emf

import "testing"

// A metric whose value is an empty array has no observation: it is dropped,
// not recorded as a zero. A zero would be a data point the function never made.
func TestAnEmptyArrayOfValuesIsNoMetric(t *testing.T) {
	line := `{"_aws":{"CloudWatchMetrics":[{"Namespace":"N","Metrics":[{"Name":"M"}]}]},"M":[]}`
	if got := Parse([]byte(line)); got != nil {
		t.Errorf("Parse = %#v, want nothing", got)
	}
}

// Looks is a gate, so it answers "maybe" for anything that has the directive
// key where it could be, including a key that is the last thing on the line:
// whether the rest parses is Parse's to say.
func TestLooksSeesTheKeyAtTheEdges(t *testing.T) {
	for line, want := range map[string]bool{
		`{"_aws"`:       true, // the key ends the line exactly
		`{"_aws":{}}`:   true, // the key follows the brace
		`{"x":1,"_aws"`: true,
		`{"_aw"`:        false, // one character short of the key
		`{}`:            false,
		`{`:             false,
		`"_aws"`:        false, // not an object
		`{"a":"_aws" `:  true,  // a gate, not a parser: it only looks for the text
		`{"a":"b"`:      false,
	} {
		if got := Looks([]byte(line)); got != want {
			t.Errorf("Looks(%q) = %v, want %v", line, got, want)
		}
	}
}
