package main

// Turning the checklist into cases.
//
// A constraint says what AWS will not accept. A case is a concrete value that
// violates it, at the path a caller would set it — the thing you can actually
// send.
//
// The trap, and the reason this emits a BASELINE alongside every case: a
// request rejected for the wrong reason looks exactly like a pass. Send
// {"MemorySize": 4} with no FunctionName and the service refuses it — for the
// missing name, not the bad memory — and a runner that only checks "was it
// refused" records a validation doze-aws does not have. So every case carries
// the request it is a mutation OF, and a runner must assert the baseline
// SUCCEEDS before asserting the mutation is refused. A case whose baseline
// fails proves nothing and must be reported as unusable, not as a pass.
//
// Baselines are not synthesised here. Filling an operation's required members
// with values that are valid *to that service* needs more than the model says —
// a table name that exists, a queue that was created first — so the baseline is
// emitted as a skeleton naming what must be filled, and the per-service runner
// supplies it. Pretending otherwise is how a generator produces a thousand
// confident, meaningless cases.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Case is one violating input, ready for a runner to send.
type Case struct {
	Operation string `json:"operation"`
	// Target is the X-Amz-Target header for the awsJson protocols, where the
	// whole request is that header plus a JSON body. Empty for protocols where
	// the request is not that simple.
	Target string `json:"target,omitempty"`
	// Path is where the value goes, in the shape the model describes:
	// "MemorySize", "KeySchema[].AttributeName", "Attributes{}".
	Path string `json:"path"`
	// Why the value is invalid, in words a failure message can use.
	Why string `json:"why"`
	// Value is the violating value. Null means "omit this member", which is
	// how a @required case is expressed.
	Value any `json:"value"`
	// Constraint is the rule being tested, for the report.
	Constraint string `json:"constraint"`
	// RequiredMembers are the operation's top-level required inputs — what a
	// baseline has to supply for the case to mean anything.
	RequiredMembers []string `json:"required_members,omitempty"`
}

// violations turns one constraint into the values that break it. A range gives
// two cases (below and above); the others give one.
func violations(c constraint) []struct {
	Why   string
	Value any
} {
	type v = struct {
		Why   string
		Value any
	}
	switch c.Kind {
	case "range":
		var out []v
		if c.Min != nil {
			out = append(out, v{fmt.Sprintf("below the minimum of %s", trimNum(*c.Min)), *c.Min - 1})
		}
		if c.Max != nil {
			out = append(out, v{fmt.Sprintf("above the maximum of %s", trimNum(*c.Max)), *c.Max + 1})
		}
		return out
	case "length":
		var out []v
		if c.Min != nil && *c.Min > 0 {
			out = append(out, v{fmt.Sprintf("shorter than the minimum length of %s", trimNum(*c.Min)), ""})
		}
		if c.Max != nil {
			out = append(out, v{fmt.Sprintf("longer than the maximum length of %s", trimNum(*c.Max)),
				strings.Repeat("a", int(*c.Max)+1)})
		}
		return out
	case "pattern":
		// A string chosen to be outside the common AWS name patterns
		// (alphanumerics, dash, underscore, dot). Not proof against every
		// possible pattern — a runner that sees this accepted should check the
		// pattern by hand rather than assume a gap.
		return []v{{"does not match " + c.Pattern, "!! not valid !!"}}
	case "enum":
		return []v{{"not one of " + strings.Join(c.Values, ", "), "__DOZE_NOT_A_MEMBER__"}}
	case "required":
		return []v{{"required member omitted", nil}}
	}
	return nil
}

// emitCases writes the cases for a service as JSON.
func emitCases(w io.Writer, m *model, found []finding, opFilter string) error {
	id, proto, _ := m.service()
	if !strings.HasPrefix(proto, "awsJson") {
		fmt.Fprintf(w, "// %s speaks %s, not awsJson.\n"+
			"// The request is more than a target header and a JSON body there, so cases\n"+
			"// are emitted without a Target and the runner must build the request itself.\n",
			shortName(id), proto)
	}

	required := requiredByOp(found)
	var cases []Case
	for _, f := range found {
		// A required member is its own case; it is not also a value violation.
		for _, v := range violations(f.Constraint) {
			cases = append(cases, Case{
				Operation:       f.Op,
				Target:          targetFor(id, proto, f.Op),
				Path:            f.Path,
				Why:             v.Why,
				Value:           v.Value,
				Constraint:      f.Constraint.String(),
				RequiredMembers: required[f.Op],
			})
		}
	}
	sort.Slice(cases, func(i, j int) bool {
		if cases[i].Operation != cases[j].Operation {
			return cases[i].Operation < cases[j].Operation
		}
		return cases[i].Path < cases[j].Path
	})

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(cases)
}

// requiredByOp collects each operation's top-level required members — the ones
// a baseline must supply. Nested requireds are left out: they only matter once
// the member containing them is being sent at all.
func requiredByOp(found []finding) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, f := range found {
		if f.Constraint.Kind != "required" || strings.ContainsAny(f.Path, ".[{") {
			continue
		}
		key := f.Op + "/" + f.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out[f.Op] = append(out[f.Op], f.Path)
	}
	for op := range out {
		sort.Strings(out[op])
	}
	return out
}

// targetFor builds the X-Amz-Target header the awsJson protocols use. The
// prefix is the service shape's own name, which is what AWS generates from.
func targetFor(serviceID, proto, op string) string {
	if !strings.HasPrefix(proto, "awsJson") {
		return ""
	}
	return shortName(serviceID) + "." + op
}
