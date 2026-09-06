package asl

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The JSONPath data-flow pipeline:
//
//	raw input ──InputPath──▶ effective input ──Parameters──▶ task input
//	                                              (the task runs)
//	raw result ──ResultSelector──▶ selected
//	          ──ResultPath into the RAW input──▶ merged ──OutputPath──▶ output
//
// Two spec details are load-bearing and tested rather than assumed:
// ResultPath merges into the *raw* state input, not the post-InputPath
// effective input; and an explicit null path is distinct from an absent one —
// `"InputPath": null` yields {}, absent yields $.
//
// Documents move through the pipeline as decoded values (map[string]any,
// []any, json.Number, string, bool, nil), decoded with UseNumber so a
// 19-digit integer survives the trip.

// decodeDoc decodes a JSON document for pipeline work. A frame's Input always
// came from JSON, so the error path collapses to null.
func decodeDoc(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil
	}
	return v
}

// encodeDoc is the way back to the wire.
func encodeDoc(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

// resolve walks a parsed path against the data document, or the context
// object for a $$ path. ok is false when the path selects nothing.
func resolve(p Path, data, ctxObj any) (any, bool) {
	cur := data
	switch p.Root {
	case RootContext:
		cur = ctxObj
	case RootVariable:
		// The frame's variables ride inside the context object (buildContext),
		// so a $name path needs nothing the callers do not already pass.
		ctx, _ := ctxObj.(map[string]any)
		vars, _ := ctx[variablesKey].(map[string]any)
		v, present := vars[p.Variable]
		if !present {
			return nil, false
		}
		cur = v
	}
	for _, seg := range p.Segments {
		if seg.IsIndex {
			arr, isArr := cur.([]any)
			if !isArr || seg.Index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.Index]
			continue
		}
		obj, isObj := cur.(map[string]any)
		if !isObj {
			return nil, false
		}
		v, present := obj[seg.Key]
		if !present {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// selectPath applies a *path* field (InputPath, OutputPath): nil pointer is
// "$", the empty string is the parser's spelling of an explicit null, which
// discards the document. A path that selects nothing yields null — that is a
// path's contract, unlike a reference path's.
func selectPath(doc, ctxObj any, path *string, field string) (any, *Failure) {
	switch {
	case path == nil:
		return doc, nil
	case *path == "":
		return map[string]any{}, nil
	}
	p, err := ParsePath(*path)
	if err != nil {
		return nil, Failf(ErrRuntime, "%s: %v", field, err)
	}
	v, ok := resolve(p, doc, ctxObj)
	if !ok {
		return nil, nil
	}
	return v, nil
}

// injectPath applies ResultPath: writes result into rawInput at the
// reference path, creating intermediate objects. nil pointer replaces the
// document; the empty string (explicit null) discards the result.
func injectPath(rawInput, result any, path *string) (any, *Failure) {
	switch {
	case path == nil:
		return result, nil
	case *path == "":
		return rawInput, nil
	}
	p, err := ParsePath(*path)
	if err != nil {
		return nil, Failf(ErrResultPathMatchFailure, "ResultPath: %v", err)
	}
	// The result is deep-copied before insertion. With no Parameters the
	// result IS the raw input document, and inserting a document into itself
	// builds a cycle json.Marshal cannot serialise.
	return setAt(rawInput, p.Segments, decodeDoc(encodeDoc(result)), *path)
}

// setAt writes val at the segment chain, creating objects along the way. A
// segment that lands on the wrong shape — a field on an array, an index past
// the end — is States.ResultPathMatchFailure, which is AWS's name for "the
// result had nowhere to go".
func setAt(doc any, segs []Segment, val any, raw string) (any, *Failure) {
	if len(segs) == 0 {
		return val, nil
	}
	seg := segs[0]
	if seg.IsIndex {
		arr, isArr := doc.([]any)
		if !isArr || seg.Index >= len(arr) {
			return nil, Failf(ErrResultPathMatchFailure,
				"ResultPath %q indexes [%d] into something that is not an array of that size", raw, seg.Index)
		}
		v, f := setAt(arr[seg.Index], segs[1:], val, raw)
		if f != nil {
			return nil, f
		}
		arr[seg.Index] = v
		return arr, nil
	}
	obj, isObj := doc.(map[string]any)
	switch {
	case isObj:
	case doc == nil:
		obj = map[string]any{}
	default:
		return nil, Failf(ErrResultPathMatchFailure,
			"ResultPath %q sets a field on something that is not an object", raw)
	}
	v, f := setAt(obj[seg.Key], segs[1:], val, raw)
	if f != nil {
		return nil, f
	}
	obj[seg.Key] = v
	return obj, nil
}

// evalTemplate walks a payload template (Parameters, ResultSelector,
// ItemSelector). A key ending in ".$" is stripped, and its string value is
// evaluated: a $ or $$ path, or a States.* intrinsic call.
func evalTemplate(tmpl json.RawMessage, data, ctxObj any, env Env) (any, *Failure) {
	return evalTemplateNode(decodeDoc(tmpl), data, ctxObj, env)
}

func evalTemplateNode(node, data, ctxObj any, env Env) (any, *Failure) {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			if strings.HasSuffix(k, ".$") {
				expr, isStr := v.(string)
				if !isStr {
					return nil, Failf(ErrParameterPathFailure,
						"the value of %q must be a string path or intrinsic call", k)
				}
				val, f := evalExpr(expr, data, ctxObj, env)
				if f != nil {
					return nil, f
				}
				out[strings.TrimSuffix(k, ".$")] = val
				continue
			}
			val, f := evalTemplateNode(v, data, ctxObj, env)
			if f != nil {
				return nil, f
			}
			out[k] = val
		}
		return out, nil
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			val, f := evalTemplateNode(v, data, ctxObj, env)
			if f != nil {
				return nil, f
			}
			out[i] = val
		}
		return out, nil
	default:
		return node, nil
	}
}

// evalExpr evaluates one ".$" expression: a path into the data, a $$ path
// into the context object, or an intrinsic call.
func evalExpr(expr string, data, ctxObj any, env Env) (any, *Failure) {
	switch {
	case strings.HasPrefix(expr, "$"):
		p, err := ParsePath(expr)
		if err != nil {
			return nil, Failf(ErrParameterPathFailure, "%v", err)
		}
		v, ok := resolve(p, data, ctxObj)
		if !ok {
			return nil, Failf(ErrParameterPathFailure, "%q selects nothing here", expr)
		}
		return v, nil
	case strings.HasPrefix(expr, "States."):
		return evalIntrinsic(expr, data, ctxObj, env)
	default:
		return nil, Failf(ErrParameterPathFailure,
			"%q is neither a path nor an intrinsic call", expr)
	}
}
