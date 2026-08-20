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
