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
	"os"
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
