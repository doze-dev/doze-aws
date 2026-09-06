package asl

import (
	"fmt"
	"strconv"
	"strings"
)

// Reference paths.
//
// ASL has two path flavours and conflating them is a real bug, not a pedantic
// distinction. A *path* (InputPath, OutputPath) may select nothing, in which
// case the state's input becomes null. A *reference path* (ResultPath,
// ItemsPath, and every ...Path operand) must identify exactly one node, so
// wildcards and filters are illegal in it — AWS rejects them at create time,
// and so do we, because a ResultPath containing a wildcard has no meaning to
// fall back on.
//
// This deliberately implements the subset ASL actually permits rather than
// general JSONPath. Supporting `$..book[?(@.price<10)]` would be more code and
// would accept definitions AWS refuses, which is the wrong direction for an
// emulator: passing here and failing on deploy is the failure we exist to
// prevent.

// Root says which document a path starts from.
type Root int

const (
	// RootInput is "$" — the state's input.
	RootInput Root = iota
	// RootContext is "$$" — the execution context object.
	RootContext
	// RootVariable is "$name" — a variable written by an earlier state's
	// Assign; the name is the path's Variable.
	RootVariable
)

// variablesKey is where buildContext carries the frame's variables inside
// the context object, so a $name path resolves wherever a $$ path does.
// Not an AWS field; a definition that spells it out gets nothing useful.
const variablesKey = "\x00vars"

// Segment is one step of a path: a field name or an array index.
type Segment struct {
	Key     string
	Index   int
	IsIndex bool
}

// Path is a parsed reference path.
type Path struct {
	Root     Root
	Segments []Segment
	// Variable is the name after $ for a RootVariable path.
	Variable string
	// Raw is the path as written, for error messages that quote it back.
	Raw string
}

// IsRoot reports whether the path selects the whole document.
func (p Path) IsRoot() bool { return len(p.Segments) == 0 }

// ParsePath parses a reference path. It accepts the dotted form (`$.a.b`), the
// bracketed form (`$['a']['b']`), array indices (`$.a[0]`), and the context
// object (`$$.Execution.Name`).
func ParsePath(s string) (Path, error) {
	p := Path{Raw: s}
	switch {
	case s == "":
		return p, fmt.Errorf("a reference path may not be empty")
	case strings.HasPrefix(s, "$$"):
		p.Root, s = RootContext, s[2:]
	case len(s) > 1 && s[0] == '$' && isVariableStart(s[1]):
		// $name — a variable. The name runs to the first '.' or '['.
		p.Root = RootVariable
		end := 1
		for end < len(s) && s[end] != '.' && s[end] != '[' {
			end++
		}
		p.Variable, s = s[1:end], s[end:]
	case strings.HasPrefix(s, "$"):
		p.Root, s = RootInput, s[1:]
	default:
		return p, fmt.Errorf("a reference path must start with $ or $$, got %q", p.Raw)
	}

	for s != "" {
		switch s[0] {
		case '.':
			// A second dot is JSONPath's recursive descent, which selects an
			// unbounded set and so cannot be a reference path.
			if strings.HasPrefix(s, "..") {
				return p, fmt.Errorf("recursive descent (..) is not a reference path: %q", p.Raw)
			}
			name, rest := splitField(s[1:])
			if name == "" {
				return p, fmt.Errorf("empty field name in %q", p.Raw)
			}
			if err := checkPlain(name, p.Raw); err != nil {
				return p, err
			}
			p.Segments = append(p.Segments, Segment{Key: name})
			s = rest
		case '[':
			seg, rest, err := parseBracket(s, p.Raw)
			if err != nil {
				return p, err
			}
			p.Segments = append(p.Segments, seg)
			s = rest
		default:
			return p, fmt.Errorf("unexpected %q in %q", s[0], p.Raw)
		}
	}
	return p, nil
}

// splitField reads a dotted field name up to the next . or [.
func splitField(s string) (name, rest string) {
	i := strings.IndexAny(s, ".[")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// checkPlain rejects the JSONPath constructs that make a path select more than
// one node. Naming the offending character matters: "wildcards are not allowed"
// sends someone hunting, where quoting it does not.
func checkPlain(name, raw string) error {
	for _, bad := range []struct {
		tok, what string
	}{
		{"*", "a wildcard"},
		{"?", "a filter"},
		{"@", "a current-node reference"},
		{",", "a union"},
	} {
		if strings.Contains(name, bad.tok) {
			return fmt.Errorf("%s (%s) is not allowed in a reference path: %q", bad.what, bad.tok, raw)
		}
	}
	return nil
}

// parseBracket reads one [...] segment: an index, or a quoted field name.
func parseBracket(s, raw string) (Segment, string, error) {
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return Segment{}, "", fmt.Errorf("unclosed [ in %q", raw)
	}
	inner, rest := s[1:end], s[end+1:]
	if inner == "" {
		return Segment{}, "", fmt.Errorf("empty [] in %q", raw)
	}

	// A quoted field: ['name'] or ["name"].
	if len(inner) >= 2 {
		if (inner[0] == '\'' && inner[len(inner)-1] == '\'') ||
			(inner[0] == '"' && inner[len(inner)-1] == '"') {
			return Segment{Key: inner[1 : len(inner)-1]}, rest, nil
		}
	}

	if strings.Contains(inner, ":") {
		return Segment{}, "", fmt.Errorf("a slice ([%s]) selects many nodes and is not a reference path: %q", inner, raw)
	}
	if err := checkPlain(inner, raw); err != nil {
		return Segment{}, "", err
	}

	n, err := strconv.Atoi(inner)
	if err != nil {
		return Segment{}, "", fmt.Errorf("expected an array index or a quoted field in [%s]: %q", inner, raw)
	}
	if n < 0 {
		return Segment{}, "", fmt.Errorf("a negative array index (%d) is not a reference path: %q", n, raw)
	}
	return Segment{Index: n, IsIndex: true}, rest, nil
}

// ValidatePath checks a path used where a *path* is legal (InputPath,
// OutputPath). It permits everything ParsePath does.
func ValidatePath(s string) error {
	_, err := ParsePath(s)
	return err
}

// ValidateReferencePath checks a path used where a *reference path* is required
// — ResultPath, ItemsPath, and the ...Path operands. ParsePath already refuses
// the multi-node constructs, so the extra rule here is that the context object
// is not a place a result can be written back to.
func ValidateReferencePath(s string) error {
	p, err := ParsePath(s)
	if err != nil {
		return err
	}
	if p.Root == RootContext {
		return fmt.Errorf("the context object ($$) is read-only and cannot be a target: %q", s)
	}
	if p.Root == RootVariable {
		return fmt.Errorf("a variable ($%s) is written by Assign, not by a path target: %q", p.Variable, s)
	}
	return nil
}

// IsPathExpr reports whether a payload-template value is a path reference —
// the ".$" suffixed key convention, where {"a.$": "$.b"} copies $.b into a.
func IsPathExpr(v string) bool {
	return strings.HasPrefix(v, "$")
}

// isVariableStart reports whether a byte after "$" begins a variable name
// rather than a path segment: a letter or underscore, as AWS's variable
// names are.
func isVariableStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
