package asl

import "encoding/json"

// Distributed Map. Every item runs as its own execution rather than a frame
// of this one, under a Map Run the service keeps a record of. The
// interpreter's part is small on purpose: it parks the frame and hands the
// engine the items (or the fact that an ItemReader must fetch them — reading
// S3 is the engine's job, this package does no I/O), and later takes the
// joined result through Deliver like any other parked task.

// EffMapRun asks the engine to run a Map state's items as child executions.
// Items holds the items after ItemsPath (or Items) and before ItemSelector,
// or nil when the state has an ItemReader for the engine to consult.
type EffMapRun struct {
	Frame int
	Items []json.RawMessage
	// FromReader is true when Items is nil because an ItemReader supplies
	// them.
	FromReader bool
}

func (EffMapRun) isEffect() {}

// startMapRun parks the frame and returns the effect. Called by advanceMap
// and jsonataMap once the item list is in hand.
func startMapRun(s *State, ex *Exec, f *Frame, list []any, fromReader bool) (Effect, []Note, error) {
	f.Status = FrameParked
	f.BeginSpawn()
	eff := EffMapRun{Frame: f.ID, FromReader: fromReader}
	for _, item := range list {
		eff.Items = append(eff.Items, encodeDoc(item))
	}
	return eff, nil, nil
}

// SelectItems applies a Map state's ItemSelector (or Parameters, its retired
// spelling) to each item, with $$.Map.Item.Index/Value in the context — the
// same evaluation an inline Map does per frame, exposed for the engine's
// Map Run to feed child executions. items is the ItemReader's output when
// the state has one. A selector failure fails the state as it would inline.
func SelectItems(d *Definition, ex *Exec, f *Frame, items []json.RawMessage, env Env) ([]json.RawMessage, *Failure) {
	sub, err := d.Sub(f.Def)
	if err != nil {
		return nil, Failf(ErrRuntime, "%v", err)
	}
	s := sub.States[f.State]
	if s == nil {
		return nil, Failf(ErrRuntime, "state %q does not exist", f.State)
	}
	selector := s.ItemSelector
	if len(selector) == 0 {
		selector = s.Parameters
	}
	if len(selector) == 0 {
		return items, nil
	}
	input := decodeDoc(f.Input)
	if s.Dialect() == JSONata {
		// Only an ItemReader's items reach here unselected under JSONata;
		// jsonataMap selects inline items itself before parking.
		out := make([]json.RawMessage, 0, len(items))
		for i, itemRaw := range items {
			probe := &Frame{ID: f.ID, State: s.Name, Branch: i, MapItem: itemRaw, EnteredAt: f.EnteredAt, Vars: f.Vars}
			scope := &jsonataScope{input: input, context: buildContext(ex, probe, ""), vars: f.Vars, env: env}
			v, _, fail := scope.value(selector)
			if fail != nil {
				return nil, fail
			}
			out = append(out, encodeDoc(v))
		}
		return out, nil
	}
	ctxObj := buildContext(ex, f, "")
	eff, fail := selectPath(input, ctxObj, s.InputPath, "InputPath")
	if fail != nil {
		return nil, fail
	}
	out := make([]json.RawMessage, 0, len(items))
	for i, itemRaw := range items {
		probe := &Frame{ID: f.ID, State: s.Name, Branch: i, MapItem: itemRaw, EnteredAt: f.EnteredAt, Vars: f.Vars}
		itemCtx := buildContext(ex, probe, "")
		selected, fail := evalTemplate(selector, eff, itemCtx, env)
		if fail != nil {
			return nil, fail
		}
		out = append(out, encodeDoc(selected))
	}
	return out, nil
}

// MapRunConfig is what the engine needs from a Distributed Map state.
type MapRunConfig struct {
	MaxConcurrency             int
	ToleratedFailureCount      *int
	ToleratedFailurePercentage *float64
	ItemReader                 json.RawMessage
	ItemBatcher                json.RawMessage
	ResultWriter               json.RawMessage
	Label                      string
	ExecutionType              string // STANDARD | EXPRESS
	Processor                  *Definition
}

// MapRunConfigOf reads the state's Map Run configuration for frame f.
func MapRunConfigOf(d *Definition, f *Frame) (MapRunConfig, bool) {
	sub, err := d.Sub(f.Def)
	if err != nil {
		return MapRunConfig{}, false
	}
	s := sub.States[f.State]
	if s == nil || s.Type != Map {
		return MapRunConfig{}, false
	}
	cfg := MapRunConfig{
		ItemReader: s.ItemReader, ItemBatcher: s.ItemBatcher, ResultWriter: s.ResultWriter,
		Label: s.Label, Processor: s.Processor(), ExecutionType: "STANDARD",
	}
	if p := s.Processor(); p != nil && p.ProcessorExecutionType != "" {
		cfg.ExecutionType = p.ProcessorExecutionType
	}
	if s.MaxConcurrency != nil {
		cfg.MaxConcurrency = int(*s.MaxConcurrency)
	}
	if f.Limit > 0 {
		cfg.MaxConcurrency = f.Limit
	}
	if s.ToleratedFailureCount != nil {
		n := int(*s.ToleratedFailureCount)
		cfg.ToleratedFailureCount = &n
	}
	cfg.ToleratedFailurePercentage = s.ToleratedFailurePercentage
	return cfg, true
}

// EvalParameters evaluates a payload template (an ItemReader's or
// ResultWriter's Parameters) against a document, with no context object —
// $$ is not available to them on AWS either.
func EvalParameters(tmpl json.RawMessage, input json.RawMessage) (json.RawMessage, *Failure) {
	if len(tmpl) == 0 {
		return json.RawMessage(`{}`), nil
	}
	v, fail := evalTemplate(tmpl, decodeDoc(input), map[string]any{}, Env{})
	if fail != nil {
		return nil, fail
	}
	return encodeDoc(v), nil
}
