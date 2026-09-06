package asl

import (
	"fmt"
	"strings"
)

// The intrinsic functions are their own mini-language — States.Format('{}',
// $.a) is a call expression with string literals, numbers, paths and nested
// calls, not "a few helper functions on a string". LocalStack maintains a
// separate ANTLR grammar for exactly this; here it is a hand scanner and a
// recursive-descent parser, split scan/parse/eval across three files.

type itokKind int

const (
	tokIdent  itokKind = iota // States.Format
	tokString                 // 'hello'
	tokNumber                 // 42, -1.5
	tokTrue
	tokFalse
	tokNull
	tokPath // $.a.b, $$.Execution.Id, $
	tokLParen
	tokRParen
	tokComma
	tokEOF
)

type itok struct {
	kind itokKind
	text string
}

// scanIntrinsic tokenises one intrinsic expression. Inside a string literal a
// backslash escapes the quote and itself; any other backslash pair is kept
// verbatim, because \{ and \} must survive to States.Format, which is the
// layer that unescapes them.
func scanIntrinsic(src string) ([]itok, error) {
	var toks []itok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(':
			toks = append(toks, itok{tokLParen, "("})
			i++
		case c == ')':
			toks = append(toks, itok{tokRParen, ")"})
			i++
		case c == ',':
			toks = append(toks, itok{tokComma, ","})
			i++
		case c == '\'':
			text, rest, err := scanString(src[i+1:])
			if err != nil {
				return nil, err
			}
			toks = append(toks, itok{tokString, text})
			i = len(src) - len(rest)
		case c == '$':
			j := i
			for j < len(src) && !strings.ContainsRune("(), \t\n", rune(src[j])) {
				j++
			}
			toks = append(toks, itok{tokPath, src[i:j]})
			i = j
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(src) && (src[j] == '.' || (src[j] >= '0' && src[j] <= '9')) {
				j++
			}
			toks = append(toks, itok{tokNumber, src[i:j]})
			i = j
		case isIdentChar(c):
			j := i
			for j < len(src) && isIdentChar(src[j]) {
				j++
			}
			word := src[i:j]
			switch word {
			case "true":
				toks = append(toks, itok{tokTrue, word})
			case "false":
				toks = append(toks, itok{tokFalse, word})
			case "null":
				toks = append(toks, itok{tokNull, word})
			default:
				toks = append(toks, itok{tokIdent, word})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected %q at position %d", c, i)
		}
	}
	return append(toks, itok{tokEOF, ""}), nil
}

func scanString(s string) (text, rest string, err error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			return b.String(), s[i+1:], nil
		case '\\':
			if i+1 >= len(s) {
				return "", "", fmt.Errorf("a string literal ends in a bare backslash")
			}
			next := s[i+1]
			if next == '\'' || next == '\\' {
				b.WriteByte(next)
			} else {
				// Keep \{ and friends for States.Format to interpret.
				b.WriteByte('\\')
				b.WriteByte(next)
			}
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", fmt.Errorf("unterminated string literal")
}

func isIdentChar(c byte) bool {
	return c == '.' || c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
