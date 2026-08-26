package asl

import (
	"fmt"
	"sort"
)

// Static analysis: everything that can be known about a definition without
// running it.
//
// This is what makes CreateStateMachine worth having locally. AWS validates a
// definition at create time and refuses it with a list of diagnostics, so a
// definition that is accepted here and rejected on deploy is exactly the
// failure doze-aws exists to prevent — and the reverse, refusing something AWS
// accepts, breaks a working template. Both directions matter.
//
// Every rule reports rather than returns: a definition with four mistakes
// should produce four diagnostics, not four edit-and-retry cycles.

// Diagnostic is one problem with a definition.
type Diagnostic struct {
	// Where names the state, branch or rule, e.g. `States.Process.Retry[0]`.
	Where string
	// Message says what is wrong, in AWS's vocabulary where it has one.
	Message string
}

func (d Diagnostic) String() string {
	if d.Where == "" {
		return d.Message
	}
	return d.Where + ": " + d.Message
}

// Report is the outcome of analysing a definition.
type Report struct {
	Diagnostics []Diagnostic
}

// OK reports whether the definition is valid.
func (r *Report) OK() bool { return len(r.Diagnostics) == 0 }

// Error renders the report as one message, for the paths that want a single
// string — CreateStateMachine's InvalidDefinition, for instance.
func (r *Report) Error() string {
	if r.OK() {
		return ""
	}
	msg := r.Diagnostics[0].String()
	if n := len(r.Diagnostics) - 1; n > 0 {
		msg += fmt.Sprintf(" (and %d more)", n)
	}
	return msg
}

func (r *Report) addf(where, format string, args ...any) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Where: where, Message: fmt.Sprintf(format, args...)})
}

// Analyse validates a whole definition.
func Analyse(d *Definition) *Report {
	r := &Report{}
	analyseDefinition(d, "States", r)
	return r
}

// analyseDefinition checks one machine or sub-machine. at is the path prefix
// for diagnostics, so a Map's inner states report as
// `States.Process.ItemProcessor.States.Step`.
func analyseDefinition(d *Definition, at string, r *Report) {
	if len(d.States) == 0 {
		r.addf(at, "a state machine must have at least one state")
		return
	}
	if d.StartAt == "" {
		r.addf(at, "StartAt is required")
	} else if _, ok := d.States[d.StartAt]; !ok {
		r.addf(at, "StartAt names %q, which is not a state here", d.StartAt)
	}
	if d.QueryLanguage != "" && d.QueryLanguage != JSONPath && d.QueryLanguage != JSONata {
		r.addf(at, "QueryLanguage must be JSONPath or JSONata, got %q", d.QueryLanguage)
	}
	if d.TimeoutSeconds != nil && *d.TimeoutSeconds <= 0 {
		r.addf(at, "TimeoutSeconds must be positive, got %v", *d.TimeoutSeconds)
	}

	for _, name := range d.Order {
		analyseState(d, d.States[name], at+"."+name, r)
	}
	reportUnreachable(d, at, r)
}

// reportUnreachable finds states nothing transitions to. AWS refuses these, and
// the refusal is worth keeping: an unreachable state is nearly always a renamed
// Next that was not updated, and it fails silently at runtime by simply never
// running.
func reportUnreachable(d *Definition, at string, r *Report) {
	if d.StartAt == "" {
		return // already reported; reachability would be noise on top
	}
	seen := map[string]bool{}
	var walk func(name string)
	walk = func(name string) {
		s, ok := d.States[name]
		if !ok || seen[name] {
			return
		}
		seen[name] = true
		for _, next := range transitions(s) {
			walk(next)
		}
	}
	walk(d.StartAt)

	var orphans []string
	for name := range d.States {
		if !seen[name] {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		r.addf(at+"."+name, "no other state transitions here, and it is not StartAt")
	}
}

// transitions lists every state name this state can move to.
func transitions(s *State) []string {
	var out []string
	if s.Next != "" {
		out = append(out, s.Next)
	}
	if s.Default != "" {
		out = append(out, s.Default)
	}
	for _, c := range s.Choices {
		if c.Next != "" {
			out = append(out, c.Next)
		}
	}
	for _, c := range s.Catch {
		if c.Next != "" {
			out = append(out, c.Next)
		}
	}
	return out
}

// ValidateDefinition parses and analyses in one step, which is what
// CreateStateMachine and ValidateStateMachineDefinition both want.
func ValidateDefinition(raw []byte) (*Definition, *Report) {
	d, err := Parse(raw)
	if err != nil {
		return nil, &Report{Diagnostics: []Diagnostic{{Message: err.Error()}}}
	}
	return d, Analyse(d)
}
