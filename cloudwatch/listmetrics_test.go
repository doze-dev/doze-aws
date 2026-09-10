package cloudwatch_test

// D1b's exit criterion: the RESPONSE side, three ways.
//
// PutMetricData proved decoding — it returns no body at all. ListMetrics is
// the mirror: no required input, and a result that is a list of structures
// each carrying a nested list of dimensions. If one result struct really does
// serve `json`, `xml` and CBOR, this is where it shows.

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/rpcv2cbor"
)

// seed publishes two metrics that share a name and differ only by dimensions —
// the case Moto gets wrong by summing on name alone.
func seed(t *testing.T, base string) {
	t.Helper()
	for _, stage := range []string{"prod", "staging"} {
		body := `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts",` +
			`"Dimensions":[{"Name":"Stage","Value":"` + stage + `"}],"Value":1}]}`
		if code, _, out := do(t, jsonRequest(t, base, "PutMetricData", body)); code != 200 {
			t.Fatalf("seeding %s: status %d: %s", stage, code, out)
		}
	}
	// A second namespace, so a filtered listing has something to exclude.
	body := `{"Namespace":"Other","MetricData":[{"MetricName":"Checkouts","Value":1}]}`
	if code, _, out := do(t, jsonRequest(t, base, "PutMetricData", body)); code != 200 {
		t.Fatalf("seeding Other: status %d: %s", code, out)
	}
}

func TestListMetricsOnAllThreeWires(t *testing.T) {
	ts := server(t)
	seed(t, ts.URL)

	t.Run("awsJson1_0", func(t *testing.T) {
		code, _, body := do(t, jsonRequest(t, ts.URL, "ListMetrics", `{"Namespace":"Shop"}`))
		if code != 200 {
			t.Fatalf("status %d: %s", code, body)
		}
		var out struct {
			Metrics []struct {
				Namespace  string
				MetricName string
				Dimensions []struct{ Name, Value string }
			}
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		if len(out.Metrics) != 2 {
			t.Fatalf("want 2 metrics in Shop, got %d: %s", len(out.Metrics), body)
		}
		for _, m := range out.Metrics {
			if m.Namespace != "Shop" || m.MetricName != "Checkouts" {
				t.Errorf("unexpected metric %+v", m)
			}
			if len(m.Dimensions) != 1 || m.Dimensions[0].Name != "Stage" {
				t.Errorf("dimensions did not survive: %+v", m.Dimensions)
			}
		}
	})

	t.Run("rpcv2Cbor", func(t *testing.T) {
		// An empty CBOR map: ListMetrics has no required members.
		r := rawCBOR(t, ts.URL, "ListMetrics", []byte{0xa0})
		code, _, body := do(t, r)
		if code != 200 {
			t.Fatalf("status %d: %x", code, body)
		}
		out, err := rpcv2cbor.DecodeMap(body)
		if err != nil {
			t.Fatalf("the response is not decodable CBOR: %v", err)
		}
		metrics, ok := out["Metrics"].([]any)
		if !ok {
			t.Fatalf("Metrics is %T: %#v", out["Metrics"], out)
		}
		if len(metrics) != 3 {
			t.Fatalf("want all 3 metrics unfiltered, got %d", len(metrics))
		}
		// The nested list has to have survived the encode as a list.
		for _, m := range metrics {
			mm := m.(map[string]any)
			if mm["Namespace"] == "Shop" {
				if _, ok := mm["Dimensions"].([]any); !ok {
					t.Errorf("Dimensions is %T, want a list: %#v", mm["Dimensions"], mm)
				}
			}
		}
	})

	t.Run("awsQuery", func(t *testing.T) {
		code, _, body := do(t, queryRequest(t, ts.URL,
			"Action=ListMetrics&Version=2010-08-01&Namespace=Shop"))
		if code != 200 {
			t.Fatalf("status %d: %s", code, body)
		}
		// The Query envelope wraps every list element in <member>, which is
		// the one structural difference from the other two wires.
		var out struct {
			Result struct {
				Metrics []struct {
					Namespace  string
					MetricName string
					Dimensions []struct {
						Name  string
						Value string
					} `xml:"Dimensions>member"`
				} `xml:"Metrics>member"`
			} `xml:"ListMetricsResult"`
		}
		if err := xml.Unmarshal(body, &out); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		if len(out.Result.Metrics) != 2 {
			t.Fatalf("want 2 metrics in Shop, got %d: %s", len(out.Result.Metrics), body)
		}
		m := out.Result.Metrics[0]
		if m.Namespace != "Shop" || m.MetricName != "Checkouts" {
			t.Errorf("unexpected metric %+v", m)
		}
		if len(m.Dimensions) != 1 || m.Dimensions[0].Name != "Stage" {
			t.Errorf("nested dimensions did not survive the XML envelope: %+v", m.Dimensions)
		}
		if !strings.Contains(string(body), "<member>") {
			t.Errorf("the Query envelope has no <member> wrapping: %s", body)
		}
	})
}

// Two metrics sharing a name and differing only by dimensions are two
// metrics. This is the bug Moto has, so it is worth an explicit test rather
// than an incidental one.
func TestDimensionsArePartOfMetricIdentity(t *testing.T) {
	ts := server(t)
	seed(t, ts.URL)

	code, _, body := do(t, jsonRequest(t, ts.URL, "ListMetrics",
		`{"Namespace":"Shop","MetricName":"Checkouts"}`))
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var out struct {
		Metrics []struct {
			Dimensions []struct{ Name, Value string }
		}
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Metrics) != 2 {
		t.Fatalf("one name and two dimension sets are two metrics, got %d: %s",
			len(out.Metrics), body)
	}
	seen := map[string]bool{}
	for _, m := range out.Metrics {
		if len(m.Dimensions) != 1 {
			t.Fatalf("want one dimension per metric, got %+v", m.Dimensions)
		}
		seen[m.Dimensions[0].Value] = true
	}
	if !seen["prod"] || !seen["staging"] {
		t.Errorf("both dimension sets should be listed, saw %v", seen)
	}

	// Republishing an existing series does not create a second one.
	body2 := `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts",` +
		`"Dimensions":[{"Name":"Stage","Value":"prod"}],"Value":99}]}`
	if code, _, out := do(t, jsonRequest(t, ts.URL, "PutMetricData", body2)); code != 200 {
		t.Fatalf("status %d: %s", code, out)
	}
	code, _, body = do(t, jsonRequest(t, ts.URL, "ListMetrics",
		`{"Namespace":"Shop","MetricName":"Checkouts"}`))
	if code != 200 {
		t.Fatal(code)
	}
	out.Metrics = nil
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Metrics) != 2 {
		t.Errorf("republishing created a duplicate series: %d", len(out.Metrics))
	}
}

// Dimension order is not part of the identity: AWS treats {a,b} and {b,a} as
// one metric, and so must the key.
func TestDimensionOrderDoesNotForkTheSeries(t *testing.T) {
	ts := server(t)
	for _, dims := range []string{
		`[{"Name":"A","Value":"1"},{"Name":"B","Value":"2"}]`,
		`[{"Name":"B","Value":"2"},{"Name":"A","Value":"1"}]`,
	} {
		body := `{"Namespace":"Ord","MetricData":[{"MetricName":"M","Dimensions":` + dims + `,"Value":1}]}`
		if code, _, out := do(t, jsonRequest(t, ts.URL, "PutMetricData", body)); code != 200 {
			t.Fatalf("status %d: %s", code, out)
		}
	}
	code, _, body := do(t, jsonRequest(t, ts.URL, "ListMetrics", `{"Namespace":"Ord"}`))
	if code != 200 {
		t.Fatal(code)
	}
	var out struct{ Metrics []struct{ MetricName string } }
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Metrics) != 1 {
		t.Errorf("dimension order forked the series: %d metrics: %s", len(out.Metrics), body)
	}
}

// A filter narrows, and a dimension filter with no value matches any value.
func TestListMetricsFilters(t *testing.T) {
	ts := server(t)
	seed(t, ts.URL)

	count := func(input string) int {
		t.Helper()
		code, _, body := do(t, jsonRequest(t, ts.URL, "ListMetrics", input))
		if code != 200 {
			t.Fatalf("status %d: %s", code, body)
		}
		var out struct{ Metrics []json.RawMessage }
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return len(out.Metrics)
	}

	if n := count(`{}`); n != 3 {
		t.Errorf("unfiltered = %d, want 3", n)
	}
	if n := count(`{"Namespace":"Other"}`); n != 1 {
		t.Errorf("by namespace = %d, want 1", n)
	}
	if n := count(`{"Dimensions":[{"Name":"Stage"}]}`); n != 2 {
		t.Errorf("dimension name only = %d, want 2", n)
	}
	if n := count(`{"Dimensions":[{"Name":"Stage","Value":"prod"}]}`); n != 1 {
		t.Errorf("dimension name and value = %d, want 1", n)
	}
	if n := count(`{"Namespace":"Nope"}`); n != 0 {
		t.Errorf("no matches = %d, want 0", n)
	}
}

// An empty catalogue answers with an empty list, not a null — a client paging
// through metrics should see "none", not "missing".
func TestListMetricsEmptyIsAList(t *testing.T) {
	ts := server(t)
	code, _, body := do(t, jsonRequest(t, ts.URL, "ListMetrics", `{}`))
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	if !strings.Contains(string(body), `"Metrics":[]`) {
		t.Errorf("want an empty list, got: %s", body)
	}
}
