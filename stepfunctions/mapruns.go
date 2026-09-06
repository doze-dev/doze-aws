package stepfunctions

import (
	"encoding/json"
	"fmt"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// Distributed Map on the driver. The interpreter parked the frame and
// handed over the items (or an ItemReader to consult); this side reads,
// selects and batches them, launches each as a child execution under a Map
// Run record, and when the last child reports in, joins the outcomes into
// the state's result — through a ResultWriter into S3 if the state has one.
//
// Every step is a store write on the driver goroutine except S3, which is a
// peer call: reading items and writing results go through the same worker
// path a Task uses, so a slow bucket never stalls the driver.

// mapRunLaunchCap is how many child executions run at once when the state
// sets no MaxConcurrency. AWS's default is 10,000; locally that is a way to
// start ten thousand executions in one tick, so the cap is one the driver
// keeps up with. UpdateMapRun can raise it.
const mapRunLaunchCap = 40

// startMapRun is the EffMapRun handler.
func (g *engine) startMapRun(r *run, e asl.EffMapRun) {
	f := r.e.Exec.Frame(e.Frame)
	if f == nil {
		return
	}
	cfg, ok := asl.MapRunConfigOf(r.def, f)
	if !ok || cfg.Processor == nil {
		g.deliver(r, f, failResult(asl.ErrRuntime, "the Map state has no ItemProcessor"))
		return
	}
	items := e.Items
	if e.FromReader {
		// The reader is I/O: fetch on a worker, resume through a delivery.
		g.readItems(r, f, cfg)
		return
	}
	g.beginMapRun(r, f, cfg, items)
}

// beginMapRun has the items in hand: select, batch, record, launch.
func (g *engine) beginMapRun(r *run, f *asl.Frame, cfg asl.MapRunConfig, items []json.RawMessage) {
	selected, fail := asl.SelectItems(r.def, r.e.Exec, f, items, g.envFor(r))
	if fail != nil {
		g.deliver(r, f, asl.TaskResult{Failure: fail})
		return
	}
	inputs, err := batchItems(cfg.ItemBatcher, selected)
	if err != nil {
		g.deliver(r, f, failResult(asl.ErrRuntime, err.Error()))
		return
	}
	processor, err := rawProcessor(r.e.Definition, f)
	if err != nil {
		g.deliver(r, f, failResult(asl.ErrRuntime, err.Error()))
		return
	}
	id := newToken()[2:18]
	mr := &MapRun{
		ARN: mapRunARN(r.e.Exec.MachineName, r.e.Name, cfg.Label, id), ID: id,
		ExecKey: r.key, ExecARN: r.e.ARN, MachineARN: r.e.MachineARN, Machine: r.e.Exec.MachineName,
		Frame: f.ID, Label: cfg.Label, Status: "RUNNING", StartedAt: g.srv.store.now(),
		MaxConcurrency: cfg.MaxConcurrency, ToleratedFailureCount: cfg.ToleratedFailureCount,
		ToleratedFailurePercentage: cfg.ToleratedFailurePercentage,
		ExecutionType:              cfg.ExecutionType, Processor: processor, RoleARN: r.e.RoleARN,
		ResultWriter: cfg.ResultWriter, Inputs: inputs,
	}
	if err := g.srv.store.PutMapRun(mr); err != nil {
		g.deliver(r, f, failResult(asl.ErrRuntime, err.Error()))
		return
	}
	f.MapRun = mr.ARN
	g.event(r, f, "MapRunStarted", "mapRunStartedEventDetails", map[string]any{"mapRunArn": mr.ARN})
	g.event(r, f, "MapStateStarted", "mapStateStartedEventDetails", map[string]any{"length": len(inputs)})
	g.persist(r)
	g.launchMapChildren(r, f, mr)
}

// launchMapChildren starts children up to the run's concurrency, and
// settles an empty run.
func (g *engine) launchMapChildren(r *run, f *asl.Frame, mr *MapRun) {
	limit := mr.MaxConcurrency
	if limit <= 0 {
		limit = mapRunLaunchCap
	}
	for mr.Next < mr.Total() && mr.Running < limit {
		i := mr.Next
		mr.Next++
		mr.Running++
		if err := g.launchMapChild(r, mr, i); err != nil {
			mr.Running--
			g.srv.store.PutMapItem(mr.ARN, &MapItem{Index: i, Status: "FAILED", Input: mr.Inputs[i], Error: "States.Runtime", Cause: err.Error()})
			mr.Failed++
		}
	}
	g.srv.store.PutMapRun(mr)
	if mr.done() {
		g.completeMapRun(r, f, mr)
	}
}

// launchMapChild starts one item's execution of the processor.
func (g *engine) launchMapChild(r *run, mr *MapRun, index int) error {
	m := &StateMachine{
		Name: mr.Machine, ARN: mr.MachineARN, Definition: mr.Processor, RoleARN: mr.RoleARN, Type: mr.ExecutionType,
	}
	name := fmt.Sprintf("%s-%d", mr.ID, index)
	input := string(mr.Inputs[index])
	if mr.ExecutionType == "EXPRESS" {
		e, aerr := g.srv.newExpressExecution(g.workerCtx, m, map[string]any{"name": name, "input": input}, "", "")
		if aerr != nil {
			return fmt.Errorf("%s: %s", aerr.Code, aerr.Message)
		}
		e.StartedBy, e.MapRunARN, e.MapIndex = mr.ExecARN, mr.ARN, index
		if err := g.srv.store.SaveTransition(e, nil, nil); err != nil {
			return err
		}
		g.nudge(e.Key())
		return nil
	}
	_, aerr := g.srv.launch(launchSpec{
		Machine: m, Name: name, Input: input, TraceHeader: r.e.TraceHeader, StartedBy: mr.ExecARN,
		MapRunARN: mr.ARN, MapIndex: index,
	})
	if aerr != nil {
		return fmt.Errorf("%s: %s", aerr.Code, aerr.Message)
	}
	return nil
}

// mapChildFinished runs in a child's finalize: record the outcome, launch
// the next item, and join when the last one is in.
func (g *engine) mapChildFinished(child *Execution) {
	mr, err := g.srv.store.GetMapRun(child.MapRunARN)
	if err != nil || mr == nil || mr.Status != "RUNNING" {
		return
	}
	it := &MapItem{Index: child.MapIndex, Status: child.Status, Input: json.RawMessage(child.Input), Output: child.Output, Error: child.Error, Cause: child.Cause}
	g.srv.store.PutMapItem(mr.ARN, it)
	mr.Running--
	switch child.Status {
	case "SUCCEEDED":
		mr.Succeeded++
	case "TIMED_OUT":
		mr.TimedOut++
	case "ABORTED":
		mr.Aborted++
	default:
		mr.Failed++
	}
	parent := g.ensure(mr.ExecKey)
	if parent == nil {
		mr.Status = "ABORTED"
		mr.StoppedAt = g.srv.store.now()
		g.srv.store.PutMapRun(mr)
		return
	}
	f := parent.e.Exec.Frame(mr.Frame)
	if f == nil || f.MapRun != mr.ARN {
		g.srv.store.PutMapRun(mr)
		return
	}
	// Fail fast once the tolerance is crossed: nothing else needs to run.
	if mr.failures() > mr.tolerated() && mr.Next < mr.Total() {
		mr.Next = mr.Total()
	}
	g.launchMapChildren(parent, f, mr)
}

// completeMapRun joins the outcomes into the state's result and delivers.
func (g *engine) completeMapRun(r *run, f *asl.Frame, mr *MapRun) {
	items, _ := g.srv.store.MapItems(mr.ARN)
	now := g.srv.store.now()
	if mr.failures() > mr.tolerated() {
		mr.Status, mr.StoppedAt = "FAILED", now
		g.srv.store.PutMapRun(mr)
		cause := fmt.Sprintf("%d of %d items failed; the state tolerates %d", mr.failures(), mr.Total(), mr.tolerated())
		g.event(r, f, "MapRunFailed", "mapRunFailedEventDetails", map[string]any{"error": asl.ErrExceedToleratedFailureThreshold, "cause": cause})
		g.event(r, f, "MapStateFailed", "", nil)
		f.MapRun = ""
		g.deliver(r, f, failResult(asl.ErrExceedToleratedFailureThreshold, cause))
		return
	}
	outputs := make([]any, mr.Total())
	for _, it := range items {
		if it.Status == "SUCCEEDED" && it.Output != nil {
			outputs[it.Index] = json.RawMessage(it.Output)
		}
	}
	mr.Status, mr.StoppedAt = "SUCCEEDED", now
	if len(mr.ResultWriter) > 0 {
		// Results go to S3 on a worker; the state's output points at them.
		g.srv.store.PutMapRun(mr)
		g.writeResults(r, f, mr, items)
		return
	}
	g.srv.store.PutMapRun(mr)
	g.event(r, f, "MapRunSucceeded", "", nil)
	g.event(r, f, "MapStateSucceeded", "", nil)
	f.MapRun = ""
	raw, _ := json.Marshal(outputs)
	g.deliver(r, f, asl.TaskResult{Output: raw})
}

// resumeMapRuns runs when a run is loaded or nudged: a frame parked on a
// run whose children all finished while nobody was listening completes
// now, and a run with items still to launch launches them.
func (g *engine) resumeMapRuns(r *run) {
	for _, f := range r.e.Exec.Frames {
		if f.Status != asl.FrameParked || f.MapRun == "" {
			continue
		}
		mr, err := g.srv.store.GetMapRun(f.MapRun)
		if err != nil || mr == nil {
			g.deliver(r, f, failResult(asl.ErrRuntime, "the Map Run "+f.MapRun+" no longer exists"))
			continue
		}
		if mr.Status != "RUNNING" {
			continue
		}
		// Children that finished while the parent was down reported into
		// the record; running ones are being resumed by the sweep. Launch
		// what is left and settle if nothing is.
		g.launchMapChildren(r, f, mr)
	}
}

// abortMapRuns stops a parent's map runs when the parent is aborted: the
// record closes and the running children are aborted with it, as AWS does.
func (g *engine) abortMapRuns(r *run) {
	for _, f := range r.e.Exec.Frames {
		if f.MapRun == "" {
			continue
		}
		mr, err := g.srv.store.GetMapRun(f.MapRun)
		if err != nil || mr == nil || mr.Status != "RUNNING" {
			continue
		}
		mr.Status, mr.StoppedAt = "ABORTED", g.srv.store.now()
		g.srv.store.PutMapRun(mr)
		for i := 0; i < mr.Next; i++ {
			key := execKey(mr.Machine, fmt.Sprintf("%s-%d", mr.ID, i))
			if child := g.ensure(key); child != nil {
				g.finalize(child, "ABORTED", nil, "States.Runtime", "the parent execution was stopped")
			}
		}
	}
}

// ---- items ----

// batchItems applies an ItemBatcher: MaxItemsPerBatch groups items, and
// BatchInput is merged into every batch's input beside Items.
func batchItems(batcher json.RawMessage, items []json.RawMessage) ([]json.RawMessage, error) {
	if len(batcher) == 0 {
		return items, nil
	}
	var b struct {
		MaxItemsPerBatch      int             `json:"MaxItemsPerBatch"`
		MaxInputBytesPerBatch int             `json:"MaxInputBytesPerBatch"`
		BatchInput            json.RawMessage `json:"BatchInput"`
	}
	if err := json.Unmarshal(batcher, &b); err != nil {
		return nil, fmt.Errorf("ItemBatcher: %v", err)
	}
	if b.MaxItemsPerBatch <= 0 && b.MaxInputBytesPerBatch <= 0 {
		b.MaxItemsPerBatch = len(items)
		if b.MaxItemsPerBatch == 0 {
			b.MaxItemsPerBatch = 1
		}
	}
	var out []json.RawMessage
	for start := 0; start < len(items); {
		end, size := start, 0
		for end < len(items) {
			size += len(items[end])
			if (b.MaxItemsPerBatch > 0 && end-start >= b.MaxItemsPerBatch) ||
				(b.MaxInputBytesPerBatch > 0 && end > start && size > b.MaxInputBytesPerBatch) {
				break
			}
			end++
		}
		if end == start {
			end = start + 1
		}
		batch := map[string]any{}
		if len(b.BatchInput) > 0 {
			json.Unmarshal(b.BatchInput, &batch)
		}
		batch["Items"] = items[start:end]
		raw, _ := json.Marshal(batch)
		out = append(out, raw)
		start = end
	}
	return out, nil
}

// rawProcessor extracts the ItemProcessor's definition text for the frame's
// Map state from the execution's frozen definition, so a child execution
// freezes the exact text rather than a re-marshalled AST.
func rawProcessor(definition string, f *asl.Frame) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(definition), &doc); err != nil {
		return "", err
	}
	cur := doc
	for _, hop := range f.Def {
		states, _ := cur["States"].(map[string]any)
		st, _ := states[hop.State].(map[string]any)
		if st == nil {
			return "", fmt.Errorf("state %q not found in the definition", hop.State)
		}
		if hop.Branch >= 0 {
			branches, _ := st["Branches"].([]any)
			if hop.Branch >= len(branches) {
				return "", fmt.Errorf("state %q has no branch %d", hop.State, hop.Branch)
			}
			cur, _ = branches[hop.Branch].(map[string]any)
		} else {
			cur, _ = st["ItemProcessor"].(map[string]any)
			if cur == nil {
				cur, _ = st["Iterator"].(map[string]any)
			}
		}
		if cur == nil {
			return "", fmt.Errorf("could not descend into %q", hop.State)
		}
	}
	states, _ := cur["States"].(map[string]any)
	st, _ := states[f.State].(map[string]any)
	if st == nil {
		return "", fmt.Errorf("state %q not found", f.State)
	}
	proc := st["ItemProcessor"]
	if proc == nil {
		proc = st["Iterator"]
	}
	raw, err := json.Marshal(proc)
	return string(raw), err
}

// mapRunOf reads the mapRunArn parameter's record.
func (s *Server) mapRunOf(p map[string]any) (*MapRun, string) {
	arn := awsjson.Str(p, "mapRunArn")
	mr, err := s.store.GetMapRun(arn)
	if err != nil || mr == nil {
		return nil, arn
	}
	return mr, arn
}
