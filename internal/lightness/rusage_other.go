//go:build !unix

package lightness

// rusage has no portable equivalent on non-unix platforms. Zeroes rather than
// a build failure: doze-aws ships a Windows binary, so this package has to
// compile there even though the budget is only ever measured on a developer
// machine or a Linux runner. The footprint test skips when it reads zeroes.
func rusage() (cpuMicros, maxRSS int64) { return 0, 0 }

func maxRSSBytes(n int64) int64 { return n }
