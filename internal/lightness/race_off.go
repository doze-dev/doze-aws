//go:build !race

package lightness

// RaceEnabled reports whether this binary was built with the race detector.
// See race_on.go for why the footprint budget cares.
const RaceEnabled = false
