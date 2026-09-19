//go:build unix && !darwin

package lightness

// maxRSSBytes normalises Rusage.Maxrss from kibibytes on Linux and the BSDs.
// See maxrss_darwin.go for why this is not shared.
func maxRSSBytes(n int64) int64 { return n * 1024 }
