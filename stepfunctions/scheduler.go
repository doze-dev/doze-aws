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

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-g.stop:
			return
		case key := <-g.nudges:
			g.drive(g.ensure(key))
		case d := <-g.deliveries:
			g.applyDelivery(d)
		case <-tick.C:
			g.fireDue()
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
