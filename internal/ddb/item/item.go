// Package item models DynamoDB items: the full AttributeValue type system
// (S, N, B, BOOL, NULL, M, L, SS, NS, BS) with DynamoDB's semantics — numbers
// are arbitrary-precision decimals compared numerically (never floats), sets
// reject duplicates, and item size follows the documented 400 KB accounting.
package item

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Type enumerates AttributeValue kinds by their wire tag.
type Type string

const (
	TypeS    Type = "S"
	TypeN    Type = "N"
	TypeB    Type = "B"
	TypeBool Type = "BOOL"
	TypeNull Type = "NULL"
	TypeM    Type = "M"
	TypeL    Type = "L"
	TypeSS   Type = "SS"
	TypeNS   Type = "NS"
	TypeBS   Type = "BS"
)

// Value is one AttributeValue.
type Value struct {
	Type Type
	S    string
	N    Decimal
	B    []byte
	Bool bool
	M    map[string]Value
	L    []Value
	SS   []string
	NS   []Decimal
	BS   [][]byte
}

// Item is a stored item: attribute name → value.
type Item = map[string]Value

func errValidation(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ValidationException", format, args...)
}

// FromJSON decodes one wire-format AttributeValue: {"S":"x"}, {"N":"1.5"}, ...
func FromJSON(raw json.RawMessage) (Value, *awshttp.APIError) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return Value{}, errValidation("malformed AttributeValue: %v", err)
	}
	if len(m) != 1 {
		return Value{}, errValidation("an AttributeValue must have exactly one type key, got %d", len(m))
	}
	for tag, inner := range m {
		return fromTagged(Type(tag), inner)
	}
	panic("unreachable")
}

func fromTagged(tag Type, inner json.RawMessage) (Value, *awshttp.APIError) {
	switch tag {
	case TypeS:
		var s string
		if err := json.Unmarshal(inner, &s); err != nil {
			return Value{}, errValidation("S must be a string")
		}
		return Value{Type: TypeS, S: s}, nil
	case TypeN:
		var s string
		if err := json.Unmarshal(inner, &s); err != nil {
			return Value{}, errValidation("N must be a string-encoded number")
		}
		d, aerr := ParseDecimal(s)
		if aerr != nil {
			return Value{}, aerr
		}
		return Value{Type: TypeN, N: d}, nil
	case TypeB:
		var s string
		if err := json.Unmarshal(inner, &s); err != nil {
			return Value{}, errValidation("B must be base64")
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return Value{}, errValidation("B is not valid base64")
		}
		return Value{Type: TypeB, B: b}, nil
	case TypeBool:
		var b bool
		if err := json.Unmarshal(inner, &b); err != nil {
			return Value{}, errValidation("BOOL must be true or false")
		}
		return Value{Type: TypeBool, Bool: b}, nil
	case TypeNull:
		return Value{Type: TypeNull}, nil
	case TypeM:
		var mm map[string]json.RawMessage
		if err := json.Unmarshal(inner, &mm); err != nil {
			return Value{}, errValidation("M must be an object")
		}
		out := make(map[string]Value, len(mm))
		for k, v := range mm {
			pv, aerr := FromJSON(v)
			if aerr != nil {
				return Value{}, aerr
			}
			out[k] = pv
		}
		return Value{Type: TypeM, M: out}, nil
	case TypeL:
		var list []json.RawMessage
		if err := json.Unmarshal(inner, &list); err != nil {
			return Value{}, errValidation("L must be an array")
		}
		out := make([]Value, 0, len(list))
		for _, v := range list {
			pv, aerr := FromJSON(v)
			if aerr != nil {
				return Value{}, aerr
			}
			out = append(out, pv)
		}
		return Value{Type: TypeL, L: out}, nil
	case TypeSS:
		var ss []string
		if err := json.Unmarshal(inner, &ss); err != nil {
			return Value{}, errValidation("SS must be an array of strings")
		}
		if len(ss) == 0 {
			return Value{}, errValidation("SS may not be empty")
		}
		if hasDupStrings(ss) {
			return Value{}, errValidation("SS contains duplicate values")
		}
		return Value{Type: TypeSS, SS: ss}, nil
	case TypeNS:
		var ss []string
		if err := json.Unmarshal(inner, &ss); err != nil {
			return Value{}, errValidation("NS must be an array of string-encoded numbers")
		}
		if len(ss) == 0 {
			return Value{}, errValidation("NS may not be empty")
		}
		out := make([]Decimal, 0, len(ss))
		for _, s := range ss {
			d, aerr := ParseDecimal(s)
			if aerr != nil {
				return Value{}, aerr
			}
			out = append(out, d)
		}
		for i := range out {
			for j := i + 1; j < len(out); j++ {
				if Compare(out[i], out[j]) == 0 {
					return Value{}, errValidation("NS contains duplicate values")
				}
			}
		}
		return Value{Type: TypeNS, NS: out}, nil
	case TypeBS:
		var ss []string
		if err := json.Unmarshal(inner, &ss); err != nil {
			return Value{}, errValidation("BS must be an array of base64 values")
		}
		if len(ss) == 0 {
			return Value{}, errValidation("BS may not be empty")
		}
		out := make([][]byte, 0, len(ss))
		for _, s := range ss {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return Value{}, errValidation("BS member is not valid base64")
			}
			out = append(out, b)
		}
		for i := range out {
			for j := i + 1; j < len(out); j++ {
				if string(out[i]) == string(out[j]) {
					return Value{}, errValidation("BS contains duplicate values")
				}
			}
		}
		return Value{Type: TypeBS, BS: out}, nil
	}
	return Value{}, errValidation("unknown AttributeValue type %q", tag)
}

// ItemFromJSON decodes a wire item (map of AttributeValues).
func ItemFromJSON(raw json.RawMessage) (Item, *awshttp.APIError) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errValidation("malformed item: %v", err)
	}
	out := make(Item, len(m))
	for k, v := range m {
		pv, aerr := FromJSON(v)
		if aerr != nil {
			return nil, aerr
		}
		out[k] = pv
	}
	return out, nil
}

// JSON renders the wire form of a value.
func (v Value) JSON() json.RawMessage {
	var inner any
	switch v.Type {
	case TypeS:
		inner = v.S
	case TypeN:
		inner = v.N.String()
	case TypeB:
		inner = base64.StdEncoding.EncodeToString(v.B)
	case TypeBool:
		inner = v.Bool
	case TypeNull:
		inner = true
	case TypeM:
		m := make(map[string]json.RawMessage, len(v.M))
		for k, mv := range v.M {
			m[k] = mv.JSON()
		}
		raw, _ := json.Marshal(map[string]any{"M": m})
		return raw
	case TypeL:
		l := make([]json.RawMessage, 0, len(v.L))
		for _, lv := range v.L {
			l = append(l, lv.JSON())
		}
		raw, _ := json.Marshal(map[string]any{"L": l})
		return raw
	case TypeSS:
		inner = v.SS
	case TypeNS:
		ns := make([]string, 0, len(v.NS))
		for _, d := range v.NS {
			ns = append(ns, d.String())
		}
		inner = ns
	case TypeBS:
		bs := make([]string, 0, len(v.BS))
		for _, b := range v.BS {
			bs = append(bs, base64.StdEncoding.EncodeToString(b))
		}
		inner = bs
	default:
		inner = nil
	}
	raw, _ := json.Marshal(map[string]any{string(v.Type): inner})
	return raw
}

// ItemJSON renders a stored item back to the wire form.
func ItemJSON(it Item) json.RawMessage {
	m := make(map[string]json.RawMessage, len(it))
	for k, v := range it {
		m[k] = v.JSON()
	}
	raw, _ := json.Marshal(m)
	return raw
}

// Equal reports deep equality with DynamoDB semantics (numeric comparison for
// N/NS; set order irrelevant).
func Equal(a, b Value) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case TypeS:
		return a.S == b.S
	case TypeN:
		return Compare(a.N, b.N) == 0
	case TypeB:
		return string(a.B) == string(b.B)
	case TypeBool:
		return a.Bool == b.Bool
	case TypeNull:
		return true
	case TypeM:
		if len(a.M) != len(b.M) {
			return false
		}
		for k, av := range a.M {
			bv, ok := b.M[k]
			if !ok || !Equal(av, bv) {
				return false
			}
		}
		return true
	case TypeL:
		if len(a.L) != len(b.L) {
			return false
		}
		for i := range a.L {
			if !Equal(a.L[i], b.L[i]) {
				return false
			}
		}
		return true
	case TypeSS:
		return sameStringSet(a.SS, b.SS)
	case TypeNS:
		if len(a.NS) != len(b.NS) {
			return false
		}
		as := sortedDecimals(a.NS)
		bs := sortedDecimals(b.NS)
		for i := range as {
			if Compare(as[i], bs[i]) != 0 {
				return false
			}
		}
		return true
	case TypeBS:
		if len(a.BS) != len(b.BS) {
			return false
		}
		as := sortedBytes(a.BS)
		bs := sortedBytes(b.BS)
		for i := range as {
			if string(as[i]) != string(bs[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// Size computes an item's size per DynamoDB's documented accounting:
// attribute-name lengths plus value sizes.
func Size(it Item) int {
	n := 0
	for k, v := range it {
		n += len(k) + valueSize(v)
	}
	return n
}

// MaxItemSize is DynamoDB's 400 KB item bound.
const MaxItemSize = 400 << 10

func valueSize(v Value) int {
	switch v.Type {
	case TypeS:
		return len(v.S)
	case TypeN:
		return numberSize(v.N)
	case TypeB:
		return len(v.B)
	case TypeBool, TypeNull:
		return 1
	case TypeM:
		n := 3
		for k, mv := range v.M {
			n += len(k) + valueSize(mv) + 1
		}
		return n
	case TypeL:
		n := 3
		for _, lv := range v.L {
			n += valueSize(lv) + 1
		}
		return n
	case TypeSS:
		n := 0
		for _, s := range v.SS {
			n += len(s)
		}
		return n
	case TypeNS:
		n := 0
		for _, d := range v.NS {
			n += numberSize(d)
		}
		return n
	case TypeBS:
		n := 0
		for _, b := range v.BS {
			n += len(b)
		}
		return n
	}
	return 0
}

// numberSize approximates DynamoDB's number sizing: roughly (significant
// digits)/2 + 1, which is close enough for local-size enforcement.
func numberSize(d Decimal) int {
	return len(d.digits)/2 + 1
}

func hasDupStrings(ss []string) bool {
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		if seen[s] {
			return true
		}
		seen[s] = true
	}
	return false
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func sortedDecimals(ds []Decimal) []Decimal {
	out := append([]Decimal(nil), ds...)
	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) < 0 })
	return out
}

func sortedBytes(bs [][]byte) [][]byte {
	out := append([][]byte(nil), bs...)
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}

// TypeName renders the human-facing type name used in error messages.
func (v Value) TypeName() string {
	return string(v.Type)
}

// DebugString renders a compact human-readable form (admin/inspection).
func (v Value) DebugString() string {
	switch v.Type {
	case TypeS:
		return fmt.Sprintf("%q", v.S)
	case TypeN:
		return v.N.String()
	case TypeB:
		return fmt.Sprintf("b64(%s)", base64.StdEncoding.EncodeToString(v.B))
	case TypeBool:
		return fmt.Sprintf("%v", v.Bool)
	case TypeNull:
		return "null"
	case TypeM:
		parts := make([]string, 0, len(v.M))
		for k, mv := range v.M {
			parts = append(parts, k+": "+mv.DebugString())
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ", ") + "}"
	case TypeL:
		parts := make([]string, 0, len(v.L))
		for _, lv := range v.L {
			parts = append(parts, lv.DebugString())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return string(v.Type)
	}
}
