package auditkit

import "strings"

// A max-length case proves a service refuses a value longer than the model
// allows, so the value only has to be that long — its content never matters.
// Written out, those cases dominated the fixtures: 127 of them held 35 MB of
// the 37.5 MB on disk, one of them a single 10 MB line of "a", in a repository
// whose first design rule is to stay light. So the fixture stores the shape
// and the harness builds the string.
//
//	{"path": "Data", "why": "longer than the maximum length of 1048576",
//	 "value_repeat": {"of": "a", "times": 1048577}}
//
// Cases whose value carries meaning — a pattern violation, an enum, a number —
// keep storing it literally under "value"; only the padding is folded away.
type Repeat struct {
	Of    string `json:"of"`
	Times int    `json:"times"`
}

// RepeatThreshold is the length past which the generator folds a repeated
// value into a Repeat. Below it the literal is smaller than the shape that
// would describe it, and easier to read in a diff.
const RepeatThreshold = 512

// Build materialises the string a Repeat stands for.
func (r Repeat) Build() string {
	if r.Times <= 0 || r.Of == "" {
		return ""
	}
	return strings.Repeat(r.Of, r.Times)
}

// Materialize returns the value a case carries: the literal when it has one,
// the expanded padding when it carries a Repeat instead. A case with neither
// has a nil value, which is itself a legal case (a required member omitted).
func Materialize(value any, r *Repeat) any {
	if r == nil {
		return value
	}
	return r.Build()
}

// FoldRepeat is the inverse, for the generator: a string long enough to be
// worth folding and made of one repeated character becomes a Repeat, and
// anything else stays a literal. Counted in characters rather than bytes, so
// a fold and its Build round-trip even if the padding is not ASCII; a longer
// repeating cycle is not worth detecting for the one case it might save.
func FoldRepeat(value any) (any, *Repeat) {
	s, ok := value.(string)
	if !ok || len(s) < RepeatThreshold {
		return value, nil
	}
	runes := []rune(s)
	for _, c := range runes {
		if c != runes[0] {
			return value, nil
		}
	}
	return nil, &Repeat{Of: string(runes[0]), Times: len(runes)}
}
