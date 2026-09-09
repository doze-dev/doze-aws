package iampolicy

// Condition operators: string, numeric, date, bool, IP, ARN and Null, with
// the IfExists and ForAnyValue/ForAllValues prefixes AWS accepts.

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// conditionsMatch evaluates a Condition block. Every operator in the block must
// pass (they are ANDed); within one operator every key must pass; within one
// key the supplied values are ORed.
func conditionsMatch(block conditionBlock, ctx map[string][]string) bool {
	for op, keys := range block {
		for key, want := range keys {
			if !conditionMatches(op, key, want, ctx) {
				return false
			}
		}
	}
	return true
}

func conditionMatches(op, key string, want []string, ctx map[string][]string) bool {
	// ForAllValues:/ForAnyValue: change how a multi-valued context key is
	// quantified; the remaining operator is applied per value.
	quant := ""
	if rest, ok := strings.CutPrefix(op, "ForAllValues:"); ok {
		quant, op = "all", rest
	} else if rest, ok := strings.CutPrefix(op, "ForAnyValue:"); ok {
		quant, op = "any", rest
	}
	// ...IfExists passes when the key is absent entirely.
	ifExists := false
	if rest, ok := strings.CutSuffix(op, "IfExists"); ok {
		ifExists, op = true, rest
	}

	have, present := ctx[lookupKey(ctx, key)]
	if op == "Null" {
		// Null asks about presence itself, so it is evaluated before the
		// absence shortcut below.
		wantNull := len(want) > 0 && strings.EqualFold(want[0], "true")
		return wantNull != present
	}
	if !present || len(have) == 0 {
		return ifExists
	}

	test := func(v string) bool {
		for _, w := range want {
			if applyOperator(op, v, w) {
				return true
			}
		}
		return false
	}
	switch quant {
	case "all":
		for _, v := range have {
			if !test(v) {
				return false
			}
		}
		return true
	default: // "any" and the unquantified case both pass on a single match
		for _, v := range have {
			if test(v) {
				return true
			}
		}
		return false
	}
}

// lookupKey finds a context key case-insensitively — AWS condition keys are
// documented in mixed case but compared without regard to it.
func lookupKey(ctx map[string][]string, key string) string {
	if _, ok := ctx[key]; ok {
		return key
	}
	for k := range ctx {
		if strings.EqualFold(k, key) {
			return k
		}
	}
	return key
}

// applyOperator evaluates one condition operator against one context value.
// Unknown operators return false: refusing to guess is safer than inventing a
// semantic, and Simulate surfaces the mismatch.
func applyOperator(op, have, want string) bool {
	switch op {
	case "StringEquals", "ArnEquals":
		return have == want
	case "StringNotEquals", "ArnNotEquals":
		return have != want
	case "StringEqualsIgnoreCase":
		return strings.EqualFold(have, want)
	case "StringNotEqualsIgnoreCase":
		return !strings.EqualFold(have, want)
	case "StringLike", "ArnLike":
		return globMatch(want, have)
	case "StringNotLike", "ArnNotLike":
		return !globMatch(want, have)
	case "Bool":
		return strings.EqualFold(have, want)
	case "NumericEquals", "NumericNotEquals", "NumericLessThan",
		"NumericLessThanEquals", "NumericGreaterThan", "NumericGreaterThanEquals":
		h, err1 := strconv.ParseFloat(have, 64)
		w, err2 := strconv.ParseFloat(want, 64)
		if err1 != nil || err2 != nil {
			return false
		}
		return compareNumeric(op, h, w)
	case "DateEquals", "DateNotEquals", "DateLessThan",
		"DateLessThanEquals", "DateGreaterThan", "DateGreaterThanEquals":
		h, err1 := parseDate(have)
		w, err2 := parseDate(want)
		if err1 != nil || err2 != nil {
			return false
		}
		return compareNumeric(strings.Replace(op, "Date", "Numeric", 1),
			float64(h.Unix()), float64(w.Unix()))
	case "IpAddress":
		return ipMatch(have, want)
	case "NotIpAddress":
		return !ipMatch(have, want)
	}
	return false
}

func compareNumeric(op string, h, w float64) bool {
	switch op {
	case "NumericEquals":
		return h == w
	case "NumericNotEquals":
		return h != w
	case "NumericLessThan":
		return h < w
	case "NumericLessThanEquals":
		return h <= w
	case "NumericGreaterThan":
		return h > w
	case "NumericGreaterThanEquals":
		return h >= w
	}
	return false
}

func parseDate(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0), nil
	}
	return time.Time{}, fmt.Errorf("not a date: %q", v)
}

func ipMatch(have, want string) bool {
	ip := net.ParseIP(have)
	if ip == nil {
		return false
	}
	if _, cidr, err := net.ParseCIDR(want); err == nil {
		return cidr.Contains(ip)
	}
	return have == want
}
