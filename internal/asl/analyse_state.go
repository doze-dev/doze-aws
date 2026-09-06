package asl

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Per-state and per-rule validation. Split from analyse.go because the
// machine-level rules and the state-level rules are two different jobs and one
// file holding both would be the longest in the package.

// fieldsByType names the state-specific fields each type may carry. Anything
// present but not listed for the state's type is reported — a Wait state with a
// Resource is a copy-paste that would otherwise be silently ignored at runtime.
var fieldsByType = map[StateType]map[string]bool{
	Pass:     {"Result": true, "Parameters": true, "ResultPath": true, "Assign": true},
	Task:     {"Resource": true, "Parameters": true, "ResultSelector": true, "ResultPath": true, "Retry": true, "Catch": true, "TimeoutSeconds": true, "TimeoutSecondsPath": true, "HeartbeatSeconds": true, "HeartbeatSecondsPath": true, "Credentials": true, "Assign": true},
	Choice:   {"Choices": true, "Default": true, "Assign": true},
	Wait:     {"Seconds": true, "SecondsPath": true, "Timestamp": true, "TimestampPath": true, "Assign": true},
	Succeed:  {},
	Fail:     {"Error": true, "ErrorPath": true, "Cause": true, "CausePath": true},
	Parallel: {"Branches": true, "Parameters": true, "ResultSelector": true, "ResultPath": true, "Retry": true, "Catch": true, "Assign": true},
	Map:      {"ItemProcessor": true, "Iterator": true, "ItemsPath": true, "ItemSelector": true, "Parameters": true, "MaxConcurrency": true, "MaxConcurrencyPath": true, "ItemReader": true, "ItemBatcher": true, "ResultWriter": true, "ResultSelector": true, "ResultPath": true, "Retry": true, "Catch": true, "ToleratedFailureCount": true, "ToleratedFailurePercentage": true, "Assign": true},
}

func analyseState(d *Definition, s *State, at string, r *Report) {
	if s.Type == "" {
		r.addf(at, "Type is required")
		return
	}
	allowed, known := fieldsByType[s.Type]
	if !known {
		r.addf(at, "unknown state type %q; expected one of Pass, Task, Choice, Wait, Succeed, Fail, Parallel, Map", s.Type)
		return
	}
	// The dialect decides the field table. A machine that says JSONata may
	// not set a state back to JSONPath — AWS allows the upgrade per state,
	// not the downgrade.
	lang := s.Dialect()
	if s.QueryLanguage == JSONPath && d.QueryLanguage == JSONata {
		r.addf(at, "QueryLanguage may not be JSONPath inside a JSONata state machine")
	}
	if lang == JSONata {
		allowed = jsonataFieldsByType[s.Type]
	}

	checkTransition(d, s, at, r)
	if lang == JSONPath {
		checkPaths(s, at, r)
		checkJSONPathDialect(s, at, r)
	} else {
		checkJSONataState(d, s, at, r)
	}
	checkMisplacedFields(s, allowed, lang, at, r)

	switch s.Type {
	case Task:
		if s.Resource == "" {
			r.addf(at, "Task requires Resource")
		}
	case Choice:
		if len(s.Choices) == 0 {
			r.addf(at, "Choice requires a non-empty Choices array")
		}
		for i, rule := range s.Choices {
			if lang == JSONata {
				break // checkJSONataState covered the rules
			}
			analyseRule(d, rule, fmt.Sprintf("%s.Choices[%d]", at, i), true, r)
		}
		if s.Default != "" {
			checkTarget(d, s.Default, at+".Default", r)
		}
	case Wait:
		checkWait(s, at, r)
	case Parallel:
		if len(s.Branches) == 0 {
			r.addf(at, "Parallel requires a non-empty Branches array")
		}
		for i, b := range s.Branches {
			analyseDefinition(b, fmt.Sprintf("%s.Branches[%d].States", at, i), r)
		}
	case Map:
		p := s.Processor()
		if p == nil {
			r.addf(at, "Map requires ItemProcessor (or the retired spelling, Iterator)")
		} else {
			label := ".ItemProcessor.States"
			if s.ItemProcessor == nil {
				label = ".Iterator.States"
			}
			analyseDefinition(p, at+label, r)
		}
		if s.ItemProcessor != nil && s.Iterator != nil {
			r.addf(at, "ItemProcessor and Iterator are the same field under two names; set only one")
		}
		if s.MaxConcurrency != nil && *s.MaxConcurrency < 0 {
			r.addf(at, "MaxConcurrency may not be negative, got %v", *s.MaxConcurrency)
		}
	case Fail:
		if s.Error != "" && s.ErrorPath != "" {
			r.addf(at, "set Error or ErrorPath, not both")
		}
		if s.Cause != "" && s.CausePath != "" {
			r.addf(at, "set Cause or CausePath, not both")
		}
	}

	if len(s.Retry) > 0 || len(s.Catch) > 0 {
		if s.Type != Task && s.Type != Parallel && s.Type != Map {
			r.addf(at, "Retry and Catch are only allowed on Task, Parallel and Map")
		}
	}
	for i, retrier := range s.Retry {
		checkErrorEquals(retrier.ErrorEquals, i == len(s.Retry)-1,
			fmt.Sprintf("%s.Retry[%d]", at, i), r)
		if retrier.MaxAttempts != nil && *retrier.MaxAttempts < 0 {
			r.addf(fmt.Sprintf("%s.Retry[%d]", at, i), "MaxAttempts may not be negative")
		}
		if retrier.BackoffRate != nil && *retrier.BackoffRate < 1 {
			r.addf(fmt.Sprintf("%s.Retry[%d]", at, i), "BackoffRate must be at least 1.0, got %v", *retrier.BackoffRate)
		}
	}
	for i, catcher := range s.Catch {
		where := fmt.Sprintf("%s.Catch[%d]", at, i)
		checkErrorEquals(catcher.ErrorEquals, i == len(s.Catch)-1, where, r)
		if catcher.Next == "" {
			r.addf(where, "Next is required")
		} else {
			checkTarget(d, catcher.Next, where+".Next", r)
		}
		if catcher.ResultPath != nil {
			if err := ValidateReferencePath(*catcher.ResultPath); err != nil {
				r.addf(where+".ResultPath", "%v", err)
			}
		}
	}
}

// checkTransition enforces the Next/End rules, which differ by type: Choice
// transitions only through its rules, Succeed and Fail are terminal, and
// everything else needs exactly one of Next or End.
func checkTransition(d *Definition, s *State, at string, r *Report) {
	switch s.Type {
	case Succeed, Fail:
		if s.Next != "" || s.End {
			r.addf(at, "%s is terminal and may not set Next or End", s.Type)
		}
	case Choice:
		if s.Next != "" || s.End {
			r.addf(at, "Choice transitions through its rules and Default, not Next or End")
		}
	default:
		switch {
		case s.Next != "" && s.End:
			r.addf(at, "set Next or End, not both")
		case s.Next == "" && !s.End:
			r.addf(at, "needs Next, or End: true")
		case s.Next != "":
			checkTarget(d, s.Next, at+".Next", r)
		}
	}
}

func checkTarget(d *Definition, name, at string, r *Report) {
	if _, ok := d.States[name]; !ok {
		r.addf(at, "names %q, which is not a state here", name)
	}
}

func checkWait(s *State, at string, r *Report) {
	set := 0
	for _, present := range []bool{
		s.Seconds != nil || s.SecondsExpr != "", s.SecondsPath != "", s.Timestamp != "", s.TimestampPath != "",
	} {
		if present {
			set++
		}
	}
	switch {
	case set == 0:
		r.addf(at, "Wait requires exactly one of Seconds, SecondsPath, Timestamp or TimestampPath")
	case set > 1:
		r.addf(at, "Wait sets %d of Seconds, SecondsPath, Timestamp and TimestampPath; exactly one is allowed", set)
	}
	if s.Seconds != nil && *s.Seconds < 0 {
		r.addf(at, "Seconds may not be negative, got %v", *s.Seconds)
	}
}

// checkPaths validates every path-shaped field, distinguishing paths from
// reference paths — a wildcard is legal in neither, but the context object is
// readable and not writable.
func checkPaths(s *State, at string, r *Report) {
	for _, f := range []struct {
		name string
		val  *string
	}{{"InputPath", s.InputPath}, {"OutputPath", s.OutputPath}} {
		// A null path is meaningful: it discards the document.
		if f.val != nil && *f.val != "" {
			if err := ValidatePath(*f.val); err != nil {
				r.addf(at+"."+f.name, "%v", err)
			}
		}
	}
	if s.ResultPath != nil && *s.ResultPath != "" {
		if err := ValidateReferencePath(*s.ResultPath); err != nil {
			r.addf(at+".ResultPath", "%v", err)
		}
	}
	for _, f := range []struct{ name, val string }{
		{"ItemsPath", s.ItemsPath},
		{"SecondsPath", s.SecondsPath},
		{"TimestampPath", s.TimestampPath},
		{"TimeoutSecondsPath", s.TimeoutSecondsPath},
		{"HeartbeatSecondsPath", s.HeartbeatSecondsPath},
		{"MaxConcurrencyPath", s.MaxConcurrencyPath},
		{"ErrorPath", s.ErrorPath},
		{"CausePath", s.CausePath},
	} {
		if f.val != "" {
			if err := ValidatePath(f.val); err != nil {
				r.addf(at+"."+f.name, "%v", err)
			}
		}
	}
}

// checkMisplacedFields reports a field that belongs to a different state type.
// Runtime would ignore it; ignoring it is how a Task's Retry ends up on the
// Choice state above it and nobody notices until production.
func checkMisplacedFields(s *State, allowed map[string]bool, lang QueryLanguage, at string, r *Report) {
	for _, f := range []struct {
		name    string
		present bool
	}{
		{"Result", len(s.Result) > 0},
		{"Resource", s.Resource != ""},
		{"Choices", len(s.Choices) > 0},
		{"Default", s.Default != ""},
		{"Branches", len(s.Branches) > 0},
		{"ItemProcessor", s.ItemProcessor != nil},
		{"Iterator", s.Iterator != nil},
		{"ItemsPath", s.ItemsPath != ""},
		{"Seconds", s.Seconds != nil},
		{"SecondsPath", s.SecondsPath != ""},
		{"Timestamp", s.Timestamp != ""},
		{"TimestampPath", s.TimestampPath != ""},
		{"Error", s.Error != ""},
		{"Cause", s.Cause != ""},
		{"ResultPath", s.ResultPath != nil},
		{"ResultSelector", len(s.ResultSelector) > 0},
		{"Parameters", len(s.Parameters) > 0},
		{"MaxConcurrency", s.MaxConcurrency != nil},
		{"HeartbeatSeconds", s.HeartbeatSeconds != nil},
		{"Arguments", len(s.Arguments) > 0},
		{"Output", len(s.Output) > 0},
		{"Assign", len(s.Assign) > 0},
		{"Items", len(s.Items) > 0},
		// The rest are JSONPath fields the JSONPath tables never listed —
		// InputPath belongs to every JSONPath state, the Path twins are
		// validated by checkPaths — and are here only so a JSONata state
		// carrying one is refused by name.
		{"InputPath", lang == JSONata && s.InputPath != nil},
		{"OutputPath", lang == JSONata && s.OutputPath != nil},
		{"TimeoutSecondsPath", lang == JSONata && s.TimeoutSecondsPath != ""},
		{"HeartbeatSecondsPath", lang == JSONata && s.HeartbeatSecondsPath != ""},
		{"MaxConcurrencyPath", lang == JSONata && s.MaxConcurrencyPath != ""},
		{"ErrorPath", lang == JSONata && s.ErrorPath != ""},
		{"CausePath", lang == JSONata && s.CausePath != ""},
	} {
		switch {
		case !f.present || allowed[f.name]:
		case lang == JSONata && jsonpathOnlyFields[f.name], lang == JSONPath && jsonataOnlyFields[f.name]:
			r.addf(at, "%s", notSupported(f.name, lang))
		default:
			r.addf(at, "%s is not a field of a %s state", f.name, s.Type)
		}
	}
}

// checkErrorEquals enforces the States.ALL rules: it must be alone in its
// array, and it must be in the last Retrier or Catcher — anything after it is
// dead, because it matches everything.
func checkErrorEquals(names []string, last bool, at string, r *Report) {
	if len(names) == 0 {
		r.addf(at, "ErrorEquals is required and may not be empty")
		return
	}
	for _, name := range names {
		if name == ErrAll {
			if len(names) > 1 {
				r.addf(at, "States.ALL matches every error, so it must be the only entry of ErrorEquals")
			}
			if !last {
				r.addf(at, "States.ALL matches every error, so nothing after it can ever run")
			}
			continue
		}
		if strings.HasPrefix(name, "States.") && !IsReserved(name) {
			r.addf(at, "%q is not an error name ASL defines, and the States. prefix is reserved", name)
		}
	}
}

// analyseRule validates one choice rule. top says whether it is a member of
// Choices (which must carry Next) or nested inside And/Or/Not (which must not).
func analyseRule(d *Definition, rule *ChoiceRule, at string, top bool, r *Report) {
	combinators := 0
	if len(rule.And) > 0 {
		combinators++
	}
	if len(rule.Or) > 0 {
		combinators++
	}
	if rule.Not != nil {
		combinators++
	}

	switch {
	case top && rule.Next == "":
		r.addf(at, "a top-level choice rule requires Next")
	case top:
		checkTarget(d, rule.Next, at+".Next", r)
	case rule.Next != "":
		r.addf(at, "only a top-level choice rule may set Next")
	}

	hasComparison := rule.Comparison != nil
	switch {
	case combinators > 1:
		r.addf(at, "a rule may use only one of And, Or or Not")
	case combinators == 1 && hasComparison:
		r.addf(at, "a rule is either a comparison or a boolean combinator, not both")
	case combinators == 0 && !hasComparison && len(rule.Condition) == 0:
		r.addf(at, "a rule needs a comparison, or And, Or or Not")
	}

	if hasComparison {
		if rule.Variable == "" {
			r.addf(at, "a comparison rule requires Variable")
		} else if err := ValidatePath(rule.Variable); err != nil {
			r.addf(at+".Variable", "%v", err)
		}
		checkOperand(rule.Comparison, at, r)
	} else if rule.Variable != "" && combinators > 0 {
		r.addf(at, "Variable belongs on the comparison inside %s, not beside it", combinatorName(rule))
	}

	for i, sub := range rule.And {
		analyseRule(d, sub, fmt.Sprintf("%s.And[%d]", at, i), false, r)
	}
	for i, sub := range rule.Or {
		analyseRule(d, sub, fmt.Sprintf("%s.Or[%d]", at, i), false, r)
	}
	if rule.Not != nil {
		analyseRule(d, rule.Not, at+".Not", false, r)
	}
}

func combinatorName(rule *ChoiceRule) string {
	switch {
	case len(rule.And) > 0:
		return "And"
	case len(rule.Or) > 0:
		return "Or"
	default:
		return "Not"
	}
}

// checkOperand type-checks a comparison's operand against what the operator
// expects — a NumericEquals compared against a string is a definition AWS
// refuses, and one that would otherwise simply never match.
func checkOperand(c *Comparison, at string, r *Report) {
	where := at + "." + c.Op
	if c.IsPath {
		var p string
		if json.Unmarshal(c.Operand, &p) != nil {
			r.addf(where+"Path", "the operand of a ...Path comparison must be a reference path string")
			return
		}
		if err := ValidatePath(p); err != nil {
			r.addf(where+"Path", "%v", err)
		}
		return
	}

	_, _, kind, _ := LookupOperator(c.Op)
	var v any
	if json.Unmarshal(c.Operand, &v) != nil {
		r.addf(where, "the operand is not valid JSON")
		return
	}
	switch kind {
	case OperandString, OperandTimestamp:
		s, ok := v.(string)
		if !ok {
			r.addf(where, "expects a string operand, got %s", jsonKind(v))
			return
		}
		if kind == OperandTimestamp && !isRFC3339(s) {
			r.addf(where, "expects an ISO-8601 timestamp, got %q", s)
		}
	case OperandNumber:
		if _, ok := v.(float64); !ok {
			r.addf(where, "expects a numeric operand, got %s", jsonKind(v))
		}
	case OperandBool, OperandPresence:
		if _, ok := v.(bool); !ok {
			r.addf(where, "expects a boolean operand, got %s", jsonKind(v))
		}
	}
}

func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	default:
		return "an object"
	}
}
