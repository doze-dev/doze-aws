package dozeaws_test

// The lightness budget: the claims about weight, turned into assertions.
//
// README's first line is "One small static binary", and docs/performance.md
// publishes an idle footprint, a CPU figure, a wakeup rate and a goroutine
// count. Every one of those was measured once, by hand, and nothing defends
// any of them — the binary could double and the suite would stay green.
//
// This file is where that stops. It starts with the dependency set, which is
// the cheapest and sharpest of the lot; the rest of the budget lands beside it.

import (
	"flag"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/lightness"
)

const budgetPath = "testdata/lightness.json"

// update rewrites the budget instead of asserting against it.
//
// A flag rather than an environment variable because that is what the Go
// toolchain's own golden-file tests use, and because `task lightness:update`
// is the only thing expected to pass it. Recording a new number must stay a
// deliberate act with a reviewable diff — the budget is worth nothing if the
// reflex on a red run is to regenerate.
var update = flag.Bool("lightness.update", false,
	"rewrite testdata/lightness.json instead of asserting against it")

// TestOnlyTheDeclaredModulesAreLinked pins the set of non-stdlib modules that
// reach the shipped binary.
//
// This is the highest-value check in the budget and the one with no noise at
// all. Binary size regressions are CAUSED by dependencies, and a diff saying
// "+github.com/aws/aws-sdk-go-v2" is worth more than one saying "+1.2 MB": it
// names what happened. It is also toolchain-independent, so unlike a byte
// count it never needs regenerating when Go bumps a patch version.
//
// It makes an existing boast checkable. README and docs/README.md both say
// "Five runtime dependencies", which has been true and has been guarded by
// nothing — the direct require block in go.mod has thirty entries, twenty-three
// of them AWS SDK packages that only tests import. The distinction between
// "declared" and "linked" is exactly what this measures, and the five named in
// the docs are these six less golang.org/x/sys, which arrives transitively
// through bbolt and is not a dependency anybody chose.
func TestOnlyTheDeclaredModulesAreLinked(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to go list")
	}
	want, err := lightness.Load(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	got := linkedModules(t)

	if *update {
		want.Portable.Modules = got
		if err := lightness.Save(budgetPath, want); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %d linked modules in %s", len(got), budgetPath)
		return
	}

	added, removed := lightness.Diff(want.Portable.Modules, got)
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	for _, m := range added {
		t.Errorf("%s is now linked into the binary and is not in the budget.\n"+
			"  If it belongs there, run `task lightness:update` and commit the diff\n"+
			"  with the change that added it — and check whether README's\n"+
			"  \"five runtime dependencies\" is still true.", m)
	}
	for _, m := range removed {
		t.Errorf("%s is in the budget but is no longer linked.\n"+
			"  Good news, probably: run `task lightness:update` to record it.", m)
	}
}

// linkedModules asks the toolchain which modules reach cmd/doze-aws.
//
// `go list -deps` rather than parsing go.mod, because go.mod records what is
// DECLARED and this needs what is REACHED. The two differ here by twenty-four
// modules, so the distinction is the entire point.
//
// GOWORK is pinned off: a go.work file in a parent directory would otherwise
// change the answer depending on where the suite was run from, which is the
// opposite of the portable fact this is supposed to be.
func linkedModules(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps",
		"-f", "{{if .Module}}{{.Module.Path}}{{end}}", "./cmd/doze-aws")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		t.Fatalf("go list -deps ./cmd/doze-aws: %v\n%s", err, stderr)
	}

	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		m := strings.TrimSpace(line)
		// The module under test is not a dependency of itself, and a blank
		// line is a stdlib package, which carries no module path.
		if m == "" || m == "github.com/doze-dev/doze-aws" {
			continue
		}
		seen[m] = true
	}
	mods := make([]string, 0, len(seen))
	for m := range seen {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	return mods
}
