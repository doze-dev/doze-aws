package stepfunctions

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// The driver owns every execution's state, and both ways of reaching it —
// nudge and stopExec — hand work over a buffered channel and otherwise wait on
// g.stop. A panic does not close g.stop.
//
// So a dead driver used to mean: the nudge buffer fills and then every
// StartExecution blocks FOREVER; stopExec waits on a reply nothing will ever
// close; and close() returns cleanly, because loop's deferred close(g.done)
// fires on a panic unwind just as happily as on a clean return. Step Functions
// stops answering with no error and no crash.
//
// internal/bg documents exactly this and says the cleanup is mandatory for a
// queue-draining worker. This driver — whose own comment said "a panic would
// silently stop every running execution while close() reported a clean
// shutdown" — was the one worker of that shape without one.
//
// The engine is built here WITHOUT starting loop, which is what a driver that
// has panicked actually looks like: the channels exist, g.stop is open, and
// nothing is draining. Tiny buffers so "does not block" means something.
func deadEngine(t *testing.T) (*engine, func() string) {
	t.Helper()

	var mu sync.Mutex
	var said []string
	srv := &Server{logf: func(format string, args ...any) {
		mu.Lock()
		said = append(said, fmt.Sprintf(format, args...))
		mu.Unlock()
	}}

	g := &engine{
		srv:        srv,
		runs:       map[string]*run{},
		nudges:     make(chan string, 2),
		deliveries: make(chan delivery, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		dead:       make(chan struct{}),
	}
	g.die() // what bg.Recover calls when the driver panics

	return g, func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(said, "\n")
	}
}

func TestADeadDriverReportsRatherThanBlockingOnNudge(t *testing.T) {
	g, transcript := deadEngine(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Well past the buffer: a version that only selects on nudges and stop
		// fills it and then waits for a drainer that no longer exists.
		for i := range 20 {
			g.nudge(fmt.Sprintf("machine:exec%d", i))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("nudge blocked on a dead driver — this is the wedge")
	}

	got := transcript()
	if !strings.Contains(got, "not running") {
		t.Errorf("a dropped nudge was not reported:\n%s", got)
	}
	if !strings.Contains(got, "restart") {
		t.Errorf("the report does not say what would fix it:\n%s", got)
	}
}

func TestADeadDriverDoesNotStrandStopExec(t *testing.T) {
	g, _ := deadEngine(t)

	stopped := make(chan bool, 1)
	go func() { stopped <- g.stopExec("machine:exec", "Stopped", "by test") }()

	select {
	case ok := <-stopped:
		// False, not true: nothing stopped the execution, and saying otherwise
		// would make StopExecution lie to its caller.
		if ok {
			t.Error("stopExec claimed success with no driver to do the stopping")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopExec blocked on a dead driver — the reply channel is the driver's to close")
	}
}
