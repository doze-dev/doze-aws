package modelcheck

// The walker resolves each constraint's path into a caller-owned buffer that is
// reused across the whole table, so a table of 464 constraints costs one
// allocation rather than 464. That is only sound while every constraint sees
// exactly its own sites.

import "testing"

// TestSitesDoNotLeakBetweenConstraints is the guard on the shared buffer.
//
// If sites forgets to truncate it, constraint N is checked against the sites of
// constraints 1..N-1 as well, and a perfectly valid body is refused for a
// member the rule does not apply to. The failure is silent in the common case —
// a leaked site is usually absent, and an absent site passes everything — so
// this fixture is built so it cannot be: both members are present, and each
// one's value violates the other one's rule.
func TestSitesDoNotLeakBetweenConstraints(t *testing.T) {
	table := []Constraint{
		{Path: "Namespace", Kind: KindLength, Min: 1, Max: 255},
		{Path: "Unit", Kind: KindEnum, Enum: []string{"Count", "Seconds"}},
	}
	body := map[string]any{
		"Namespace": "Shop/Checkout", // not a Unit
		"Unit":      "Count",         // fine everywhere
	}
	if err := ValidateMap(body, table); err != nil {
		t.Fatalf("a valid body was refused, which means a site outlived its "+
			"constraint: %v", err)
	}
}

// TestSitesDoNotLeakAcrossMultipleResolutions is the same guard for a path that
// resolves to several sites, where the leak is larger and the ordering differs:
// the list's three elements would still be in the buffer when the scalar
// constraint after it is checked.
func TestSitesDoNotLeakAcrossMultipleResolutions(t *testing.T) {
	table := []Constraint{
		{Path: "AttributesToGet[]", Kind: KindLength, Min: 1, Max: 255},
		{Path: "Select", Kind: KindEnum, Enum: []string{"ALL_ATTRIBUTES", "COUNT"}},
	}
	body := map[string]any{
		// None of these is a Select value, so a leak is refused rather than
		// quietly tolerated the way a numeric check tolerates a string.
		"AttributesToGet": []any{"id", "name", "total"},
		"Select":          "ALL_ATTRIBUTES",
	}
	if err := ValidateMap(body, table); err != nil {
		t.Fatalf("a valid body was refused, which means list sites outlived "+
			"their constraint: %v", err)
	}
}

// TestAbsentStructureYieldsNothing pins the other half of the buffer contract:
// a path whose enclosing structure was not sent must contribute no sites at
// all. Returning the buffer unchanged is what makes that true, and returning a
// stale one would make a constraint on an optional structure fire against
// whatever was checked before it.
func TestAbsentStructureYieldsNothing(t *testing.T) {
	var buf []site
	buf = sites(map[string]any{"TableName": "orders"}, "TableName", buf)
	if len(buf) != 1 {
		t.Fatalf("TableName resolved to %d sites, want 1", len(buf))
	}
	buf = sites(map[string]any{"TableName": "orders"}, "Projection.ProjectionType", buf)
	if len(buf) != 0 {
		t.Fatalf("a path into an absent structure resolved to %d sites, want 0: %+v",
			len(buf), buf)
	}
}

// TestNestedScalarKeepsItsFullDisplayPath pins the spelling a refusal carries
// for a member inside a structure. The walker has a fast path for the flat case
// that skips building the path piece by piece, and getting its prefix handling
// wrong would name 'projectionType' where AWS names
// 'projection.projectionType' — a refusal for the right reason under the wrong
// name, which the parity suites read as a mismatch.
func TestNestedScalarKeepsItsFullDisplayPath(t *testing.T) {
	raw := map[string]any{
		"Projection": map[string]any{"ProjectionType": "SOMETHING"},
	}
	got := sites(raw, "Projection.ProjectionType", nil)
	if len(got) != 1 {
		t.Fatalf("resolved to %d sites, want 1", len(got))
	}
	if got[0].disp != "projection.projectionType" {
		t.Errorf("display path = %q, want %q", got[0].disp, "projection.projectionType")
	}
}
