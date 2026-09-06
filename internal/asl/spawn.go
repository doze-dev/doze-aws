package asl

// Parallel and Map: branches are sibling frames sharing a parent, which is
// what makes a branch parked on a task token just another suspended frame —
// the case that breaks goroutine-per-execution designs. MaxConcurrency is
// "at most N non-terminal children at once", enforced by spawning the excess
// as PENDING and letting the engine promote them as siblings settle.

func advanceParallel(s *State, ex *Exec, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return deliverFailure(s, ex, f, fail, env, notes)
	}
	if len(s.Parameters) > 0 {
		eff, fail = evalTemplate(s.Parameters, eff, ctxObj, env)
		if fail != nil {
			return deliverFailure(s, ex, f, fail, env, notes)
		}
	}
	branchInput := encodeDoc(eff)
	f.BeginSpawn()
	spawn := EffSpawn{Parent: f.ID}
	for i := range s.Branches {
		hop := append(append([]DefHop{}, f.Def...), DefHop{State: s.Name, Branch: i})
		child := ex.Spawn(f, i, hop, branchInput, FrameRunnable)
		spawn.Frames = append(spawn.Frames, child.ID)
	}
	f.Status = FrameJoin
	return spawn, notes, nil
}

func advanceMap(s *State, ex *Exec, f *Frame, input, ctxObj any, env Env, notes []Note) (Effect, []Note, error) {
	eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return deliverFailure(s, ex, f, fail, env, notes)
	}
	items := eff
	if s.ItemsPath != "" {
		items, fail = pathValue(s.ItemsPath, "ItemsPath", eff, ctxObj)
		if fail != nil {
			return deliverFailure(s, ex, f, fail, env, notes)
		}
	}
	list, isList := items.([]any)
	if !isList {
		return deliverFailure(s, ex, f, Failf(ErrRuntime,
			"a Map state's items must be an array"), env, notes)
	}

	limit := 0 // 0 = unlimited
	if s.MaxConcurrency != nil {
		limit = int(*s.MaxConcurrency)
	}
	if s.MaxConcurrencyPath != "" {
		v, fail := pathValue(s.MaxConcurrencyPath, "MaxConcurrencyPath", eff, ctxObj)
		if fail != nil {
			return deliverFailure(s, ex, f, fail, env, notes)
		}
		n, isNum := toFloat(v)
		if !isNum || n < 0 {
			return deliverFailure(s, ex, f, Failf(ErrRuntime,
				"MaxConcurrencyPath must select a non-negative number"), env, notes)
		}
		limit = int(n)
	}

	// ItemSelector is the current spelling; Parameters is the retired one
	// Map used before ItemSelector existed.
	selector := s.ItemSelector
	if len(selector) == 0 {
		selector = s.Parameters
	}

	hop := append(append([]DefHop{}, f.Def...), DefHop{State: s.Name, Branch: -1})
	f.BeginSpawn()
	spawn := EffSpawn{Parent: f.ID}
	for i, item := range list {
		itemRaw := encodeDoc(item)
		childInput := itemRaw
		if len(selector) > 0 {
			// The selector sees the item through $$.Map.Item — built against
			// a probe frame so the real child's fields don't half-exist yet.
			probe := &Frame{ID: f.ID, State: s.Name, Branch: i, MapItem: itemRaw, EnteredAt: f.EnteredAt}
			itemCtx := buildContext(ex, probe, "")
			selected, fail := evalTemplate(selector, eff, itemCtx, env)
			if fail != nil {
				return deliverFailure(s, ex, f, fail, env, notes)
			}
			childInput = encodeDoc(selected)
		}
		status := FrameRunnable
		if limit > 0 && i >= limit {
			status = FramePending
		}
		child := ex.Spawn(f, i, hop, childInput, status)
		child.MapItem = itemRaw
		spawn.Frames = append(spawn.Frames, child.ID)
	}
	f.Status = FrameJoin
	return spawn, notes, nil
}

// JoinReady inspects a JOIN parent's children. Pure: the engine calls it
// whenever a child settles, and once done, feeds the result through Deliver.
// A failed branch fails the whole state with that branch's own error — unless
// the Map declares a failure tolerance, in which case failed items yield null
// slots until the tolerance is crossed.
func JoinReady(d *Definition, ex *Exec, parent *Frame) (TaskResult, bool) {
	children := ex.Children(parent.ID)
	if len(children) == 0 {
		// A Map over zero items joins immediately with an empty array.
		if parent.Status == FrameJoin {
			return TaskResult{Output: encodeDoc([]any{})}, true
		}
		return TaskResult{}, false
	}
	sub, err := d.Sub(parent.Def)
	if err != nil {
		return TaskResult{Failure: Failf(ErrRuntime, "%v", err)}, true
	}
	s := sub.States[parent.State]
	if s == nil {
		return TaskResult{Failure: Failf(ErrRuntime, "join state %q does not exist", parent.State)}, true
	}

	tolerated, hasTolerance := toleranceOf(s, len(children))
	outputs := make([]any, len(children))
	failed, settled := 0, 0
	var firstFail *Failure
	for _, c := range children {
		switch c.Status {
		case FrameDone:
			settled++
			if c.Branch >= 0 && c.Branch < len(outputs) {
				outputs[c.Branch] = decodeDoc(c.Output)
			}
		case FrameFailed:
			settled++
			failed++
			if firstFail == nil {
				firstFail = c.Failure
			}
		}
	}

	switch {
	case !hasTolerance && failed > 0:
		// Fail fast: the first branch failure ends the state; the engine
		// abandons the surviving siblings.
		return TaskResult{Failure: firstFail}, true
	case hasTolerance && failed > tolerated:
		return TaskResult{Failure: Failf(ErrExceedToleratedFailureThreshold,
			"%d items failed, more than the tolerated %d", failed, tolerated)}, true
	case settled < len(children):
		return TaskResult{}, false
	}
	return TaskResult{Output: encodeDoc(outputs)}, true
}

// toleranceOf reads a Map's failure tolerance; ok is false when none is
// declared (Parallel never has one).
func toleranceOf(s *State, total int) (int, bool) {
	if s.Type != Map {
		return 0, false
	}
	switch {
	case s.ToleratedFailureCount != nil:
		return int(*s.ToleratedFailureCount), true
	case s.ToleratedFailurePercentage != nil:
		return int(*s.ToleratedFailurePercentage * float64(total) / 100), true
	}
	return 0, false
}

// PromotePending flips PENDING children RUNNABLE while the parent's
// MaxConcurrency allows, and returns the ones it started. The engine calls
// it as siblings settle and records MapIterationStarted for each — the event
// belongs to the moment an iteration begins, not to the spawn.
func PromotePending(d *Definition, ex *Exec, parent *Frame) []*Frame {
	sub, err := d.Sub(parent.Def)
	if err != nil {
		return nil
	}
	s := sub.States[parent.State]
	if s == nil {
		return nil
	}
	// A JSONata Map evaluated its MaxConcurrency expression on entry and
	// left the value on the frame; a literal is read from the state.
	limit := parent.Limit
	if s.MaxConcurrency != nil {
		limit = int(*s.MaxConcurrency)
	}
	if limit <= 0 {
		return nil
	}
	active := 0
	for _, c := range ex.Children(parent.ID) {
		if !c.Status.Terminal() && c.Status != FramePending {
			active++
		}
	}
	var started []*Frame
	for _, c := range ex.Children(parent.ID) {
		if active >= limit {
			break
		}
		if c.Status == FramePending {
			c.Status = FrameRunnable
			started = append(started, c)
			active++
		}
	}
	return started
}

// AbandonSiblings marks the parent's surviving children failed — the engine
// calls it when a fail-fast join has decided, so late task results become
// stale and the frames stop being runnable.
func AbandonSiblings(ex *Exec, parent *Frame) {
	for _, c := range ex.Children(parent.ID) {
		if !c.Status.Terminal() {
			c.Status = FrameFailed
			c.Failure = Failf(ErrRuntime, "abandoned: a sibling branch failed")
		}
	}
}
