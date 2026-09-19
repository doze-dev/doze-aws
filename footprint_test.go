package dozeaws_test

// What a stack costs at rest, measured in a process of its own.
//
// # Why a child process
//
// Three of these numbers cannot be read honestly from inside the test binary.
// Rusage's Maxrss is a HIGH-WATER MARK for the whole process, so in a package
// that also runs a 4,000-round bounds test and a stress suite it reports what
// the largest of those did and says nothing about a stack at rest. CPU time is
// cumulative for the same reason. And the goroutine count carries whatever
// every other test left parked.
//
// Re-execing the test binary with one shape to measure fixes all three at once,
// and costs about thirty lines. The child boots a stack, sits still, prints one
// line of JSON, and exits.
//
// # Why this is where the absolute baseline lives
//
// resourcebounds_test.go compares N rounds against 2N *within one run*, which
// makes it blind by construction to the starting point moving: a commit that
// takes idle heap from 3.8 MiB to 8 MiB passes it every time. The obvious fix —
// making that test absolute — does not work, because its measurement point
// moves with DOZE_BOUND_ROUNDS and an absolute number would be wrong at one of
// the two scales.
//
// So the two split the question cleanly, and the division states itself:
// resourcebounds asks "does it grow?", and this asks "how big is it?"

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/lightness"
)

// shapeEnv names the shape a child process should measure. Its presence is
// also how the child knows it is the child.
const shapeEnv = "DOZE_LIGHTNESS_SHAPE"

// marker prefixes the child's one line of output, so the parent can find it
// among everything `go test` prints around it.
const marker = "LIGHTNESS_JSON "

// shapes are the stack configurations worth a budget: everything, and the two
// services resourcebounds_test.go drives so the two tests describe the same
// thing from different angles.
var shapes = map[string][]string{
	"full":   nil, // nil enables every implemented service
	"sqs+s3": {"sqs", "s3"},
}

func TestLightnessFootprint(t *testing.T) {
	if shape := os.Getenv(shapeEnv); shape != "" {
		measureInChild(t, shape)
		return
	}
	if testing.Short() {
		t.Skip("boots a stack in a child process and sits still for a window")
	}
	if lightness.RaceEnabled {
		t.Skip("the race detector multiplies heap and resident memory, so a " +
			"footprint ceiling measured under it would be measuring the detector")
	}

	want, err := lightness.Load(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	if *update && want.Local.Shapes == nil {
		want.Local.Shapes = map[string]lightness.Shape{}
	}
	if want.Local.IdleWindowSeconds == 0 {
		want.Local.IdleWindowSeconds = 3
	}

	for name := range shapes {
		got := runChild(t, name, want.Local.IdleWindowSeconds)
		if *update {
			cur := want.Local.Shapes[name]
			want.Local.Shapes[name] = lightness.Shape{
				Heap:        cur.Heap.Record(got.HeapAlloc, lightness.Footprint),
				Retained:    cur.Retained.Record(got.Retained, lightness.Footprint),
				MaxRSS:      cur.MaxRSS.Record(got.MaxRSS, lightness.Footprint),
				Goroutines:  cur.Goroutines.Record(int64(got.Goroutines), lightness.Count),
				CPUMicros:   cur.CPUMicros.Record(got.CPUMicros, lightness.Noisy),
				SchedEvents: cur.SchedEvents.Record(got.SchedEvents, lightness.Noisy),
				BootMillis:  cur.BootMillis.Record(got.BootMillis, lightness.Catastrophe),
			}
			continue
		}
		budget, ok := want.Local.Shapes[name]
		if !ok {
			t.Errorf("no budget for the %q shape — run `task lightness:update`", name)
			continue
		}
		t.Logf("%-8s boot %dms · %d goroutines · heap %s · retained %s · peak RSS %s · "+
			"%d µs CPU and %d wakeups over %ds",
			name, got.BootMillis, got.Goroutines, mib(got.HeapAlloc), mib(got.Retained),
			mib(got.MaxRSS), got.CPUMicros, got.SchedEvents, want.Local.IdleWindowSeconds)

		check(t, name, "live heap", budget.Heap, got.HeapAlloc,
			"this is the absolute baseline resourcebounds_test.go cannot see")
		check(t, name, "retained memory", budget.Retained, got.Retained,
			"the footprint figure docs/performance.md quotes")
		check(t, name, "peak RSS", budget.MaxRSS, got.MaxRSS, "")
		check(t, name, "goroutines at rest", budget.Goroutines, int64(got.Goroutines), "")
		check(t, name, "idle CPU", budget.CPUMicros, got.CPUMicros,
			"a stack doing nothing should cost nothing")
		check(t, name, "wakeups", budget.SchedEvents, got.SchedEvents,
			"a woken core never reaches its deeper idle states, which is what a battery notices")
		check(t, name, "boot time (ms)", budget.BootMillis, got.BootMillis,
			"twenty times the measurement, so this is not slowness — it is something "+
				"blocking in NewStack")
	}

	if *update {
		if err := lightness.Save(budgetPath, want); err != nil {
			t.Fatal(err)
		}
	}
}

func check(t *testing.T, shape, what string, b lightness.Budget, got int64, why string) {
	t.Helper()
	if !b.Over(got) {
		return
	}
	msg := fmt.Sprintf("%s: %s is %d, over its ceiling of %d", shape, what, got, b.Ceiling)
	if why != "" {
		msg += "\n  " + why
	}
	t.Error(msg + "\n  These ceilings are several times the measurement, so this is a " +
		"doubling rather than drift.\n  If it is wanted, `task lightness:update` records it " +
		"and puts the number in the diff.")
}

// runChild re-execs this test binary to measure one shape alone.
func runChild(t *testing.T, shape string, window int) lightness.Snapshot {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestLightnessFootprint", "-test.v")
	cmd.Env = append(os.Environ(),
		shapeEnv+"="+shape,
		"DOZE_LIGHTNESS_WINDOW="+fmt.Sprint(window),
		// Pinned because the runtime scales with it: mcache, GC workers and
		// scheduler events all track GOMAXPROCS, so a 2-core runner and a
		// 14-core laptop are otherwise not measuring the same quantity and the
		// band would have to be uselessly wide to cover both.
		"GOMAXPROCS=4",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("measuring the %q shape in a child process: %v\n%s", shape, err, out)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		_, rest, ok := strings.Cut(line, marker)
		if !ok {
			continue
		}
		var s lightness.Snapshot
		if err := json.Unmarshal([]byte(strings.TrimSpace(rest)), &s); err != nil {
			t.Fatalf("the %q child printed something unreadable: %v", shape, err)
		}
		return s
	}
	t.Fatalf("the %q child printed no measurement:\n%s", shape, out)
	return lightness.Snapshot{}
}

// measureInChild is the child half: boot, settle, sit still, report.
func measureInChild(t *testing.T, shape string) {
	services, ok := shapes[shape]
	if !ok {
		t.Fatalf("unknown shape %q", shape)
	}
	window := 3
	if v := os.Getenv("DOZE_LIGHTNESS_WINDOW"); v != "" {
		fmt.Sscan(v, &window)
	}

	start := time.Now()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Services: services, Logf: dozetest.Quiet(t)})
	boot := time.Since(start)
	if err != nil {
		t.Fatalf("booting the %q shape: %v", shape, err)
	}
	defer st.Close()

	// Let every janitor arm and every shipper reach its first wait before
	// anything is read. A stack measured mid-startup is measuring startup.
	time.Sleep(2 * time.Second)

	before := lightness.Take()
	time.Sleep(time.Duration(window) * time.Second)
	after := lightness.Take().Sub(before)
	after.BootMillis = boot.Milliseconds()

	b, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(marker + string(b))
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }
