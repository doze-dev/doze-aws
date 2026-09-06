package stepfunctions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math"
	mathrand "math/rand"
	"sync"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/trace"
)

// The execution engine: one driver goroutine owns every interpreter step,
// every write to the execution bucket, and the in-memory run registry.
// Blocking task calls (stage G4) run on transient workers that touch nothing
// but the peer directory and hand their result back over a channel — so a
// slow Lambda never starves a Wait wakeup, and the "goroutine writes to a
// closed bbolt" panic is structurally impossible: only the driver writes, and
// Close waits for the driver.
//
// Handlers reach the engine three ways, none of which shares memory:
// StartExecution persists then nudges; StopExecution asks and waits for the
// reply; SendTask* (stage G7) reads the token bucket then enqueues.

// run is one RUNNING execution the driver holds: the record and its parsed
// definition snapshot. Only the driver goroutine touches a run.
type run struct {
	key string
	def *asl.Definition
	e   *Execution
	// pending buffers history events, and tokens the token writes, between
	// persists; SaveTransition applies them and the execution record in one
	// transaction.
	pending []histEvent
	tokens  []tokenOp
}

type deliveryKind int

const (
	dlvStop deliveryKind = iota
	dlvTaskReturn
	dlvSendTask
	dlvHeartbeat
)

// delivery is one event headed for the driver. reply, when non-nil, is
// closed once the delivery has been applied — StopExecution waits on it so
// the stop it reports has actually happened.
type delivery struct {
	key     string
	frame   int
	kind    deliveryKind
	result  asl.TaskResult
	errName string
	cause   string
	// attempt is the frame's retry count at dispatch; a result arriving after
	// the frame has moved on (a late worker racing a timeout's retry) is
	// detectably stale and dropped.
	attempt int
	reply   chan struct{}
}

type engine struct {
	srv  *Server
	runs map[string]*run // driver-owned; nothing else may touch it

	nudges     chan string
	deliveries chan delivery
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	cancel     context.CancelFunc
	workerCtx  context.Context // cancelled on close; aborts in-flight peercalls
	wg         sync.WaitGroup  // transient task workers
}

func newEngine(srv *Server) *engine {
	ctx, cancel := context.WithCancel(context.Background())
	g := &engine{
		srv:        srv,
		runs:       map[string]*run{},
		nudges:     make(chan string, 128),
		deliveries: make(chan delivery, 128),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		cancel:     cancel,
		workerCtx:  ctx,
	}
	go g.loop(ctx)
	return g
}

// close stops the driver and waits for it and every worker — only after this
// returns may the bbolt store be closed.
func (g *engine) close() {
	g.stopOnce.Do(func() {
		g.cancel()
		close(g.stop)
		<-g.done
		g.wg.Wait()
	})
}

// nudge tells the driver an execution has work. Safe from any goroutine; a
// nudge lost to shutdown is fine, because restart re-drives every RUNNING
// execution.
func (g *engine) nudge(key string) {
	select {
	case g.nudges <- key:
	case <-g.stop:
	}
}

// stopExec asks the driver to abort an execution and waits until it has.
// Reports false when the engine is shutting down.
func (g *engine) stopExec(key, errName, cause string) bool {
	reply := make(chan struct{})
	d := delivery{key: key, kind: dlvStop, errName: errName, cause: cause, reply: reply}
	select {
	case g.deliveries <- d:
	case <-g.stop:
		return false
	}
	select {
	case <-reply:
		return true
	case <-g.stop:
		return false
	}
}

// env builds the interpreter's environment from the store clock.
func (g *engine) env() asl.Env {
	return asl.Env{
		Now:      g.srv.store.clock(),
		Rand:     mathrand.Float64,
		NewToken: newToken,
	}
}

// newToken mints a task token: opaque, unguessable, long enough that AWS's
// 1024-char limit obviously holds.
func newToken() string {
	var b [24]byte
	rand.Read(b[:])
	return "dz" + hex.EncodeToString(b[:])
}

// ensure returns the run for key, loading and parsing the snapshot on a
// miss. Loading on miss is also what restart-resume is: the startup sweep
// just nudges every RUNNING key. Returns nil when the execution is absent or
// no longer RUNNING.
func (g *engine) ensure(key string) *run {
	if r, ok := g.runs[key]; ok {
		return r
	}
	e, err := g.srv.store.GetExecutionByKey(key)
	if err != nil || e == nil || e.Status != "RUNNING" {
		return nil
	}
	def, perr := asl.Parse([]byte(e.Definition))
	if perr != nil {
		// The snapshot was validated at create time; failing here means the
		// store is damaged. Fail the execution rather than wedge it.
		e.Status = "FAILED"
		e.Error = "States.Runtime"
		e.Cause = "the definition snapshot no longer parses: " + perr.Error()
		e.StoppedAt = g.srv.store.now()
		g.srv.store.PutExecution(e)
		return nil
	}
	r := &run{key: key, def: def, e: e}
	g.runs[key] = r
	return r
}

// drive advances a run until nothing is runnable: the inner loop. Persists
// after every interpreter call — that cadence is the durability contract.
func (g *engine) drive(r *run) {
	if r == nil {
		return
	}
	for r.e.Status == "RUNNING" {
		f := g.runnableFrame(r)
		if f == nil {
			g.persist(r)
			return
		}
		eff, notes, err := asl.Advance(r.def, r.e.Exec, f, g.env())
		g.recordNotes(r, notes)
		if err != nil {
			g.finalize(r, "FAILED", nil, "States.Runtime", err.Error())
			return
		}
		g.apply(r, eff)
	}
}

// runnableFrame picks the next frame to advance. With Parallel and Map
// (stage G6) this becomes concurrency-aware; the scan is already general.
func (g *engine) runnableFrame(r *run) *asl.Frame {
	for _, f := range r.e.Exec.Frames {
		if f.Status == asl.FrameRunnable {
			return f
		}
	}
	return nil
}

// apply performs one effect. Every branch persists before anything else can
// observe the transition.
func (g *engine) apply(r *run, eff asl.Effect) {
	switch e := eff.(type) {
	case asl.EffContinue:
		g.persist(r)
	case asl.EffSleep:
		g.persist(r)
	case asl.EffDone:
		if e.Frame == 1 {
			g.finalize(r, "SUCCEEDED", e.Output, "", "")
			return
		}
		g.persist(r)
		g.childSettled(r, e.Frame)
	case asl.EffFail:
		if e.Frame == 1 {
			g.finalize(r, "FAILED", nil, e.Failure.Name, e.Failure.Cause)
			return
		}
		g.persist(r)
		g.childSettled(r, e.Frame)
	case asl.EffCallTask:
		// Persist BEFORE the call goes out: a worker whose answer arrives
		// instantly must find the frame already CALLING (or the token already
		// redeemable), and a crash between here and the send re-dispatches.
		g.taskScheduledEvents(r, r.e.Exec.Frame(e.Frame), e)
		if e.Token != "" {
			g.registerToken(r, e)
		}
		g.persist(r)
		g.dispatch(r, e)
	case asl.EffSpawn:
		g.spawnEvents(r, e)
		g.persist(r)
		// A Map over zero items has no child to settle; the join decides now.
		if len(e.Frames) == 0 {
			if parent := r.e.Exec.Frame(e.Parent); parent != nil {
				if res, done := asl.JoinReady(r.def, r.e.Exec, parent); done {
					g.deliver(r, parent, res)
				}
			}
		}
	}
}

// spawnEvents records a Parallel/Map start and re-roots each child's history
// chain on the Started event — that is what makes previousEventId causal
// along a branch while ids interleave globally.
func (g *engine) spawnEvents(r *run, e asl.EffSpawn) {
	parent := r.e.Exec.Frame(e.Parent)
	if parent == nil {
		return
	}
	s := r.stateOf(parent, parent.State)
	if s == nil {
		return
	}
	switch s.Type {
	case asl.Parallel:
		g.event(r, parent, "ParallelStateStarted", "", nil)
		for _, id := range e.Frames {
			if c := r.e.Exec.Frame(id); c != nil {
				c.PrevEventID = parent.PrevEventID
			}
		}
	case asl.Map:
		g.event(r, parent, "MapStateStarted", "mapStateStartedEventDetails", map[string]any{
			"length": len(e.Frames),
		})
		for _, id := range e.Frames {
			c := r.e.Exec.Frame(id)
			if c == nil {
				continue
			}
			c.PrevEventID = parent.PrevEventID
			g.event(r, c, "MapIterationStarted", "mapIterationStartedEventDetails", map[string]any{
				"name": s.Name, "index": c.Branch,
			})
		}
	}
}

// childSettled runs the join protocol when a Parallel branch or Map item
// finishes: promote pending siblings, check the join, and on a decision
// abandon the survivors and deliver the result to the parent.
func (g *engine) childSettled(r *run, frameID int) {
	f := r.e.Exec.Frame(frameID)
	if f == nil || f.Parent == 0 {
		return
	}
	parent := r.e.Exec.Frame(f.Parent)
	if parent == nil || parent.Status != asl.FrameJoin {
		return
	}
	s := r.stateOf(parent, parent.State)
	if s != nil && s.Type == asl.Map {
		typ, key := "MapIterationSucceeded", "mapIterationSucceededEventDetails"
		if f.Status == asl.FrameFailed {
			typ, key = "MapIterationFailed", "mapIterationFailedEventDetails"
		}
		g.event(r, parent, typ, key, map[string]any{"name": parent.State, "index": f.Branch})
	}
	asl.PromotePending(r.def, r.e.Exec, parent)
	res, done := asl.JoinReady(r.def, r.e.Exec, parent)
	if !done {
		g.persist(r)
		return
	}
	asl.AbandonSiblings(r.e.Exec, parent)
	if s != nil {
		prefix := "ParallelState"
		if s.Type == asl.Map {
			prefix = "MapState"
		}
		if res.Failure == nil {
			g.event(r, parent, prefix+"Succeeded", "", nil)
		} else {
			g.event(r, parent, prefix+"Failed", "", nil)
		}
	}
	g.deliver(r, parent, res)
}

// dispatch performs a task call on a transient worker. The worker touches no
// store — it runs the peercall and hands the result back over the deliveries
// channel; the driver applies it.
func (g *engine) dispatch(r *run, call asl.EffCallTask) {
	attempt := 0
	if f := r.e.Exec.Frame(call.Frame); f != nil {
		attempt = f.RetryCount()
	}
	key, header := r.key, r.e.TraceHeader
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ctx := trace.Continue(g.workerCtx, g.srv.sink, header)
		res := g.srv.performTask(ctx, call.Resource, call.Input)
		if call.Token != "" && res.Failure == nil {
			// A token task's send succeeded; the real result arrives via
			// SendTaskSuccess. Only a failed send is worth delivering.
			return
		}
		select {
		case g.deliveries <- delivery{key: key, frame: call.Frame, kind: dlvTaskReturn, result: res, attempt: attempt}:
		case <-g.stop:
			// Close in progress: drop the result. The frame is persisted
			// CALLING, so a restart re-dispatches — at-least-once, like AWS.
		}
	}()
}

func (g *engine) persist(r *run) {
	events, tokens := r.pending, r.tokens
	r.pending, r.tokens = nil, nil
	if err := g.srv.store.SaveTransition(r.e, events, tokens); err != nil {
		g.srv.logf("stepfunctions: persist %s: %v", r.e.ARN, err)
	}
}

// finalize ends an execution, records its terminal event, and forgets its
// run.
func (g *engine) finalize(r *run, status string, output []byte, errName, cause string) {
	r.e.Status = status
	r.e.StoppedAt = g.srv.store.now()
	r.e.Output = output
	r.e.Error = errName
	r.e.Cause = cause
	switch status {
	case "SUCCEEDED":
		g.event(r, nil, "ExecutionSucceeded", "executionSucceededEventDetails", map[string]any{
			"output": string(output), "outputDetails": notTruncated(),
		})
	case "FAILED":
		g.event(r, nil, "ExecutionFailed", "executionFailedEventDetails", map[string]any{
			"error": errName, "cause": cause,
		})
	case "ABORTED":
		g.event(r, nil, "ExecutionAborted", "executionAbortedEventDetails", map[string]any{
			"error": errName, "cause": cause,
		})
	case "TIMED_OUT":
		g.event(r, nil, "ExecutionTimedOut", "executionTimedOutEventDetails", map[string]any{
			"error": errName, "cause": cause,
		})
	}
	g.persist(r)
	delete(g.runs, r.key)
}

// applyDelivery routes one delivery to its run.
func (g *engine) applyDelivery(d delivery) {
	defer func() {
		if d.reply != nil {
			close(d.reply)
		}
	}()
	r := g.ensure(d.key)
	if r == nil {
		return
	}
	switch d.kind {
	case dlvStop:
		g.finalize(r, "ABORTED", nil, d.errName, d.cause)
	case dlvHeartbeat:
		f := r.e.Exec.Frame(d.frame)
		if f == nil || f.Status != asl.FrameParked {
			return
		}
		f.HeartbeatAt = g.srv.store.now()
		g.persist(r)
	case dlvTaskReturn, dlvSendTask:
		f := r.e.Exec.Frame(d.frame)
		if f == nil || (f.Status != asl.FrameCalling && f.Status != asl.FrameParked) {
			return // stale: the frame moved on (timed out, was stopped)
		}
		if d.kind == dlvTaskReturn && d.attempt != f.RetryCount() {
			return // stale: a result from a previous attempt
		}
		g.deliver(r, f, d.result)
	}
}

// registerToken buffers the token row for a parked frame — written by the
// same SaveTransition that persists the PARKED status.
func (g *engine) registerToken(r *run, call asl.EffCallTask) {
	_, resourceType, resourceAPI := taskFamily(call.Resource)
	g.event(r, r.e.Exec.Frame(call.Frame), "TaskSubmitted", "taskSubmittedEventDetails", map[string]any{
		"resourceType": resourceType, "resource": resourceAPI,
	})
	r.tokens = append(r.tokens, tokenOp{Token: call.Token, Ref: &TokenRef{
		ExecKey: r.key, Frame: call.Frame, IssuedAt: g.srv.store.now(),
	}})
}

// deliver feeds a task result into a frame and keeps driving. A token the
// frame held stops being redeemable in the same transaction that applies the
// result — unless the failure was retried, in which case the token stays
// live for the re-dispatch.
func (g *engine) deliver(r *run, f *asl.Frame, res asl.TaskResult) {
	if res.Failure == nil {
		if s := r.stateOf(f, f.State); s != nil && s.Type == asl.Task {
			g.taskSuccessEvent(r, f, s.Resource, res.Output)
		}
	}
	hadToken := f.Token
	eff, notes, err := asl.Deliver(r.def, r.e.Exec, f, res, g.env())
	g.recordNotes(r, notes)
	if err != nil {
		g.finalize(r, "FAILED", nil, "States.Runtime", err.Error())
		return
	}
	// The token dies with the frame's park: a state exit or catch clears
	// f.Token, an uncaught failure leaves it on a terminal frame; only a
	// retry keeps the park alive for the re-dispatch.
	if hadToken != "" && (f.Token != hadToken || f.Status.Terminal()) {
		r.tokens = append(r.tokens, tokenOp{Token: hadToken})
	}
	g.apply(r, eff)
	if r.e.Status == "RUNNING" {
		g.drive(r)
	}
}

// timeoutFailure is what an execution-level deadline reports.
func mustPositive(v float64) int64 {
	if v <= 0 || math.IsNaN(v) {
		return 0
	}
	return int64(v * 1000)
}
