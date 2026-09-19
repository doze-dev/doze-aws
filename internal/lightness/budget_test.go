package lightness

// Record decides what a regenerated budget gates on, and it has been wrong in
// a way that is invisible: a ceiling left standing over a measurement that fell
// out from under it still LOOKS like a budget. Both times it took a service
// going back to its old behaviour and nothing failing to notice.

import "testing"

func TestAFreshEntryTakesThePolicysBand(t *testing.T) {
	got := Budget{}.Record(100, Bytes)
	if got.Measured != 100 {
		t.Errorf("Measured is %d, want 100", got.Measured)
	}
	if want := Bytes.Ceiling(100); got.Ceiling != want {
		t.Errorf("Ceiling is %d, want %d", got.Ceiling, want)
	}
}

func TestAMeasurementThatOutgrowsItsCeilingWidensIt(t *testing.T) {
	for name, h := range map[string]Headroom{"exact": Bytes, "observed": Footprint} {
		t.Run(name, func(t *testing.T) {
			b := Budget{Measured: 100, Ceiling: 200}
			got := b.Record(5000, h)
			if want := h.Ceiling(5000); got.Ceiling != want {
				t.Errorf("a measurement of 5000 under a ceiling of 200 gave %d, want %d",
					got.Ceiling, want)
			}
		})
	}
}

// The failure this file exists for. An exact quantity that falls has to take
// its ceiling with it, or the band is left describing a world that is gone.
func TestAnExactCeilingFollowsItsMeasurementDown(t *testing.T) {
	// The real case: a service's untouched data directory when its database
	// stopped being created at boot.
	b := Budget{Measured: 131166, Ceiling: 164864}
	got := b.Record(94, Bytes)

	if got.Ceiling == 164864 {
		t.Fatal("a 94-byte measurement kept its 164,864-byte ceiling.\n" +
			"  That is not a loose band, it is no band: the service would have to go " +
			"back to\n  creating a 131,072-byte database to trip it, which is the " +
			"regression it exists to catch.")
	}
	if want := Bytes.Ceiling(94); got.Ceiling != want {
		t.Errorf("Ceiling is %d, want %d", got.Ceiling, want)
	}
	// And the band still has to be a band: big enough not to fire on itself,
	// small enough that the thing it is watching for breaches it.
	if got.Ceiling < 94 {
		t.Errorf("Ceiling %d is below the measurement it was derived from", got.Ceiling)
	}
	if !got.Over(131166) {
		t.Errorf("a re-derived ceiling of %d does not fire on the 131,166 bytes it "+
			"used to allow", got.Ceiling)
	}
}

// The other half, and it is a deliberate asymmetry rather than an oversight.
func TestAnObservedCeilingDoesNotFollowItsMeasurementDown(t *testing.T) {
	for name, h := range map[string]Headroom{
		"footprint":   Footprint,
		"count":       Count,
		"noisy":       Noisy,
		"catastrophe": Catastrophe,
	} {
		t.Run(name, func(t *testing.T) {
			b := Budget{Measured: 4_000_000, Ceiling: 6_000_000}
			got := b.Record(3_000_000, h)
			if got.Ceiling != 6_000_000 {
				t.Errorf("a quiet run moved the ceiling from 6,000,000 to %d.\n"+
					"  These numbers move with the machine, so re-deriving from one "+
					"reading ratchets the\n  band down to a quiet afternoon and fails "+
					"on an ordinary one. A gate that fires on\n  noise is a gate "+
					"somebody mutes.", got.Ceiling)
			}
			if got.Measured != 3_000_000 {
				t.Errorf("Measured is %d, want 3,000,000 — the recorded value is what "+
					"makes the drift visible in a diff", got.Measured)
			}
		})
	}
}

// Catastrophe's floor is the point of it: twenty times a sub-millisecond boot
// is a ceiling an ordinary run breaches, so the floor has to survive a
// measurement small enough to make the multiple meaningless.
func TestCatastropheKeepsItsFloorWhenTheMeasurementCollapses(t *testing.T) {
	got := Budget{}.Record(2510, Catastrophe)
	if got.Ceiling != 5_000_000 {
		t.Errorf("a 2,510µs boot got a ceiling of %d, want the 5,000,000µs floor", got.Ceiling)
	}
}

func TestOverIgnoresAnUnsetCeiling(t *testing.T) {
	if (Budget{Measured: 10}).Over(1 << 40) {
		t.Error("a budget with no ceiling fired; an unrecorded entry must not gate")
	}
}
