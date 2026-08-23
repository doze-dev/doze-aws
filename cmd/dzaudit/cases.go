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
	"regexp"
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
	// HTTP is the REST binding: which members go in the URI, the query string,
	// the headers and the body. Nil for the protocols where the request is a
	// header plus one encoded body.
	HTTP *httpBinding `json:"http,omitempty"`
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
		// Derived, not guessed. The previous version returned a fixed
		// "!! not valid !!" and hoped it fell outside the pattern, which for
		// Secrets Manager's filter values it did not — that charset allows
		// spaces and exclamation marks, so the service correctly accepted the
		// case and the runner reported a gap that was not there. A generator
		// that manufactures fake gaps is worse than one that emits nothing.
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return nil // an unparseable pattern cannot be reasoned about
		}
		for _, cand := range patternViolators {
			if !re.MatchString(cand) {
				return []v{{"does not match " + c.Pattern, cand}}
			}
		}
		// Nothing on hand breaks it. Emitting a case anyway would report the
		// service as permissive for accepting a value that is in fact valid.
		return nil
	case "enum":
		return []v{{"not one of " + strings.Join(c.Values, ", "), "__DOZE_NOT_A_MEMBER__"}}
	case "required":
		return []v{{"required member omitted", nil}}
	}
	return nil
}

// emitCases writes the cases for a service as JSON.
func emitCases(w io.Writer, m *model, found []finding, opFilter string) error {
	id, proto, ops := m.service()
	if !strings.HasPrefix(proto, "awsJson") {
		fmt.Fprintf(w, "// %s speaks %s, not awsJson.\n"+
			"// The request is more than a target header and a JSON body there, so cases\n"+
			"// are emitted without a Target and the runner must build the request itself.\n",
			shortName(id), proto)
	}

	required := requiredByOp(found)
	// The REST binding is per operation, so it is read once and shared rather
	// than re-derived for each of an operation's cases.
	bindings := map[string]*httpBinding{}
	for _, opID := range ops {
		if b := m.httpFor(opID); b != nil {
			bindings[shortName(opID)] = b
		}
	}
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
				Constraint:      f.Constraint.Full(),
				RequiredMembers: required[f.Op],
				HTTP:            bindings[f.Op],
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

// patternViolators are candidate strings for breaking a @pattern, tried in
// order from least likely to appear in any AWS charset. Control characters come
// first because no AWS name pattern admits them; the printable oddities follow
// for the rare pattern that does.
var patternViolators = []string{
	"\x00\x01",
	"\u00ab\u00bb", // guillemets: non-ASCII, outside every AWS name charset seen
	"\n\t",
	"<>\"\\",
	"!! not valid !!",
	" ",
}

// httpBinding is how a REST-protocol operation puts its input on the wire. The
// awsJson and awsQuery services do not need it — the whole request there is a
// header plus one encoded body — but restJson1 and restXml spread a single
// operation's input across the method, the URI, the query string, headers and
// the body, and a runner cannot replay a case without knowing which is which.
type httpBinding struct {
	Method string `json:"method"`
	// URI is the template as the model spells it, labels and all:
	// "/restapis/{restApiId}/resources/{resourceId}".
	URI string `json:"uri"`
	// Bind maps a top-level input member to where it goes: "label",
	// "query:<name>", "header:<name>", "payload", or absent, meaning the body.
	Bind map[string]string `json:"bind,omitempty"`
}

// httpFor reads an operation's REST binding out of the model. Returns nil when
// the operation has no @http trait, which is every operation of an awsJson or
// awsQuery service.
func (m *model) httpFor(opID string) *httpBinding {
	op, ok := m.Shapes[opID]
	if !ok {
		return nil
	}
	raw, ok := op.Traits["smithy.api#http"]
	if !ok {
		return nil
	}
	var h struct {
		Method string `json:"method"`
		URI    string `json:"uri"`
	}
	if json.Unmarshal(raw, &h) != nil || h.Method == "" {
		return nil
	}
	b := &httpBinding{Method: h.Method, URI: h.URI, Bind: map[string]string{}}
	if op.Input == nil {
		return b
	}
	in, ok := m.Shapes[op.Input.Target]
	if !ok {
		return b
	}
	for name, mem := range in.Members {
		switch {
		case has(mem.Traits, "smithy.api#httpLabel"):
			b.Bind[name] = "label"
		case has(mem.Traits, "smithy.api#httpPayload"):
			b.Bind[name] = "payload"
		case has(mem.Traits, "smithy.api#httpQuery"):
			b.Bind[name] = "query:" + traitString(mem.Traits["smithy.api#httpQuery"], name)
		case has(mem.Traits, "smithy.api#httpHeader"):
			b.Bind[name] = "header:" + traitString(mem.Traits["smithy.api#httpHeader"], name)
		}
	}
	if len(b.Bind) == 0 {
		b.Bind = nil
	}
	return b
}

func has(traits map[string]json.RawMessage, id string) bool {
	_, ok := traits[id]
	return ok
}

// traitString reads a trait whose value is a bare string — @httpQuery("name")
// and @httpHeader("X-Name"). Falls back to the member name, which is what
// Smithy does for a trait with no argument.
func traitString(raw json.RawMessage, fallback string) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil || s == "" {
		return fallback
	}
	return s
}

// emitRoutes writes every operation's REST binding, including the operations
// with no constraints at all.
//
// `cases` cannot answer this: an operation with nothing to violate produces no
// cases, so it vanishes. That matters, because a runner needs the WHOLE set to
// tell an invalid request from a different valid one — omitting the last label
// of GET /restapis/{restApiId} gives GET /restapis, which is GetRestApis, an
// operation with no constrained input and therefore no cases. Without it the
// runner reads that case as a validation gap.
func emitRoutes(w io.Writer, m *model) error {
	_, _, ops := m.service()
	sort.Strings(ops)
	out := map[string]*httpBinding{}
	for _, opID := range ops {
		if b := m.httpFor(opID); b != nil {
			out[shortName(opID)] = b
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
