package cloudwatch

// Reading a decoded request, whichever wire carried it.
//
// CloudWatch speaks three protocols, and two of them already agree: CBOR and
// JSON decode to the same Go values (internal/rpcv2cbor launders integers to
// float64 and blobs to base64 strings for exactly this reason). Query is the
// odd one — modelcheck.FromQuery rebuilds the nesting but leaves every leaf a
// string, because that is all a form has and validation does not care.
//
// Handlers do care. `p["Period"].(float64)` is false-and-zero for a Query
// request, silently, which is the kind of bug that only shows up for the one
// client generation nobody tested. So the accessors below coerce: they take
// the typed value when there is one and parse the string when there is not,
// the same leniency modelcheck.toNumber already applies a layer down.
//
// SQS solved the two-wire version of this differently — dual-branch accessors
// over `params{obj, form}` (sqs/codec.go) — because its two wires disagree
// about shape as well as type. Here the shapes already match, so one map and
// coercion is less machinery than three branches everywhere.

import (
	"strconv"
	"strings"
	"time"
)

// params is a decoded request body. Absent members read as zero values, which
// is what makes an optional member optional without every handler checking.
type params map[string]any

// Str reads a string member.
func (p params) Str(name string) string { return asStr(p[name]) }

// Bool reads a boolean. Query spells it "true"/"false".
func (p params) Bool(name string) bool {
	switch v := p[name].(type) {
	case bool:
		return v
	case string:
		b, _ := strconv.ParseBool(v)
		return b
	}
	return false
}

// Float reads a number, and reports whether the member was there and usable.
// The two results matter separately: 0 is a legal metric value, so "absent"
// and "zero" are different answers.
func (p params) Float(name string) (float64, bool) { return asFloat(p[name]) }

// Int reads a whole number, falling back to def when the member is absent or
// not a number.
func (p params) Int(name string, def int) int {
	if f, ok := asFloat(p[name]); ok {
		return int(f)
	}
	return def
}

// Time reads a timestamp. Each wire spells one differently — CBOR tag 1
// arrives already decoded, JSON sends epoch seconds, Query sends ISO 8601 —
// so this is the one place the three are reconciled.
func (p params) Time(name string) (time.Time, bool) {
	switch v := p[name].(type) {
	case time.Time:
		return v, true
	case float64:
		sec, frac := int64(v), v-float64(int64(v))
		return time.Unix(sec, int64(frac*1e9)).UTC(), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
			if t, err := time.Parse(layout, v); err == nil {
				return t.UTC(), true
			}
		}
		// A Query client may also send epoch seconds as a bare number.
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Unix(int64(f), 0).UTC(), true
		}
	}
	return time.Time{}, false
}

// List reads a list member as a list of sub-params. A member that is not a
// list reads as empty rather than as a one-element list: guessing at the
// caller's intent here is how a malformed request becomes a plausible one.
func (p params) List(name string) []params {
	raw, ok := p[name].([]any)
	if !ok {
		return nil
	}
	out := make([]params, 0, len(raw))
	for _, el := range raw {
		if m, ok := el.(map[string]any); ok {
			out = append(out, params(m))
		}
	}
	return out
}

// Strs reads a list of strings.
func (p params) Strs(name string) []string {
	raw, ok := p[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, el := range raw {
		out = append(out, asStr(el))
	}
	return out
}

// Map reads a nested structure.
func (p params) Map(name string) params {
	if m, ok := p[name].(map[string]any); ok {
		return params(m)
	}
	return nil
}

// Has reports whether a member was sent at all.
func (p params) Has(name string) bool { _, ok := p[name]; return ok }

func asStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// Whole numbers read back as they were written, not as "60.000000".
		return strconv.FormatFloat(t, 'f', -1, 64)
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}
