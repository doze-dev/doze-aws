// Package eventpattern implements EventBridge's event pattern language: a
// pattern is a JSON document whose leaves are arrays of conditions; an event
// matches when every pattern field matches (AND across fields), any condition
// in a leaf array matches (OR within a field), and arrays in the EVENT match
// if any element satisfies the condition.
//
// Supported operators: exact values (strings, numbers, booleans, null),
// {"prefix": s}, {"suffix": s}, {"equals-ignore-case": s}, {"wildcard": s}
// (with * wildcards), {"anything-but": v|[v...]|{"prefix"/"suffix"/
// "equals-ignore-case"/"wildcard": ...}}, {"numeric": [op, n, ...]},
// {"exists": bool}, {"cidr": "a.b.c.d/n"}, and {"$or": [subpattern, ...]}.
package eventpattern

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// Pattern is a compiled event pattern.
type Pattern struct {
	root patternNode
}

// Parse compiles a pattern document.
func Parse(src []byte) (*Pattern, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(src, &raw); err != nil {
		return nil, fmt.Errorf("event pattern is not a JSON object: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("event pattern must have at least one field")
	}
	node, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	return &Pattern{root: node}, nil
}

// Match evaluates the pattern against an event document.
func (p *Pattern) Match(event []byte) (bool, error) {
	var doc any
	if err := json.Unmarshal(event, &doc); err != nil {
		return false, fmt.Errorf("event is not valid JSON: %w", err)
	}
	return p.root.match(doc), nil
}

// patternNode matches one level of the event document.
type patternNode interface {
	match(doc any) bool
}

// objectNode requires every field to match (AND), where each field is either a
// nested object or a leaf condition list.
type objectNode struct {
	fields map[string]patternNode
	ors    [][]patternNode // each $or: a list of alternative sub-objects
}

func parseObject(raw map[string]json.RawMessage) (patternNode, error) {
	node := &objectNode{fields: map[string]patternNode{}}
	for key, val := range raw {
		if key == "$or" {
			var alts []map[string]json.RawMessage
			if err := json.Unmarshal(val, &alts); err != nil {
				return nil, fmt.Errorf("$or must be an array of pattern objects")
			}
			var parsed []patternNode
			for _, alt := range alts {
				sub, err := parseObject(alt)
				if err != nil {
					return nil, err
				}
				parsed = append(parsed, sub)
			}
			if len(parsed) == 0 {
				return nil, fmt.Errorf("$or must have at least one alternative")
			}
			node.ors = append(node.ors, parsed)
			continue
		}
		// A field value is either a nested object (recurse) or a leaf array.
		trimmed := strings.TrimSpace(string(val))
		if strings.HasPrefix(trimmed, "{") {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(val, &nested); err != nil {
				return nil, fmt.Errorf("field %q: %w", key, err)
			}
			sub, err := parseObject(nested)
			if err != nil {
				return nil, err
			}
			node.fields[key] = sub
			continue
		}
		leaf, err := parseLeaf(key, val)
		if err != nil {
			return nil, err
		}
		node.fields[key] = leaf
	}
	return node, nil
}

func (n *objectNode) match(doc any) bool {
	obj, ok := doc.(map[string]any)
	if !ok {
		return false
	}
	for key, sub := range n.fields {
		if leaf, isLeaf := sub.(*leafNode); isLeaf {
			val, present := obj[key]
			if !leaf.matchValue(val, present) {
				return false
			}
			continue
		}
		val, present := obj[key]
		if !present || !sub.match(val) {
			return false
		}
	}
	for _, alts := range n.ors {
		anyMatched := false
		for _, alt := range alts {
			if alt.match(doc) {
				anyMatched = true
				break
			}
		}
		if !anyMatched {
			return false
		}
	}
	return true
}

// leafNode is a list of conditions; any may match (OR).
type leafNode struct {
	conds []condition
}

func parseLeaf(key string, val json.RawMessage) (*leafNode, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(val, &list); err != nil {
		return nil, fmt.Errorf("field %q must be an array of conditions or a nested object", key)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("field %q has an empty condition list", key)
	}
	leaf := &leafNode{}
	for _, c := range list {
		cond, err := parseCondition(key, c)
		if err != nil {
			return nil, err
		}
		leaf.conds = append(leaf.conds, cond)
	}
	return leaf, nil
}

// match implements patternNode for completeness (leaf fields are dispatched
// via matchValue to see field absence).
func (l *leafNode) match(doc any) bool { return l.matchValue(doc, true) }

// matchValue evaluates the leaf against a field value. Event arrays match if
// any element matches.
func (l *leafNode) matchValue(val any, present bool) bool {
	candidates := []any{val}
	if arr, ok := val.([]any); ok && present {
		candidates = arr
	}
	for _, cond := range l.conds {
		if _, isExists := cond.(existsCond); isExists {
			if cond.matches(nil, present) {
				return true
			}
			continue
		}
		if !present {
			continue
		}
		for _, cand := range candidates {
			if cond.matches(cand, present) {
				return true
			}
		}
	}
	return false
}

// condition is one leaf matcher.
type condition interface {
	matches(val any, present bool) bool
}

func parseCondition(key string, raw json.RawMessage) (condition, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var op map[string]json.RawMessage
		if err := json.Unmarshal(raw, &op); err != nil || len(op) != 1 {
			return nil, fmt.Errorf("field %q: operator conditions must be single-key objects", key)
		}
		for name, arg := range op {
			return parseOperator(key, name, arg)
		}
	}
	// Exact value: string, number, bool, or null.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("field %q: %w", key, err)
	}
	return exactCond{want: v}, nil
}

func parseOperator(key, name string, arg json.RawMessage) (condition, error) {
	switch name {
	case "prefix":
		// Plain string, or {"equals-ignore-case": s}.
		var s string
		if json.Unmarshal(arg, &s) == nil {
			return prefixCond{s: s}, nil
		}
		var sub map[string]string
		if json.Unmarshal(arg, &sub) == nil {
			if ic, ok := sub["equals-ignore-case"]; ok {
				return prefixCond{s: ic, ignoreCase: true}, nil
			}
		}
		return nil, fmt.Errorf("field %q: prefix expects a string", key)
	case "suffix":
		var s string
		if json.Unmarshal(arg, &s) == nil {
			return suffixCond{s: s}, nil
		}
		var sub map[string]string
		if json.Unmarshal(arg, &sub) == nil {
			if ic, ok := sub["equals-ignore-case"]; ok {
				return suffixCond{s: ic, ignoreCase: true}, nil
			}
		}
		return nil, fmt.Errorf("field %q: suffix expects a string", key)
	case "equals-ignore-case":
		var s string
		if err := json.Unmarshal(arg, &s); err != nil {
			return nil, fmt.Errorf("field %q: equals-ignore-case expects a string", key)
		}
		return ignoreCaseCond{s: s}, nil
	case "wildcard":
		var s string
		if err := json.Unmarshal(arg, &s); err != nil {
			return nil, fmt.Errorf("field %q: wildcard expects a string", key)
		}
		return wildcardCond{pattern: s}, nil
	case "anything-but":
		inner, err := parseAnythingBut(key, arg)
		if err != nil {
			return nil, err
		}
		return notCond{inner: inner}, nil
	case "numeric":
		return parseNumeric(key, arg)
	case "exists":
		var b bool
		if err := json.Unmarshal(arg, &b); err != nil {
			return nil, fmt.Errorf("field %q: exists expects true or false", key)
		}
		return existsCond{want: b}, nil
	case "cidr":
		var s string
		if err := json.Unmarshal(arg, &s); err != nil {
			return nil, fmt.Errorf("field %q: cidr expects a string", key)
		}
		_, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("field %q: %v", key, err)
		}
		return cidrCond{net: ipnet}, nil
	}
	return nil, fmt.Errorf("field %q: unknown operator %q", key, name)
}

// parseAnythingBut handles the value, list, and sub-operator forms.
func parseAnythingBut(key string, arg json.RawMessage) (condition, error) {
	trimmed := strings.TrimSpace(string(arg))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var op map[string]json.RawMessage
		if err := json.Unmarshal(arg, &op); err != nil || len(op) != 1 {
			return nil, fmt.Errorf("field %q: anything-but sub-operator must be a single-key object", key)
		}
		for name, sub := range op {
			switch name {
			case "prefix", "suffix", "equals-ignore-case", "wildcard":
				return parseOperator(key, name, sub)
			}
			return nil, fmt.Errorf("field %q: anything-but does not support %q", key, name)
		}
	case strings.HasPrefix(trimmed, "["):
		var list []json.RawMessage
		if err := json.Unmarshal(arg, &list); err != nil {
			return nil, fmt.Errorf("field %q: %v", key, err)
		}
		set := &anyOfCond{}
		for _, item := range list {
			var v any
			if err := json.Unmarshal(item, &v); err != nil {
				return nil, fmt.Errorf("field %q: %v", key, err)
			}
			set.conds = append(set.conds, exactCond{want: v})
		}
		return set, nil
	}
	var v any
	if err := json.Unmarshal(arg, &v); err != nil {
		return nil, fmt.Errorf("field %q: %v", key, err)
	}
	return exactCond{want: v}, nil
}

// parseNumeric handles ["<op>", n, ("<op>", n)?].
func parseNumeric(key string, arg json.RawMessage) (condition, error) {
	var parts []any
	if err := json.Unmarshal(arg, &parts); err != nil {
		return nil, fmt.Errorf("field %q: numeric expects an array", key)
	}
	if len(parts) != 2 && len(parts) != 4 {
		return nil, fmt.Errorf("field %q: numeric expects [op, n] or [op, n, op, n]", key)
	}
	cond := numericCond{}
	for i := 0; i < len(parts); i += 2 {
		op, ok := parts[i].(string)
		if !ok {
			return nil, fmt.Errorf("field %q: numeric operator must be a string", key)
		}
		n, ok := toFloat(parts[i+1])
		if !ok {
			return nil, fmt.Errorf("field %q: numeric bound must be a number", key)
		}
		switch op {
		case "=", "<", "<=", ">", ">=":
			cond.bounds = append(cond.bounds, numericBound{op: op, n: n})
		default:
			return nil, fmt.Errorf("field %q: unknown numeric operator %q", key, op)
		}
	}
	return cond, nil
}

// ---- conditions ----

type exactCond struct{ want any }

func (c exactCond) matches(val any, present bool) bool {
	if !present {
		return false
	}
	// Numbers compare numerically (JSON numbers are float64 here).
	if wf, ok := toFloat(c.want); ok {
		vf, ok := toFloat(val)
		return ok && wf == vf
	}
	switch w := c.want.(type) {
	case string:
		s, ok := val.(string)
		return ok && s == w
	case bool:
		b, ok := val.(bool)
		return ok && b == w
	case nil:
		return val == nil
	}
	return false
}

type prefixCond struct {
	s          string
	ignoreCase bool
}

func (c prefixCond) matches(val any, present bool) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	if c.ignoreCase {
		return len(s) >= len(c.s) && strings.EqualFold(s[:len(c.s)], c.s)
	}
	return strings.HasPrefix(s, c.s)
}

type suffixCond struct {
	s          string
	ignoreCase bool
}

func (c suffixCond) matches(val any, present bool) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	if c.ignoreCase {
		return len(s) >= len(c.s) && strings.EqualFold(s[len(s)-len(c.s):], c.s)
	}
	return strings.HasSuffix(s, c.s)
}

type ignoreCaseCond struct{ s string }

func (c ignoreCaseCond) matches(val any, present bool) bool {
	s, ok := val.(string)
	return ok && strings.EqualFold(s, c.s)
}

type wildcardCond struct{ pattern string }

func (c wildcardCond) matches(val any, present bool) bool {
	s, ok := val.(string)
	return ok && wildcardMatch(c.pattern, s)
}

// wildcardMatch matches * against any run (including empty), iteratively.
func wildcardMatch(pattern, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == s[si]):
			pi++
			si++
		case pi < len(pattern) && pattern[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

type notCond struct{ inner condition }

func (c notCond) matches(val any, present bool) bool {
	if !present {
		return false
	}
	return !c.inner.matches(val, present)
}

type anyOfCond struct{ conds []condition }

func (c *anyOfCond) matches(val any, present bool) bool {
	for _, sub := range c.conds {
		if sub.matches(val, present) {
			return true
		}
	}
	return false
}

type numericBound struct {
	op string
	n  float64
}

type numericCond struct{ bounds []numericBound }

func (c numericCond) matches(val any, present bool) bool {
	v, ok := toFloat(val)
	if !ok {
		return false
	}
	for _, b := range c.bounds {
		switch b.op {
		case "=":
			if v != b.n {
				return false
			}
		case "<":
			if !(v < b.n) {
				return false
			}
		case "<=":
			if !(v <= b.n) {
				return false
			}
		case ">":
			if !(v > b.n) {
				return false
			}
		case ">=":
			if !(v >= b.n) {
				return false
			}
		}
	}
	return true
}

type existsCond struct{ want bool }

func (c existsCond) matches(_ any, present bool) bool { return present == c.want }

type cidrCond struct{ net *net.IPNet }

func (c cidrCond) matches(val any, present bool) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	ip := net.ParseIP(s)
	return ip != nil && c.net.Contains(ip)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	}
	// Strings are never coerced to numbers — EventBridge does not either.
	return 0, false
}
