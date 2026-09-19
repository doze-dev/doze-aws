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

// Record sets Measured, and widens Ceiling only when the measurement has
// passed it — so regenerating never silently tightens a band somebody chose.
func (b Budget) Record(n int64) Budget {
	b.Measured = n
	if b.Ceiling == 0 || n > b.Ceiling {
		b.Ceiling = headroom(n)
	}
	return b
}

// headroom is the ceiling a fresh measurement gets: a quarter above, rounded
// up to a kibibyte, with a 4 KiB floor so a tiny tree is not held to the byte.
func headroom(n int64) int64 {
	c := n + n/4
	if c < n+4096 {
		c = n + 4096
	}
	return (c + 1023) / 1024 * 1024
}

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
