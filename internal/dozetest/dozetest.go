// Package dozetest holds the standing assertions every package's tests make.
//
// # Why this exists
//
// An audit of this tree found thirteen real bugs. The 1,045-test suite found
// none of them: nine of the thirteen only appear after a stack has been running
// or while it is shutting down — a goroutine that outlives Close, a map that
// grows without bound, a channel a dying worker leaves unserviced — and every
// test in the suite lives a few hundred milliseconds and then stops looking.
//
// The response to that is not more tests. It is making the tests that already
// exist watch for the class of failure they were structurally blind to. A test
// that boots a stack, does its work and returns has already exercised startup,
// the request path and shutdown; it simply never asked whether any of them
// leaked. Asking costs nothing per test and covers all of them at once.
//
// # What is asserted
//
//	goroutines   nothing this package started is still running at the end
//	faults       no stack answered a 5xx while a test believed it was fine
//	panics       no background goroutine recovered from a panic
//
// The last two are free because the plumbing already exists: Stack.Faults()
// records every 5xx against the stack that answered it, and internal/bg already
// logs a distinctive line for every contained panic.
package dozetest

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// Main runs a package's tests with the goroutine-leak check around them.
//
// Call it from TestMain:
//
//	func TestMain(m *testing.M) { dozetest.Main(m) }
//
// It checks ONCE, after the whole package's tests, rather than per test. That
// is the cheap end of the trade: it will not say which test leaked, but it
// needs no per-test wiring and so actually gets adopted. A package that starts
// failing can narrow it down with -run.
func Main(m *testing.M, extra ...goleak.Option) {
	code := m.Run()
	runAtExit()
	if code == 0 {
		if err := verify(append(ignored(), extra...)...); err != nil {
			fmt.Fprintf(os.Stderr, "goroutines outlived this package's tests:\n%v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

// AtExit registers a teardown to run after the package's tests and BEFORE the
// goroutine check.
//
// Every fixture in this tree is per-test, torn down by t.Cleanup, which is
// right until something has to outlive a single test. A fuzz target is the
// case that forces it: the target body runs millions of times, booting a stack
// each time costs about seventy-five milliseconds, and a stack shared across
// iterations has no *testing.T whose Cleanup could close it. Without somewhere
// to hang that close, the shared stack is still running when goleak looks, and
// the only ways out are to stop checking that package or to add its whole
// goroutine set to the ignore list — both of which give up the assertion to
// keep the fixture.
//
// Ordering is the whole point: teardown runs first, so what a package-level
// fixture started still has to have stopped.
func AtExit(f func()) {
	atExitMu.Lock()
	atExit = append(atExit, f)
	atExitMu.Unlock()
}

var (
	atExitMu sync.Mutex
	atExit   []func()
)

// runAtExit runs the registered teardowns in reverse order of registration,
// the way defer and t.Cleanup do — a fixture built on top of another must come
// down first.
func runAtExit() {
	atExitMu.Lock()
	fns := atExit
	atExit = nil
	atExitMu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}

// settle is how long a goroutine has to finish shutting down before it counts
// as leaked.
//
// goleak's own retry budget is a few hundred milliseconds, which is ample on an
// idle machine and not always ample when the whole suite is running: shutting
// down a stack flushes shippers and drains queues, and under contention the
// tail of that can land late. A goroutine that exits half a second after Close
// is not the failure being looked for; one that never exits is.
//
// Three seconds, then, and NOT more — the point of the assertion is lost if it
// waits long enough for anything to finish.
const settle = 3 * time.Second

func verify(opts ...goleak.Option) error {
	deadline := time.Now().Add(settle)
	var err error
	for {
		if err = goleak.Find(opts...); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ignored lists goroutines that are not leaks.
//
// Every entry here is a promise that the goroutine either belongs to something
// outside doze-aws's control or is genuinely transient, and each says which.
// Adding to this list to make a failure go away is how a leak detector stops
// detecting leaks, so an entry that is not explained does not belong.
func ignored() []goleak.Option {
	return []goleak.Option{
		// net/http keeps idle connections and their readers alive after a
		// response is delivered. httptest.Server.Close does not wait for the
		// client side of a keep-alive connection to notice.
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
		// net/http's server-side disconnect detection: while a handler is
		// running, the server reads the connection in the background so it can
		// notice the client going away. It ends with the connection, which
		// httptest.Server.Close does not wait for. Same category as the two
		// above — net/http's goroutine, net/http's lifecycle.
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		// The runtime's own finalizer goroutine.
		goleak.IgnoreTopFunction("runtime.gopark"),
		// os/exec reaps child processes — Lambda runtimes — on its own
		// schedule, after Wait has already returned to us.
		goleak.IgnoreTopFunction("os/exec.(*Cmd).watchCtx"),
	}
}

// NoFaults fails the test if the stack answered any 5xx.
//
// A server fault during a request a test believes is valid is a bug by
// definition — that is what makes this assertable at all without knowing
// anything about what the test was doing.
//
// Register it with t.Cleanup so it runs whatever the test does:
//
//	t.Cleanup(func() { dozetest.NoFaults(t, stack) })
//
// Generic over the fault type rather than naming it: this package must not
// import the root, which imports every service, and a declared mirror of
// dozeaws.Fault would not satisfy `Faults() []dozeaws.Fault` anyway — Go
// matches the slice's element type by name, not by shape. The fault is printed
// with %+v, which is why no accessor is needed.
func NoFaults[F any](t testing.TB, s interface{ Faults() []F }) {
	t.Helper()
	faults := s.Faults()
	if len(faults) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "the stack answered %d server fault(s) during this test:\n", len(faults))
	for _, f := range faults {
		fmt.Fprintf(&b, "  %+v\n", f)
	}
	b.WriteString("a 5xx during a request the test believes is valid is a bug in doze-aws")
	t.Error(b.String())
}

// PanicWatcher wraps a Logf and remembers whether any background goroutine
// reported a recovered panic through it.
//
// internal/bg contains a panic on a background goroutine rather than letting it
// kill the process, which is right for a development tool — but it means a
// panicking sweeper produces a log line and an otherwise green test run. The
// line is distinctive, so watching for it turns a contained panic back into a
// failure without giving up the containment.
type PanicWatcher struct {
	mu     sync.Mutex
	seen   []string
	inner  func(string, ...any)
	marker string
}

// Logf returns a log function to hand to any Options.Logf, forwarding to
// t.Logf and failing the test if a background goroutine reports a recovered
// panic.
//
// It registers its own t.Cleanup, so adopting it is a one-word change at the
// call site and nothing else — which is the only reason it reached a hundred
// and eighteen of them. A helper that also needed a defer would have been
// adopted in three.
func Logf(t testing.TB) func(string, ...any) {
	w := &PanicWatcher{inner: t.Logf, marker: panicMarker}
	t.Cleanup(func() { w.Check(t) })
	return w.logf
}

// Quiet is Logf without the forwarding: the panic check, and nothing in the
// output. For tests that would otherwise bury their own failure in log lines.
func Quiet(t testing.TB) func(string, ...any) {
	w := &PanicWatcher{marker: panicMarker}
	t.Cleanup(func() { w.Check(t) })
	return w.logf
}

// Watcher is Quiet for a fixture that has no *testing.T to hang a Cleanup on —
// a package-level stack shared by a fuzz target, in practice. The caller owns
// the checking and must call Check itself.
//
// A zero PanicWatcher is NOT usable in its place: its marker would be the empty
// string, every log line would match it, and the check would fail on the first
// line of any output. Hence a constructor rather than an exported field.
func Watcher() *PanicWatcher { return &PanicWatcher{marker: panicMarker} }

// Logf returns the log function to hand to Options.Logf.
func (w *PanicWatcher) Logf() func(string, ...any) { return w.logf }

// panicMarker is the distinctive part of what internal/bg writes when it
// contains a panic. If that wording changes, this finds nothing and every test
// silently stops watching — which TestThePanicMarkerStillMatches guards.
const panicMarker = "recovered from a panic"

func (w *PanicWatcher) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if strings.Contains(line, w.marker) {
		w.mu.Lock()
		w.seen = append(w.seen, line)
		w.mu.Unlock()
	}
	if w.inner != nil {
		w.inner(format, args...)
	}
}

// Check fails the test if any background goroutine recovered from a panic.
func (w *PanicWatcher) Check(t testing.TB) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range w.seen {
		t.Errorf("a background goroutine panicked during this test:\n%s", line)
	}
}
