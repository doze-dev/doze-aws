package asl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"

	jsonata "github.com/blues/jsonata-go"
	"github.com/blues/jsonata-go/jtypes"
)

// The five functions AWS adds to JSONata, standing in for the intrinsics a
// JSONata state cannot call. Four of them are the intrinsic under a new name,
// so they wrap the intrinsic's implementation rather than re-deriving it: the
// argument and edge-case rules — a zero step, an unknown hash algorithm, the
// 1000-item range cap — are AWS's, already encoded once in intrinsic_fn.go.
//
// $random is the one that differs: JSONata's own $random takes no seed, and
// AWS's takes an optional one, so this overrides the built-in. Both $random and
// $uuid draw from Env.Rand when the caller injected one, which is how tests
// pin them.

// jsonataExts builds the extension table for one evaluation. Rebuilt per call
// because $random and $uuid close over the Env.
func jsonataExts(env Env) map[string]jsonata.Extension {
	return map[string]jsonata.Extension{
		"partition": {Func: func(arr, size any) (any, error) {
			return viaIntrinsic("$partition", fnArrayPartition, env, arr, size)
		}},
		"range": {Func: func(start, end, step any) (any, error) {
			return viaIntrinsic("$range", fnArrayRange, env, start, end, step)
		}},
		"hash": {Func: func(data, algo any) (any, error) {
			return viaIntrinsic("$hash", fnHash, env, data, algo)
		}},
		"uuid": {Func: func() (any, error) {
			return viaIntrinsic("$uuid", fnUUID, env)
		}},
		"random": {Func: func(seed jtypes.OptionalFloat64) (any, error) {
			if seed.IsSet() {
				return rand.New(rand.NewSource(int64(seed.Float64))).Float64(), nil
			}
			if env.Rand != nil {
				return env.Rand(), nil
			}
			return rand.Float64(), nil
		}},
		// ---- gaps in the embedded library, filled here ----
		//
		// Everything below this line is standard JSONata that AWS supports and
		// blues/jsonata-go does not implement. Measured, not guessed:
		// jsonata_dialect_test.go probes the documented library and names what
		// is missing, and removing any of these makes it fail.
		//
		// They live beside the AWS additions because the mechanism is the same —
		// RegisterExts, which can shadow a built-in as $random already does —
		// but they are a different KIND of thing: the additions above are
		// AWS-specific, these are the base language.

		// $assert(condition, message) raises when the condition is false and
		// yields nothing when it is true. jtypes.OptionalString because the
		// message is optional in practice even though the docs show two
		// arguments; a bare $assert(cond) should not be an arity error.
		"assert": {Func: func(ok bool, msg jtypes.OptionalString) (any, error) {
			if ok {
				// Nothing, rather than a value: JSONata's $assert returns
				// undefined on success, and a caller writing
				// $assert(c, m); rest would be surprised by a null appearing.
				return nil, jtypes.ErrUndefined
			}
			if msg.IsSet() {
				return nil, errors.New(msg.String)
			}
			return nil, errors.New("$assert: the condition was false")
		}},

		// $string(value, prettify) — the library has only the one-argument
		// form, so this SHADOWS it and has to reproduce the single-argument
		// behaviour exactly as well as adding the second. That is the risk in
		// this entry and the reason the dialect test probes $string against
		// every JSON type in both forms rather than once.
		"string": {Func: func(v any, prettify jtypes.OptionalBool) (any, error) {
			return jsonataString(v, prettify.IsSet() && prettify.Bool)
		}},

		// The docs' rule for the built-ins: "functions that require integer
		// values as parameters will automatically round down any non-integer
		// numbers provided." integralOf truncates, and refuses a value no
		// int64 can hold rather than wrapping silently.
		"formatInteger": {Func: func(n float64, picture string) (any, error) {
			i, ok := integralOf(n)
			if !ok {
				return nil, fmt.Errorf("$formatInteger: %v is not a whole number this can render", n)
			}
			return formatInteger(i, picture)
		}},
		"parseInteger": {Func: func(s, picture string) (any, error) {
			return parseInteger(s, picture)
		}},

		"parse": {Func: func(s string) (any, error) {
			dec := json.NewDecoder(bytes.NewReader([]byte(s)))
			dec.UseNumber()
			var v any
			if err := dec.Decode(&v); err != nil {
				return nil, errors.New("$parse: the argument is not valid JSON")
			}
			if dec.More() {
				return nil, errors.New("$parse: the argument holds more than one JSON value")
			}
			return toJSONata(v), nil
		}},
	}
}

// viaIntrinsic calls an intrinsic implementation with JSONata-side arguments:
// each is normalised into pipeline shape, the intrinsic runs, and its result
// (or its failure, as an error the library surfaces) comes back in JSONata
// shape. The name is the JSONata one, so diagnostics say $range, not
// States.ArrayRange.
func viaIntrinsic(name string, fn func(string, []any, Env) (any, *Failure), env Env, args ...any) (any, error) {
	in := make([]any, len(args))
	for i, a := range args {
		in[i] = fromJSONata(a)
	}
	out, fail := fn(name, in, env)
	if fail != nil {
		return nil, errors.New(fail.Cause)
	}
	return toJSONata(out), nil
}
