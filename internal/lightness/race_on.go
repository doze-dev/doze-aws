//go:build race

package lightness

// RaceEnabled reports whether this binary was built with the race detector.
//
// It matters because the detector inflates heap and resident memory several
// times over — it shadows every memory access — so a footprint ceiling measured
// under it is measuring the detector, not doze-aws. `task check:full` and CI's
// main step both run with -race, so without this the budget would fail on every
// push for a reason that has nothing to do with the code.
const RaceEnabled = true
