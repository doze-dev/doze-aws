//go:build unix

package lightness

import "syscall"

// rusage reads this process's own CPU time and peak resident size.
//
// Per-process, which is the property that makes CPU gateable at all: a busy
// shared CI runner cannot inflate our rusage, only our own work can. Wall-clock
// on the same runner varies by tens of percent, which is why this repo already
// refuses to gate benchmarks.
func rusage() (cpuMicros, maxRSS int64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0
	}
	cpu := int64(ru.Utime.Sec)*1e6 + int64(ru.Utime.Usec) +
		int64(ru.Stime.Sec)*1e6 + int64(ru.Stime.Usec)
	return cpu, maxRSSBytes(int64(ru.Maxrss))
}
