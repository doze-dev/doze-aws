package asl

import (
	"time"
)

// The JSONata dialect's state semantics, one function per seam in exec.go.
//
// The data flow is simpler than JSONPath's five fields, and the same for every
// state type:
//
//	state input ──Arguments──▶ what the task / branches / items see
//	                              (the task runs)
//	result ──Output──▶ state output      result ──Assign──▶ variables
//
// Output and Assign are evaluated independently, both against the values on
// state entry. The state's output IS the evaluated Output; with no Output
// field it is the result for Task, Parallel and Map, and the input for
// everything else. A failed evaluation is States.QueryEvaluationError, which
// Task, Parallel and Map route through Retry and Catch like any other error.

// jsonataScopeFor is the scope on state entry: input, context, variables.
// The variables are copied so that a state's own Assign, once written, is
// invisible to any evaluation still holding this scope.
func jsonataScopeFor(f *Frame, input, ctxObj any, env Env) *jsonataScope {
	return &jsonataScope{input: input, context: ctxObj, vars: copyVars(f.Vars), env: env}
}

// advanceJSONata is the Advance seam.
func advanceJSONata(s *State, ex *Exec, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	sc := jsonataScopeFor(f, input, ctxObj, env)
	next := s.Next
	if s.Terminal() {
		next = ""
	}
	switch s.Type {
	case Pass:
		result := input
		if len(s.Result) > 0 {
			result = decodeDoc(s.Result)
			sc.result, sc.hasResult = result, true
		}
		return jsonataFinish(sc, s, f, next, result, env, notes, failFrame)
	case Succeed:
		return jsonataFinish(sc, s, f, "", input, env, notes, failFrame)
	case Choice:
		return jsonataChoice(sc, s, f, env, notes)
	case Wait:
		return jsonataWaitEnter(sc, s, f, env, notes)
	case Fail:
		return jsonataFail(sc, s, f, notes)
	case Task:
		return jsonataTask(sc, s, ex, f, input, env, notes)
	case Parallel:
		return jsonataParallel(sc, s, ex, f, input, env, notes)
	case Map:
		return jsonataMap(sc, s, ex, f, input, env, notes)
	}
	return failFrame(f, s, Failf(ErrRuntime, "unknown state type %q", s.Type), notes)
}

// failer is how a state fails: straight to the frame for the simple states,
// through Retry and Catch for Task, Parallel and Map.
type failer func(f *Frame, s *State, fail *Failure, notes []Note) (Effect, []Note, error)

// jsonataFinish runs the exit half — Output, Assign, transition — with dflt
// as the output when the state has no Output field.
func jsonataFinish(sc *jsonataScope, s *State, f *Frame, next string, dflt any, env Env, notes []Note, onFail failer) (Effect, []Note, error) {
	output := dflt
	if v, ok, fail := sc.value(s.Output); fail != nil {
		return onFail(f, s, fail, notes)
	} else if ok {
		output = v
	}
	if fail := jsonataAssign(sc, s.Assign, f); fail != nil {
		return onFail(f, s, fail, notes)
	}
	return exitState(f, s, next, output, env, notes)
}

func jsonataChoice(sc *jsonataScope, s *State, f *Frame, env Env, notes []Note) (Effect, []Note, error) {
	next, rule, fail := jsonataChoose(sc, s)
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	output := sc.input
	if v, ok, fail := sc.value(s.Output); fail != nil {
		return failFrame(f, s, fail, notes)
	} else if ok {
		output = v
	}
	// The state's Assign, then the taken rule's: both evaluated against the
	// entry scope (sc holds a copy of the variables), rule winning a collision.
	if fail := jsonataAssign(sc, s.Assign, f); fail != nil {
		return failFrame(f, s, fail, notes)
	}
	if rule != nil {
		if fail := jsonataAssign(sc, rule.Assign, f); fail != nil {
			return failFrame(f, s, fail, notes)
		}
	}
	return exitState(f, s, next, output, env, notes)
}

// jsonataChoose evaluates the rules' Conditions in order. A Condition that
// yields anything but a boolean is an evaluation error, not "false".
func jsonataChoose(sc *jsonataScope, s *State) (string, *ChoiceRule, *Failure) {
	for i, rule := range s.Choices {
		v, _, fail := sc.value(rule.Condition)
		if fail != nil {
			return "", nil, fail
		}
		ok, isBool := v.(bool)
		if !isBool {
			return "", nil, Failf(ErrQueryEvaluationError,
				"Choices[%d].Condition must evaluate to a boolean, got %s", i, jsonKindOf(v))
		}
		if ok {
			return rule.Next, rule, nil
		}
	}
	if s.Default != "" {
		return s.Default, nil, nil
	}
	return "", nil, Failf(ErrNoChoiceMatched, "no rule of Choice state %q matched and there is no Default", s.Name)
}

func jsonataWaitEnter(sc *jsonataScope, s *State, f *Frame, env Env, notes []Note) (Effect, []Note, error) {
	var until time.Time
	switch {
	case s.Seconds != nil || s.SecondsExpr != "":
		secs, _, fail := sc.number("Seconds", s.Seconds, s.SecondsExpr)
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
		if secs < 0 {
			return failFrame(f, s, Failf(ErrQueryEvaluationError, "Seconds must not be negative, got %v", secs), notes)
		}
		until = env.Now.Add(time.Duration(secs * float64(time.Second)))
	case s.Timestamp != "":
		str, fail := sc.str("Timestamp", s.Timestamp)
		if fail != nil {
			return failFrame(f, s, fail, notes)
		}
		t, err := time.Parse(time.RFC3339, str)
		if err != nil {
			return failFrame(f, s, Failf(ErrQueryEvaluationError, "Timestamp %q is not RFC3339", str), notes)
		}
		until = t
	default:
		return failFrame(f, s, Failf(ErrRuntime, "Wait state %q sets neither Seconds nor Timestamp", s.Name), notes)
	}
	// A wait already in the past completes now rather than costing a tick.
	if !until.After(env.Now) {
		return jsonataWake(s, f, sc.input, sc.context, env, notes)
	}
	f.Status = FrameSleeping
	f.WakeAt = until.UnixMilli()
	return EffSleep{Frame: f.ID, Until: f.WakeAt}, notes, nil
}

// jsonataWake is the Wake seam: the Wait elapsed, so Output and Assign run.
func jsonataWake(s *State, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	sc := jsonataScopeFor(f, input, ctxObj, env)
	next := s.Next
	if s.Terminal() {
		next = ""
	}
	return jsonataFinish(sc, s, f, next, input, env, notes, failFrame)
}

func jsonataFail(sc *jsonataScope, s *State, f *Frame, notes []Note) (Effect, []Note, error) {
	name, fail := sc.str("Error", s.Error)
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	cause, fail := sc.str("Cause", s.Cause)
	if fail != nil {
		return failFrame(f, s, fail, notes)
	}
	return failFrame(f, s, &Failure{Name: name, Cause: cause}, notes)
}

func jsonataTask(sc *jsonataScope, s *State, ex *Exec, f *Frame, input any, env Env, notes []Note) (Effect, []Note, error) {
	onFail := func(f *Frame, s *State, fail *Failure, notes []Note) (Effect, []Note, error) {
		return deliverFailure(s, ex, f, fail, env, notes)
	}
	token := ""
	if TaskParks(s.Resource) {
		if env.NewToken == nil {
			return onFail(f, s, Failf(ErrRuntime,
				".waitForTaskToken is not available to this execution type"), notes)
		}
		token = env.NewToken()
		// Minted before Arguments so $states.context.Task.Token resolves.
		sc.context = buildContext(ex, f, token)
	}
	taskInput := input
	if v, ok, fail := sc.value(s.Arguments); fail != nil {
		return onFail(f, s, fail, notes)
	} else if ok {
		taskInput = v
	}
	timeout, _, fail := sc.number("TimeoutSeconds", s.TimeoutSecondsState, s.TimeoutSecondsExpr)
	if fail != nil {
		return onFail(f, s, fail, notes)
	}
	heartbeat, _, fail := sc.number("HeartbeatSeconds", s.HeartbeatSeconds, s.HeartbeatSecondsExpr)
	if fail != nil {
		return onFail(f, s, fail, notes)
	}
	if timeout < 0 || heartbeat < 0 {
		return onFail(f, s, Failf(ErrQueryEvaluationError, "TimeoutSeconds and HeartbeatSeconds must not be negative"), notes)
	}
	f.TaskInput = encodeDoc(taskInput)
	f.Token = token
	f.TimeoutS = timeout
	f.Deadline = taskDeadline(s, f, env)
	if heartbeat > 0 {
		f.HeartbeatS = int64(heartbeat)
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

// jsonataDeliver is the Deliver seam for a successful result: $states.result
// is bound, and Output defaults to it.
func jsonataDeliver(s *State, ex *Exec, f *Frame, result, ctxObj any, env Env) (Effect, []Note, error) {
	sc := jsonataScopeFor(f, decodeDoc(f.Input), ctxObj, env)
	sc.result, sc.hasResult = result, true
	next := s.Next
	if s.Terminal() {
		next = ""
	}
	return jsonataFinish(sc, s, f, next, result, env, nil, func(f *Frame, s *State, fail *Failure, notes []Note) (Effect, []Note, error) {
		return deliverFailure(s, ex, f, fail, env, notes)
	})
}

// jsonataCatch is the Catch seam: the catcher's Output (default: the error
// output itself) becomes the next state's input, and its Assign writes into
// this frame — the outer scope, when the failed state is a Parallel or Map.
// A failure inside the catcher's own expressions is final: the catch has
// already fired, and re-entering Retry would loop.
func jsonataCatch(s *State, ex *Exec, f *Frame, c *Catcher, errObj map[string]any, env Env, notes []Note) (Effect, []Note, error) {
	sc := jsonataScopeFor(f, decodeDoc(f.Input), buildContext(ex, f, ""), env)
	sc.errorOutput, sc.hasError = errObj, true
	var output any = errObj
	if v, ok, fail := sc.value(c.Output); fail != nil {
		return failFrame(f, s, fail, notes)
	} else if ok {
		output = v
	}
	if fail := jsonataAssign(sc, c.Assign, f); fail != nil {
		return failFrame(f, s, fail, notes)
	}
	notes = append(notes, Note{
		Kind: NoteCaught, Frame: f.ID, State: s.Name, Type: s.Type,
		Data: encodeDoc(errObj),
	})
	f.State = c.Next
	f.Input = encodeDoc(output)
	f.Status = FrameRunnable
	f.Attempts, f.RetryIdx = nil, 0
	f.TaskInput, f.Token, f.WaitExec, f.MapRun = nil, "", "", ""
	f.WakeAt, f.Deadline, f.HeartbeatAt, f.HeartbeatS, f.TimeoutS, f.Limit = 0, 0, 0, 0, 0, 0
	f.EnteredAt = env.Now.UnixMilli()
	return EffContinue{}, notes, nil
}

func jsonataParallel(sc *jsonataScope, s *State, ex *Exec, f *Frame, input any, env Env, notes []Note) (Effect, []Note, error) {
	branchInput := input
	if v, ok, fail := sc.value(s.Arguments); fail != nil {
		return deliverFailure(s, ex, f, fail, env, notes)
	} else if ok {
		branchInput = v
	}
	raw := encodeDoc(branchInput)
	f.BeginSpawn()
	spawn := EffSpawn{Parent: f.ID}
	for i := range s.Branches {
		hop := append(append([]DefHop{}, f.Def...), DefHop{State: s.Name, Branch: i})
		child := ex.Spawn(f, i, hop, raw, FrameRunnable)
		spawn.Frames = append(spawn.Frames, child.ID)
	}
	f.Status = FrameJoin
	return spawn, notes, nil
}

func jsonataMap(sc *jsonataScope, s *State, ex *Exec, f *Frame, input any, env Env, notes []Note) (Effect, []Note, error) {
	items := input
	if v, ok, fail := sc.value(s.Items); fail != nil {
		return deliverFailure(s, ex, f, fail, env, notes)
	} else if ok {
		items = v
	}
	list, isList := items.([]any)
	if !isList {
		return deliverFailure(s, ex, f, Failf(ErrQueryEvaluationError,
			"Items must evaluate to an array, got %s", jsonKindOf(items)), env, notes)
	}
	limit, _, fail := sc.number("MaxConcurrency", s.MaxConcurrency, s.MaxConcurrencyExpr)
	if fail != nil {
		return deliverFailure(s, ex, f, fail, env, notes)
	}
	if limit < 0 {
		return deliverFailure(s, ex, f, Failf(ErrQueryEvaluationError,
			"MaxConcurrency must not be negative, got %v", limit), env, notes)
	}
	f.Limit = int(limit)

	// Distributed: the items (selected here, since the selector is a JSONata
	// expression) go to the engine as child executions. An ItemReader is
	// the engine's to read, in which case Items is not consulted.
	if s.Processor().Distributed() {
		if len(s.ItemReader) > 0 {
			eff, notes2, err := startMapRun(s, ex, f, nil, true)
			return eff, append(notes, notes2...), err
		}
		selected := make([]any, 0, len(list))
		for i, item := range list {
			childInput := item
			if len(s.ItemSelector) > 0 {
				probe := &Frame{ID: f.ID, State: s.Name, Branch: i, MapItem: encodeDoc(item), EnteredAt: f.EnteredAt}
				itemScope := &jsonataScope{input: sc.input, context: buildContext(ex, probe, ""), vars: sc.vars, env: env}
				v, _, fail := itemScope.value(s.ItemSelector)
				if fail != nil {
					return deliverFailure(s, ex, f, fail, env, notes)
				}
				childInput = v
			}
			selected = append(selected, childInput)
		}
		eff, notes2, err := startMapRun(s, ex, f, selected, false)
		return eff, append(notes, notes2...), err
	}

	hop := append(append([]DefHop{}, f.Def...), DefHop{State: s.Name, Branch: -1})
	f.BeginSpawn()
	spawn := EffSpawn{Parent: f.ID}
	for i, item := range list {
		itemRaw := encodeDoc(item)
		childInput := itemRaw
		if len(s.ItemSelector) > 0 {
			// The selector sees the item through $states.context.Map.Item,
			// built against a probe frame the way the JSONPath path does.
			probe := &Frame{ID: f.ID, State: s.Name, Branch: i, MapItem: itemRaw, EnteredAt: f.EnteredAt}
			itemScope := &jsonataScope{input: sc.input, context: buildContext(ex, probe, ""), vars: sc.vars, env: env}
			selected, _, fail := itemScope.value(s.ItemSelector)
			if fail != nil {
				return deliverFailure(s, ex, f, fail, env, notes)
			}
			childInput = encodeDoc(selected)
		}
		status := FrameRunnable
		if f.Limit > 0 && i >= f.Limit {
			status = FramePending
		}
		child := ex.Spawn(f, i, hop, childInput, status)
		child.MapItem = itemRaw
		spawn.Frames = append(spawn.Frames, child.ID)
	}
	f.Status = FrameJoin
	return spawn, notes, nil
}
