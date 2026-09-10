package asl

import (
	"encoding/json"
	"time"
)

// Runtime evaluation of Choice rules, against the operator table choice.go
// owns — the parser, the analyser and this evaluator agree on what an
// operator is by construction.
//
// Two behaviours are AWS's, not obvious, and tested: a comparison whose
// Variable selects nothing fails the execution with States.Runtime (IsPresent
// is the one safe probe), and a comparison whose variable is present but of
// the wrong type is simply false — a NumericEquals over a string does not
// match, it does not throw.

// chooseRule is chooseNext that also says which rule matched (nil for the
// Default), so the caller can run that rule's Assign.
func chooseRule(s *State, data, ctxObj any) (string, *ChoiceRule, *Failure) {
	for _, rule := range s.Choices {
		ok, f := evalRule(rule, data, ctxObj)
		if f != nil {
			return "", nil, f
		}
		if ok {
			return rule.Next, rule, nil
		}
	}
	if s.Default != "" {
		return s.Default, nil, nil
	}
	return "", nil, Failf(ErrNoChoiceMatched, "no rule of Choice state %q matched and there is no Default", s.Name)
}

func evalRule(r *ChoiceRule, data, ctxObj any) (bool, *Failure) {
	switch {
	case len(r.And) > 0:
		for _, sub := range r.And {
			ok, f := evalRule(sub, data, ctxObj)
			if f != nil || !ok {
				return false, f
			}
		}
		return true, nil
	case len(r.Or) > 0:
		for _, sub := range r.Or {
			ok, f := evalRule(sub, data, ctxObj)
			if f != nil || ok {
				return ok, f
			}
		}
		return false, nil
	case r.Not != nil:
		ok, f := evalRule(r.Not, data, ctxObj)
		return !ok, f
	}
	return evalComparison(r, data, ctxObj)
}

func evalComparison(r *ChoiceRule, data, ctxObj any) (bool, *Failure) {
	c := r.Comparison
	if c == nil {
		return false, Failf(ErrRuntime, "a choice rule with no comparison reached evaluation")
	}
	p, err := ParsePath(r.Variable)
	if err != nil {
		return false, Failf(ErrRuntime, "Variable: %v", err)
	}
	v, present := resolve(p, data, ctxObj)

	// IsPresent is the one comparison that a missing variable answers rather
	// than breaks; everything else on a missing variable is States.Runtime.
	if c.Op == "IsPresent" {
		return present == operandBool(c), nil
	}
	if !present {
		return false, Failf(ErrRuntime,
			"the Variable %q of a %s comparison selects nothing", r.Variable, c.Op)
	}

	switch c.Op {
	case "IsNull":
		return (v == nil) == operandBool(c), nil
	case "IsNumeric":
		_, isNum := toFloat(v)
		return isNum == operandBool(c), nil
	case "IsString":
		_, isStr := v.(string)
		return isStr == operandBool(c), nil
	case "IsBoolean":
		_, isBool := v.(bool)
		return isBool == operandBool(c), nil
	case "IsTimestamp":
		s, isStr := v.(string)
		return (isStr && isRFC3339(s)) == operandBool(c), nil
	}

	operand := decodeDoc(c.Operand)
	if c.IsPath {
		expr, isStr := operand.(string)
		if !isStr {
			return false, Failf(ErrRuntime, "the operand of %sPath must be a reference path", c.Op)
		}
		op, err := ParsePath(expr)
		if err != nil {
			return false, Failf(ErrRuntime, "%sPath: %v", c.Op, err)
		}
		resolved, ok := resolve(op, data, ctxObj)
		if !ok {
			return false, Failf(ErrRuntime, "the operand path %q of %sPath selects nothing", expr, c.Op)
		}
		operand = resolved
	}

	_, _, kind, _ := LookupOperator(c.Op)
	switch kind {
	case OperandString:
		val, okV := v.(string)
		want, okW := operand.(string)
		if !okV || !okW {
			return false, nil
		}
		if c.Op == "StringMatches" {
			return globMatch(want, val), nil
		}
		return ordered(c.Op, compareStrings(val, want)), nil
	case OperandNumber:
		val, okV := toFloat(v)
		want, okW := toFloat(operand)
		if !okV || !okW {
			return false, nil
		}
		return ordered(c.Op, compareFloats(val, want)), nil
	case OperandBool:
		val, okV := v.(bool)
		want, okW := operand.(bool)
		if !okV || !okW {
			return false, nil
		}
		return val == want, nil
	case OperandTimestamp:
		val, okV := toTime(v)
		want, okW := toTime(operand)
		if !okV || !okW {
			return false, nil
		}
		return ordered(c.Op, compareTimes(val, want)), nil
	}
	return false, Failf(ErrRuntime, "unhandled comparison operator %q", c.Op)
}

// operandBool reads a presence operator's operand, which the analyser has
// already required to be a boolean.
func operandBool(c *Comparison) bool {
	var b bool
	json.Unmarshal(c.Operand, &b)
	return b
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	}
	return 0, false
}

func toTime(v any) (time.Time, bool) {
	s, isStr := v.(string)
	if !isStr {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareFloats(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareTimes(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}

// ordered maps a three-way comparison onto the operator's direction. The
// operator names share suffixes, so the suffix is the dispatch.
func ordered(op string, cmp int) bool {
	switch {
	case hasSuffix(op, "LessThanEquals"):
		return cmp <= 0
	case hasSuffix(op, "GreaterThanEquals"):
		return cmp >= 0
	case hasSuffix(op, "LessThan"):
		return cmp < 0
	case hasSuffix(op, "GreaterThan"):
		return cmp > 0
	default: // ...Equals
		return cmp == 0
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// globMatch implements StringMatches: * matches any run of characters, and a
// backslash escapes the character after it, so \* is a literal asterisk.
func globMatch(pattern, s string) bool {
	if pattern == "" {
		return s == ""
	}
	switch pattern[0] {
	case '*':
		for i := 0; i <= len(s); i++ {
			if globMatch(pattern[1:], s[i:]) {
				return true
			}
		}
		return false
	case '\\':
		if len(pattern) < 2 {
			return false
		}
		return s != "" && s[0] == pattern[1] && globMatch(pattern[2:], s[1:])
	default:
		return s != "" && s[0] == pattern[0] && globMatch(pattern[1:], s[1:])
	}
}
