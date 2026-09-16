package dozetest

// The panic watcher is coupled to a string internal/bg writes. That coupling is
// the whole mechanism, and it is invisible: if bg's wording changes, the watcher
// matches nothing, every test using it silently stops watching, and the suite
// stays green while the assertion is gone.
//
// So the coupling is asserted against the real thing rather than against a copy
// of the string.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/bg"
)

// recorder captures what a logger was told, standing in for a *testing.T.
type recorder struct {
	testing.TB
	lines  []string
	failed []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failed = append(r.failed, format)
}

// Error as well as Errorf: NoFaults builds its message and calls Error, and a
// recorder that only intercepts Errorf lets the real t fail instead — which is
// exactly what happened the first time.
func (r *recorder) Error(args ...any) {
	r.failed = append(r.failed, fmt.Sprint(args...))
}
func (r *recorder) Cleanup(func()) {}

func TestThePanicMarkerStillMatches(t *testing.T) {
	var got []string
	logf := func(format string, args ...any) {
		got = append(got, format)
	}

	// Drive a real contained panic through the real bg.Recover, rather than
	// asserting against a hand-copied string.
	func() {
		defer bg.Recover(logf, "dozetest: deliberate")
		panic("boom")
	}()

	if len(got) == 0 {
		t.Fatal("bg.Recover logged nothing for a contained panic")
	}
	for _, line := range got {
		if strings.Contains(line, panicMarker) {
			return
		}
	}
	t.Errorf("internal/bg no longer writes %q, so PanicWatcher matches nothing and every\n"+
		"test using dozetest.Logf has silently stopped watching for panics.\nbg wrote: %q",
		panicMarker, got)
}

// The watcher fires on a contained panic and stays quiet otherwise — the two
// halves that make it worth having.
func TestPanicWatcherFiresOnlyOnAPanic(t *testing.T) {
	t.Run("fires", func(t *testing.T) {
		w := &PanicWatcher{marker: panicMarker}
		func() {
			defer bg.Recover(w.logf, "dozetest: deliberate")
			panic("boom")
		}()
		r := &recorder{TB: t}
		w.Check(r)
		if len(r.failed) == 0 {
			t.Error("a contained panic did not fail the test")
		}
	})

	t.Run("quiet", func(t *testing.T) {
		w := &PanicWatcher{marker: panicMarker}
		w.logf("sqs: janitor swept %d messages", 3)
		w.logf("doze-aws: answered 500 InternalFailure — request id abc")
		r := &recorder{TB: t}
		w.Check(r)
		if len(r.failed) != 0 {
			t.Errorf("ordinary log lines failed the test: %v", r.failed)
		}
	})
}

// NoFaults reports what it found, and says nothing when there is nothing.
func TestNoFaultsReportsEveryFault(t *testing.T) {
	t.Run("quiet when clean", func(t *testing.T) {
		r := &recorder{TB: t}
		NoFaults(r, stubFaulter{})
		if len(r.failed) != 0 {
			t.Errorf("a clean stack failed: %v", r.failed)
		}
	})

	t.Run("fails when faulted", func(t *testing.T) {
		r := &recorder{TB: t}
		NoFaults(r, stubFaulter{{RequestID: "a", Code: "InternalFailure", Status: 500}})
		if len(r.failed) == 0 {
			t.Error("a stack that answered a 500 did not fail the test")
		}
	})
}

// stubFault stands in for dozeaws.Fault, which this package cannot import.
type stubFault struct {
	RequestID string
	Code      string
	Status    int
}

type stubFaulter []stubFault

func (s stubFaulter) Faults() []stubFault { return s }
