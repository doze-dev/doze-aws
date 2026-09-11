package stepfunctions

import (
	"context"
	"time"

	"github.com/doze-dev/doze-aws/internal/asl"
)

// The driver loop and its single ticker — the same shape as eventbridge's
// scheduler: one goroutine, one time.Ticker, persisted state as the only
// schedule. No timer heap and no timer bucket: the frames' WakeAt/Deadline
// fields are compared against the clock every second, which is AWS's own
// resolution for Wait, and can never desync from the frames because the
// frames are the schedule.

func (g *engine) loop(ctx context.Context) {
	defer close(g.done)

	// Resume: every RUNNING execution is re-driven exactly the way a nudge
	// would. Restart is not a special case — that is the point of the
	// snapshot contract. Keys are collected before driving: drive persists,
	// and a bbolt Update inside the scan's View transaction deadlocks.
	var resume []string
	g.srv.store.EachRunning(func(key string) { resume = append(resume, key) })
	for _, key := range resume {
		g.drive(g.ensure(key))
	}

	// The ticker runs only while there is something to time.
	//
	// fireDue walks g.runs, so with no loaded executions it wakes the process
	// once a second to look at an empty map. On an idle stack that was most of
	// the wakeups doze-aws made, and a wakeup a second is what stops a laptop's
	// CPU reaching its deeper idle states.
	//
	// A stopped ticker's channel is never sent on, and receiving from the nil
	// channel that replaces it blocks forever, so an idle loop parks on nudges
	// and deliveries alone. Work only ever arrives through those two, and both
	// re-arm below — so nothing can be left waiting on a clock that is not
	// running.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	ticking := true
	arm := func() {
		want := len(g.runs) > 0
		switch {
		case want && !ticking:
			tick.Reset(time.Second)
			ticking = true
		case !want && ticking:
			tick.Stop()
			ticking = false
		}
	}
	arm() // resume may have loaded nothing, or plenty

	for {
		var tickC <-chan time.Time
		if ticking {
			tickC = tick.C
		}
		select {
		case <-g.stop:
			return
		case key := <-g.nudges:
			r := g.ensure(key)
			if r != nil {
				// A nudge on a live run is also how UpdateMapRun asks for
				// more children to launch.
				g.resumeMapRuns(r)
			}
			g.drive(r)
			arm()
		case d := <-g.deliveries:
			g.applyDelivery(d)
			arm()
		case <-tickC:
			g.fireDue()
			arm()
		}
	}
}

// fireDue walks the loaded runs and fires whatever the clock has passed:
// Wait and retry wakeups, and execution-level deadlines. Task deadlines and
// heartbeats join in stages G4 and G7.
func (g *engine) fireDue() {
	now := g.srv.store.clock().UnixMilli()
	// Collect first: waking a frame mutates g.runs on finalize.
	var due []*run
	for _, r := range g.runs {
		due = append(due, r)
	}
	for _, r := range due {
		if r.e.Status != "RUNNING" {
			continue
		}
		if r.e.Deadline != 0 && now >= r.e.Deadline {
			g.finalize(r, "TIMED_OUT", nil, "States.Timeout",
				"the execution exceeded its TimeoutSeconds")
			continue
		}
		woke := false
		for _, f := range r.e.Exec.Frames {
			switch f.Status {
			case asl.FrameSleeping, asl.FrameRetryWait:
				if f.WakeAt == 0 || now < f.WakeAt {
					continue
				}
				eff, notes, err := asl.Wake(r.def, r.e.Exec, f, g.envFor(r))
				g.recordNotes(r, notes)
				if err != nil {
					g.finalize(r, "FAILED", nil, "States.Runtime", err.Error())
					break
				}
				g.apply(r, eff)
				woke = true
			case asl.FrameCalling, asl.FrameParked:
				switch {
				case f.Deadline != 0 && now >= f.Deadline:
					// A late worker result for this attempt is now stale and
					// will be dropped by the attempt check.
					g.deliver(r, f, asl.TaskResult{Failure: &asl.Failure{
						Name: "States.Timeout", Cause: "the task exceeded its TimeoutSeconds"}})
					woke = true
				case f.Status == asl.FrameParked && f.HeartbeatS > 0 &&
					now >= f.HeartbeatAt+f.HeartbeatS*1000:
					g.deliver(r, f, asl.TaskResult{Failure: &asl.Failure{
						Name: "States.HeartbeatTimeout", Cause: "no heartbeat within HeartbeatSeconds"}})
					woke = true
				}
			}
			if r.e.Status != "RUNNING" {
				break
			}
		}
		if woke && r.e.Status == "RUNNING" {
			g.drive(r)
		}
	}
}
