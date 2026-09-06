package asl

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Evaluation of the States.* intrinsic functions. Every failure — wrong
// arity, wrong type, a broken template — is States.IntrinsicFailure, which is
// the name a Catch matches; the cause strings name the function and the
// mistake so the failure reads like a diagnostic rather than a shrug.

func evalIntrinsic(expr string, data, ctxObj any, env Env) (any, *Failure) {
	call, err := parseIntrinsic(expr)
	if err != nil {
		return nil, Failf(ErrIntrinsicFailure, "%q: %v", expr, err)
	}
	return evalCall(call, data, ctxObj, env)
}

func evalCall(call *callExpr, data, ctxObj any, env Env) (any, *Failure) {
	args := make([]any, len(call.args))
	for i, argExpr := range call.args {
		switch a := argExpr.(type) {
		case *litExpr:
			args[i] = a.val
		case *pathExpr:
			v, ok := resolve(a.path, data, ctxObj)
			if !ok {
				return nil, Failf(ErrIntrinsicFailure, "%s: the path %q selects nothing", call.name, a.path.Raw)
			}
			args[i] = v
		case *callExpr:
			v, fail := evalCall(a, data, ctxObj, env)
			if fail != nil {
				return nil, fail
			}
			args[i] = v
		}
	}

	fn, known := intrinsics[call.name]
	if !known {
		return nil, Failf(ErrIntrinsicFailure, "%q is not an intrinsic function", call.name)
	}
	return fn(call.name, args, env)
}

type intrinsicFn func(name string, args []any, env Env) (any, *Failure)

var intrinsics = map[string]intrinsicFn{
	"States.Format":         fnFormat,
	"States.StringToJson":   fnStringToJson,
	"States.JsonToString":   fnJsonToString,
	"States.Array":          fnArray,
	"States.ArrayPartition": fnArrayPartition,
	"States.ArrayContains":  fnArrayContains,
	"States.ArrayRange":     fnArrayRange,
	"States.ArrayGetItem":   fnArrayGetItem,
	"States.ArrayLength":    fnArrayLength,
	"States.ArrayUnique":    fnArrayUnique,
	"States.Base64Encode":   fnBase64Encode,
	"States.Base64Decode":   fnBase64Decode,
	"States.Hash":           fnHash,
	"States.JsonMerge":      fnJsonMerge,
	"States.MathRandom":     fnMathRandom,
	"States.MathAdd":        fnMathAdd,
	"States.StringSplit":    fnStringSplit,
	"States.UUID":           fnUUID,
}

func arity(name string, args []any, want int) *Failure {
	if len(args) != want {
		return Failf(ErrIntrinsicFailure, "%s takes %d argument(s), got %d", name, want, len(args))
	}
	return nil
}

func argString(name string, args []any, i int) (string, *Failure) {
	s, ok := args[i].(string)
	if !ok {
		return "", Failf(ErrIntrinsicFailure, "%s: argument %d must be a string", name, i+1)
	}
	return s, nil
}

func argNumber(name string, args []any, i int) (float64, *Failure) {
	f, ok := toFloat(args[i])
	if !ok {
		return 0, Failf(ErrIntrinsicFailure, "%s: argument %d must be a number", name, i+1)
	}
	return f, nil
}

func argArray(name string, args []any, i int) ([]any, *Failure) {
	a, ok := args[i].([]any)
	if !ok {
		return nil, Failf(ErrIntrinsicFailure, "%s: argument %d must be an array", name, i+1)
	}
	return a, nil
}

// fnFormat replaces each {} in the template with the next argument — strings
// verbatim, everything else JSON-encoded. \{ and \} are literal braces.
func fnFormat(name string, args []any, _ Env) (any, *Failure) {
	if len(args) == 0 {
		return nil, Failf(ErrIntrinsicFailure, "%s needs a template string", name)
	}
	tmpl, fail := argString(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	var b strings.Builder
	next := 1
	for i := 0; i < len(tmpl); i++ {
		switch {
		case tmpl[i] == '\\' && i+1 < len(tmpl) && (tmpl[i+1] == '{' || tmpl[i+1] == '}'):
			b.WriteByte(tmpl[i+1])
			i++
		case tmpl[i] == '{' && i+1 < len(tmpl) && tmpl[i+1] == '}':
			if next >= len(args) {
				return nil, Failf(ErrIntrinsicFailure, "%s: the template has more {} than arguments", name)
			}
			if s, isStr := args[next].(string); isStr {
				b.WriteString(s)
			} else {
				b.Write(encodeDoc(args[next]))
			}
			next++
			i++
		default:
			b.WriteByte(tmpl[i])
		}
	}
	return b.String(), nil
}

func fnStringToJson(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 1); fail != nil {
		return nil, fail
	}
	s, fail := argString(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	var probe any
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return nil, Failf(ErrIntrinsicFailure, "%s: %v", name, err)
	}
	return decodeDoc(json.RawMessage(s)), nil
}

func fnJsonToString(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 1); fail != nil {
		return nil, fail
	}
	return string(encodeDoc(args[0])), nil
}

func fnArray(_ string, args []any, _ Env) (any, *Failure) {
	if args == nil {
		args = []any{}
	}
	return args, nil
}

func fnArrayPartition(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 2); fail != nil {
		return nil, fail
	}
	arr, fail := argArray(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	size, fail := argNumber(name, args, 1)
	if fail != nil {
		return nil, fail
	}
	n := int(size)
	if n <= 0 {
		return nil, Failf(ErrIntrinsicFailure, "%s: the chunk size must be positive", name)
	}
	out := []any{}
	for start := 0; start < len(arr); start += n {
		end := min(start+n, len(arr))
		out = append(out, append([]any{}, arr[start:end]...))
	}
	return out, nil
}

func fnArrayContains(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 2); fail != nil {
		return nil, fail
	}
	arr, fail := argArray(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	want := string(encodeDoc(args[1]))
	for _, v := range arr {
		if string(encodeDoc(v)) == want {
			return true, nil
		}
	}
	return false, nil
}

func fnArrayRange(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 3); fail != nil {
		return nil, fail
	}
	start, fail := argNumber(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	end, fail := argNumber(name, args, 1)
	if fail != nil {
		return nil, fail
	}
	step, fail := argNumber(name, args, 2)
	if fail != nil {
		return nil, fail
	}
	if step == 0 {
		return nil, Failf(ErrIntrinsicFailure, "%s: the step may not be zero", name)
	}
	out := []any{}
	for v := start; (step > 0 && v <= end) || (step < 0 && v >= end); v += step {
		out = append(out, numberValue(v))
		if len(out) > 1000 {
			return nil, Failf(ErrIntrinsicFailure, "%s: the range would exceed 1000 items", name)
		}
	}
	return out, nil
}

func fnArrayGetItem(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 2); fail != nil {
		return nil, fail
	}
	arr, fail := argArray(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	idx, fail := argNumber(name, args, 1)
	if fail != nil {
		return nil, fail
	}
	i := int(idx)
	if i < 0 || i >= len(arr) {
		return nil, Failf(ErrIntrinsicFailure, "%s: index %d is outside an array of %d", name, i, len(arr))
	}
	return arr[i], nil
}

func fnArrayLength(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 1); fail != nil {
		return nil, fail
	}
	arr, fail := argArray(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	return json.Number(strconv.Itoa(len(arr))), nil
}

func fnArrayUnique(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 1); fail != nil {
		return nil, fail
	}
	arr, fail := argArray(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	seen := map[string]bool{}
	out := []any{}
	for _, v := range arr {
		key := string(encodeDoc(v))
		if !seen[key] {
			seen[key] = true
			out = append(out, v)
		}
	}
	return out, nil
}

func fnBase64Encode(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 1); fail != nil {
		return nil, fail
	}
	s, fail := argString(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}

func fnBase64Decode(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 1); fail != nil {
		return nil, fail
	}
	s, fail := argString(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, Failf(ErrIntrinsicFailure, "%s: %v", name, err)
	}
	return string(decoded), nil
}

func fnHash(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 2); fail != nil {
		return nil, fail
	}
	data, fail := argString(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	algo, fail := argString(name, args, 1)
	if fail != nil {
		return nil, fail
	}
	var sum []byte
	switch algo {
	case "MD5":
		h := md5.Sum([]byte(data))
		sum = h[:]
	case "SHA-1":
		h := sha1.Sum([]byte(data))
		sum = h[:]
	case "SHA-256":
		h := sha256.Sum256([]byte(data))
		sum = h[:]
	case "SHA-384":
		h := sha512.Sum384([]byte(data))
		sum = h[:]
	case "SHA-512":
		h := sha512.Sum512([]byte(data))
		sum = h[:]
	default:
		return nil, Failf(ErrIntrinsicFailure,
			"%s: %q is not a supported algorithm (MD5, SHA-1, SHA-256, SHA-384, SHA-512)", name, algo)
	}
	return hex.EncodeToString(sum), nil
}

func fnJsonMerge(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 3); fail != nil {
		return nil, fail
	}
	a, aOK := args[0].(map[string]any)
	b, bOK := args[1].(map[string]any)
	if !aOK || !bOK {
		return nil, Failf(ErrIntrinsicFailure, "%s merges two objects", name)
	}
	deep, deepOK := args[2].(bool)
	if !deepOK || deep {
		// AWS itself only implements the shallow mode; the flag must be false.
		return nil, Failf(ErrIntrinsicFailure, "%s: the third argument must be false (only shallow merge exists)", name)
	}
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out, nil
}

func fnMathRandom(name string, args []any, env Env) (any, *Failure) {
	if len(args) != 2 && len(args) != 3 {
		return nil, Failf(ErrIntrinsicFailure, "%s takes start and end (and an optional seed), got %d arguments", name, len(args))
	}
	start, fail := argNumber(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	end, fail := argNumber(name, args, 1)
	if fail != nil {
		return nil, fail
	}
	if end < start {
		return nil, Failf(ErrIntrinsicFailure, "%s: end is below start", name)
	}
	r := 0.5
	if env.Rand != nil {
		r = env.Rand()
	}
	v := math.Floor(start + r*(end-start+1))
	if v > end {
		v = end
	}
	return json.Number(strconv.FormatInt(int64(v), 10)), nil
}

func fnMathAdd(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 2); fail != nil {
		return nil, fail
	}
	a, fail := argNumber(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	b, fail := argNumber(name, args, 1)
	if fail != nil {
		return nil, fail
	}
	return numberValue(a + b), nil
}

func fnStringSplit(name string, args []any, _ Env) (any, *Failure) {
	if fail := arity(name, args, 2); fail != nil {
		return nil, fail
	}
	s, fail := argString(name, args, 0)
	if fail != nil {
		return nil, fail
	}
	sep, fail := argString(name, args, 1)
	if fail != nil {
		return nil, fail
	}
	if sep == "" {
		return nil, Failf(ErrIntrinsicFailure, "%s: the separator may not be empty", name)
	}
	out := []any{}
	for _, part := range strings.Split(s, sep) {
		if part != "" { // AWS drops the empty segments
			out = append(out, part)
		}
	}
	return out, nil
}

func fnUUID(name string, args []any, env Env) (any, *Failure) {
	if fail := arity(name, args, 0); fail != nil {
		return nil, fail
	}
	var b [16]byte
	if env.Rand != nil {
		// Deterministic under an injected Rand, so tests can pin it.
		for i := range b {
			b[i] = byte(env.Rand() * 256)
		}
	} else if _, err := rand.Read(b[:]); err != nil {
		return nil, Failf(ErrIntrinsicFailure, "%s: %v", name, err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// numberValue renders a float as a json.Number, keeping integers integral so
// MathAdd(1, 2) is 3 rather than 3.0 on the wire.
func numberValue(f float64) json.Number {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return json.Number(strconv.FormatInt(int64(f), 10))
	}
	return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
}
