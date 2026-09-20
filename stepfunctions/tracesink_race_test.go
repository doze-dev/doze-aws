package stepfunctions

import (
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/trace"
)

// nopSink is a Sink that records nothing; the test is about the FIELD, not
// about what is emitted through it.
type nopSink struct{}

func (nopSink) ReserveCascade() trace.Cause { return 0 }
func (nopSink) EmitCascade(trace.Event)     {}

// SetTraceSink writes a field that already-running goroutines read, and it is
// called AFTER those goroutines start.
//
// The engine's driver goroutine is launched inside New (engine.go), and the
// first thing loop does is resume every RUNNING execution — which reaches
// dispatch and reads srv.sink. The binary calls SetTraceSink much later:
// cmd/doze-aws/main.go builds the regions at :375 and sets the sink at :457.
// So a data directory holding one RUNNING Standard execution is enough for the
// resume to be reading the field while main writes it.
//
// trace.Sink is an INTERFACE — two words — so a torn read can pair a new itab
// with an old data pointer. That is a segfault inside trace.Continue, not a
// clean 500.
//
// Run with -race, which is what `task check:full` and CI do. Before the fix
// this reports a data race between the write here and the read in dispatch;
// it is deterministic enough to catch because the loop is tight.
func TestSetTraceSinkIsSafeWhileTheEngineRuns(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), &testClock{now: time.Now()})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// A reader standing in for the driver goroutine's dispatch path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = srv.traceSink()
			}
		}
	}()

	// The writer, as main does it: after the engine is already running.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			srv.SetTraceSink(nopSink{})
			srv.SetTraceSink(nil)
		}
		close(stop)
	}()

	wg.Wait()
}
