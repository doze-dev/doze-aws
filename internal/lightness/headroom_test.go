package lightness

import "testing"

// The bands are arithmetic, and the arithmetic is the whole of what makes a
// gate able to fire: a ceiling that is a factor off is a gate that never fires
// or always does. Each is held to values worked out by hand, not recomputed
// with the formula under test.
func TestTheBandsHaveTheirExactCeilings(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    Headroom
		n    int64
		want int64
	}{
		// Bytes: a quarter more, at least 4096 more, rounded up to a KiB.
		{"bytes 0", Bytes, 0, 4096},
		{"bytes 100 (the floor wins)", Bytes, 100, 5120},
		{"bytes 1 000 000 (a quarter wins)", Bytes, 1_000_000, 1_250_304},
		// Footprint: half again, at least a mebibyte more, rounded up to a KiB.
		{"footprint 0", Footprint, 0, 1_048_576},
		{"footprint 100 (the floor wins)", Footprint, 100, 1_049_600},
		{"footprint 10 000 000 (half again wins)", Footprint, 10_000_000, 15_000_576},
		// Count: half again, at least four more.
		{"count 2 (the floor wins)", Count, 2, 6},
		{"count 100", Count, 100, 150},
		// Noisy: ten times, at least 1000.
		{"noisy 5", Noisy, 5, 1000},
		{"noisy 500", Noisy, 500, 5000},
		// Catastrophe: twenty times, at least five million (of whatever unit the value is in).
		{"catastrophe 1000", Catastrophe, 1000, 5_000_000},
		{"catastrophe 1 000 000", Catastrophe, 1_000_000, 20_000_000},
	} {
		if got := tc.h.Ceiling(tc.n); got != tc.want {
			t.Errorf("%s: ceiling %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestTheHelpersAtTheirEdges(t *testing.T) {
	for n, want := range map[int64]int64{0: 0, 1: 1024, 1023: 1024, 1024: 1024, 1025: 2048, 2048: 2048} {
		if got := roundKiB(n); got != want {
			t.Errorf("roundKiB(%d) = %d, want %d", n, got, want)
		}
	}
	if atLeast(5, 5) != 5 || atLeast(4, 5) != 5 || atLeast(6, 5) != 6 {
		t.Error("atLeast does not return the larger of its two")
	}
}

// A measurement exactly at the ceiling is not over it, and is not an outgrowth:
// only one past it is either.
func TestTheCeilingItselfIsAllowed(t *testing.T) {
	b := Budget{Measured: 90, Ceiling: 100}
	if b.Over(100) || !b.Over(101) {
		t.Errorf("Over(100) %v, Over(101) %v: want false, true", b.Over(100), b.Over(101))
	}
	if (Budget{}).Over(1 << 40) {
		t.Error("a budget with no ceiling set is never over")
	}
	if got := b.Record(100, Footprint); got.Ceiling != 100 || got.Measured != 100 {
		t.Errorf("recording the ceiling itself widened it: %+v", got)
	}
	if got := b.Record(101, Footprint); got.Ceiling == 100 {
		t.Errorf("recording one past the ceiling left it where it was: %+v", got)
	}
	if got := (Budget{}).Record(7, Footprint); got.Ceiling == 0 {
		t.Errorf("a fresh entry got no ceiling: %+v", got)
	}
}
