package sns

import "testing"

// The decomposition is only sound because SNS ANDs attributes and ORs the
// conditions within one. If that ever stops holding, a rejection reason starts
// naming a key that did not actually reject the message — which is worse than
// naming none, because it sends someone to edit the wrong line.
func TestRejectionReasonsNameTheKeyThatRefused(t *testing.T) {
	attrs := map[string]Attr{
		"eventType": {DataType: "String", StringValue: "order.created"},
		"price":     {DataType: "Number", StringValue: "250"},
	}
	for _, c := range []struct {
		name, policy string
		wantMatch    bool
		wantKeys     []string
	}{
		{"empty policy matches", "", true, nil},
		{"single key satisfied", `{"eventType":["order.created"]}`, true, nil},
		{"single key refuses", `{"eventType":["order.shipped"]}`, false, []string{"eventType"}},
		{"numeric satisfied", `{"price":[{"numeric":[">",100]}]}`, true, nil},
		{"numeric refuses", `{"price":[{"numeric":["<",100]}]}`, false, []string{"price"}},
		{"absent key refuses", `{"region":["eu-west-1"]}`, false, []string{"region"}},
		// The one that proves it: two keys, one satisfied and one not. Only the
		// failing one may be reported.
		{"only the failing half", `{"eventType":["order.created"],"region":["eu-west-1"]}`, false, []string{"region"}},
	} {
		gotMatch := matchFilter(c.policy, attrs)
		if gotMatch != c.wantMatch {
			t.Errorf("%s: matchFilter = %v, want %v", c.name, gotMatch, c.wantMatch)
			continue
		}
		if gotMatch {
			continue
		}
		reasons := rejectionReasons(c.policy, attrs)
		var keys []string
		for _, r := range reasons {
			keys = append(keys, r.Key)
		}
		if len(keys) != len(c.wantKeys) {
			t.Errorf("%s: reasons %v, want %v", c.name, keys, c.wantKeys)
			continue
		}
		for i := range keys {
			if keys[i] != c.wantKeys[i] {
				t.Errorf("%s: reasons %v, want %v", c.name, keys, c.wantKeys)
			}
		}
	}
}

// $or is a disjunction ACROSS keys, so splitting it per key would attribute the
// refusal to one branch when it was the whole set that failed. It is reported
// whole instead.
func TestOrIsReportedWholeNotSplit(t *testing.T) {
	attrs := map[string]Attr{"eventType": {DataType: "String", StringValue: "order.created"}}
	policy := `{"$or":[{"region":["eu-west-1"]},{"tier":["premium"]}]}`
	if matchFilter(policy, attrs) {
		t.Fatal("policy should not match — neither branch is satisfiable with these attributes")
	}
	reasons := rejectionReasons(policy, attrs)
	if len(reasons) != 1 || reasons[0].Key != "$or" {
		t.Fatalf("want a single $or reason, got %+v", reasons)
	}
	if reasons[0].Present {
		t.Error("$or is not a message attribute and must not claim to be present")
	}
}

// The preview must evaluate exactly what delivery evaluates. MatchPolicy — the
// other exported entry point — flattens every attribute to a String, so a
// numeric policy answers differently through it. This is the regression guard
// for reaching for the convenient helper.
func TestPreviewAgreesWithDeliveryOnTypedAttributes(t *testing.T) {
	policy := `{"price":[{"numeric":[">",100]}]}`
	typed := map[string]Attr{"price": {DataType: "Number", StringValue: "250"}}

	if !matchFilter(policy, typed) {
		t.Fatal("delivery would deliver this; the preview predicate says no")
	}
	if MatchPolicy(policy, map[string]string{"price": "250"}) {
		t.Skip("MatchPolicy now preserves types; this guard can be retired")
	}
	// Documented divergence: this is why dozeMatchSubscriptions parses the real
	// MessageAttributes block instead of taking the string map.
}
