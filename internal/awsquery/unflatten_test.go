package awsquery

import (
	"net/url"
	"reflect"
	"testing"
)

// The property this exists for, and the one modelcheck.FromQuery does not
// have: every element of a list survives. Losing elements here is silent —
// the request still succeeds, with less data than the caller sent.
func TestUnflattenKeepsEveryListElement(t *testing.T) {
	got := Unflatten(url.Values{
		"Namespace":                                     {"Shop"},
		"MetricData.member.1.MetricName":                {"Checkouts"},
		"MetricData.member.1.Dimensions.member.1.Name":  {"Stage"},
		"MetricData.member.1.Dimensions.member.1.Value": {"prod"},
		"MetricData.member.1.Dimensions.member.2.Name":  {"Region"},
		"MetricData.member.1.Dimensions.member.2.Value": {"eu"},
		"MetricData.member.2.MetricName":                {"Refunds"},
	})
	want := map[string]any{
		"Namespace": "Shop",
		"MetricData": []any{
			map[string]any{
				"MetricName": "Checkouts",
				"Dimensions": []any{
					map[string]any{"Name": "Stage", "Value": "prod"},
					map[string]any{"Name": "Region", "Value": "eu"},
				},
			},
			map[string]any{"MetricName": "Refunds"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// Indices order the result, not the order the form happened to present them
// in — a map iteration over url.Values has no order at all.
func TestUnflattenOrdersByIndex(t *testing.T) {
	for range 20 {
		got := Unflatten(url.Values{
			"Keys.member.3": {"c"},
			"Keys.member.1": {"a"},
			"Keys.member.2": {"b"},
		})
		want := map[string]any{"Keys": []any{"a", "b", "c"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mismatch\n got: %#v\nwant: %#v", got, want)
		}
	}
}

// A list of {Name,Value} structures and a map spelled as pairs are flattened
// identically but for the marker word, so the marker is what decides.
func TestUnflattenDistinguishesMembersFromEntries(t *testing.T) {
	got := Unflatten(url.Values{
		"Dimensions.member.1.Name":  {"Stage"},
		"Dimensions.member.1.Value": {"prod"},
		"Attributes.entry.1.Name":   {"DisplayName"},
		"Attributes.entry.1.Value":  {"shop"},
		"Tags.entry.1.key":          {"env"},
		"Tags.entry.1.value":        {"dev"},
	})
	want := map[string]any{
		"Dimensions": []any{map[string]any{"Name": "Stage", "Value": "prod"}},
		"Attributes": map[string]any{"DisplayName": "shop"},
		"Tags":       map[string]any{"env": "dev"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestUnflattenShapes(t *testing.T) {
	cases := []struct {
		name string
		in   url.Values
		want map[string]any
	}{
		{"empty", url.Values{}, map[string]any{}},
		{"flat members", url.Values{"Action": {"ListMetrics"}, "Namespace": {"Shop"}},
			map[string]any{"Action": "ListMetrics", "Namespace": "Shop"}},
		{"nested structure", url.Values{"Config.Level": {"INFO"}},
			map[string]any{"Config": map[string]any{"Level": "INFO"}}},
		{"scalar list", url.Values{"AlarmNames.member.1": {"a"}, "AlarmNames.member.2": {"b"}},
			map[string]any{"AlarmNames": []any{"a", "b"}}},
		{"memberless list", url.Values{"Keys.1": {"a"}, "Keys.2": {"b"}},
			map[string]any{"Keys": []any{"a", "b"}}},
		{"list of structures under a structure",
			url.Values{"Alarm.Dimensions.member.1.Name": {"Stage"}},
			map[string]any{"Alarm": map[string]any{
				"Dimensions": []any{map[string]any{"Name": "Stage"}}}}},
		// Index 0 is not a Query index — AWS numbers from 1 — so it stays a
		// plain member name rather than silently becoming a list.
		{"zero index is not an index", url.Values{"Weird.0": {"x"}},
			map[string]any{"Weird": map[string]any{"0": "x"}}},
	}
	for _, c := range cases {
		if got := Unflatten(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got: %#v\nwant: %#v", c.name, got, c.want)
		}
	}
}

// Sparse and out-of-order indices are what a hand-built request produces.
// They must not panic or drop the elements that are there.
func TestUnflattenToleratesSparseIndices(t *testing.T) {
	got := Unflatten(url.Values{
		"Keys.member.1":  {"a"},
		"Keys.member.9":  {"i"},
		"Keys.member.40": {"z"},
	})
	want := map[string]any{"Keys": []any{"a", "i", "z"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch\n got: %#v\nwant: %#v", got, want)
	}
}
