package expr

import (
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// KeyCondition is the restricted condition shape Query accepts:
//
//	pk = :v
//	pk = :v AND sk <op> :v
//	pk = :v AND sk BETWEEN :lo AND :hi
//	pk = :v AND begins_with(sk, :prefix)
type KeyCondition struct {
	PKName  string
	PKValue item.Value

	SKName   string     // empty when no sort-key condition
	SKOp     string     // "=", "<", "<=", ">", ">=", "BETWEEN", "begins_with"
	SKValue  item.Value // operand (or BETWEEN low bound)
	SKValue2 item.Value // BETWEEN high bound
}

// ParseKeyCondition parses and validates a KeyConditionExpression.
func ParseKeyCondition(src string, env *Env) (*KeyCondition, *awshttp.APIError) {
	toks, aerr := lex(src)
	if aerr != nil {
		return nil, aerr
	}
	p := &parser{toks: toks, env: env}
	out := &KeyCondition{}

	first, aerr := p.parseKeyClause()
	if aerr != nil {
		return nil, aerr
	}
	clauses := []keyClause{first}
	if _, ok := p.keyword("AND"); ok {
		second, aerr := p.parseKeyClause()
		if aerr != nil {
			return nil, aerr
		}
		clauses = append(clauses, second)
	}
	if !p.atEOF() {
		return nil, errSyntax("unexpected %s in KeyConditionExpression", fmtToken(p.peek()))
	}

	// Exactly one clause must be the equality on the partition key.
	eqIdx := -1
	for i, c := range clauses {
		if c.op == "=" {
			eqIdx = i
			break
		}
	}
	if eqIdx < 0 {
		return nil, errSyntax("KeyConditionExpression must test the partition key with =")
	}
	pk := clauses[eqIdx]
	out.PKName, out.PKValue = pk.name, pk.v1
	if len(clauses) == 2 {
		sk := clauses[1-eqIdx]
		out.SKName, out.SKOp, out.SKValue, out.SKValue2 = sk.name, sk.op, sk.v1, sk.v2
	}
	return out, nil
}

// keyClause is one `name op value` clause of a key condition.
type keyClause struct {
	name   string
	op     string
	v1, v2 item.Value
}

func (p *parser) parseKeyClause() (keyClause, *awshttp.APIError) {
	// begins_with(sk, :prefix)
	if t := p.peek(); t.kind == tokIdent && strings.EqualFold(t.text, "begins_with") {
		p.next()
		if _, aerr := p.expect(tokLParen, "( after begins_with"); aerr != nil {
			return keyClause{}, aerr
		}
		name, aerr := p.parseKeyName()
		if aerr != nil {
			return keyClause{}, aerr
		}
		if _, aerr := p.expect(tokComma, ", in begins_with"); aerr != nil {
			return keyClause{}, aerr
		}
		v, aerr := p.parseKeyValue()
		if aerr != nil {
			return keyClause{}, aerr
		}
		if _, aerr := p.expect(tokRParen, ")"); aerr != nil {
			return keyClause{}, aerr
		}
		return keyClause{name: name, op: "begins_with", v1: v}, nil
	}

	name, aerr := p.parseKeyName()
	if aerr != nil {
		return keyClause{}, aerr
	}
	if _, ok := p.keyword("BETWEEN"); ok {
		lo, aerr := p.parseKeyValue()
		if aerr != nil {
			return keyClause{}, aerr
		}
		if _, ok := p.keyword("AND"); !ok {
			return keyClause{}, errSyntax("BETWEEN requires AND")
		}
		hi, aerr := p.parseKeyValue()
		if aerr != nil {
			return keyClause{}, aerr
		}
		return keyClause{name: name, op: "BETWEEN", v1: lo, v2: hi}, nil
	}
	opTok := p.next()
	if opTok.kind != tokCompare || opTok.text == "<>" {
		return keyClause{}, errSyntax("key conditions support =, <, <=, >, >=, BETWEEN, begins_with")
	}
	v, aerr := p.parseKeyValue()
	if aerr != nil {
		return keyClause{}, aerr
	}
	return keyClause{name: name, op: opTok.text, v1: v}, nil
}

func (p *parser) parseKeyName() (string, *awshttp.APIError) {
	t := p.next()
	switch t.kind {
	case tokIdent:
		return t.text, nil
	case tokNameRef:
		return p.env.resolveName(t.text)
	}
	return "", errSyntax("expected a key attribute name at offset %d", t.pos)
}

func (p *parser) parseKeyValue() (item.Value, *awshttp.APIError) {
	t, aerr := p.expect(tokValueRef, "a :value reference")
	if aerr != nil {
		return item.Value{}, aerr
	}
	return p.env.resolveValue(t.text)
}

// Projection is a parsed projection expression: the paths to keep.
type Projection struct {
	paths []Path
}

// ParseProjection parses a ProjectionExpression.
func ParseProjection(src string, env *Env) (*Projection, *awshttp.APIError) {
	toks, aerr := lex(src)
	if aerr != nil {
		return nil, aerr
	}
	p := &parser{toks: toks, env: env}
	out := &Projection{}
	for {
		path, aerr := p.parsePath()
		if aerr != nil {
			return nil, aerr
		}
		out.paths = append(out.paths, path)
		if p.peek().kind == tokComma {
			p.next()
			continue
		}
		break
	}
	if !p.atEOF() {
		return nil, errSyntax("unexpected %s in ProjectionExpression", fmtToken(p.peek()))
	}
	return out, nil
}

// Apply projects an item down to the requested paths.
func (pr *Projection) Apply(it item.Item) item.Item {
	out := item.Item{}
	for _, path := range pr.paths {
		v, ok := path.Get(it)
		if !ok {
			continue
		}
		// Rebuild the nested structure along the path.
		graft(out, path.segs, v)
	}
	return out
}

// graft writes v into out at the given path, creating maps/lists as needed.
// Projection paths into lists keep the element (position collapses — matching
// how a local projection is consumed in practice).
func graft(out item.Item, segs []pathSeg, v item.Value) {
	if len(segs) == 1 {
		if segs[0].attr != "" {
			out[segs[0].attr] = v
		}
		return
	}
	head := segs[0]
	if head.attr == "" {
		return // list-index roots cannot occur (paths start with a name)
	}
	rest := segs[1:]
	// Only map nesting is rebuilt; a path through a list keeps the whole
	// element under the list attribute.
	if rest[0].attr == "" {
		cur, ok := out[head.attr]
		if !ok {
			cur = item.Value{Type: item.TypeL}
		}
		cur.L = append(cur.L, v)
		out[head.attr] = cur
		return
	}
	cur, ok := out[head.attr]
	if !ok || cur.Type != item.TypeM {
		cur = item.Value{Type: item.TypeM, M: item.Item{}}
	}
	graft(cur.M, rest, v)
	out[head.attr] = cur
}
