package logs

// The two pieces of metric-filter logic that are decisions rather than
// plumbing: what a `$.field` reference resolves to, and the existence
// wildcard the filter language needs to express "every line carrying this".

import "testing"

func TestMetricValueOfResolvesLiteralsAndReferences(t *testing.T) {
	half := 0.5
	for _, tc := range []struct {
		name    string
		t       MetricTransformation
		message string
		want    float64
		ok      bool
	}{
		{"a literal counts occurrences", MetricTransformation{Value: "1"}, "anything", 1, true},
		{"a literal need not be one", MetricTransformation{Value: "2.5"}, "anything", 2.5, true},
		{"a literal that is not a number publishes nothing",
			MetricTransformation{Value: "lots"}, "anything", 0, false},
		{"a reference reads the field",
			MetricTransformation{Value: "$.latency"}, `{"latency": 42}`, 42, true},
		{"a reference reaches into nested objects",
			MetricTransformation{Value: "$.http.status"}, `{"http": {"status": 503}}`, 503, true},
		{"a number printed as a string still counts",
			MetricTransformation{Value: "$.latency"}, `{"latency": "42"}`, 42, true},
		{"a bool is one or zero",
			MetricTransformation{Value: "$.cached"}, `{"cached": true}`, 1, true},
		// The distinction defaultValue exists for: an unresolved reference
		// publishes nothing, which is not the same as publishing zero.
		{"an unresolved reference with no default publishes nothing",
			MetricTransformation{Value: "$.latency"}, `{"other": 1}`, 0, false},
		{"an unresolved reference falls back to the default",
			MetricTransformation{Value: "$.latency", Default: &half}, `{"other": 1}`, 0.5, true},
		{"a non-JSON line cannot satisfy a reference",
			MetricTransformation{Value: "$.latency"}, "ERROR boom", 0, false},
		{"a non-JSON line still takes the default",
			MetricTransformation{Value: "$.latency", Default: &half}, "ERROR boom", 0.5, true},
		{"an object at the leaf is not a number",
			MetricTransformation{Value: "$.latency"}, `{"latency": {"p50": 1}}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := metricValueOf(tc.t, tc.message)
			if ok != tc.ok || got != tc.want {
				t.Errorf("metricValueOf = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestJSONPatternWildcardTestsExistence(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		message string
		want    bool
	}{
		{"{$.latency = *}", `{"latency": 42}`, true},
		{"{$.latency = *}", `{"latency": 0}`, true},
		// Present but null still counts as present, matching AWS.
		{"{$.latency = *}", `{"latency": null}`, true},
		{"{$.latency = *}", `{"other": 1}`, false},
		{"{$.latency != *}", `{"other": 1}`, true},
		{"{$.latency != *}", `{"latency": 42}`, false},
		// The wildcard composes with the rest of the language.
		{"{$.latency = * && $.level = \"ERROR\"}", `{"latency": 1, "level": "ERROR"}`, true},
		{"{$.latency = * && $.level = \"ERROR\"}", `{"latency": 1, "level": "INFO"}`, false},
		// A quoted star is still the string glob it always was, and so only
		// matches string values.
		{`{$.level = "*"}`, `{"level": "ERROR"}`, true},
		{`{$.level = "*"}`, `{"level": 42}`, false},
	} {
		t.Run(tc.pattern+" on "+tc.message, func(t *testing.T) {
			match, err := compile(tc.pattern)
			if err != nil {
				t.Fatalf("compile(%q): %v", tc.pattern, err)
			}
			if got := match(tc.message); got != tc.want {
				t.Errorf("match = %v, want %v", got, tc.want)
			}
		})
	}
}
