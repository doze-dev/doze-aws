package dynamodb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

type stmtKind int

const (
	stSelect stmtKind = iota
	stInsert
	stUpdate
	stDelete
)

// partiqlStmt is a parsed statement.
type partiqlStmt struct {
	kind    stmtKind
	table   string
	where   assignments // WHERE equality conditions (attr = value)
	set     assignments // UPDATE SET assignments
	values  assignments // INSERT VALUE map entries
	columns []string    // SELECT projection list ("*" or empty means all)
}

// assignment is one attr = value pair (value is a raw token: "?", a quoted
// string, a number, or a boolean/null keyword).
type assignment struct {
	attr  string
	value string
}

type assignments []assignment

func (a assignments) attrNames() map[string]bool {
	out := map[string]bool{}
	for _, x := range a {
		out[x.attr] = true
	}
	return out
}

// attributeMap binds each assignment's value and returns an AttributeValue map
// (the wire shape for Item/Key).
func (a assignments) attributeMap(b *paramBinder) (map[string]json.RawMessage, *awshttp.APIError) {
	out := map[string]json.RawMessage{}
	for _, x := range a {
		av, aerr := b.value(x.value)
		if aerr != nil {
			return nil, aerr
		}
		out[x.attr] = av
	}
	return out, nil
}

// updateExpr builds a DynamoDB UpdateExpression (SET ...) plus its name/value maps.
func (a assignments) updateExpr(b *paramBinder) (names map[string]string, vals map[string]json.RawMessage, expr string, aerr *awshttp.APIError) {
	names = map[string]string{}
	vals = map[string]json.RawMessage{}
	var sets []string
	for i, x := range a {
		n := fmt.Sprintf("#s%d", i)
		v := fmt.Sprintf(":s%d", i)
		names[n] = x.attr
		av, e := b.value(x.value)
		if e != nil {
			return nil, nil, "", e
		}
		vals[v] = av
		sets = append(sets, n+" = "+v)
	}
	return names, vals, "SET " + strings.Join(sets, ", "), nil
}

// paramBinder resolves '?' tokens against the positional Parameters, and literal
// tokens into AttributeValues.
type paramBinder struct {
	params []json.RawMessage
	next   int
}

func (b *paramBinder) value(token string) (json.RawMessage, *awshttp.APIError) {
	if token == "?" {
		if b.next >= len(b.params) {
			return nil, awshttp.Errf(400, "ValidationException", "not enough parameters for the statement")
		}
		p := b.params[b.next]
		b.next++
		return p, nil
	}
	// Literal: 'string', number, true/false/null.
	if len(token) >= 2 && token[0] == '\'' && token[len(token)-1] == '\'' {
		s := strings.ReplaceAll(token[1:len(token)-1], "''", "'")
		raw, _ := json.Marshal(map[string]string{"S": s})
		return raw, nil
	}
	switch strings.ToLower(token) {
	case "true", "false":
		raw, _ := json.Marshal(map[string]bool{"BOOL": token == "true"})
		return raw, nil
	case "null":
		raw, _ := json.Marshal(map[string]bool{"NULL": true})
		return raw, nil
	}
	if _, err := strconv.ParseFloat(token, 64); err == nil {
		raw, _ := json.Marshal(map[string]string{"N": token})
		return raw, nil
	}
	return nil, awshttp.Errf(400, "ValidationException", "unrecognized literal %q", token)
}

// parsePartiQL parses a single supported statement.
func parsePartiQL(statement string) (*partiqlStmt, *awshttp.APIError) {
	toks := tokenizePartiQL(statement)
	if len(toks) == 0 {
		return nil, awshttp.Errf(400, "ValidationException", "empty statement")
	}
	switch strings.ToUpper(toks[0]) {
	case "SELECT":
		return parseSelect(toks)
	case "INSERT":
		return parseInsert(toks)
	case "UPDATE":
		return parseUpdate(toks)
	case "DELETE":
		return parseDelete(toks)
	}
	return nil, awshttp.Errf(400, "ValidationException", "unsupported statement keyword %q", toks[0])
}

func parseSelect(toks []string) (*partiqlStmt, *awshttp.APIError) {
	// SELECT <proj> FROM <table> [WHERE ...]
	from := indexOfKW(toks, "FROM")
	if from < 0 || from+1 >= len(toks) {
		return nil, awshttp.Errf(400, "ValidationException", "SELECT requires FROM <table>")
	}
	st := &partiqlStmt{kind: stSelect, table: unquoteIdent(toks[from+1])}
	// Capture the projection list (tokens between SELECT and FROM); "*" or an
	// empty list means all attributes.
	for _, tk := range toks[1:from] {
		if tk == "," {
			continue
		}
		if tk == "*" {
			st.columns = nil
			break
		}
		st.columns = append(st.columns, unquoteIdent(tk))
	}
	if where := indexOfKW(toks, "WHERE"); where >= 0 {
		conds, aerr := parseConditions(toks[where+1:])
		if aerr != nil {
			return nil, aerr
		}
		st.where = conds
	}
	return st, nil
}

func parseDelete(toks []string) (*partiqlStmt, *awshttp.APIError) {
	from := indexOfKW(toks, "FROM")
	if from < 0 || from+1 >= len(toks) {
		return nil, awshttp.Errf(400, "ValidationException", "DELETE requires FROM <table>")
	}
	st := &partiqlStmt{kind: stDelete, table: unquoteIdent(toks[from+1])}
	where := indexOfKW(toks, "WHERE")
	if where < 0 {
		return nil, awshttp.Errf(400, "ValidationException", "DELETE requires a WHERE key clause")
	}
	conds, aerr := parseConditions(toks[where+1:])
	if aerr != nil {
		return nil, aerr
	}
	st.where = conds
	return st, nil
}

func parseUpdate(toks []string) (*partiqlStmt, *awshttp.APIError) {
	// UPDATE <table> SET a=v[, ...] WHERE ...
	if len(toks) < 2 {
		return nil, awshttp.Errf(400, "ValidationException", "UPDATE requires a table")
	}
	st := &partiqlStmt{kind: stUpdate, table: unquoteIdent(toks[1])}
	set := indexOfKW(toks, "SET")
	where := indexOfKW(toks, "WHERE")
	if set < 0 || where < 0 || where < set {
		return nil, awshttp.Errf(400, "ValidationException", "UPDATE requires SET ... WHERE ...")
	}
	setAssigns, aerr := parseAssignments(toks[set+1 : where])
	if aerr != nil {
		return nil, aerr
	}
	conds, aerr := parseConditions(toks[where+1:])
	if aerr != nil {
		return nil, aerr
	}
	st.set = setAssigns
	st.where = conds
	return st, nil
}

func parseInsert(toks []string) (*partiqlStmt, *awshttp.APIError) {
	// INSERT INTO <table> VALUE { 'k': v, ... }
	into := indexOfKW(toks, "INTO")
	value := indexOfKW(toks, "VALUE")
	if into < 0 || into+1 >= len(toks) || value < 0 {
		return nil, awshttp.Errf(400, "ValidationException", "INSERT requires INTO <table> VALUE {...}")
	}
	st := &partiqlStmt{kind: stInsert, table: unquoteIdent(toks[into+1])}
	entries, aerr := parseValueMap(toks[value+1:])
	if aerr != nil {
		return nil, aerr
	}
	st.values = entries
	return st, nil
}

// parseConditions parses `attr = value [AND attr = value]...` (equality only).
func parseConditions(toks []string) (assignments, *awshttp.APIError) {
	var out assignments
	i := 0
	for i < len(toks) {
		if i+2 >= len(toks) || toks[i+1] != "=" {
			return nil, awshttp.Errf(400, "ValidationException", "only `attr = value` equality conditions are supported")
		}
		out = append(out, assignment{attr: unquoteIdent(toks[i]), value: toks[i+2]})
		i += 3
		if i < len(toks) {
			if strings.ToUpper(toks[i]) != "AND" {
				return nil, awshttp.Errf(400, "ValidationException", "conditions must be joined with AND")
			}
			i++
		}
	}
	return out, nil
}

// parseAssignments parses `attr = value, attr = value` (SET clause).
func parseAssignments(toks []string) (assignments, *awshttp.APIError) {
	var out assignments
	i := 0
	for i < len(toks) {
		if i+2 >= len(toks) || toks[i+1] != "=" {
			return nil, awshttp.Errf(400, "ValidationException", "SET requires `attr = value`")
		}
		out = append(out, assignment{attr: unquoteIdent(toks[i]), value: toks[i+2]})
		i += 3
		if i < len(toks) && toks[i] == "," {
			i++
		}
	}
	return out, nil
}

// parseValueMap parses `{ 'k': v, 'k': v }` into assignments.
func parseValueMap(toks []string) (assignments, *awshttp.APIError) {
	if len(toks) == 0 || toks[0] != "{" || toks[len(toks)-1] != "}" {
		return nil, awshttp.Errf(400, "ValidationException", "VALUE must be a { ... } map")
	}
	inner := toks[1 : len(toks)-1]
	var out assignments
	i := 0
	for i < len(inner) {
		if i+2 >= len(inner) || inner[i+1] != ":" {
			return nil, awshttp.Errf(400, "ValidationException", "VALUE entries must be `'key': value`")
		}
		out = append(out, assignment{attr: unquoteIdent(inner[i]), value: inner[i+2]})
		i += 3
		if i < len(inner) && inner[i] == "," {
			i++
		}
	}
	return out, nil
}

func indexOfKW(toks []string, kw string) int {
	for i, t := range toks {
		if strings.ToUpper(t) == kw {
			return i
		}
	}
	return -1
}

// unquoteIdent strips surrounding double quotes or single quotes from an
// identifier/key token.
func unquoteIdent(t string) string {
	if len(t) >= 2 && (t[0] == '"' || t[0] == '\'') && t[len(t)-1] == t[0] {
		return t[1 : len(t)-1]
	}
	return t
}

// tokenizePartiQL splits a statement into tokens, keeping quoted strings/idents
// intact and treating punctuation ({ } : , = ) as standalone tokens.
func tokenizePartiQL(s string) []string {
	var toks []string
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'' || c == '"':
			// Quoted string/identifier; '' inside a single-quoted string escapes a quote.
			q := c
			j := i + 1
			var sb strings.Builder
			sb.WriteByte(q)
			for j < len(s) {
				if s[j] == q {
					if q == '\'' && j+1 < len(s) && s[j+1] == '\'' {
						sb.WriteByte('\'')
						sb.WriteByte('\'')
						j += 2
						continue
					}
					sb.WriteByte(q)
					j++
					break
				}
				sb.WriteByte(s[j])
				j++
			}
			toks = append(toks, sb.String())
			i = j
		case c == '{' || c == '}' || c == ':' || c == ',' || c == '=' || c == '?' || c == '(' || c == ')':
			toks = append(toks, string(c))
			i++
		default:
			j := i
			for j < len(s) && !isDelim(s[j]) {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks
}

func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '{', '}', ':', ',', '=', '?', '(', ')', '\'', '"':
		return true
	}
	return false
}
