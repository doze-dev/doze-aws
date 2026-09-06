package asl

import (
	"encoding/json"
	"fmt"
	"unicode"
)

// Workflow variables: Assign writes them, `$name` reads them.
//
// The scope model is the frame's (see Frame.Vars). What lives here is the
// evaluation rule, which AWS fixes precisely: every expression in an Assign is
// evaluated against the variables as they stood on state entry, and only then
// are the assignments made — so `"x": "{% $a %}", "nextX": "{% $x %}"` reads
// the old x into nextX. Assign and Output are likewise independent: an Output
// expression never sees what the same state's Assign wrote.

// copyVars duplicates a scope for a child frame. The values are immutable
// json.RawMessage, so a shallow map copy is a full one.
func copyVars(vars map[string]json.RawMessage) map[string]json.RawMessage {
	if len(vars) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}

// jsonataAssign evaluates an Assign object in the scope and writes the
// results into the frame. Absent Assign is a no-op. The evaluation sees the
// frame's variables as they were, because the scope was built before this
// call and the writes land after every expression has been evaluated.
func jsonataAssign(sc *jsonataScope, raw json.RawMessage, f *Frame) *Failure {
	if len(raw) == 0 {
		return nil
	}
	v, _, fail := sc.value(raw)
	if fail != nil {
		return fail
	}
	obj, isObj := v.(map[string]any)
	if !isObj {
		return Failf(ErrQueryEvaluationError, "Assign must be an object, got %s", jsonKindOf(v))
	}
	if f.Vars == nil {
		f.Vars = make(map[string]json.RawMessage, len(obj))
	}
	for name, val := range obj {
		f.Vars[name] = encodeDoc(val)
	}
	return nil
}

// checkAssign is the analyser's half: Assign must be an object, and each key
// must be a variable name — an identifier by Unicode's rules (UAX #31), at
// most 80 characters, which is what makes `$name` parse in an expression.
// Expressions inside are checked by the field walk that covers every JSONata
// field, so only the shape is here.
func checkAssign(raw json.RawMessage, at string, r *Report) {
	if len(raw) == 0 {
		return
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		r.addf(at, "Assign must be a JSON object")
		return
	}
	for name := range obj {
		if err := checkVariableName(name); err != nil {
			r.addf(at, "%v", err)
		}
	}
}

func checkVariableName(name string) error {
	runes := []rune(name)
	switch {
	case len(runes) == 0:
		return fmt.Errorf("a variable name may not be empty")
	case len(runes) > 80:
		return fmt.Errorf("variable name %q is longer than 80 characters", name)
	case name == "states":
		return fmt.Errorf("$states is reserved and cannot be assigned")
	}
	for i, c := range runes {
		if i == 0 && !(unicode.IsLetter(c) || c == '_') {
			return fmt.Errorf("variable name %q must start with a letter or underscore", name)
		}
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || unicode.Is(unicode.Mn, c) || unicode.Is(unicode.Mc, c) || unicode.Is(unicode.Pc, c)) {
			return fmt.Errorf("variable name %q contains %q, which is not allowed in an identifier", name, string(c))
		}
	}
	return nil
}
