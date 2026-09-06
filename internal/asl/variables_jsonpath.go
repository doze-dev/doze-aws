package asl

import "encoding/json"

// Variables in the JSONPath dialect. Assign is a payload template — `.$`
// keys hold paths — evaluated against the state's input for Pass, Wait,
// Choice and Succeed and against the raw task result for Task, Parallel and
// Map, with $$ and $name available as anywhere else; a Choice rule's Assign
// runs when that rule is taken and a Catcher's against the error output.
// The result is written into the frame's scope, the same scope JSONata
// states use, so a machine that mixes dialects shares its variables.

// assignPaths evaluates tmpl and writes the result's fields into f.Vars.
// Every value is evaluated first, then written: a template that fails
// halfway leaves the variables as they were.
func assignPaths(f *Frame, tmpl json.RawMessage, data, ctxObj any, env Env) *Failure {
	if len(tmpl) == 0 {
		return nil
	}
	v, fail := evalTemplate(tmpl, data, ctxObj, env)
	if fail != nil {
		return fail
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return Failf(ErrRuntime, "Assign must be an object")
	}
	if len(obj) == 0 {
		return nil
	}
	if f.Vars == nil {
		f.Vars = map[string]json.RawMessage{}
	}
	for name, val := range obj {
		if err := checkVariableName(name); err != nil {
			return Failf(ErrRuntime, "Assign: %v", err)
		}
		f.Vars[name] = encodeDoc(val)
	}
	return nil
}
