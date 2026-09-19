// Package lightness holds the budget doze-aws is measured against, and the
// primitives that measure it.
//
// # Why this exists
//
// "Run fearlessly on a laptop" is the whole claim this project makes. Every
// number behind it — the binary's size, what it costs at rest, how many
// goroutines it parks, what it leaves on disk — was produced once, by hand, and
// written into docs/performance.md. None of them was defended by anything. The
// binary could double, the idle footprint could double, and every test in the
// tree would stay green.
//
// # Portable and local, which is the distinction everything follows from
//
// Some of these numbers are FACTS: identical on any machine for a given
// toolchain. The size of a cross-compiled binary, the bytes of an embedded
// asset, the set of modules linked in, the goroutines one constructor starts.
// Those are gated EXACTLY, because there is no noise to allow for and a
// one-byte change is a real change.
//
// The rest are OBSERVATIONS: heap, resident memory, CPU time, boot latency.
// They move with the machine, the Go version and what else is running. Those
// get a ceiling wide enough that it cannot fire on noise, and a recorded
// measurement that only says where the maintainer's machine last stood.
//
// Mixing the two is how a budget becomes a thing people mute. Keeping them
// apart is why the exact half can be exact.
//
// # Both a measurement and a ceiling
//
// Every entry carries both. The ceiling gates. The measurement is what makes a
// change VISIBLE: a creep from 20.4 to 21.9 MiB under a 22 MiB ceiling passes
// every gate and shows up immediately in a diff. That is not a hypothetical —
// this repo's heap band could not fire at all for weeks, and the leak that was
// hiding under it was found by a logged number, not by an assertion.
package lightness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Fixture is testdata/lightness.json.
type Fixture struct {
	Note     string   `json:"_"`
	Recorded Recorded `json:"recorded"`
	Portable Portable `json:"portable"`
	Local    Local    `json:"local"`
}

// Local holds the numbers that move with the machine.
//
// Ceilings here are wide — several times the measurement — and that is the
// point rather than a weakness. A band that can fire on a slower runner is a
// band somebody disables, and a disabled band catches nothing at all. These
// exist to catch a doubling, not a drift; the `measured` values next to them
// are what catch a drift, by appearing in a diff.
type Local struct {
	// IdleWindowSeconds is how long a stack is left alone before its CPU and
	// scheduler deltas are read.
	IdleWindowSeconds int `json:"idle_window_seconds"`
	// Shapes is what a given set of services costs at rest, keyed by a name
	// the test knows: "full" is everything, "sqs+s3" is the two services
	// resourcebounds_test.go drives.
	Shapes map[string]Shape `json:"shapes"`
}

// Shape is one stack configuration, measured at rest.
type Shape struct {
	// Heap is the live heap after two collections — the absolute baseline that
	// resourcebounds_test.go structurally cannot see, because it compares N
	// rounds against 2N within a single run and so is blind to the starting
	// point moving.
	Heap Budget `json:"heap_alloc_bytes"`
	// Retained is MemStats.Sys minus HeapReleased: what the runtime has taken
	// from the OS and not given back. This is the footprint figure worth
	// quoting, and the one docs/performance.md established agrees with vmmap's
	// dirty total to within a megabyte.
	Retained Budget `json:"retained_bytes"`
	// MaxRSS is the process's peak resident size. Only meaningful because the
	// measurement runs in its own process: as a high-water mark it would
	// otherwise report whatever the largest test in the package did.
	MaxRSS Budget `json:"max_rss_bytes"`
	// Goroutines at rest, including the runtime's own.
	Goroutines Budget `json:"goroutines"`
	// CPUMicros is this process's own CPU time across the idle window. A
	// busy neighbour cannot inflate it, which is why it can be gated at all.
	CPUMicros Budget `json:"cpu_micros"`
	// SchedEvents is how many times a goroutine went runnable across the idle
	// window — the wakeup proxy, and the number a laptop battery notices.
	SchedEvents Budget `json:"sched_events"`
	// BootColdMicros is how long NewStack took on a FRESH data directory: the
	// first run in a project.
	//
	// In microseconds, and it used to be milliseconds. That was right when this
	// was 225 ms of bbolt creating sixteen databases and it stopped being right
	// the moment lazy opening removed them — the number fell to well under a
	// millisecond and the field recorded 0, which is a measurement that cannot
	// move. The same trap the warm figure below was split out to avoid, sprung
	// on the other one a commit later.
	//
	// Its ceiling is a catastrophe ceiling, twenty times the measurement, and
	// that is deliberate rather than lazy. Wall-clock on a shared runner varies
	// by tens of percent — .github/workflows/bench.yml refuses to gate on it
	// for exactly that reason and is right — so any band tight enough to catch
	// a slowdown would fire on noise and get deleted. Twenty times cannot fire
	// on noise, and still catches what actually goes wrong here: a synchronous
	// network call, a sleep, or an eager compile pass finding its way into
	// startup. The benchmarks in boot_bench_test.go are where a real
	// regression is meant to be seen.
	BootColdMicros Budget `json:"boot_cold_micros"`
	// BootWarmMicros is a second boot over the same data directory.
	//
	// It is much closer to the cold figure than it used to be, and that is the
	// result rather than a fault in the measurement: with databases created on
	// first use, the difference between a fresh directory and a used one is
	// Lambda's three runtime shims and two encryption keys, not sixteen files.
	BootWarmMicros Budget `json:"boot_warm_micros"`
}

// Recorded says where and when the observations were taken. It explains a diff
// rather than gating anything.
type Recorded struct {
	Toolchain string `json:"toolchain"`
	Host      string `json:"host"`
	Date      string `json:"date"`
}

// Portable holds the numbers that are identical on any machine.
type Portable struct {
	// Modules is every non-stdlib module linked into cmd/doze-aws, sorted.
	//
	// A set rather than a count, because the count alone is the least useful
	// form of this fact: "+1 module" tells you nothing and
	// "+github.com/aws/aws-sdk-go-v2" tells you what happened and who to ask.
	Modules []string `json:"modules"`

	// Embed is what each //go:embed tree weighs inside the binary, keyed by
	// site. Per site rather than one total, so a failure says WHICH tree grew.
	Embed map[string]Tree `json:"embed"`

	// Services is what one service costs to start and to store, keyed by the
	// name in dozeaws.Implemented.
	Services map[string]Service `json:"services"`

	// Binary is the size of the thing people download.
	Binary Binary `json:"binary"`
}

// Binary is what a release artifact weighs.
//
// # Why one pinned target and not the host's
//
// The size of this project's binary was quoted in three documents and defended
// by nothing, and it could not be defended while it was a local observation: a
// darwin/arm64 build and a linux/amd64 build of the same commit differ by more
// than a megabyte, so "20.4 MiB" was true only on the machine that measured it.
//
// Pinning ONE cross-compiled target is what turns it into a fact. It is the
// same number on any host with the same toolchain — verified byte-identical
// across repeat builds — so it can be gated exactly rather than banded.
type Binary struct {
	// Target is the GOOS/GOARCH this figure is for, recorded so the number is
	// never read as "the binary" when it is one of five that ship.
	Target string `json:"target"`
	// Flags is the build that produced it, likewise. -X main.version is pinned
	// to a fixed string: a real tag makes the binary longer by the difference
	// in the version string's length, which is not a change worth a diff.
	Flags string `json:"flags"`
	// Bytes is the stripped binary.
	Bytes Budget `json:"bytes"`
	// Gzipped approximates the download, which ships as a .tar.gz. Compressed
	// with the stdlib at best compression rather than the system gzip, so the
	// number is reproducible from this repo alone.
	Gzipped Budget `json:"gzipped_bytes"`
}

// Tree is an embedded asset tree.
type Tree struct {
	Bytes Budget `json:"bytes"`
	Files int    `json:"files"`
}

// Service is what one service costs before anything has asked it for anything.
type Service struct {
	// Goroutines is how many the constructor leaves running. Exact: a delta
	// across one NewStack call, so it is immune to whatever else the test
	// binary is doing, and a change in it is always deliberate.
	Goroutines int `json:"goroutines"`
	// DataDirBytes is what an untouched service writes to disk just by being
	// enabled — bbolt's initial pages, mostly.
	DataDirBytes Budget `json:"datadir_bytes"`
}

// Budget is a number with room above it.
//
// Ceiling gates; Measured records. Both, because they answer different
// questions: the ceiling asks "has this become a problem", and the measured
// value asks "did this move", which is the question a diff answers and an
// assertion cannot. A creep from 1.0 to 1.4 MB under a 1.5 MB ceiling passes
// every gate and is obvious in a review.
//
// Bytes get a ceiling rather than exact equality on purpose. Console assets are
// edited routinely, and a gate that fires on a one-byte CSS change is a gate
// that gets a `lightness:update` reflex rather than a reading — which is
// exactly how a budget stops meaning anything.
type Budget struct {
	Measured int64 `json:"measured"`
	Ceiling  int64 `json:"ceiling"`
}

// Over reports whether n breaches the ceiling.
func (b Budget) Over(n int64) bool { return b.Ceiling > 0 && n > b.Ceiling }

// Record sets Measured and chooses Ceiling.
//
// A fresh entry, or one its measurement has outgrown, takes the policy's band.
// After that the two halves part company, and the reason is the same
// portable/local split the whole package is built on:
//
// An EXACT quantity follows its measurement down as well as up. It has to.
// Twice now a number here fell by two orders of magnitude — a service's
// untouched data directory going from 131,166 bytes to 94 when its database
// stopped being created at boot, and Lambda's from 19,925 to 94 when its
// runtime shims did — and both times regenerating left the old ceiling
// standing, because widening-only cannot see a number get smaller. A 164,864
// ceiling over a 94-byte measurement is not a loose band, it is no band: the
// service would have to go back to creating the file to trip it, which is
// exactly the regression it was meant to catch. Both had to be reset by hand,
// and the second time was no more obvious than the first.
//
// An OBSERVATION does not, deliberately. Heap, resident memory, CPU and boot
// latency move with the machine, so re-deriving from whatever the last run
// happened to read would ratchet the band down to a quiet afternoon and fail
// on an ordinary one. These ceilings are several times their measurement
// precisely so they cannot fire on noise, and a gate that fires on noise is a
// gate somebody mutes. A stale ceiling here costs less than a flaky one, and
// the `measured` field is what makes the drift visible in a diff — which is
// the division this file already argues for everywhere else.
func (b Budget) Record(n int64, h Headroom) Budget {
	b.Measured = n
	if b.Ceiling == 0 || n > b.Ceiling || h.Exact {
		b.Ceiling = h.Ceiling(n)
	}
	return b
}

// A Headroom turns a measurement into the ceiling it gets, and says whether
// that measurement is a fact or an observation.
//
// There is no single right amount, and pretending otherwise produces a gate
// that cannot fire. The first version of this file had one rule with a 4 KiB
// floor — sensible for an asset tree, and it gave a goroutine count of 25 a
// ceiling of 5,120. That is not a loose budget, it is no budget: the stack
// would have to leak five thousand goroutines to trip it. Same shape as the
// heap band a few commits ago that required 8 MiB before it would evaluate a
// heap that is 3.8. A ceiling has to be chosen against the units it is in.
type Headroom struct {
	// Ceiling is the band a measurement of n gets.
	Ceiling func(n int64) int64
	// Exact marks a quantity that is identical on any machine for a given
	// toolchain, so there is no noise for a ceiling to absorb and the band can
	// be re-derived from any run. See Budget.Record for what that buys, and
	// for why the observations deliberately do not get it.
	Exact bool
}

var (
	// Bytes is for deterministic byte counts — embedded trees, an untouched
	// data directory. The value does not move unless someone moved it, so a
	// quarter of room is generous, and its ceiling tracks it in both
	// directions.
	Bytes = Headroom{
		Exact:   true,
		Ceiling: func(n int64) int64 { return roundKiB(atLeast(n+n/4, n+4096)) },
	}

	// Footprint is for memory measured on whatever machine is running: heap,
	// retained, resident. Half again, with a mebibyte of floor, because these
	// legitimately differ between a laptop and a CI runner and a band that
	// fires on that difference is a band that gets deleted.
	Footprint = Headroom{
		Ceiling: func(n int64) int64 { return roundKiB(atLeast(n+n/2, n+1<<20)) },
	}

	// Count is for small exact-ish integers — goroutines at rest. Half again
	// plus four, so 25 becomes 41: tight enough that a dozen leaked goroutines
	// fire it, loose enough to absorb a GC worker appearing.
	//
	// Exact-ish is not exact, which is why it is not marked so: the runtime
	// can add a worker without this repo changing, and re-deriving from a run
	// that happened to have one fewer would hand the next run a failure.
	Count = Headroom{
		Ceiling: func(n int64) int64 { return atLeast(n+n/2, n+4) },
	}

	// Noisy is for what a shared runner's slower cores stretch: CPU time and
	// scheduler events. Ten times, which sounds absurd until you consider what
	// it still catches — a per-100ms ticker added to each of seventeen
	// services is a 10x rise on its own.
	Noisy = Headroom{
		Ceiling: func(n int64) int64 { return atLeast(n*10, 1000) },
	}

	// Catastrophe is for wall-clock MICROSECONDS, where no honest band exists.
	// Twenty times, with a five-second floor, so it cannot fire on a slow
	// runner and still fails on the things that actually break startup: a
	// synchronous network call, a sleep, an eager compile pass. Anything
	// subtler belongs in a benchmark, which is where this repo already decided
	// timing regressions get looked at rather than gated.
	//
	// The floor is in the same units as the value, which is the whole of the
	// note above about choosing a ceiling against its units. It read 5000 when
	// boot was recorded in milliseconds and would have become a five-MILLISECOND
	// catastrophe band the moment that field changed to microseconds — a
	// ceiling a healthy stack breaches on any ordinary run.
	Catastrophe = Headroom{
		Ceiling: func(n int64) int64 { return atLeast(n*20, 5_000_000) },
	}
)

func atLeast(n, floor int64) int64 {
	if n < floor {
		return floor
	}
	return n
}

func roundKiB(n int64) int64 { return (n + 1023) / 1024 * 1024 }

// Load reads the fixture.
func Load(path string) (*Fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// Save writes the fixture back, formatted the way it is committed.
func Save(path string, f *Fixture) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// FSBytes totals an embedded tree: the bytes that ship and the files they are
// in. Walks the embed.FS rather than the directory, so it counts what is
// compiled in and not what happens to be lying beside it on disk.
func FSBytes(fsys fs.FS) (bytes int64, files int, err error) {
	err = fs.WalkDir(fsys, ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files, err
}

// DirBytes totals a directory on disk: what a data directory costs.
func DirBytes(root string) (bytes int64, files int, err error) {
	err = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files, err
}

// Diff reports what moved between two sorted sets, as the two lists a reader
// needs: what appeared and what went away.
func Diff(want, got []string) (added, removed []string) {
	in := func(list []string, s string) bool {
		i := sort.SearchStrings(list, s)
		return i < len(list) && list[i] == s
	}
	w := append([]string(nil), want...)
	g := append([]string(nil), got...)
	sort.Strings(w)
	sort.Strings(g)
	for _, s := range g {
		if !in(w, s) {
			added = append(added, s)
		}
	}
	for _, s := range w {
		if !in(g, s) {
			removed = append(removed, s)
		}
	}
	return added, removed
}
