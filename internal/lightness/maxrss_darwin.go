//go:build darwin

package lightness

// maxRSSBytes normalises Rusage.Maxrss, which is one of the few fields whose
// UNIT differs by platform: darwin reports bytes, Linux reports kibibytes.
// Reading it raw is a 1024x error that looks plausible either way, which is
// exactly the kind of bug a budget would then enshrine.
func maxRSSBytes(n int64) int64 { return n }
