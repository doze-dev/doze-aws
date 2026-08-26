package asl

import "strings"

// The Choice state's comparison vocabulary.
//
// ASL defines a closed set of operators, and every one is a key on the rule
// object alongside "Variable". Most have a "...Path" twin whose operand is a
// reference path to read the comparand from rather than a literal — so
// StringEquals and StringEqualsPath are the same comparison with the operand
// fetched differently, which is why they share one entry here and differ only
// by the IsPath flag on the parsed Comparison.
//
// Keeping the set in a table rather than in a switch means the parser, the
// analyser and (from stage 2) the evaluator all agree on what an operator is by
// construction. A switch in each would be three places to forget StringMatches.

// OperandKind is what an operator's operand must be.
type OperandKind int

const (
	// OperandString, OperandNumber, OperandBool and OperandTimestamp each
	// require a literal of that JSON type — or, for the ...Path twin, a
	// reference path yielding one.
	OperandString OperandKind = iota
	OperandNumber
	OperandBool
	OperandTimestamp
	// OperandPresence covers IsNull, IsPresent, IsNumeric, IsString,
	// IsBoolean and IsTimestamp: the operand is always a boolean saying which
	// way round the test runs, and there is no ...Path twin.
	OperandPresence
)

// operators maps the base operator name to what its operand must be. The
// "...Path" spelling of each is derived, not listed, so the two cannot drift.
var operators = map[string]OperandKind{
	"StringEquals":            OperandString,
	"StringLessThan":          OperandString,
	"StringGreaterThan":       OperandString,
	"StringLessThanEquals":    OperandString,
	"StringGreaterThanEquals": OperandString,

	"NumericEquals":            OperandNumber,
	"NumericLessThan":          OperandNumber,
	"NumericGreaterThan":       OperandNumber,
	"NumericLessThanEquals":    OperandNumber,
	"NumericGreaterThanEquals": OperandNumber,

	"BooleanEquals": OperandBool,

	"TimestampEquals":            OperandTimestamp,
	"TimestampLessThan":          OperandTimestamp,
	"TimestampGreaterThan":       OperandTimestamp,
	"TimestampLessThanEquals":    OperandTimestamp,
	"TimestampGreaterThanEquals": OperandTimestamp,

	"IsNull":      OperandPresence,
	"IsPresent":   OperandPresence,
	"IsNumeric":   OperandPresence,
	"IsString":    OperandPresence,
	"IsBoolean":   OperandPresence,
	"IsTimestamp": OperandPresence,
}

// noPathTwin are the operators with no "...Path" spelling. The presence tests
// have none because their operand is the direction of the test rather than a
// comparand, and StringMatches has none because AWS never defined one.
var noPathTwin = map[string]bool{
	"IsNull": true, "IsPresent": true, "IsNumeric": true,
	"IsString": true, "IsBoolean": true, "IsTimestamp": true,
	"StringMatches": true,
}

func init() {
	// StringMatches is a glob against the variable, so its operand is a string
	// like a comparison but it has no Path twin. Registered here rather than in
	// the literal above so the exception sits next to the rule that excludes it.
	operators["StringMatches"] = OperandString
}

// LookupOperator resolves a rule key to its base operator and whether the key
// was the "...Path" spelling. ok is false for a key that is not an operator at
// all — "Variable", "Next", "And" and so on.
func LookupOperator(key string) (base string, isPath bool, kind OperandKind, ok bool) {
	if kind, found := operators[key]; found {
		return key, false, kind, true
	}
	if strings.HasSuffix(key, "Path") {
		base = strings.TrimSuffix(key, "Path")
		if kind, found := operators[base]; found && !noPathTwin[base] {
			return base, true, kind, true
		}
	}
	return "", false, 0, false
}

// ruleStructuralKeys are the keys on a choice rule that are not operators.
var ruleStructuralKeys = map[string]bool{
	"Variable": true, "Next": true, "And": true, "Or": true, "Not": true,
	"Comment": true, "Condition": true, "Assign": true, "Output": true,
}
