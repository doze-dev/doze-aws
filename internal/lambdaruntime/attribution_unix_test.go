//go:build unix

package lambdaruntime

import (
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

// What a function prints before it asks for work belongs to no invocation.
//
// The pump reads the child's pipe on its own schedule, and a line is attributed
// by whichever invocation is current at the moment it is READ. If the first
// fetch makes its invocation current before the pipe is read, a slow reader
// gives the init output the invocation's request id — which is how a macOS CI
// runner failed TestOutputIsAttributedToItsInvocation. The reader is stalled
// here on purpose, the way a busy machine stalls it, so the order of the two
// events is the test's and not the scheduler's.
func TestInitOutputIsReadBeforeTheFirstFetchBecomesCurrent(t *testing.T) {
	sink := &recordingSink{}
	r := NewRunner(Spec{Name: "stalled", Runtime: "provided.al2023", Dir: t.TempDir(), Timeout: 5 * time.Second, LogSink: sink}, t.Logf)
	r.stream = "stream"
	r.out = newLineSplitter(r.logTail, sink, r.stream, r.currentID)
	pump, err := newOutputPump(exec.Command("true"), r.out)
	if err != nil {
		t.Fatal(err)
	}
	defer pump.close()
	r.pump = pump

	pump.mu.Lock() // the reader cannot deliver anything while this is held
	if _, err := pump.w.Write([]byte("booting\n")); err != nil {
		t.Fatal(err)
	}

	inv := &invocation{id: "req-1", done: make(chan Result, 1), dispatched: make(chan struct{})}
	r.queue <- inv
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r.handleNext(httptest.NewRecorder(), httptest.NewRequest("GET", "/2018-06-01/runtime/invocation/next", nil))
	}()

	time.Sleep(150 * time.Millisecond)
	if got := r.currentID(); got != "" {
		t.Errorf("the invocation became current (%q) while the function's earlier output was still unread", got)
	}
	pump.mu.Unlock()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch never completed")
	}

	lines, _ := sink.snapshot()
	for _, l := range lines {
		if l.text == "booting" {
			if l.rid != "" {
				t.Errorf("init output carries request id %q, want none", l.rid)
			}
			return
		}
	}
	t.Errorf("init output was never delivered: %+v", lines)
}
