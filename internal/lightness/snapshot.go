package lightness

// What a running process costs, read from the runtime rather than from a tool.
//
// # Why these fields and not the obvious ones
//
// `ps` reports resident memory, and docs/performance.md already established
// that its number is wrong for this purpose: on macOS RSS counts file-backed
// pages — the binary's own text, system libraries — that are shared and not
// this process's to give back. It reads ~40 MB where `vmmap` puts the dirty
// total at 15 MB. MemStats.Sys minus HeapReleased is what the runtime itself
// has taken and not returned, and it agrees with the dirty figure to within a
// megabyte. That is Retained below, and it is the footprint number worth
// quoting.
//
// # Wakeups, and the field that looks right and is not
//
// A process using no measurable CPU can still wake a core hundreds of times a
// minute, and a core that is woken never reaches its deeper idle states, which
// is what a laptop's battery notices. So wakeups matter more than CPU% for the
// claim this project makes.
//
// Rusage's Nvcsw is the field that looks like the answer. It is not: the Go
// runtime's own sysmon parks and wakes at up to 10ms intervals regardless of
// what the program does, which floors Nvcsw around a thousand per ten-second
// window and swamps doze-aws's own handful. sysmon is not a goroutine — and
// that is exactly why the scheduler's own counter is the right proxy.
// SchedEvents below counts goroutines going runnable, so an idle stack's
// tickers show up and the runtime's housekeeping does not.

import (
	"runtime"
	"runtime/metrics"
)

// Snapshot is what the process costs at one moment.
type Snapshot struct {
	HeapAlloc   int64 `json:"heap_alloc_bytes"`
	Retained    int64 `json:"retained_bytes"`
	MaxRSS      int64 `json:"max_rss_bytes"`
	Goroutines  int   `json:"goroutines"`
	CPUMicros   int64 `json:"cpu_micros"`
	SchedEvents int64 `json:"sched_events"`
	// BootColdMicros is how long the stack took to come up on a FRESH data
	// directory — the first run in a project.
	BootColdMicros int64 `json:"boot_cold_micros"`
	// BootWarmMicros is a second boot over the same directory: every run after
	// the first, which is the one that decides how the tool feels.
	//
	// Both are in microseconds, and neither started that way. Recording a
	// figure in units it rounds to zero in is how a measurement stops being
	// able to move: a 96ms per-service fsync at startup went unnoticed because
	// only the cold number was watched and it barely moved there, and then the
	// cold number itself fell below a millisecond once lazy opening landed.
	BootWarmMicros int64 `json:"boot_warm_micros"`

	// entryCPU and entrySched are the two running totals as they stood when
	// Take was CALLED, before it collected. See Sub.
	entryCPU, entrySched int64
}

// Take reads the current cost. It forces two collections first: one is not
// enough, because the first can queue finalisers whose objects only become
// free in the second, and reading after a single GC reports garbage as live.
func Take() Snapshot {
	// The running totals first, before this function disturbs them: the two
	// collections below are work, and they are this function's work.
	entryCPU, _ := rusage()
	entrySched := schedEvents()

	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	cpu, rss := rusage()
	return Snapshot{
		entryCPU: entryCPU, entrySched: entrySched,
		HeapAlloc:   int64(m.HeapAlloc),
		Retained:    int64(m.Sys - m.HeapReleased),
		MaxRSS:      rss,
		Goroutines:  runtime.NumGoroutine(),
		CPUMicros:   cpu,
		SchedEvents: schedEvents(),
	}
}

// schedEvents is the cumulative count of goroutines becoming runnable.
//
// Read out of the scheduler's latency histogram, whose bucket counts are a
// count of scheduling events even though its values are durations. A count
// rather than a timing, so it is deterministic enough to compare.
func schedEvents() int64 {
	const name = "/sched/latencies:seconds"
	sample := []metrics.Sample{{Name: name}}
	metrics.Read(sample)
	h := sample[0].Value.Float64Histogram()
	if h == nil {
		return 0
	}
	var n int64
	for _, c := range h.Counts {
		n += int64(c)
	}
	return n
}

// Sub reports what happened between two snapshots: the deltas that only make
// sense as differences, carried alongside the absolute values from the later
// one.
//
// The window runs from the end of the earlier snapshot to the START of this
// one — after the earlier one's collections, before this one's. It used to
// run to the end of this one, so the closing snapshot counted its own two
// collections as something the idle process had done. On a quiet machine that
// is a few dozen scheduling events and nobody noticed. On a shared CI runner
// the collector's workers are preempted and rescheduled, each time a
// goroutine going runnable, and the "idle wakeups" of a stack that normally
// measures thirty came out at 613, 1,752 and 7,061 on three different runs —
// the measurement reporting the runner's weather as the product's weight.
func (s Snapshot) Sub(earlier Snapshot) Snapshot {
	s.CPUMicros = s.entryCPU - earlier.CPUMicros
	s.SchedEvents = s.entrySched - earlier.SchedEvents
	return s
}
