// Package expr implements DynamoDB's expression languages — condition, filter,
// and key-condition expressions (one shared grammar), update expressions, and
// projection expressions — with a real lexer and recursive-descent parsers.
// Expressions carry no literals: every value arrives as a :ref resolved
// against ExpressionAttributeValues, and #refs substitute attribute names.
//
// The evaluators run on item.Item values. Unused name/value references are
// detected (ValidationException, like real DynamoDB) via the Used sets.
package expr

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokNameRef  // #name
	tokValueRef // :value
	tokCompare  // = <> < <= > >=
	tokLParen
	tokRParen
	tokComma
	tokDot
	tokLBracket
	tokRBracket
	tokNumber // list index inside [ ]
	tokPlus
	tokMinus
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func errSyntax(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ValidationException", "Invalid expression: "+format, args...)
}

// lex tokenizes an expression.
func lex(src string) ([]token, *awshttp.APIError) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{tokLParen, "(", i})
			i++
		case c == ')':
			out = append(out, token{tokRParen, ")", i})
			i++
		case c == ',':
			out = append(out, token{tokComma, ",", i})
			i++
		case c == '.':
			out = append(out, token{tokDot, ".", i})
			i++
		case c == '[':
			out = append(out, token{tokLBracket, "[", i})
			i++
		case c == ']':
			out = append(out, token{tokRBracket, "]", i})
			i++
		case c == '+':
			out = append(out, token{tokPlus, "+", i})
			i++
		case c == '-':
			out = append(out, token{tokMinus, "-", i})
			i++
		case c == '=':
			out = append(out, token{tokCompare, "=", i})
			i++
		case c == '<':
			switch {
			case strings.HasPrefix(src[i:], "<>"):
				out = append(out, token{tokCompare, "<>", i})
				i += 2
			case strings.HasPrefix(src[i:], "<="):
				out = append(out, token{tokCompare, "<=", i})
				i += 2
			default:
				out = append(out, token{tokCompare, "<", i})
				i++
			}
		case c == '>':
			if strings.HasPrefix(src[i:], ">=") {
				out = append(out, token{tokCompare, ">=", i})
				i += 2
			} else {
				out = append(out, token{tokCompare, ">", i})
				i++
			}
		case c == '#':
			j := i + 1
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			if j == i+1 {
				return nil, errSyntax("dangling # at offset %d", i)
			}
			out = append(out, token{tokNameRef, src[i:j], i})
			i = j
		case c == ':':
			j := i + 1
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			if j == i+1 {
				return nil, errSyntax("dangling : at offset %d", i)
			}
			out = append(out, token{tokValueRef, src[i:j], i})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			out = append(out, token{tokNumber, src[i:j], i})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			out = append(out, token{tokIdent, src[i:j], i})
			i = j
		default:
			return nil, errSyntax("unexpected character %q at offset %d", c, i)
		}
	}
	out = append(out, token{tokEOF, "", len(src)})
	return out, nil
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

// parser is the shared token cursor.
type parser struct {
	toks []token
	pos  int
	env  *Env
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) atEOF() bool { return p.peek().kind == tokEOF }
func (p *parser) backup()     { p.pos-- }

// expect consumes a token of the given kind or errors.
func (p *parser) expect(kind tokKind, what string) (token, *awshttp.APIError) {
	t := p.next()
	if t.kind != kind {
		return token{}, errSyntax("expected %s at offset %d, got %q", what, t.pos, t.text)
	}
	return t, nil
}

// keyword matches a case-insensitive identifier.
func (p *parser) keyword(words ...string) (string, bool) {
	t := p.peek()
	if t.kind != tokIdent {
		return "", false
	}
	for _, w := range words {
		if strings.EqualFold(t.text, w) {
			p.pos++
			return strings.ToUpper(t.text), true
		}
	}
	return "", false
}

func fmtToken(t token) string {
	if t.kind == tokEOF {
		return "end of expression"
	}
	return fmt.Sprintf("%q", t.text)
}
