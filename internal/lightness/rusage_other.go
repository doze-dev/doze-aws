//go:build !unix

package lightness

// rusage has no portable equivalent off unix. Zeroes rather than a build
// failure, so `GOOS=<anything> go vet ./...` still works even though doze-aws
// only ships linux and darwin binaries and the budget is only ever measured on
// a developer machine or a Linux runner. The footprint test skips when it
// reads zeroes.
func rusage() (cpuMicros, maxRSS int64) { return 0, 0 }

func maxRSSBytes(n int64) int64 { return n }
