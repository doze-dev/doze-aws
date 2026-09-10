package modelcheck

import (
	"reflect"
	"testing"
)

// TestFromQueryRebuildsTheNesting pins the translation from the Query
// protocol's flattened keys to the shape constraint paths describe. Getting
// this wrong does not fail loudly — it produces a tree the walker finds nothing
// in, so every constraint on the service passes vacuously.
func TestFromQueryRebuildsTheNesting(t *testing.T) {
	got := FromQuery(map[string][]string{
		"Action":                                {"AssumeRole"},
		"RoleArn":                               {"arn:aws:iam::000000000000:role/r"},
		"Tags.member.1.Key":                     {"env"},
		"Tags.member.1.Value":                   {"dev"},
		"PolicyArns.member.1.arn":               {"arn:aws:iam::aws:policy/ReadOnly"},
		"TransitiveTagKeys.member.1":            {"env"},
		"TaskPolicyArn.arn":                     {"arn:aws:iam::aws:policy/Admin"},
		"ProvidedContexts.member.1.ProviderArn": {"arn:aws:iam::aws:contextProvider/X"},
	})
	want := map[string]any{
		"Action":  "AssumeRole",
		"RoleArn": "arn:aws:iam::000000000000:role/r",
		"Tags": []any{map[string]any{
			"Key": "env", "Value": "dev",
		}},
		"PolicyArns":        []any{map[string]any{"arn": "arn:aws:iam::aws:policy/ReadOnly"}},
		"TransitiveTagKeys": []any{"env"},
		"TaskPolicyArn":     map[string]any{"arn": "arn:aws:iam::aws:policy/Admin"},
		"ProvidedContexts": []any{map[string]any{
			"ProviderArn": "arn:aws:iam::aws:contextProvider/X",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FromQuery mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// A list of {Name, Value} structures is a LIST, and a map spelled as pairs is
// a MAP, and the only thing that tells them apart is the marker word.
//
// AWS flattens list<Struct{Name,Value}> exactly like map<String,String>, so
// the two arrive here identical but for `.member.` versus `.entry.`.
// Collapsing on shape alone turned CloudWatch's Dimensions — a genuine list —
// into a map, and every constraint written `Dimensions[].Name` then resolved
// to no sites and passed vacuously.
//
// The existing case above missed this only by luck: STS spells its tag member
// `Key`, and the collapse looks for `Name` or lowercase `key`.
func TestFromQueryKeepsMemberListsAndCollapsesEntryMaps(t *testing.T) {
	got := FromQuery(map[string][]string{
		// CloudWatch: a list of dimensions, Name/Value shaped.
		"MetricData.member.1.MetricName":                {"Hits"},
		"MetricData.member.1.Dimensions.member.1.Name":  {"FunctionName"},
		"MetricData.member.1.Dimensions.member.1.Value": {"checkout"},
		// SNS: a genuine map, spelled as entries.
		"Attributes.entry.1.Name":  {"DisplayName"},
		"Attributes.entry.1.Value": {"shop"},
	})
	want := map[string]any{
		"MetricData": []any{map[string]any{
			"MetricName": "Hits",
			"Dimensions": []any{map[string]any{
				"Name": "FunctionName", "Value": "checkout",
			}},
		}},
		"Attributes": map[string]any{"DisplayName": "shop"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FromQuery mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// The sites a constraint path resolves to are what the collapse actually
// affects, so assert on those too: a path written with list syntax must find
// the value, which is the thing that silently stopped happening.
// Both members are sent on purpose: entriesToMap only collapses elements with
// exactly two keys, so a Name-only fixture would survive the collapse and this
// test would pass whether or not the fix is in place.
func TestNestedMemberListResolvesListPaths(t *testing.T) {
	raw := FromQuery(map[string][]string{
		"MetricData.member.1.Dimensions.member.1.Name":  {"FunctionName"},
		"MetricData.member.1.Dimensions.member.1.Value": {"checkout"},
	})
	got := sites(raw, "MetricData[].Dimensions[].Name")
	if len(got) != 1 {
		t.Fatalf("MetricData[].Dimensions[].Name resolved to %d sites, want 1 — "+
			"a constraint on it would pass vacuously", len(got))
	}
	if got[0].val != "FunctionName" {
		t.Errorf("site value = %v", got[0].val)
	}
}

// TestRangeReadsANumericString covers the reason the above is useful: a Query
// value is a string, so a @range constraint would otherwise never be checked.
func TestRangeReadsANumericString(t *testing.T) {
	table := []Constraint{{Path: "DurationSeconds", Kind: KindRange, Min: 900, Max: 43200}}

	if err := ValidateMap(map[string]any{"DurationSeconds": "800"}, table); err == nil {
		t.Error("800 was accepted for a 900..43200 range")
	}
	if err := ValidateMap(map[string]any{"DurationSeconds": "3600"}, table); err != nil {
		t.Errorf("3600 was refused for a 900..43200 range: %v", err)
	}
	// A non-numeric string is not this check's business: the model says number,
	// the protocol layer says malformed, and inventing a range error would name
	// the wrong problem.
	if err := ValidateMap(map[string]any{"DurationSeconds": "soon"}, table); err != nil {
		t.Errorf("a non-numeric value produced a range error: %v", err)
	}
}

// TestFromQueryCollapsesEntryMaps covers the Query protocol's spelling of a
// map: a numbered list of Name/Value pairs. The model calls the member a map
// (MessageAttributes{}.DataType), so if this rebuilt a list instead, the walker
// would look for a map, find a list, and pass every constraint underneath
// without checking one of them.
func TestFromQueryCollapsesEntryMaps(t *testing.T) {
	got := FromQuery(map[string][]string{
		"Message":                                     {"hello"},
		"MessageAttributes.entry.1.Name":              {"kind"},
		"MessageAttributes.entry.1.Value.DataType":    {"String"},
		"MessageAttributes.entry.1.Value.StringValue": {"order"},
	})
	attrs, ok := got["MessageAttributes"].(map[string]any)
	if !ok {
		t.Fatalf("MessageAttributes is %T, want a map: %#v", got["MessageAttributes"], got)
	}
	kind, ok := attrs["kind"].(map[string]any)
	if !ok {
		t.Fatalf("the entry did not key on its Name: %#v", attrs)
	}
	if kind["DataType"] != "String" {
		t.Errorf("DataType = %v, want String", kind["DataType"])
	}

	// And the constraint the model states actually reaches it.
	table := []Constraint{{Path: "MessageAttributes{}.DataType", Kind: KindEnum,
		Enum: []string{"String", "Number", "Binary"}}}
	if err := ValidateMapAs(got, table, CodeQuery); err != nil {
		t.Errorf("a valid DataType was refused: %v", err)
	}
	bad := FromQuery(map[string][]string{
		"MessageAttributes.entry.1.Name":           {"kind"},
		"MessageAttributes.entry.1.Value.DataType": {"NotAType"},
	})
	if err := ValidateMapAs(bad, table, CodeQuery); err == nil {
		t.Error("an invalid DataType was accepted — the map was not reachable")
	}
}

// TestKeyValueEntriesCollapseToo covers the other spelling in use.
func TestKeyValueEntriesCollapseToo(t *testing.T) {
	got := FromQuery(map[string][]string{
		"Attributes.entry.1.key":   {"DisplayName"},
		"Attributes.entry.1.value": {"orders"},
	})
	attrs, ok := got["Attributes"].(map[string]any)
	if !ok || attrs["DisplayName"] != "orders" {
		t.Errorf("key/value entries did not collapse: %#v", got["Attributes"])
	}
}
