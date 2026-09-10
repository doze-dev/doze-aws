package emf

import (
	"reflect"
	"testing"
)

// The shape AWS's own EMF libraries emit, including the detail most readers
// get wrong: Dimensions is a list of dimension SETS, and each set produces
// its own copy of every metric.
func TestParseDimensionSets(t *testing.T) {
	line := []byte(`{
	  "_aws": {
	    "Timestamp": 1700000000000,
	    "CloudWatchMetrics": [{
	      "Namespace": "Shop",
	      "Dimensions": [["Stage"], []],
	      "Metrics": [{"Name": "Checkouts", "Unit": "Count"}]
	    }]
	  },
	  "Stage": "prod",
	  "Checkouts": 3,
	  "requestId": "abc"
	}`)
	got := Parse(line)
	want := []Metric{
		{Namespace: "Shop", Name: "Checkouts", Unit: "Count", Value: 3,
			Dimensions: map[string]string{"Stage": "prod"}, TimestampMs: 1700000000000},
		{Namespace: "Shop", Name: "Checkouts", Unit: "Count", Value: 3,
			Dimensions: nil, TimestampMs: 1700000000000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []Metric
	}{
		{
			name: "an array of values sums, which is what batching one line means",
			line: `{"_aws":{"CloudWatchMetrics":[{"Namespace":"N","Metrics":[{"Name":"M"}]}]},"M":[1,2,3]}`,
			want: []Metric{{Namespace: "N", Name: "M", Value: 6}},
		},
		{
			name: "a numeric dimension value renders rather than being refused",
			line: `{"_aws":{"CloudWatchMetrics":[{"Namespace":"N","Dimensions":[["Code"]],"Metrics":[{"Name":"M"}]}]},"Code":200,"M":1}`,
			want: []Metric{{Namespace: "N", Name: "M", Value: 1,
				Dimensions: map[string]string{"Code": "200"}}},
		},
		{
			name: "high resolution is carried through",
			line: `{"_aws":{"CloudWatchMetrics":[{"Namespace":"N","Metrics":[{"Name":"M","StorageResolution":1}]}]},"M":1}`,
			want: []Metric{{Namespace: "N", Name: "M", Value: 1, StorageResolution: 1}},
		},
		{
			name: "no Dimensions member is one undimensioned copy, not zero copies",
			line: `{"_aws":{"CloudWatchMetrics":[{"Namespace":"N","Metrics":[{"Name":"M"}]}]},"M":7}`,
			want: []Metric{{Namespace: "N", Name: "M", Value: 7}},
		},
		{
			name: "a metric the body does not carry is dropped, not published as zero",
			line: `{"_aws":{"CloudWatchMetrics":[{"Namespace":"N","Metrics":[{"Name":"Missing"}]}]},"Other":1}`,
			want: nil,
		},
		{
			name: "a dimension the body does not carry drops only that set",
			line: `{"_aws":{"CloudWatchMetrics":[{"Namespace":"N","Dimensions":[["Absent"],[]],"Metrics":[{"Name":"M"}]}]},"M":1}`,
			want: []Metric{{Namespace: "N", Name: "M", Value: 1}},
		},
		{
			name: "a set with no namespace is skipped",
			line: `{"_aws":{"CloudWatchMetrics":[{"Metrics":[{"Name":"M"}]}]},"M":1}`,
			want: nil,
		},
	}
	for _, c := range cases {
		if got := Parse([]byte(c.line)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got: %#v\nwant: %#v", c.name, got, c.want)
		}
	}
}

// This runs over arbitrary function output, so anything that is not EMF has
// to come back as "no metrics" rather than as an error or a panic.
func TestParseIgnoresNonEMF(t *testing.T) {
	for _, line := range []string{
		"",
		"hello",
		"{",
		"{}",
		`{"message":"a normal structured log line"}`,
		`{"_aws":"not an object"}`,
		`{"_aws":{}}`,
		`{"_aws":{"CloudWatchMetrics":"wrong"}}`,
		`[1,2,3]`,
		// Mentions _aws but is not JSON at all: Looks says maybe, Parse says no.
		`this line mentions "_aws" in prose`,
	} {
		if got := Parse([]byte(line)); got != nil {
			t.Errorf("Parse(%q) = %#v, want nothing", line, got)
		}
	}
}

// Looks is the cheap gate run on every line a function prints, so it has to
// be right about the common case: ordinary output is not EMF.
func TestLooks(t *testing.T) {
	if Looks([]byte("plain output")) {
		t.Error("plain output looked like EMF")
	}
	if Looks([]byte(`{"level":"info","msg":"hi"}`)) {
		t.Error("an ordinary JSON log line looked like EMF")
	}
	if !Looks([]byte(`{"_aws":{"CloudWatchMetrics":[]}}`)) {
		t.Error("an EMF line did not look like one")
	}
}
