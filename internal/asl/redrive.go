package asl

// Redrive restarts a finished execution from where it stopped, the way
// RedriveExecution does: every frame that failed, or was cut off mid-state
// by an abort or a timeout, runs its state again with the input it had;
// everything that finished stays finished. A Parallel or Map whose branch
// failed re-runs only that branch — the join parent goes back to waiting
// rather than spawning afresh — which is the point of redrive over a fresh
// StartExecution: the work that succeeded is not repeated.
//
// Returns how many frames were reset; zero means there was nothing to
// redrive, which the caller reports as ExecutionNotRedrivable.
func Redrive(d *Definition, ex *Exec) int {
	reset := 0
	// Deepest first, so a parent's decision sees its children's new status.
	for i := len(ex.Frames) - 1; i >= 0; i-- {
		f := ex.Frames[i]
		switch f.Status {
		case FrameDone, FramePending, FrameRunnable:
			continue
		case FrameJoin:
			// Still waiting for children that are themselves being reset.
			continue
		}
		// A child of an earlier fan-out on its parent is history — its
		// failure was caught or its state moved on — and stays as it is.
		if p := ex.Frame(f.Parent); p != nil && f.Gen != p.SpawnGen {
			continue
		}
		// FAILED, or cut off in CALLING/PARKED/SLEEPING/RETRY_WAIT.
		if f.Status == FrameFailed && isJoinState(d, f) && hasChildren(ex, f) {
			// The state itself did not fail; a branch did. Wait for it again.
			f.Status = FrameJoin
			f.Failure = nil
			reset++
			continue
		}
		f.Status = FrameRunnable
		f.Failure = nil
		f.Attempts, f.RetryIdx = nil, 0
		f.TaskInput, f.Token, f.WaitExec, f.MapRun = nil, "", "", ""
		f.WakeAt, f.Deadline, f.HeartbeatAt, f.HeartbeatS = 0, 0, 0, 0
		reset++
	}
	return reset
}

func isJoinState(d *Definition, f *Frame) bool {
	sub, err := d.Sub(f.Def)
	if err != nil {
		return false
	}
	s := sub.States[f.State]
	return s != nil && (s.Type == Parallel || s.Type == Map)
}

func hasChildren(ex *Exec, f *Frame) bool {
	return len(ex.Children(f.ID)) > 0
}
