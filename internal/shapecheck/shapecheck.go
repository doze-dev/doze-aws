// Package shapecheck holds a response against the shape AWS says it has.
//
// # What this is for
//
// internal/modelcheck refuses the inputs AWS's models say are invalid. This is
// the other direction, from the other half of the same models: given what an
// operation ANSWERED, does it match the output shape AWS declares?
//
// Nothing else in the tree asks that. A rejection-parity case proves a bad
// request is refused. An SDK contract test proves one operation somebody
// thought to write a test for round-trips. Neither says anything about the
// shape of the other 280-odd responses, and the failures hide well: a missing
// required member reaches an SDK as a zero value that looks like an answer, and
// a number rendered as a string is a decode error in a strongly-typed client
// and silence in a loose one.
//
// # What it checks, and what it cannot
//
//	required   a member the model marks required is present
//	type       a value is the JSON kind its declared Smithy type produces
//	enum       a value is one of the declared members
//
// It checks SHAPE, never VALUES. It cannot tell you that a count is 3 when AWS
// would say 4, or that an ARN names the wrong region. Only a real AWS response
// can, and there is none here. Saying so plainly matters more than the check
// itself: a shape test that gets mistaken for a parity test is worse than no
// test, because it retires the question.
package shapecheck

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Member is one assertion about a response, as `dzaudit shapes` emits it.
type Member struct {
	Path     string   `json:"path"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

// Shape is one operation's declared response.
type Shape struct {
	Operation string   `json:"operation"`
	Output    string   `json:"output"`
	Members   []Member `json:"members"`
}

// Problem is one way a response failed to match its shape.
type Problem struct {
	Path string
	Why  string
}

func (p Problem) String() string { return p.Path + ": " + p.Why }

// Check reports every way body fails to match the declared shape.
//
// An empty result means the response matched. A nil body is not an error here:
// plenty of operations answer nothing, and whether that is allowed is the
// caller's question, not the shape's.
func Check(body []byte, s Shape) []Problem {
	if len(body) == 0 {
		return requiredMissing(s, "the response was empty")
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return []Problem{{Path: "", Why: "the response is not JSON: " + err.Error()}}
	}
	var out []Problem
	for _, m := range s.Members {
		// Required is relative to the PARENT, not to the response.
		//
		// The first version resolved the whole path and called an empty result
		// missing, which made every required member under an absent optional
		// structure a failure: DynamoDB's RestoreSummary is not in a
		// CreateTable response at all, and its two required children were
		// reported as absent. They are not absent, they are not applicable —
		// and a checker that cannot tell those apart reports nine findings for
		// a correct response, which is how a new check gets switched off in its
		// first week.
		//
		// So: find the containers the parent names. None means the question
		// does not arise. For each one that exists, the leaf must be there.
		parent, leaf := splitPath(m.Path)
		for _, c := range resolve(root, parent) {
			values, present := step(c, leaf)
			if !present {
				if m.Required {
					out = append(out, Problem{m.Path,
						"required by the model and absent from the response"})
				}
				continue
			}
			for _, v := range values {
				if v == nil {
					// An explicit null. AWS omits rather than nulls, but a null
					// is not a TYPE error, and reporting it as one would be
					// wrong about what went wrong.
					continue
				}
				if p, bad := checkType(m, v); bad {
					out = append(out, p)
					continue
				}
				if p, bad := checkEnum(m, v); bad {
					out = append(out, p)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func requiredMissing(s Shape, why string) []Problem {
	var out []Problem
	for _, m := range s.Members {
		if m.Required && !strings.ContainsAny(m.Path, ".[{") {
			out = append(out, Problem{m.Path, "required by the model, and " + why})
		}
	}
	return out
}

func checkType(m Member, v any) (Problem, bool) {
	want := m.Type
	switch want {
	case "", "any", "blob", "timestamp":
		// blob: base64 string in JSON, bytes in CBOR — the wire decides, not
		// the model. timestamp: epoch number in JSON, ISO string in XML, and
		// both are correct. Neither is checkable without knowing the protocol,
		// and guessing would manufacture failures.
		return Problem{}, false
	case "string", "enum":
		if _, ok := v.(string); !ok {
			return Problem{m.Path, fmt.Sprintf("the model says %s, the response has %s", want, kindOf(v))}, true
		}
	case "integer":
		f, ok := v.(float64)
		if !ok {
			return Problem{m.Path, fmt.Sprintf("the model says an integer, the response has %s", kindOf(v))}, true
		}
		if f != math.Trunc(f) {
			return Problem{m.Path, fmt.Sprintf("the model says an integer, the response has %v", f)}, true
		}
	case "float":
		if _, ok := v.(float64); !ok {
			return Problem{m.Path, fmt.Sprintf("the model says a number, the response has %s", kindOf(v))}, true
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return Problem{m.Path, fmt.Sprintf("the model says a boolean, the response has %s", kindOf(v))}, true
		}
	case "list":
		if _, ok := v.([]any); !ok {
			return Problem{m.Path, fmt.Sprintf("the model says a list, the response has %s", kindOf(v))}, true
		}
	case "map", "structure":
		if _, ok := v.(map[string]any); !ok {
			return Problem{m.Path, fmt.Sprintf("the model says a %s, the response has %s", want, kindOf(v))}, true
		}
	}
	return Problem{}, false
}

func checkEnum(m Member, v any) (Problem, bool) {
	if len(m.Enum) == 0 {
		return Problem{}, false
	}
	s, ok := v.(string)
	if !ok {
		return Problem{}, false
	}
	for _, want := range m.Enum {
		if s == want {
			return Problem{}, false
		}
	}
	return Problem{m.Path, fmt.Sprintf("%q is not one of the model's values: %s",
		s, strings.Join(m.Enum, ", "))}, true
}

func kindOf(v any) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("the string %q", truncate(t))
	case float64:
		return fmt.Sprintf("the number %v", t)
	case bool:
		return fmt.Sprintf("the boolean %v", t)
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	case nil:
		return "null"
	}
	return "an unknown value"
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// resolve returns every value a path names.
//
// A path can name MANY values — "Queues[].Name" is one per element — so this
// answers with all of them, and Check reports each separately. Collapsing to
// the first would hide the case where element seventeen is the malformed one,
// which is the case worth catching.
func resolve(root any, path string) []any {
	cur := []any{root}
	for _, seg := range segments(path) {
		var next []any
		for _, v := range cur {
			switch seg.kind {
			case segMember:
				obj, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if child, ok := obj[seg.name]; ok {
					next = append(next, child)
				}
			case segElem:
				list, ok := v.([]any)
				if !ok {
					continue
				}
				next = append(next, list...)
			case segValue:
				obj, ok := v.(map[string]any)
				if !ok {
					continue
				}
				for _, child := range obj {
					next = append(next, child)
				}
			}
		}
		cur = next
		if len(cur) == 0 {
			return nil
		}
	}
	return cur
}

type segKind int

const (
	segMember segKind = iota
	segElem           // []
	segValue          // {}
)

type segment struct {
	kind segKind
	name string
}

// segments parses the path grammar dzaudit emits: Member.Child, List[], Map{}.
//
// A third implementation of this grammar, and deliberately so — auditkit's note
// says it best: a test that shares the parser it is checking cannot catch a
// parser wrong in the same way twice.
func segments(path string) []segment {
	var out []segment
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		// Strip the trailing markers first, keeping them in access order: they
		// are peeled off right to left, so each one goes in FRONT of the ones
		// already peeled. "A[]{}" is a list of maps — element, then value.
		var marks []segment
		for {
			if rest, ok := strings.CutSuffix(part, "[]"); ok {
				part = rest
				marks = append([]segment{{kind: segElem}}, marks...)
				continue
			}
			if rest, ok := strings.CutSuffix(part, "{}"); ok {
				part = rest
				marks = append([]segment{{kind: segValue}}, marks...)
				continue
			}
			break
		}
		// The member name is reached before anything that indexes into it.
		if part != "" {
			out = append(out, segment{kind: segMember, name: part})
		}
		out = append(out, marks...)
	}
	return out
}

// splitPath separates a path into the container it lives in and the final step.
//
// "Table.RestoreSummary.RestoreDateTime" is the member RestoreDateTime inside
// the container Table.RestoreSummary; "Queues[]" is the ELEMENT step inside the
// container Queues. The final step is what must be present; the container is
// what decides whether the question is asked at all.
func splitPath(path string) (parent string, leaf segment) {
	segs := segments(path)
	if len(segs) == 0 {
		return "", segment{}
	}
	leaf = segs[len(segs)-1]
	// Rebuild the parent from the original text rather than re-rendering the
	// segments, so the two parsers cannot disagree about what they mean.
	switch {
	case strings.HasSuffix(path, "[]"):
		return strings.TrimSuffix(path, "[]"), leaf
	case strings.HasSuffix(path, "{}"):
		return strings.TrimSuffix(path, "{}"), leaf
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i], leaf
	}
	return "", leaf
}

// step takes the final step from a container, returning every value it names
// and whether the step was possible at all.
//
// A list or map step EXPANDS: "Failed[]" names each element, not the list. An
// empty list therefore names nothing and is not a missing member — it is a
// response that legitimately returned no rows, and failing on that would break
// every empty List* answer there is.
func step(container any, leaf segment) ([]any, bool) {
	switch leaf.kind {
	case segMember:
		obj, ok := container.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := obj[leaf.name]
		if !ok {
			return nil, false
		}
		return []any{v}, true
	case segElem:
		list, ok := container.([]any)
		if !ok {
			return nil, false
		}
		return list, true
	case segValue:
		obj, ok := container.(map[string]any)
		if !ok {
			return nil, false
		}
		out := make([]any, 0, len(obj))
		for _, v := range obj {
			out = append(out, v)
		}
		return out, true
	}
	return nil, false
}
