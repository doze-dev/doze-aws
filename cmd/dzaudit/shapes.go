package main

// The OUTPUT half of the model.
//
// Everything else in this tool reads op.Input: what a client may send, and what
// doze-aws must refuse. The models carry the other half too — what each
// operation ANSWERS with, which members are required, what type each is, which
// are enums — and nothing has ever read it. Eighteen services' worth of
// response specification, on disk, unused.
//
// That is the half no other kind of test here covers. A rejection-parity case
// proves a bad request is refused; a contract test proves an SDK can round-trip
// one operation someone thought to write a test for. Neither says anything
// about the 288 operations' responses being shaped the way AWS says they are —
// a missing required member, a number rendered as a string, an enum value that
// is not in the enum. Those reach an SDK as a decode error or, worse, as a
// zero value that looks like an answer.
//
// What is emitted is deliberately NOT the whole shape. It is the set of
// assertions worth making about a response, in the same path grammar the case
// generator already uses (`.` for a member, `[]` for a list element, `{}` for a
// map value), so a reader who knows one knows the other.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// outMember is one assertion about a response.
type outMember struct {
	Path string `json:"path"`
	// Type is the Smithy shape type, narrowed to what a JSON/XML response can
	// actually disagree about: string, integer, long, float, double, boolean,
	// timestamp, blob, list, map, structure, enum.
	Type string `json:"type"`
	// Required members must be present. AWS marks rather few output members
	// required, and the ones it does are the ones an SDK dereferences without
	// checking — which is exactly why their absence is worth failing on.
	Required bool `json:"required,omitempty"`
	// Enum lists the permitted values, for an enum-typed member.
	Enum []string `json:"enum,omitempty"`
}

// outShape is one operation's declared response.
type outShape struct {
	Operation string      `json:"operation"`
	Output    string      `json:"output"`
	Members   []outMember `json:"members"`
}

// emitShapes writes every operation's declared output shape.
func emitShapes(w io.Writer, m *model, opFilter string) error {
	_, _, ops := m.service()
	sort.Strings(ops)

	var out []outShape
	for _, opID := range ops {
		name := shortName(opID)
		if opFilter != "" && !strings.EqualFold(name, opFilter) {
			continue
		}
		op := m.Shapes[opID]
		// An operation with no output shape answers nothing worth checking —
		// Smithy's Unit. Emitting an empty entry would imply a response that
		// had been inspected and found bare, which is a different claim.
		if op.Output == nil {
			continue
		}
		var members []outMember
		m.walkOut(op.Output.Target, "", 0, map[string]bool{}, &members)
		if len(members) == 0 {
			continue
		}
		sort.Slice(members, func(i, j int) bool { return members[i].Path < members[j].Path })
		out = append(out, outShape{
			Operation: name, Output: shortName(op.Output.Target), Members: members,
		})
	}
	if len(out) == 0 {
		return fmt.Errorf("no output shapes found — the model has no operations with responses, which is not a thing")
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(out)
}

// walkOut descends a response shape, recording what can be asserted about it.
//
// It mirrors walk() rather than sharing it, for the same reason auditkit and
// modelcheck keep separate path parsers: the two answer different questions —
// walk looks for what constrains an INPUT, this for what describes an OUTPUT —
// and folding them together would mean one set of conditionals serving two
// meanings, which is how a walker ends up subtly wrong for both.
func (m *model) walkOut(shapeID, path string, depth int, seen map[string]bool, out *[]outMember) {
	if depth > maxDepth || seen[shapeID+"@"+path] {
		return
	}
	seen[shapeID+"@"+path] = true

	s, ok := m.Shapes[shapeID]
	if !ok {
		return
	}
	switch s.Type {
	case "structure":
		// The root structure itself is not an assertion — a response IS one —
		// so only its members are recorded.
		for name, mem := range s.Members {
			child := join(path, name)
			target, tok := m.Shapes[mem.Target]
			kind := "structure"
			if tok {
				kind = target.Type
			}
			e := outMember{Path: child, Type: normaliseType(kind), Required: isRequired(mem)}
			if kind == "enum" {
				e.Enum = m.enumValues(target)
			}
			*out = append(*out, e)
			m.walkOut(mem.Target, child, depth+1, seen, out)
		}
	case "list", "set":
		if s.Member != nil {
			if target, ok := m.Shapes[s.Member.Target]; ok {
				e := outMember{Path: path + "[]", Type: normaliseType(target.Type)}
				if target.Type == "enum" {
					e.Enum = m.enumValues(target)
				}
				*out = append(*out, e)
			}
			m.walkOut(s.Member.Target, path+"[]", depth+1, seen, out)
		}
	case "map":
		if s.Value != nil {
			if target, ok := m.Shapes[s.Value.Target]; ok {
				*out = append(*out, outMember{Path: path + "{}", Type: normaliseType(target.Type)})
			}
			m.walkOut(s.Value.Target, path+"{}", depth+1, seen, out)
		}
	}
}

func isRequired(mem member) bool {
	_, ok := mem.Traits["smithy.api#required"]
	return ok
}

// normaliseType collapses the Smithy types a JSON response cannot tell apart.
//
// integer, long, short and byte all arrive as a JSON number, and a checker that
// distinguished them would report a "type error" for a value that is on the
// wire exactly as AWS sends it. bigInteger and bigDecimal are deliberately left
// as numbers for the same reason. document is Smithy's any — nothing to assert.
func normaliseType(t string) string {
	switch t {
	case "integer", "long", "short", "byte", "bigInteger":
		return "integer"
	case "float", "double", "bigDecimal":
		return "float"
	case "set":
		return "list"
	case "document", "union":
		return "any"
	}
	return t
}
