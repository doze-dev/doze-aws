package asl

import (
	"encoding/json"
	"fmt"
)

// The intrinsic expression tree: a call whose arguments are literals, paths,
// or nested calls.

type intrinsicExpr interface{ isIntrinsic() }

type callExpr struct {
	name string
	args []intrinsicExpr
}

type litExpr struct{ val any }

type pathExpr struct{ path Path }

func (*callExpr) isIntrinsic() {}
func (*litExpr) isIntrinsic()  {}
func (*pathExpr) isIntrinsic() {}

// parseIntrinsic parses one full intrinsic expression, which must be a call.
func parseIntrinsic(src string) (*callExpr, error) {
	toks, err := scanIntrinsic(src)
	if err != nil {
		return nil, err
	}
	p := &intrinsicParser{toks: toks}
	call, err := p.call()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected %q after the call", p.peek().text)
	}
	return call, nil
}

type intrinsicParser struct {
	toks []itok
	pos  int
}

func (p *intrinsicParser) peek() itok { return p.toks[p.pos] }
func (p *intrinsicParser) next() itok { t := p.toks[p.pos]; p.pos++; return t }

func (p *intrinsicParser) call() (*callExpr, error) {
	name := p.next()
	if name.kind != tokIdent {
		return nil, fmt.Errorf("expected a function name, got %q", name.text)
	}
	if open := p.next(); open.kind != tokLParen {
		return nil, fmt.Errorf("expected ( after %s", name.text)
	}
	call := &callExpr{name: name.text}
	if p.peek().kind == tokRParen {
		p.next()
		return call, nil
	}
	for {
		arg, err := p.arg()
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, arg)
		switch t := p.next(); t.kind {
		case tokComma:
		case tokRParen:
			return call, nil
		default:
			return nil, fmt.Errorf("expected , or ) in %s, got %q", call.name, t.text)
		}
	}
}

func (p *intrinsicParser) arg() (intrinsicExpr, error) {
	switch t := p.peek(); t.kind {
	case tokIdent:
		return p.call() // a nested call
	case tokString:
		p.next()
		return &litExpr{val: t.text}, nil
	case tokNumber:
		p.next()
		return &litExpr{val: json.Number(t.text)}, nil
	case tokTrue:
		p.next()
		return &litExpr{val: true}, nil
	case tokFalse:
		p.next()
		return &litExpr{val: false}, nil
	case tokNull:
		p.next()
		return &litExpr{val: nil}, nil
	case tokPath:
		p.next()
		parsed, err := ParsePath(t.text)
		if err != nil {
			return nil, err
		}
		return &pathExpr{path: parsed}, nil
	default:
		return nil, fmt.Errorf("expected an argument, got %q", t.text)
	}
}
