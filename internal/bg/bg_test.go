package bg

// Each test here would, without the package under test, crash the test binary
// rather than fail — which is the whole point of it.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture collects log lines the way a service's logf would.
type capture struct {
	mu    sync.Mutex
	lines []string
}

func (c *capture) logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Formatted, not the raw format string: the assertions are about what a
	// reader would actually see, and the goroutine's name arrives as an arg.
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lines)
}

func (c *capture) joined() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// A ticker loop survives a panicking tick and keeps ticking. This is the
// sweeper/janitor/evaluator case: one bad sweep must not end retention for
// the life of the process.
func TestTickSurvivesAndTheLoopCarriesOn(t *testing.T) {
	var c capture
	ticks := 0
	for range 5 {
		Tick(c.logf, "ssm: janitor", func() {
			ticks++
			if ticks == 2 {
				panic("a bad sweep")
			}
		})
	}
	if ticks != 5 {
		t.Errorf("the loop ran %d times, want 5 — a panicking tick stopped it", ticks)
	}
	if c.count() != 1 {
		t.Errorf("logged %d lines, want exactly 1", c.count())
	}
	if !strings.Contains(c.joined(), "ssm: janitor") {
		t.Errorf("the report does not name the goroutine:\n%s", c.joined())
	}
}

// A one-shot delivery loses itself and nothing else.
func TestGoContainsAPanickingTask(t *testing.T) {
	var c capture
	done := make(chan struct{})
	Go(c.logf, "cloudwatch: alarm action", func() {
		defer close(done)
		panic("a malformed payload")
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the task never ran")
	}
	// The log happens after the body returns, so give the deferred report a
	// moment rather than racing it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.count() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if c.count() != 1 {
		t.Errorf("logged %d lines, want 1", c.count())
	}
}

// A queue worker runs its cleanup, and the cleanup runs BEFORE whatever the
// caller registered earlier — which is what stops Close reporting a clean
// shutdown of a worker that just died.
func TestRecoverRunsCleanupBeforeEarlierDefers(t *testing.T) {
	var c capture
	var order []string

	func() {
		defer func() { order = append(order, "close(done)") }() // registered first, runs last
		defer Recover(c.logf, "logs: fan-out", func() {
			order = append(order, "marked dead")
		})
		panic("a bad batch")
	}()

	want := []string{"marked dead", "close(done)"}
	if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("order = %v, want %v — the worker was reported finished before it was marked dead",
			order, want)
	}
	if c.count() != 1 {
		t.Errorf("logged %d lines, want 1", c.count())
	}
}

// The no-panic path costs nothing and reports nothing.
func TestNoPanicIsSilent(t *testing.T) {
	var c capture
	ran := false
	Tick(c.logf, "x: y", func() { ran = true })
	if !ran {
		t.Error("the body did not run")
	}
	if c.count() != 0 {
		t.Errorf("logged %d lines on the happy path, want 0", c.count())
	}
}

// A nil logf is the constructor default in several packages before Options
// are applied; containing the panic must not then panic on the report.
func TestNilLoggerStillContains(t *testing.T) {
	Tick(nil, "x: y", func() { panic("boom") })
	Recover(nil, "x: y") // no panic in flight: must be a no-op
}
