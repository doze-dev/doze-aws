package asl

import "testing"

func TestParsePathAcceptsEverySpellingASLAllows(t *testing.T) {
	for _, tc := range []struct {
		in   string
		root Root
		segs []Segment
	}{
		{"$", RootInput, nil},
		{"$$", RootContext, nil},
		{"$.a", RootInput, []Segment{{Key: "a"}}},
		{"$.a.b", RootInput, []Segment{{Key: "a"}, {Key: "b"}}},
		{"$['a']", RootInput, []Segment{{Key: "a"}}},
		{`$["a"]["b"]`, RootInput, []Segment{{Key: "a"}, {Key: "b"}}},
		{"$.a[0]", RootInput, []Segment{{Key: "a"}, {Index: 0, IsIndex: true}}},
		{"$.a[12].b", RootInput, []Segment{{Key: "a"}, {Index: 12, IsIndex: true}, {Key: "b"}}},
		{"$$.Execution.Name", RootContext, []Segment{{Key: "Execution"}, {Key: "Name"}}},
		// A dotted name may contain characters a Go identifier may not; the
		// bracket form exists for the rest.
		{"$.a-b", RootInput, []Segment{{Key: "a-b"}}},
		{"$['a.b']", RootInput, []Segment{{Key: "a.b"}}},
	} {
		p, err := ParsePath(tc.in)
		if err != nil {
			t.Errorf("ParsePath(%q) = %v", tc.in, err)
			continue
		}
		if p.Root != tc.root {
			t.Errorf("ParsePath(%q).Root = %v, want %v", tc.in, p.Root, tc.root)
		}
		if len(p.Segments) != len(tc.segs) {
			t.Errorf("ParsePath(%q) = %d segments, want %d: %+v", tc.in, len(p.Segments), len(tc.segs), p.Segments)
			continue
		}
		for i, want := range tc.segs {
			if p.Segments[i] != want {
				t.Errorf("ParsePath(%q) segment %d = %+v, want %+v", tc.in, i, p.Segments[i], want)
			}
		}
	}
}

// TestParsePathRefusesMultiNodeSelectors is the point of the parser. A
// reference path must name exactly one node, so every JSONPath construct that
// selects a set has to be refused at create time — a ResultPath holding a
// wildcard has no meaning to fall back on at runtime, and accepting it here
// means passing locally and failing on deploy.
func TestParsePathRefusesMultiNodeSelectors(t *testing.T) {
	for _, in := range []string{
		"",
		"a.b",         // no root
		"$.",          // empty field
		"$.a[",        // unclosed
		"$.a[]",       // empty subscript
		"$.*",         // wildcard
		"$.a[*]",      // wildcard index
		"$..a",        // recursive descent
		"$.a[0:2]",    // slice
		"$.a[-1]",     // negative index
		"$.a[?(@.b)]", // filter
		"$.a[0,1]",    // union
		"$.a[b]",      // unquoted, non-numeric subscript
	} {
		if _, err := ParsePath(in); err == nil {
			t.Errorf("ParsePath(%q) was accepted; it selects zero or many nodes", in)
		}
	}
}

// TestReferencePathRejectsTheContextObject: $$ is readable but not writable, so
// it is legal in InputPath and illegal in ResultPath.
func TestReferencePathRejectsTheContextObject(t *testing.T) {
	if err := ValidatePath("$$.Execution.Id"); err != nil {
		t.Errorf("$$ should be readable as a path: %v", err)
	}
	if err := ValidateReferencePath("$$.Execution.Id"); err == nil {
		t.Error("$$ was accepted as a reference path, but the context object is read-only")
	}
	if err := ValidateReferencePath("$.result"); err != nil {
		t.Errorf("$.result should be a valid reference path: %v", err)
	}
}

// TestParsePathErrorsQuoteTheOffender — a validation message that says
// "invalid path" sends someone hunting through a hundred-state machine.
func TestParsePathErrorsQuoteTheOffender(t *testing.T) {
	_, err := ParsePath("$.orders[*].id")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"wildcard", "$.orders[*].id"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
