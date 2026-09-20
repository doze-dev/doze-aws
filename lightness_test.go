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
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/docs"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
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

// TestEmbeddedTreesFitTheirBudget weighs what ships inside the binary.
//
// Per embed site rather than one total, because a total that moved tells you
// nothing about which tree moved — and one of these four is the console, which
// is ~13% of the binary and under routine development. The point is not to stop
// it growing; it is that growing it should be a line somebody approved.
func TestEmbeddedTreesFitTheirBudget(t *testing.T) {
	want, err := lightness.Load(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	trees := embeddedTrees()

	if *update {
		if want.Portable.Embed == nil {
			want.Portable.Embed = map[string]lightness.Tree{}
		}
		for name, fsys := range trees {
			bytes, files, err := lightness.FSBytes(fsys)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			cur := want.Portable.Embed[name]
			want.Portable.Embed[name] = lightness.Tree{
				Bytes: cur.Bytes.Record(bytes, lightness.Bytes), Files: files,
			}
		}
		if err := lightness.Save(budgetPath, want); err != nil {
			t.Fatal(err)
		}
		return
	}

	for name, fsys := range trees {
		bytes, files, err := lightness.FSBytes(fsys)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		budget, ok := want.Portable.Embed[name]
		if !ok {
			t.Errorf("%s is embedded in the binary and is not in the budget — "+
				"run `task lightness:update`", name)
			continue
		}
		t.Logf("%-24s %8d bytes in %2d files (ceiling %d)", name, bytes, files, budget.Bytes.Ceiling)
		if budget.Bytes.Over(bytes) {
			t.Errorf("%s is %d bytes, over its %d ceiling by %d.\n"+
				"  Every byte here ships to every user whether they open it or not.\n"+
				"  If the growth is wanted, `task lightness:update` raises the ceiling\n"+
				"  and puts the new number in the diff.",
				name, bytes, budget.Bytes.Ceiling, bytes-budget.Bytes.Ceiling)
		}
		if files != budget.Files {
			t.Errorf("%s ships %d files, budget says %d — a file appeared or "+
				"vanished, which is worth a glance even when the bytes are fine",
				name, files, budget.Files)
		}
	}
}

// embeddedTrees is every //go:embed tree in the binary.
//
// The console and the shims hand theirs out through an accessor rather than
// exporting embed.FS values; docs.FS is already exported because console/info.go
// reads the ledger at runtime to render the fidelity panel.
func embeddedTrees() map[string]fs.FS {
	trees := map[string]fs.FS{
		"docs/api-support":    docs.FS,
		"lambdaruntime/shims": lambdaruntime.EmbeddedFS(),
	}
	for name, fsys := range console.EmbeddedFS() {
		trees[name] = fsys
	}
	return trees
}

// TestEachServiceCostsWhatTheBudgetSays measures one service at a time.
//
// Per service, not per stack, and as a DELTA across NewStack — which is what
// makes it exact rather than approximate. An absolute count would be polluted
// by whatever else the test binary has running; a delta is the service's own
// contribution and nothing else's.
//
// It also localises a regression. "The full stack now starts 31 goroutines
// instead of 27" sends you looking through seventeen services; "logs now
// starts 5 instead of 4" does not.
func TestEachServiceCostsWhatTheBudgetSays(t *testing.T) {
	if testing.Short() {
		t.Skip("boots every service in turn")
	}
	want, err := lightness.Load(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	if *update && want.Portable.Services == nil {
		want.Portable.Services = map[string]lightness.Service{}
	}

	for _, svc := range dozeaws.Implemented {
		goroutines, bytes := measureService(t, svc)
		if *update {
			cur := want.Portable.Services[svc]
			want.Portable.Services[svc] = lightness.Service{
				Goroutines: goroutines, DataDirBytes: cur.DataDirBytes.Record(bytes, lightness.Bytes),
			}
			continue
		}
		budget, ok := want.Portable.Services[svc]
		if !ok {
			t.Errorf("%s is implemented and is not in the budget — run `task lightness:update`", svc)
			continue
		}
		t.Logf("%-16s %2d goroutines, %7d bytes on disk", svc, goroutines, bytes)
		if goroutines != budget.Goroutines {
			t.Errorf("%s starts %d long-lived goroutine(s), budget says %d.\n"+
				"  A goroutine per service is a goroutine on every laptop running this,\n"+
				"  awake or parked. If the new one is wanted, record it and say why in\n"+
				"  the commit.", svc, goroutines, budget.Goroutines)
		}
		if budget.DataDirBytes.Over(bytes) {
			t.Errorf("%s writes %d bytes to an untouched data directory, over its %d ceiling",
				svc, bytes, budget.DataDirBytes.Ceiling)
		}
	}
	if *update {
		if err := lightness.Save(budgetPath, want); err != nil {
			t.Fatal(err)
		}
	}
}

// measureService boots one service alone and reports what it cost.
func measureService(t *testing.T, svc string) (goroutines int, bytes int64) {
	t.Helper()
	dir := t.TempDir()
	before := settled()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: dir, Services: []string{svc}, Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatalf("booting %s alone: %v", svc, err)
	}
	after := settled()
	if err := st.Close(); err != nil {
		t.Fatalf("closing %s: %v", svc, err)
	}
	bytes, _, err = lightness.DirBytes(dir)
	if err != nil {
		t.Fatalf("measuring %s's data dir: %v", svc, err)
	}
	return after - before, bytes
}

// settled waits for the goroutine count to stop moving before reading it.
//
// Constructors start their workers asynchronously, so an immediate count races
// them and lands anywhere. Waiting for two identical readings a tick apart is
// enough, and far steadier than a fixed sleep chosen by guess.
func settled() int {
	last := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == last {
			return n
		}
		last = n
	}
	return last
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
