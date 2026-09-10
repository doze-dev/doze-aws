package rpcv2cbor

// Encoding.
//
// Deliberately narrower than the decoder. A decoder has to accept whatever
// seven SDKs choose to send; an encoder only has to produce one valid form,
// so this one always uses definite lengths and always writes doubles for
// floating point. Skipping canonical shortest-form encoding costs a handful
// of bytes per response and removes a whole class of thing to get wrong.
//
// Struct fields are read through their `json` tags. CBOR's map/array/scalar
// model is structurally what JSON's is, so a result type carrying `json` and
// `xml` tags — which is what the services here already write, see
// sqs/responses.go — serialises to all three wires with no third vocabulary
// and no annotations added for this package's benefit.

import (
	"encoding/base64"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Marshal encodes a value as CBOR.
func Marshal(v any) ([]byte, error) {
	var e encoder
	if err := e.value(reflect.ValueOf(v), 0); err != nil {
		return nil, err
	}
	return e.buf, nil
}

type encoder struct{ buf []byte }

// head writes an initial byte and its argument in the smallest form that
// holds it. This is the only place the wire's integer encoding lives.
func (e *encoder) head(major byte, arg uint64) {
	m := major << 5
	switch {
	case arg < 24:
		e.buf = append(e.buf, m|byte(arg))
	case arg <= 0xff:
		e.buf = append(e.buf, m|24, byte(arg))
	case arg <= 0xffff:
		e.buf = append(e.buf, m|25, byte(arg>>8), byte(arg))
	case arg <= 0xffffffff:
		e.buf = append(e.buf, m|26, byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	default:
		e.buf = append(e.buf, m|27,
			byte(arg>>56), byte(arg>>48), byte(arg>>40), byte(arg>>32),
			byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	}
}

func (e *encoder) text(s string) {
	e.head(majText, uint64(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *encoder) bytes(b []byte) {
	e.head(majBytes, uint64(len(b)))
	e.buf = append(e.buf, b...)
}

func (e *encoder) null() { e.buf = append(e.buf, 0xf6) }
func (e *encoder) boolean(b bool) {
	if b {
		e.buf = append(e.buf, 0xf5)
		return
	}
	e.buf = append(e.buf, 0xf4)
}

func (e *encoder) float(f float64) {
	bits := math.Float64bits(f)
	e.buf = append(e.buf, 0xfb,
		byte(bits>>56), byte(bits>>48), byte(bits>>40), byte(bits>>32),
		byte(bits>>24), byte(bits>>16), byte(bits>>8), byte(bits))
}

func (e *encoder) integer(i int64) {
	if i < 0 {
		e.head(majNegInt, uint64(-1-i))
		return
	}
	e.head(majUint, uint64(i))
}

// timestamp writes tag 1 over an epoch value, which is how the protocol
// spells a date regardless of any per-member timestampFormat.
func (e *encoder) timestamp(t time.Time) {
	e.head(majTag, tagEpoch)
	if ns := t.Nanosecond(); ns != 0 {
		e.float(float64(t.Unix()) + float64(ns)/1e9)
		return
	}
	e.integer(t.Unix())
}

func (e *encoder) value(v reflect.Value, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("rpcv2cbor: encoding nested deeper than %d", MaxDepth)
	}
	if !v.IsValid() {
		e.null()
		return nil
	}
	// time.Time before the struct case, or it serialises as its fields.
	if v.Type() == reflect.TypeOf(time.Time{}) {
		e.timestamp(v.Interface().(time.Time))
		return nil
	}

	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			e.null()
			return nil
		}
		return e.value(v.Elem(), depth)

	case reflect.Bool:
		e.boolean(v.Bool())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.integer(v.Int())

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.head(majUint, v.Uint())

	case reflect.Float32, reflect.Float64:
		e.float(v.Float())

	case reflect.String:
		e.text(v.String())

	case reflect.Slice, reflect.Array:
		// []byte is a blob, not a list of small integers.
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			if v.IsNil() {
				e.null()
				return nil
			}
			e.bytes(v.Bytes())
			return nil
		}
		if v.Kind() == reflect.Slice && v.IsNil() {
			e.null()
			return nil
		}
		e.head(majArray, uint64(v.Len()))
		for i := range v.Len() {
			if err := e.value(v.Index(i), depth+1); err != nil {
				return err
			}
		}

	case reflect.Map:
		if v.IsNil() {
			e.null()
			return nil
		}
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("rpcv2cbor: map key is %s, want a string", v.Type().Key())
		}
		// Sorted, so a response with several keys is byte-identical run to
		// run — which is what lets a test compare encodings at all.
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		e.head(majMap, uint64(len(keys)))
		for _, k := range keys {
			e.text(k.String())
			if err := e.value(v.MapIndex(k), depth+1); err != nil {
				return err
			}
		}

	case reflect.Struct:
		return e.structValue(v, depth)

	default:
		return fmt.Errorf("rpcv2cbor: cannot encode %s", v.Kind())
	}
	return nil
}

// field is one struct member as its json tag describes it.
type field struct {
	name      string
	index     int
	omitEmpty bool
}

func fieldsOf(t reflect.Type) []field {
	var out []field
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		name, rest, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" && rest == "" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, field{name: name, index: i,
			omitEmpty: strings.Contains(rest, "omitempty")})
	}
	return out
}

func (e *encoder) structValue(v reflect.Value, depth int) error {
	fields := fieldsOf(v.Type())
	// Two passes: the map header carries a count, so which members are
	// actually being written has to be settled first.
	live := fields[:0:0]
	for _, f := range fields {
		fv := v.Field(f.index)
		if f.omitEmpty && isEmpty(fv) {
			continue
		}
		live = append(live, f)
	}
	e.head(majMap, uint64(len(live)))
	for _, f := range live {
		e.text(f.name)
		if err := e.value(v.Field(f.index), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// isEmpty matches encoding/json's notion, so `omitempty` means here what it
// means three lines away in the same struct tag.
func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}

// B64 is the spelling the decoder gives a blob, exported so a caller that
// needs the bytes back does not have to guess the encoding.
func B64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
