package asl

import (
	"fmt"
	"time"
)

// The interpreter's entry points. Each runs one frame to its next suspension
// point and returns an Effect for the service to perform, plus the Notes the
// transition produced. The engine persists the Exec after every call — that
// cadence, not anything in here, is the durability guarantee.
//
//	Advance    a RUNNABLE frame runs its current state to the boundary
//	Wake       an elapsed timer fires: SLEEPING completes its Wait,
//	           RETRY_WAIT re-dispatches its task (stage G4)
//	Deliver    a task outcome lands in a CALLING/PARKED/JOIN frame (stage G4)
//	JoinReady  a JOIN parent inspects its children (stage G6)

// Advance runs a RUNNABLE frame through exactly one state boundary.
func Advance(d *Definition, ex *Exec, f *Frame, env Env) (Effect, []Note, error) {
	if f.Status != FrameRunnable {
		return nil, nil, fmt.Errorf("asl: Advance on a %s frame", f.Status)
	}
	sub, err := d.Sub(f.Def)
	if err != nil {
		return nil, nil, err
	}
	if f.State == "" {
		f.State = sub.StartAt
		f.EnteredAt = env.Now.UnixMilli()
	}
	s := sub.States[f.State]
	if s == nil {
		return nil, nil, fmt.Errorf("asl: frame %d names state %q, which does not exist", f.ID, f.State)
	}

	notes := []Note{{Kind: NoteEntered, Frame: f.ID, State: s.Name, Type: s.Type, Data: f.Input}}
	input := decodeDoc(f.Input)
	ctxObj := buildContext(ex, f, "")

	switch s.Type {
	case Pass:
		return advancePass(s, f, input, ctxObj, env, notes)
	case Choice:
		return advanceChoice(s, f, input, ctxObj, env, notes)
	case Wait:
		return advanceWait(s, f, input, ctxObj, env, notes)
	case Succeed:
		eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
		output, fail := selectPath(eff, ctxObj, s.OutputPath, "OutputPath")
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
		return exitState(f, s, "", output, env, notes)
	case Fail:
		return advanceFail(s, f, input, ctxObj, notes)
	case Task:
		return advanceTask(s, ex, f, input, ctxObj, env, notes)
	case Parallel:
		return advanceParallel(s, ex, f, input, ctxObj, env, notes)
	case Map:
		return advanceMap(s, ex, f, input, ctxObj, env, notes)
	}
	return nil, nil, fmt.Errorf("asl: frame %d hit unknown state type %q", f.ID, s.Type)
}

// Wake fires an elapsed timer on a SLEEPING or RETRY_WAIT frame.
func Wake(d *Definition, ex *Exec, f *Frame, env Env) (Effect, []Note, error) {
	sub, err := d.Sub(f.Def)
	if err != nil {
		return nil, nil, err
	}
	s := sub.States[f.State]
	if s == nil {
		return nil, nil, fmt.Errorf("asl: frame %d names state %q, which does not exist", f.ID, f.State)
	}
	switch f.Status {
	case FrameSleeping:
		f.WakeAt = 0
		input := decodeDoc(f.Input)
		ctxObj := buildContext(ex, f, "")
		eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
		if fail != nil {
			return failFrame(f, s, fail, nil)
		}
		output, fail := selectPath(eff, ctxObj, s.OutputPath, "OutputPath")
		if fail != nil {
			return failFrame(f, s, fail, nil)
		}
		next := s.Next
		if s.Terminal() {
			next = ""
		}
		return exitState(f, s, next, output, env, nil)
	case FrameRetryWait:
		// The backoff elapsed: re-dispatch the persisted TaskInput exactly as
		// scheduled — re-evaluating Parameters would re-run intrinsics and
		// mint a fresh token, which is why TaskInput is persisted at all.
		f.WakeAt = 0
		f.Status = FrameCalling
		if f.Token != "" {
			f.Status = FrameParked
		}
		f.Deadline = taskDeadline(s, env)
		return EffCallTask{
			Frame: f.ID, Resource: s.Resource, Input: f.TaskInput,
			Token: f.Token, Deadline: f.Deadline,
		}, nil, nil
	}
	return nil, nil, fmt.Errorf("asl: Wake on a %s frame", f.Status)
}

// TaskParks reports whether a Task's resource parks the frame on a token —
// the .waitForTaskToken pattern. The full resource table lives in the service
// (stepfunctions/task.go); the interpreter needs only this one bit, and a
// shared test keeps the two in agreement.
func TaskParks(resource string) bool {
	return hasSuffix(resource, ".waitForTaskToken")
}

func advanceTask(s *State, ex *Exec, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return deliverFailure(s, f, fail, env, notes)
	}
	token := ""
	if TaskParks(s.Resource) {
		if env.NewToken == nil {
			return deliverFailure(s, f, Failf(ErrRuntime,
				".waitForTaskToken is not available to this execution type"), env, notes)
		}
		token = env.NewToken()
		// The token is minted BEFORE Parameters evaluation so $$.Task.Token
		// resolves inside the template — that ordering is the whole callback
		// pattern.
		ctxObj = buildContext(ex, f, token)
	}
	taskInput := eff
	if len(s.Parameters) > 0 {
		taskInput, fail = evalTemplate(s.Parameters, eff, ctxObj, env)
		if fail != nil {
			return deliverFailure(s, f, fail, env, notes)
		}
	}
	f.TaskInput = encodeDoc(taskInput)
	f.Token = token
	f.Deadline = taskDeadline(s, env)
	if hb := taskHeartbeat(s); hb > 0 {
		f.HeartbeatS = hb
		f.HeartbeatAt = env.Now.UnixMilli()
	}
	f.Status = FrameCalling
	if token != "" {
		f.Status = FrameParked
	}
	return EffCallTask{
		Frame: f.ID, Resource: s.Resource, Input: f.TaskInput,
		Token: token, Deadline: f.Deadline,
	}, notes, nil
}

// taskDeadline reads TimeoutSeconds into an epoch-millis cutoff, 0 for none.
func taskDeadline(s *State, env Env) int64 {
	if s.TimeoutSecondsState == nil || *s.TimeoutSecondsState <= 0 {
		return 0
	}
	return env.Now.Add(time.Duration(*s.TimeoutSecondsState * float64(time.Second))).UnixMilli()
}

func taskHeartbeat(s *State) int64 {
	if s.HeartbeatSeconds == nil || *s.HeartbeatSeconds <= 0 {
		return 0
	}
	return int64(*s.HeartbeatSeconds)
}

// Deliver feeds a task outcome — a completed call, a SendTaskSuccess/Failure
// payload, a joined Parallel/Map result, or a service-side timeout — into a
// CALLING, PARKED or JOIN frame. Retry and Catch matching live here.
func Deliver(d *Definition, ex *Exec, f *Frame, r TaskResult, env Env) (Effect, []Note, error) {
	switch f.Status {
	case FrameCalling, FrameParked, FrameJoin:
	default:
		return nil, nil, fmt.Errorf("asl: Deliver on a %s frame", f.Status)
	}
	sub, err := d.Sub(f.Def)
	if err != nil {
		return nil, nil, err
	}
	s := sub.States[f.State]
	if s == nil {
		return nil, nil, fmt.Errorf("asl: frame %d names state %q, which does not exist", f.ID, f.State)
	}

	if r.Failure != nil {
		return deliverFailure(s, f, r.Failure, env, nil)
	}

	// Success: ResultSelector → ResultPath into the raw input → OutputPath.
	ctxObj := buildContext(ex, f, "")
	result := decodeDoc(r.Output)
	if len(s.ResultSelector) > 0 {
		var fail *Failure
		result, fail = evalTemplate(s.ResultSelector, result, ctxObj, env)
		if fail != nil {
			return deliverFailure(s, f, fail, env, nil)
		}
	}
	merged, fail := injectPath(decodeDoc(f.Input), result, s.ResultPath)
	if fail != nil {
		return deliverFailure(s, f, fail, env, nil)
	}
	output, fail := selectPath(merged, ctxObj, s.OutputPath, "OutputPath")
	if fail != nil {
		return deliverFailure(s, f, fail, env, nil)
	}
	next := s.Next
	if s.Terminal() {
		next = ""
	}
	return exitState(f, s, next, output, env, nil)
}

// deliverFailure routes a failure through the state's Retry, then Catch, then
// fails the frame — the order the spec fixes.
func deliverFailure(s *State, f *Frame, fail *Failure, env Env, notes []Note) (Effect, []Note, error) {
	if idx, delay, ok := nextRetry(s, f, fail, env); ok {
		if f.Attempts == nil {
			f.Attempts = make([]int, len(s.Retry))
		}
		f.Attempts[idx]++
		f.RetryIdx = idx
		f.Status = FrameRetryWait
		f.WakeAt = env.Now.Add(delay).UnixMilli()
		notes = append(notes, Note{
			Kind: NoteRetried, Frame: f.ID, State: s.Name, Type: s.Type,
			Data: encodeDoc(map[string]any{"error": fail.Name, "cause": fail.Cause}),
		})
		return EffSleep{Frame: f.ID, Until: f.WakeAt}, notes, nil
	}
	if c := catchFor(s, fail); c != nil {
		errObj := map[string]any{"Error": fail.Name}
		if fail.Cause != "" {
			errObj["Cause"] = fail.Cause
		}
		merged, mFail := injectPath(decodeDoc(f.Input), errObj, c.ResultPath)
		if mFail != nil {
			return failFrame(f, s, mFail, notes)
		}
		notes = append(notes, Note{
			Kind: NoteCaught, Frame: f.ID, State: s.Name, Type: s.Type,
			Data: encodeDoc(errObj),
		})
		f.State = c.Next
		f.Input = encodeDoc(merged)
		f.Status = FrameRunnable
		f.Attempts, f.RetryIdx = nil, 0
		f.TaskInput, f.Token = nil, ""
		f.WakeAt, f.Deadline, f.HeartbeatAt, f.HeartbeatS = 0, 0, 0, 0
		f.EnteredAt = env.Now.UnixMilli()
		return EffContinue{}, notes, nil
	}
	return failFrame(f, s, fail, notes)
}

func advancePass(s *State, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	if len(s.Parameters) > 0 {
		eff, fail = evalTemplate(s.Parameters, eff, ctxObj, env)
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
	}
	result := eff
	if len(s.Result) > 0 {
		result = decodeDoc(s.Result)
	}
	merged, fail := injectPath(input, result, s.ResultPath)
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	output, fail := selectPath(merged, ctxObj, s.OutputPath, "OutputPath")
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	next := s.Next
	if s.Terminal() {
		next = ""
	}
	return exitState(f, s, next, output, env, notes)
}

func advanceChoice(s *State, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	next, fail := chooseNext(s, eff, ctxObj)
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	output, fail := selectPath(eff, ctxObj, s.OutputPath, "OutputPath")
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	return exitState(f, s, next, output, env, notes)
}

func advanceWait(s *State, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	until, fail := waitUntil(s, eff, ctxObj, env)
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	// A wait already in the past completes now rather than costing a tick.
	if !until.After(env.Now) {
		output, fail := selectPath(eff, ctxObj, s.OutputPath, "OutputPath")
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
		next := s.Next
		if s.Terminal() {
			next = ""
		}
		return exitState(f, s, next, output, env, notes)
	}
	f.Status = FrameSleeping
	f.WakeAt = until.UnixMilli()
	return EffSleep{Frame: f.ID, Until: f.WakeAt}, notes, nil
}

// waitUntil reads whichever of the four Wait fields is set — the analyser
// guarantees exactly one.
func waitUntil(s *State, eff, ctxObj any, env Env) (time.Time, *Failure) {
	switch {
	case s.Seconds != nil:
		return env.Now.Add(time.Duration(*s.Seconds * float64(time.Second))), nil
	case s.SecondsPath != "":
		v, fail := pathValue(s.SecondsPath, "SecondsPath", eff, ctxObj)
		if fail != nil {
			return time.Time{}, fail
		}
		secs, isNum := toFloat(v)
		if !isNum || secs < 0 {
			return time.Time{}, Failf(ErrRuntime, "SecondsPath %q must select a non-negative number", s.SecondsPath)
		}
		return env.Now.Add(time.Duration(secs * float64(time.Second))), nil
	case s.Timestamp != "":
		t, err := time.Parse(time.RFC3339, s.Timestamp)
		if err != nil {
			return time.Time{}, Failf(ErrRuntime, "Timestamp %q is not RFC3339", s.Timestamp)
		}
		return t, nil
	case s.TimestampPath != "":
		v, fail := pathValue(s.TimestampPath, "TimestampPath", eff, ctxObj)
		if fail != nil {
			return time.Time{}, fail
		}
		str, isStr := v.(string)
		if !isStr {
			return time.Time{}, Failf(ErrRuntime, "TimestampPath %q must select a string", s.TimestampPath)
		}
		t, err := time.Parse(time.RFC3339, str)
		if err != nil {
			return time.Time{}, Failf(ErrRuntime, "TimestampPath %q selected %q, which is not RFC3339", s.TimestampPath, str)
		}
		return t, nil
	}
	return time.Time{}, Failf(ErrRuntime, "Wait state %q sets none of Seconds, SecondsPath, Timestamp, TimestampPath", s.Name)
}

func advanceFail(s *State, f *Frame, input, ctxObj any, notes []Note) (Effect, []Note, error) {
	name, cause := s.Error, s.Cause
	if s.ErrorPath != "" {
		v, fail := pathValue(s.ErrorPath, "ErrorPath", input, ctxObj)
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
		str, isStr := v.(string)
		if !isStr {
			return failFrame(f, s, Failf(ErrRuntime, "ErrorPath %q must select a string", s.ErrorPath), notes)
		}
		name = str
	}
	if s.CausePath != "" {
		v, fail := pathValue(s.CausePath, "CausePath", input, ctxObj)
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
		str, isStr := v.(string)
		if !isStr {
			return failFrame(f, s, Failf(ErrRuntime, "CausePath %q must select a string", s.CausePath), notes)
		}
		cause = str
	}
	return failFrame(f, s, &Failure{Name: name, Cause: cause}, notes)
}

// pathValue resolves one of the scalar ...Path fields, which must select
// exactly one value.
func pathValue(pathStr, field string, data, ctxObj any) (any, *Failure) {
	p, err := ParsePath(pathStr)
	if err != nil {
		return nil, Failf(ErrRuntime, "%s: %v", field, err)
	}
	v, ok := resolve(p, data, ctxObj)
	if !ok {
		return nil, Failf(ErrRuntime, "%s %q selects nothing", field, pathStr)
	}
	return v, nil
}

// exitState completes the current state with the given output: terminal when
// next is empty, otherwise a transition that resets the per-state fields.
func exitState(f *Frame, s *State, next string, output any, env Env, notes []Note) (Effect, []Note, error) {
	raw := encodeDoc(output)
	notes = append(notes, Note{Kind: NoteExited, Frame: f.ID, State: s.Name, Type: s.Type, Data: raw})
	if next == "" {
		f.Status = FrameDone
		f.Output = raw
		return EffDone{Frame: f.ID, Output: raw}, notes, nil
	}
	f.State = next
	f.Input = raw
	f.Status = FrameRunnable
	f.Attempts, f.RetryIdx = nil, 0
	f.TaskInput, f.Token = nil, ""
	f.WakeAt, f.Deadline, f.HeartbeatAt, f.HeartbeatS = 0, 0, 0, 0
	f.EnteredAt = env.Now.UnixMilli()
	return EffContinue{}, notes, nil
}

// failFrame marks the frame FAILED. Catch dispatch (stage G4) intercepts
// before this for the state types that may carry one.
func failFrame(f *Frame, s *State, fail *Failure, notes []Note) (Effect, []Note, error) {
	f.Status = FrameFailed
	f.Failure = fail
	notes = append(notes, Note{
		Kind: NoteFailed, Frame: f.ID, State: s.Name, Type: s.Type,
		Data: encodeDoc(map[string]any{"error": fail.Name, "cause": fail.Cause}),
	})
	return EffFail{Frame: f.ID, Failure: fail}, notes, nil
}

// buildContext assembles the $$ context object for a frame.
func buildContext(ex *Exec, f *Frame, token string) any {
	ctx := map[string]any{
		"Execution": map[string]any{
			"Id":        ex.ID,
			"Name":      ex.Name,
			"Input":     decodeDoc(ex.Input),
			"StartTime": contextTime(ex.StartTime),
			"RoleArn":   ex.RoleARN,
		},
		"State": map[string]any{
			"Name":        f.State,
			"EnteredTime": contextTime(time.UnixMilli(f.EnteredAt)),
			"RetryCount":  f.RetryCount(),
		},
		"StateMachine": map[string]any{
			"Id":   ex.Machine,
			"Name": ex.MachineName,
		},
	}
	if token != "" {
		ctx["Task"] = map[string]any{"Token": token}
	}
	if f.MapItem != nil {
		ctx["Map"] = map[string]any{"Item": map[string]any{
			"Index": f.Branch,
			"Value": decodeDoc(f.MapItem),
		}}
	}
	return ctx
}

// contextTime renders a timestamp the way the real context object does:
// RFC3339 with milliseconds, UTC.
func contextTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
