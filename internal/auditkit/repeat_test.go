package auditkit

import (
	"strings"
	"testing"
)

// The fold and the build are inverses: whatever the generator folds away, the
// harness has to hand back byte for byte, or a max-length case stops proving
// the length it names.
func TestFoldAndBuildRoundTrip(t *testing.T) {
	for _, n := range []int{RepeatThreshold, RepeatThreshold + 1, 2048, 1048577} {
		want := strings.Repeat("a", n)
		folded, r := FoldRepeat(want)
		if r == nil {
			t.Fatalf("a %d-character pad should fold", n)
		}
		if folded != nil {
			t.Errorf("a folded case must not also carry a literal, got %v", folded)
		}
		if got := Materialize(folded, r); got != want {
			t.Errorf("round trip of %d characters produced %d", n, len(got.(string)))
		}
	}
}

func TestFoldLeavesMeaningfulValuesAlone(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"short pad below the threshold", strings.Repeat("a", RepeatThreshold-1)},
		{"long but not one repeated character", strings.Repeat("ab", 4096)},
		{"a pattern violation", "\u0000\u0001"},
		{"a number", 42.0},
		{"a list", []any{"x"}},
		{"nil", nil},
	}
	for _, c := range cases {
		got, r := FoldRepeat(c.value)
		if r != nil {
			t.Errorf("%s: should not fold, got %+v", c.name, r)
		}
		if got == nil && c.value != nil {
			t.Errorf("%s: value was dropped", c.name)
		}
	}
}

// A non-ASCII pad is counted in characters, not bytes, so the length the case
// claims is the length the service is asked to refuse.
func TestFoldCountsCharactersNotBytes(t *testing.T) {
	want := strings.Repeat("é", 1000)
	_, r := FoldRepeat(want)
	if r == nil {
		t.Fatal("a 1000-character pad should fold")
	}
	if r.Times != 1000 {
		t.Errorf("Times = %d, want 1000 characters", r.Times)
	}
	if r.Build() != want {
		t.Error("a multi-byte pad did not round-trip")
	}
}

// Materialize with no Repeat is the identity, which is what every case that
// stores its value literally relies on.
func TestMaterializeWithoutRepeat(t *testing.T) {
	if got := Materialize("literal", nil); got != "literal" {
		t.Errorf("got %v", got)
	}
	if got := Materialize(nil, nil); got != nil {
		t.Errorf("got %v, want nil for an omitted-member case", got)
	}
}
