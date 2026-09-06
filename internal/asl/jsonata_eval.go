package asl

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	jsonata "github.com/blues/jsonata-go"
)

// The JSONata dialect's evaluation core.
//
// A JSONata state's fields hold ordinary JSON in which any string of the form
// `{% <expression> %}` — at the top, or nested anywhere inside an object or
// array — is evaluated and replaced by its value. Everything else is a literal.
// There is no `.$` convention and no States.* intrinsic: JSONata brings its
// own function library, and AWS adds five ($partition, $range, $hash, $random,
// $uuid, $parse), registered in jsonata_fn.go.
//
// Expressions see one reserved variable, $states, with `input`, `context`, and
// where the phase provides them `result` and `errorOutput`; plus every
// variable the execution has assigned, as `$name`. A scope value carries
// exactly that, and every evaluation goes through it.
//
// The library (blues/jsonata-go) works on plain decoded values, but it treats
// json.Number — which decodeDoc produces so large integers survive — as a
// string. So values are converted on the way in (json.Number becomes int64
// when it fits, else float64) and normalised on the way out by a JSON round
// trip, which also flattens whatever concrete types the library's own
// functions return into the map/slice/json.Number shape the rest of the
// pipeline expects.

// jsonataScope is what one evaluation can see.
type jsonataScope struct {
	input       any
	context     any
	result      any
	hasResult   bool
	errorOutput any
	hasError    bool
	vars        map[string]json.RawMessage
	env         Env
}

// states builds the $states object for the scope.
func (sc *jsonataScope) states() map[string]any {
	st := map[string]any{
		"input":   toJSONata(sc.input),
		"context": toJSONata(sc.context),
	}
	if sc.hasResult {
		st["result"] = toJSONata(sc.result)
	}
	if sc.hasError {
		st["errorOutput"] = toJSONata(sc.errorOutput)
	}
	return st
}

// jsonataExpr reports whether a string is an expression, and returns its
// source. AWS's rule: `{%` with no leading space, `%}` with no trailing space.
func jsonataExpr(s string) (string, bool) {
	if len(s) < 4 || !strings.HasPrefix(s, "{%") || !strings.HasSuffix(s, "%}") {
		return "", false
	}
	return strings.TrimSpace(s[2 : len(s)-2]), true
}

// jsonataCompileErrs remembers, by source, whether an expression parses —
// the analyser asks on every validate and create, and a definition holds
// the same strings each time. Only the verdict is cached, not the compiled
// Expr: the library binds variables by mutating the Expr's registry and
// offers no way to unbind, so an Expr shared between evaluations would leak
// one scope's $name into the next. A fresh parse per evaluation is a few
// microseconds on expressions this size.
var jsonataCompileErrs sync.Map

// compileJSONata parses one expression.
func compileJSONata(src string) (*jsonata.Expr, error) {
	expr, err := jsonata.Compile(src)
	if err != nil {
		jsonataCompileErrs.Store(src, err)
		return nil, err
	}
	jsonataCompileErrs.Store(src, nil)
	return expr, nil
}

// jsonataParses is the cached verdict for the analyser.
func jsonataParses(src string) error {
	if v, ok := jsonataCompileErrs.Load(src); ok {
		if v == nil {
			return nil
		}
		return v.(error)
	}
	_, err := compileJSONata(src)
	return err
}

// checkJSONata is the analyser's view: the compile error for an expression
// string, or nil. A string that opens `{%` without closing `%}` (or the
// reverse) is what AWS calls "improperly opening or closing the expression",
// and is refused too.
func checkJSONata(s string) error {
	src, ok := jsonataExpr(s)
	if !ok {
		if strings.HasPrefix(s, "{%") || strings.HasSuffix(s, "%}") {
			return fmt.Errorf("%q is not a well-formed JSONata expression: it must start with {%% and end with %%}", s)
		}
		return nil
	}
	if err := jsonataParses(src); err != nil {
		return fmt.Errorf("JSONata expression %q: %v", src, err)
	}
	return nil
}

// eval runs one expression source in the scope. The library panics on a few
// internal type mismatches rather than returning an error; inside an Advance
// that would take the engine down, so a panic becomes the evaluation error
// it should have been.
func (sc *jsonataScope) eval(src string) (v any, fail *Failure) {
	defer func() {
		if r := recover(); r != nil {
			v, fail = nil, Failf(ErrQueryEvaluationError, "%q: %v", src, r)
		}
	}()
	expr, err := compileJSONata(src)
	if err != nil {
		return nil, Failf(ErrQueryEvaluationError, "%q does not compile: %v", src, err)
	}
	vars := map[string]any{"states": sc.states()}
	for name, raw := range sc.vars {
		vars[name] = toJSONata(decodeDoc(raw))
	}

	if err := expr.RegisterVars(vars); err != nil {
		return nil, Failf(ErrQueryEvaluationError, "%q: %v", src, err)
	}
	if err := expr.RegisterExts(jsonataExts(sc.env)); err != nil {
		return nil, Failf(ErrQueryEvaluationError, "%q: %v", src, err)
	}
	// `$` on its own is the state input, so `$.field` reads as it would
	// anywhere else JSONata runs; AWS documents only $states.input.
	v, err = expr.Eval(toJSONata(sc.input))
	switch {
	case errors.Is(err, jsonata.ErrUndefined):
		// JSON has no undefined, so an expression that selects nothing is
		// an error, not a null — AWS: "Failure to return a result".
		return nil, Failf(ErrQueryEvaluationError, "%q yields no result", src)
	case err != nil:
		return nil, Failf(ErrQueryEvaluationError, "%q: %v", src, err)
	}
	return fromJSONata(v), nil
}

// value evaluates a whole field: a raw JSON document with expressions
// anywhere inside it. An absent field yields nil, false.
func (sc *jsonataScope) value(raw json.RawMessage) (any, bool, *Failure) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	v, fail := sc.node(decodeDoc(raw))
	return v, true, fail
}

// node walks a decoded document, replacing expression strings.
func (sc *jsonataScope) node(v any) (any, *Failure) {
	switch n := v.(type) {
	case string:
		if src, ok := jsonataExpr(n); ok {
			return sc.eval(src)
		}
		return n, nil
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, child := range n {
			val, fail := sc.node(child)
			if fail != nil {
				return nil, fail
			}
			out[k] = val
		}
		return out, nil
	case []any:
		out := make([]any, len(n))
		for i, child := range n {
			val, fail := sc.node(child)
			if fail != nil {
				return nil, fail
			}
			out[i] = val
		}
		return out, nil
	}
	return v, nil
}

// number evaluates a field that must yield a number — TimeoutSeconds,
// Seconds, MaxConcurrency — given the literal or the expression the parser
// split it into.
func (sc *jsonataScope) number(field string, literal *float64, expr string) (float64, bool, *Failure) {
	switch {
	case literal != nil:
		return *literal, true, nil
	case expr == "":
		return 0, false, nil
	}
	v, fail := sc.node(expr)
	if fail != nil {
		return 0, false, fail
	}
	n, isNum := toFloat(v)
	if !isNum {
		return 0, false, Failf(ErrQueryEvaluationError, "%s must evaluate to a number, got %s", field, jsonKindOf(v))
	}
	return n, true, nil
}

// str evaluates a field that must yield a string — Timestamp, Error, Cause.
func (sc *jsonataScope) str(field, s string) (string, *Failure) {
	v, fail := sc.node(s)
	if fail != nil {
		return "", fail
	}
	out, isStr := v.(string)
	if !isStr {
		return "", Failf(ErrQueryEvaluationError, "%s must evaluate to a string, got %s", field, jsonKindOf(v))
	}
	return out, nil
}

// toJSONata converts a decoded document into what the library evaluates:
// json.Number becomes a Go number, everything else is already right.
func toJSONata(v any) any {
	switch n := v.(type) {
	case json.Number:
		if i, err := strconv.ParseInt(string(n), 10, 64); err == nil {
			return i
		}
		f, _ := n.Float64()
		return f
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, child := range n {
			out[k] = toJSONata(child)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, child := range n {
			out[i] = toJSONata(child)
		}
		return out
	}
	return v
}

// fromJSONata brings a library value back into pipeline shape. The round trip
// through JSON is deliberate: the library returns int64, float64, and its
// own functions' concrete slice and map types, and one Marshal/UseNumber
// decode normalises all of them at once.
func fromJSONata(v any) any {
	return decodeDoc(encodeDoc(v))
}

// jsonKind names a value's JSON type in diagnostics; the analyser's own
// spelling, extended to the pipeline's number types.
func jsonKindOf(v any) string {
	switch v.(type) {
	case json.Number, int64:
		return "a number"
	}
	return jsonKind(v)
}
