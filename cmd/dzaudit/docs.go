package main

// `dzaudit docs <model>` — the model's own words, as text a person can read.
//
// # What this closes
//
// The constraint traits (`length`, `range`, `pattern`, `enum`) are what the
// other subcommands read, and for several services they are a small part of
// what the model says. SQS's model carries no range at all: every limit, every
// default, every "can't" and "only" is in the `documentation` strings, as HTML,
// inside a JSON file nobody opens. Stage 1 wrote SQS's rules from memory and
// the older developer guide, and fourteen of its findings were found only by
// reading this text afterwards: a limit four times too small, a feature refused
// that AWS accepts, error codes that were never the model's.
//
// Reading it is step one of the protocol (plan/01-protocol.md), and this makes
// it one command instead of a JSON walk written again for each service. Per
// operation: its documentation, the errors it lists with the code and status
// each protocol gives them, and every input and output member with its traits
// and its own documentation, nested structures included once each.

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strings"
)

var (
	htmlTag    = regexp.MustCompile(`<[^>]+>`)
	whitespace = regexp.MustCompile(`\s+`)
)

// plain turns a model's documentation string (HTML) into one line of text.
func plain(doc string) string {
	doc = htmlTag.ReplaceAllString(doc, " ")
	return strings.TrimSpace(whitespace.ReplaceAllString(html.UnescapeString(doc), " "))
}

func docTrait(traits map[string]json.RawMessage, name string) string {
	var s string
	if raw, ok := traits[name]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// emitDocs writes the documentation of every operation, or of the one named.
func emitDocs(w io.Writer, m *model, opFilter string) error {
	_, _, ops := m.service()
	sort.Strings(ops)
	wrote := 0
	for _, opID := range ops {
		name := shortName(opID)
		if opFilter != "" && !strings.EqualFold(name, opFilter) {
			continue
		}
		op := m.Shapes[opID]
		wrote++
		fmt.Fprintf(w, "== %s\n", name)
		if d := plain(docTrait(op.Traits, "smithy.api#documentation")); d != "" {
			fmt.Fprintf(w, "%s\n", d)
		}
		if len(op.Errors) > 0 {
			fmt.Fprintln(w, "\n  errors:")
			for _, e := range op.Errors {
				fmt.Fprintf(w, "    %s\n", m.errorLine(e.Target))
			}
		}
		seen := map[string]bool{}
		if op.Input != nil {
			fmt.Fprintln(w, "\n  input:")
			m.docMembers(w, op.Input.Target, "    ", seen)
		}
		if op.Output != nil {
			fmt.Fprintln(w, "\n  output:")
			m.docMembers(w, op.Output.Target, "    ", seen)
		}
		fmt.Fprintln(w)
	}
	if wrote == 0 {
		return fmt.Errorf("no operation named %q", opFilter)
	}
	return nil
}

// errorLine is an error shape with what each protocol calls it and answers.
func (m *model) errorLine(id string) string {
	s := m.Shapes[id]
	parts := []string{shortName(id)}
	var q struct {
		Code   string `json:"code"`
		Status int    `json:"httpResponseCode"`
	}
	if raw, ok := s.Traits["aws.protocols#awsQueryError"]; ok && json.Unmarshal(raw, &q) == nil {
		parts = append(parts, fmt.Sprintf("query code %s, status %d", q.Code, q.Status))
	}
	var status int
	if raw, ok := s.Traits["smithy.api#httpError"]; ok && json.Unmarshal(raw, &status) == nil {
		parts = append(parts, fmt.Sprintf("httpError %d", status))
	}
	line := strings.Join(parts, " · ")
	if d := plain(docTrait(s.Traits, "smithy.api#documentation")); d != "" {
		line += " — " + d
	}
	return line
}

// docMembers writes a structure's members: name, what it is, its constraints,
// its wire name where the protocol gives it another, and its documentation.
// A nested structure is written once, under the first member that reaches it.
func (m *model) docMembers(w io.Writer, id, indent string, seen map[string]bool) {
	s, ok := m.Shapes[id]
	if !ok || seen[id] {
		return
	}
	seen[id] = true
	names := make([]string, 0, len(s.Members))
	for n := range s.Members {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mem := s.Members[n]
		target := m.Shapes[mem.Target]
		var facts []string
		if _, req := mem.Traits["smithy.api#required"]; req {
			facts = append(facts, "required")
		}
		kind := target.Type
		if kind == "" {
			kind = shortName(mem.Target)
		}
		facts = append(facts, kind)
		for _, t := range []string{"smithy.api#length", "smithy.api#range", "smithy.api#pattern"} {
			if raw, ok := target.Traits[t]; ok {
				facts = append(facts, strings.TrimPrefix(t, "smithy.api#")+" "+string(raw))
			}
		}
		if target.Type == "enum" {
			vals := make([]string, 0, len(target.Members))
			for v := range target.Members {
				vals = append(vals, v)
			}
			sort.Strings(vals)
			facts = append(facts, "one of "+strings.Join(vals, ", "))
		}
		if x := docTrait(mem.Traits, "smithy.api#xmlName"); x != "" {
			facts = append(facts, "xmlName "+x)
		}
		if _, flat := mem.Traits["smithy.api#xmlFlattened"]; flat {
			facts = append(facts, "xmlFlattened")
		}
		fmt.Fprintf(w, "%s%s (%s)\n", indent, n, strings.Join(facts, "; "))
		doc := plain(docTrait(mem.Traits, "smithy.api#documentation"))
		if doc == "" {
			doc = plain(docTrait(target.Traits, "smithy.api#documentation"))
		}
		if doc != "" {
			fmt.Fprintf(w, "%s  %s\n", indent, doc)
		}
		// What is inside: a structure's members, a list's elements, a map's values.
		for _, inner := range []string{mem.Target, memberTarget(target.Member), memberTarget(target.Value)} {
			if in, ok := m.Shapes[inner]; ok && in.Type == "structure" {
				m.docMembers(w, inner, indent+"    ", seen)
			}
		}
	}
}

func memberTarget(mem *member) string {
	if mem == nil {
		return ""
	}
	return mem.Target
}
