package logs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Filter patterns, the subset people type into `sam logs --filter` and
// `aws logs tail --filter-pattern`:
//
//	ERROR                    a term (case-sensitive, matched as a word or substring)
//	ERROR timeout            every term must appear
//	"out of memory"          a phrase
//	-DEBUG                   a term that must not appear
//	?ERROR ?WARN             any of the optional terms
//	{ $.level = "error" }    a JSON pattern over a message that parses as JSON
//	{ $.status >= 500 && $.path = "/x" }
//
// Everything else the pattern language does — field-index patterns,
// `%regex%` — answers InvalidParameterException naming the construct.

type matcher func(msg string) bool

func compile(pattern string) (matcher, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return func(string) bool { return true }, nil
	}
	if strings.HasPrefix(pattern, "{") {
		return compileJSON(pattern)
	}
	if strings.HasPrefix(pattern, "%") {
		return nil, fmt.Errorf("regular-expression patterns (%%…%%) are not supported locally; use terms, phrases or a JSON pattern")
	}
	if strings.HasPrefix(pattern, "[") {
		return nil, fmt.Errorf("space-delimited field patterns ([…]) are not supported locally; use terms, phrases or a JSON pattern")
	}
	var must, mustNot, any []string
	for _, tok := range splitTerms(pattern) {
		switch {
		case strings.HasPrefix(tok, "-") && len(tok) > 1:
			mustNot = append(mustNot, tok[1:])
		case strings.HasPrefix(tok, "?") && len(tok) > 1:
			any = append(any, tok[1:])
		default:
			must = append(must, tok)
		}
	}
	return func(msg string) bool {
		for _, t := range must {
			if !strings.Contains(msg, t) {
				return false
			}
		}
		for _, t := range mustNot {
			if strings.Contains(msg, t) {
				return false
			}
		}
		if len(any) > 0 {
			hit := false
			for _, t := range any {
				if strings.Contains(msg, t) {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		return true
	}, nil
}

// splitTerms splits on whitespace, keeping quoted phrases whole.
func splitTerms(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t' || r == '\n'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// compileJSON handles { $.a = "b" && $.n > 1 || $.c != "d" }: comparisons
// on selectors joined by && and ||, && binding tighter, no parentheses.
func compileJSON(pattern string) (matcher, error) {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(pattern, "{"), "}"))
	if body == "" {
		return nil, fmt.Errorf("empty JSON pattern")
	}
	var ors [][]comparison
	for _, disj := range strings.Split(body, "||") {
		var ands []comparison
		for _, conj := range strings.Split(disj, "&&") {
			c, err := parseComparison(strings.TrimSpace(conj))
			if err != nil {
				return nil, err
			}
			ands = append(ands, c)
		}
		ors = append(ors, ands)
	}
	return func(msg string) bool {
		var doc any
		if err := json.Unmarshal([]byte(msg), &doc); err != nil {
			return false
		}
		for _, ands := range ors {
			ok := true
			for _, c := range ands {
				if !c.match(doc) {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}, nil
}

type comparison struct {
	path []string // after "$."
	op   string
	val  any // string, float64 or nil (IS NULL / NOT EXISTS)
	null string
}

func parseComparison(s string) (comparison, error) {
	if !strings.HasPrefix(s, "$.") && s != "$" {
		return comparison{}, fmt.Errorf("JSON pattern term %q must start with $.", s)
	}
	for _, op := range []string{" IS NULL", " NOT EXISTS", " IS TRUE", " IS FALSE"} {
		if strings.HasSuffix(s, op) {
			return comparison{path: splitPath(strings.TrimSuffix(s, op)), null: strings.TrimSpace(op)}, nil
		}
	}
	for _, op := range []string{"!=", ">=", "<=", "=", ">", "<"} {
		if i := strings.Index(s, op); i > 0 {
			left, right := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(op):])
			c := comparison{path: splitPath(left), op: op}
			if strings.HasPrefix(right, `"`) && strings.HasSuffix(right, `"`) && len(right) >= 2 {
				c.val = right[1 : len(right)-1]
			} else if f, err := strconv.ParseFloat(right, 64); err == nil {
				c.val = f
			} else if right == "true" || right == "false" {
				c.val = right == "true"
			} else if right == "null" {
				c.val = nil
			} else {
				return comparison{}, fmt.Errorf("JSON pattern value %q must be a quoted string, a number, true, false or null", right)
			}
			return c, nil
		}
	}
	return comparison{}, fmt.Errorf("JSON pattern term %q has no comparison", s)
}

func splitPath(sel string) []string {
	sel = strings.TrimPrefix(strings.TrimSpace(sel), "$")
	sel = strings.TrimPrefix(sel, ".")
	if sel == "" {
		return nil
	}
	// a.b[0].c → a, b, [0], c
	var parts []string
	for _, p := range strings.Split(sel, ".") {
		for {
			i := strings.IndexByte(p, '[')
			if i < 0 {
				if p != "" {
					parts = append(parts, p)
				}
				break
			}
			if i > 0 {
				parts = append(parts, p[:i])
			}
			j := strings.IndexByte(p, ']')
			if j < i {
				break
			}
			parts = append(parts, p[i:j+1])
			p = p[j+1:]
		}
	}
	return parts
}

func (c comparison) match(doc any) bool {
	cur, found := doc, true
	for _, p := range c.path {
		switch v := cur.(type) {
		case map[string]any:
			cur, found = v[p]
		case []any:
			idx, err := strconv.Atoi(strings.Trim(p, "[]"))
			if err != nil || idx < 0 || idx >= len(v) {
				found = false
			} else {
				cur = v[idx]
			}
		default:
			found = false
		}
		if !found {
			break
		}
	}
	switch c.null {
	case "NOT EXISTS":
		return !found
	case "IS NULL":
		return found && cur == nil
	case "IS TRUE":
		return found && cur == true
	case "IS FALSE":
		return found && cur == false
	}
	if !found {
		return c.op == "!="
	}
	switch want := c.val.(type) {
	case string:
		got, ok := cur.(string)
		if !ok {
			return c.op == "!="
		}
		switch c.op {
		case "=":
			return globMatch(want, got)
		case "!=":
			return !globMatch(want, got)
		}
		return false
	case float64:
		got, ok := cur.(float64)
		if !ok {
			return c.op == "!="
		}
		switch c.op {
		case "=":
			return got == want
		case "!=":
			return got != want
		case ">":
			return got > want
		case ">=":
			return got >= want
		case "<":
			return got < want
		case "<=":
			return got <= want
		}
	case bool:
		got, ok := cur.(bool)
		return ok && ((c.op == "=") == (got == want))
	case nil:
		return (c.op == "=") == (cur == nil)
	}
	return false
}

// globMatch is the * wildcard AWS allows in string comparisons.
func globMatch(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
