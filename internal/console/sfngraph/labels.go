package sfngraph

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/asl"
)

// shorten cuts s to n runes with an ellipsis. SVG text neither wraps nor
// clips, so a long name would run out of its box and across its neighbour.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ruleLabel is the short form of a Choice rule an arrow can carry:
// `$.total > 1000`, `$.kind == "gold"`, `$.x present`. The operator names
// are ASL's — StringEquals, NumericGreaterThanPath — and the label drops the
// type, which the operand shows anyway, and keeps the comparison.
func ruleLabel(r *asl.ChoiceRule) string {
	return shorten(ruleText(r), 30)
}

func ruleText(r *asl.ChoiceRule) string {
	switch {
	case r == nil:
		return ""
	case len(r.Condition) > 0:
		var s string
		if json.Unmarshal(r.Condition, &s) != nil {
			s = string(r.Condition)
		}
		return strings.TrimSpace(s)
	case r.Not != nil:
		return "not " + ruleText(r.Not)
	case len(r.And) > 0:
		return joinRules(r.And, " and ")
	case len(r.Or) > 0:
		return joinRules(r.Or, " or ")
	case r.Comparison == nil:
		return r.Variable
	}
	op, operand := opSymbol(r.Comparison.Op), operandText(r.Comparison)
	if operand == "" {
		return r.Variable + " " + op
	}
	return r.Variable + " " + op + " " + operand
}

func joinRules(rs []*asl.ChoiceRule, sep string) string {
	if len(rs) > 2 {
		return ruleText(rs[0]) + sep + strconv.Itoa(len(rs)-1) + " more"
	}
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, ruleText(r))
	}
	return strings.Join(parts, sep)
}

// opSymbol maps an ASL comparison operator to the symbol a person would
// write. The operator is <Type><Comparison>[Path]; the type prefix goes and
// the Path suffix is shown by the operand being a path.
func opSymbol(op string) string {
	op = strings.TrimSuffix(op, "Path")
	for _, t := range []string{"String", "Numeric", "Boolean", "Timestamp"} {
		op = strings.TrimPrefix(op, t)
	}
	switch op {
	case "Equals":
		return "=="
	case "LessThan":
		return "<"
	case "GreaterThan":
		return ">"
	case "LessThanEquals":
		return "<="
	case "GreaterThanEquals":
		return ">="
	case "Matches":
		return "~"
	case "IsPresent":
		return "present"
	case "IsNull":
		return "null"
	case "IsString", "IsNumeric", "IsBoolean", "IsTimestamp":
		return "is " + strings.ToLower(strings.TrimPrefix(op, "Is"))
	}
	return op
}

// operandText renders the compared-against value: a path as itself, a string
// with its quotes (so `== "5"` and `== 5` stay distinct), everything else as
// its JSON. The Is* operators take a boolean nobody needs to read.
func operandText(c *asl.Comparison) string {
	if strings.HasPrefix(c.Op, "Is") && !c.IsPath {
		var b bool
		if json.Unmarshal(c.Operand, &b) == nil && b {
			return ""
		}
	}
	if c.IsPath {
		var s string
		if json.Unmarshal(c.Operand, &s) == nil {
			return s
		}
	}
	return strings.TrimSpace(string(c.Operand))
}

// catchLabel names the error a Catch matches — the first, with a count when
// there are more, because the arrow has room for one.
func catchLabel(c *asl.Catcher) string {
	switch len(c.ErrorEquals) {
	case 0:
		return "catch"
	case 1:
		return shorten(c.ErrorEquals[0], 30)
	}
	return shorten(c.ErrorEquals[0], 24) + " +" + strconv.Itoa(len(c.ErrorEquals)-1)
}
