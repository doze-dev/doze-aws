package stepfunctions

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/asl"
)

// Express workflows and TestState share one mechanism: a volatile execution
// the same driver runs, with a waiter the calling handler blocks on. Nothing
// about the interpreter or the driver loop is different for them — the store
// keeps the record in memory instead of bbolt, tokens are not minted (an
// Express execution cannot park on a task token on AWS either), and a
// TestState run stops at the first state boundary instead of running on.
//
// The 5-minute cap is AWS's: an Express execution that runs longer times out.
const expressMaxDuration = 5 * time.Minute

// waiters are the channels StartSyncExecution and TestState block on, keyed
// by run key, closed by finalize. Kept off the driver-owned run map because
// handlers register them before the driver has a run.
type waiters struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}

func (w *waiters) add(key string) chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.m == nil {
		w.m = map[string]chan struct{}{}
	}
	ch := make(chan struct{})
	w.m[key] = ch
	return ch
}

// release closes and forgets the waiter for key; reports whether one existed.
func (w *waiters) release(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch, ok := w.m[key]
	if ok {
		close(ch)
		delete(w.m, key)
	}
	return ok
}

func (w *waiters) releaseAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, ch := range w.m {
		close(ch)
		delete(w.m, k)
	}
}

// envFor is the interpreter environment for one run. A volatile run gets no
// token minter: Express has no SendTaskSuccess to redeem one, and the
// interpreter fails a .waitForTaskToken Task with States.Runtime when it
// cannot mint — which is the honest local shape of AWS refusing the pattern
// on Express.
func (g *engine) envFor(r *run) asl.Env {
	env := g.env()
	if r != nil && r.e.Volatile {
		env.NewToken = nil
	}
	return env
}

// finished is finalize's tail for volatile runs: wake the waiter if there is
// one, otherwise nobody will ever read the record and it is dropped now.
func (g *engine) finished(r *run) {
	if !r.e.Volatile {
		return
	}
	if !g.waiters.release(r.key) {
		g.srv.store.DropVolatile(r.key)
	}
}

// ---- TestState ----

// TestOutcome is what a TestState run leaves on its volatile execution for
// the handler to read: the state's outcome in the API's vocabulary.
type TestOutcome struct {
	Status    string `json:"status"` // SUCCEEDED | FAILED | RETRIABLE | CAUGHT_ERROR
	NextState string `json:"next_state,omitempty"`
	Error     string `json:"error,omitempty"`
	Cause     string `json:"cause,omitempty"`
	// Result is the task result the state received (a Task's response, a
	// Parallel's or Map's joined output), for inspectionData.result.
	Result json.RawMessage `json:"result,omitempty"`
	// TaskInput is what the Task sent, for inspectionData.request.
	TaskInput json.RawMessage `json:"task_input,omitempty"`
}

// testRun rides on a run whose execution is a TestState: which state is under
// test, the mock that stands in for its Task/Map/Parallel work if any, and
// the outcome as it becomes known.
type testRun struct {
	state string
	mock  *asl.TaskResult
	out   TestOutcome
}

// testStep inspects one interpreter step of a TestState run and, when the
// state under test has reached its boundary, ends the run with the outcome
// AWS would report. Returns true when it ended the run.
func (g *engine) testStep(r *run, eff asl.Effect, notes []asl.Note) bool {
	if r.test == nil {
		return false
	}
	root := r.e.Exec.Root()
	for _, n := range notes {
		switch n.Kind {
		case asl.NoteCaught:
			var ec struct{ Error, Cause string }
			json.Unmarshal(n.Data, &ec)
			r.test.out = TestOutcome{Status: "CAUGHT_ERROR", NextState: root.State, Error: ec.Error, Cause: ec.Cause,
				Result: r.test.out.Result, TaskInput: r.test.out.TaskInput}
			g.finishTest(r, root.Input)
			return true
		case asl.NoteRetried:
			var ec struct{ Error, Cause string }
			json.Unmarshal(n.Data, &ec)
			// AWS reports RETRIABLE without waiting out the backoff.
			r.test.out = TestOutcome{Status: "RETRIABLE", Error: ec.Error, Cause: ec.Cause,
				Result: r.test.out.Result, TaskInput: r.test.out.TaskInput}
			g.finishTest(r, nil)
			return true
		}
	}
	switch e := eff.(type) {
	case asl.EffContinue:
		if root.State != r.test.state {
			r.test.out.Status, r.test.out.NextState = "SUCCEEDED", root.State
			g.finishTest(r, root.Input)
			return true
		}
	case asl.EffSleep:
		// A Wait under test does not wait: the API reports where it would
		// go next, now.
		if root.Status == asl.FrameSleeping {
			root.WakeAt = 0
			eff2, notes2, err := asl.Wake(r.def, r.e.Exec, root, g.envFor(r))
			if err != nil {
				g.finalize(r, "FAILED", nil, "States.Runtime", err.Error())
				return true
			}
			g.recordNotes(r, notes2)
			if g.testStep(r, eff2, notes2) {
				return true
			}
			g.apply(r, eff2)
			return r.e.Status != "RUNNING"
		}
	case asl.EffDone, asl.EffFail:
		// The root finishing is the normal finalize; the outcome is read from
		// the execution's own status. Record it for the handler.
		_ = e
	}
	return false
}

// finishTest ends a TestState run SUCCEEDED-as-an-execution with the state's
// output, leaving the API-level outcome on the record.
func (g *engine) finishTest(r *run, output json.RawMessage) {
	out := r.test.out
	r.e.TestOutcome = &out
	g.finalize(r, "SUCCEEDED", output, "", "")
}

// testMock answers a Task, Map or Parallel under test with the mocked
// result instead of doing the work. Returns true when it did.
func (g *engine) testMock(r *run, frame int) bool {
	if r.test == nil || r.test.mock == nil {
		return false
	}
	f := r.e.Exec.Frame(frame)
	if f == nil {
		return false
	}
	asl.AbandonSiblings(r.e.Exec, f)
	res := *r.test.mock
	r.test.out.Result = res.Output
	g.deliver(r, f, res)
	return true
}

// recordTestTask keeps a Task's request for inspectionData.
func (g *engine) recordTestTask(r *run, call asl.EffCallTask) {
	if r.test != nil {
		r.test.out.TaskInput = call.Input
	}
}

// recordTestResult keeps a Task's response for inspectionData.
func (g *engine) recordTestResult(r *run, res asl.TaskResult) {
	if r.test != nil && res.Failure == nil {
		r.test.out.Result = res.Output
	}
}

// ---- the handler side ----

// startVolatile persists a volatile execution, registers a waiter, and
// nudges the driver. The caller blocks on the returned channel.
func (s *Server) startVolatile(e *Execution) (<-chan struct{}, error) {
	e.Volatile = true
	key := e.Key()
	ch := s.engine.waiters.add(key)
	if err := s.store.SaveTransition(e, nil, nil); err != nil {
		s.engine.waiters.release(key)
		return nil, err
	}
	s.logs.record(e, []histEvent{startedEvent(e)})
	s.engine.nudge(key)
	return ch, nil
}

// awaitVolatile waits for a volatile execution to finish and hands back its
// final record, dropped from memory. A caller that stops waiting (context
// cancelled) leaves the run to finish on its own and be dropped by finished.
func (s *Server) awaitVolatile(ctx context.Context, key string, ch <-chan struct{}) (*Execution, bool) {
	select {
	case <-ch:
	case <-ctx.Done():
		s.engine.waiters.release(key)
		return nil, false
	case <-s.engine.stop:
		return nil, false
	}
	e, err := s.store.GetExecutionByKey(key)
	s.store.DropVolatile(key)
	if err != nil || e == nil {
		return nil, false
	}
	return e, true
}

// expressExecARN is arn:aws:states:<r>:<a>:express:<machine>:<name>:<id>,
// the form AWS gives an Express execution. The id is what lets two Express
// executions share a name, which AWS allows and Standard does not.
func expressExecARN(ident awsident.Identity, machineName, execName, id string) string {
	return strings.Replace(execARN(ident, machineName, execName), ":execution:", ":express:", 1) + ":" + id
}

// parseExpressARN splits an Express execution ARN.
func parseExpressARN(arn string) (machine, name, id string) {
	parts := strings.SplitN(arn, ":", 9)
	if len(parts) != 9 || parts[5] != "express" {
		return "", "", ""
	}
	return parts[6], parts[7], parts[8]
}
