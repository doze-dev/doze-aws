package expr

import (
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// Cond is a parsed condition/filter/key-condition expression.
type Cond struct {
	root condNode
	src  string
}

type condNode interface {
	eval(it item.Item) (bool, *awshttp.APIError)
}

// ParseCondition parses a condition or filter expression against env.
func ParseCondition(src string, env *Env) (*Cond, *awshttp.APIError) {
	toks, aerr := lex(src)
	if aerr != nil {
		return nil, aerr
	}
	p := &parser{toks: toks, env: env}
	node, aerr := p.parseOr()
	if aerr != nil {
		return nil, aerr
	}
	if !p.atEOF() {
		return nil, errSyntax("unexpected %s after the expression", fmtToken(p.peek()))
	}
	return &Cond{root: node, src: src}, nil
}

// Eval evaluates the condition against an item.
func (c *Cond) Eval(it item.Item) (bool, *awshttp.APIError) {
	return c.root.eval(it)
}

// ---- grammar: OR > AND > NOT > primary ----

func (p *parser) parseOr() (condNode, *awshttp.APIError) {
	left, aerr := p.parseAnd()
	if aerr != nil {
		return nil, aerr
	}
	for {
		if _, ok := p.keyword("OR"); !ok {
			return left, nil
		}
		right, aerr := p.parseAnd()
		if aerr != nil {
			return nil, aerr
		}
		left = &orNode{left, right}
	}
}

func (p *parser) parseAnd() (condNode, *awshttp.APIError) {
	left, aerr := p.parseNot()
	if aerr != nil {
		return nil, aerr
	}
	for {
		if _, ok := p.keyword("AND"); !ok {
			return left, nil
		}
		right, aerr := p.parseNot()
		if aerr != nil {
			return nil, aerr
		}
		left = &andNode{left, right}
	}
}

func (p *parser) parseNot() (condNode, *awshttp.APIError) {
	if _, ok := p.keyword("NOT"); ok {
		inner, aerr := p.parseNot()
		if aerr != nil {
			return nil, aerr
		}
		return &notNode{inner}, nil
	}
	return p.parsePrimaryCond()
}

// parsePrimaryCond handles parenthesized groups, boolean functions, and
// operand-led comparisons/BETWEEN/IN.
func (p *parser) parsePrimaryCond() (condNode, *awshttp.APIError) {
	// Parenthesized condition — but "(", could also start... conditions only.
	if p.peek().kind == tokLParen {
		p.next()
		inner, aerr := p.parseOr()
		if aerr != nil {
			return nil, aerr
		}
		if _, aerr := p.expect(tokRParen, ")"); aerr != nil {
			return nil, aerr
		}
		return &groupNode{inner}, nil
	}

	// Boolean functions.
	if t := p.peek(); t.kind == tokIdent {
		switch strings.ToLower(t.text) {
		case "attribute_exists", "attribute_not_exists", "attribute_type", "begins_with", "contains":
			return p.parseBoolFunc()
		}
	}

	// Operand-led: comparison, BETWEEN, IN.
	left, aerr := p.parseOperand()
	if aerr != nil {
		return nil, aerr
	}
	if t := p.peek(); t.kind == tokCompare {
		p.next()
		right, aerr := p.parseOperand()
		if aerr != nil {
			return nil, aerr
		}
		return &cmpNode{op: t.text, left: left, right: right}, nil
	}
	if _, ok := p.keyword("BETWEEN"); ok {
		lo, aerr := p.parseOperand()
		if aerr != nil {
			return nil, aerr
		}
		if _, ok := p.keyword("AND"); !ok {
			return nil, errSyntax("BETWEEN requires AND")
		}
		hi, aerr := p.parseOperand()
		if aerr != nil {
			return nil, aerr
		}
		return &betweenNode{val: left, lo: lo, hi: hi}, nil
	}
	if _, ok := p.keyword("IN"); ok {
		if _, aerr := p.expect(tokLParen, "( after IN"); aerr != nil {
			return nil, aerr
		}
		var opts []operand
		for {
			o, aerr := p.parseOperand()
			if aerr != nil {
				return nil, aerr
			}
			opts = append(opts, o)
			if p.peek().kind == tokComma {
				p.next()
				continue
			}
			break
		}
		if _, aerr := p.expect(tokRParen, ")"); aerr != nil {
			return nil, aerr
		}
		return &inNode{val: left, opts: opts}, nil
	}
	return nil, errSyntax("expected a comparator, BETWEEN, or IN at offset %d", p.peek().pos)
}

func (p *parser) parseBoolFunc() (condNode, *awshttp.APIError) {
	name := strings.ToLower(p.next().text)
	if _, aerr := p.expect(tokLParen, "( after "+name); aerr != nil {
		return nil, aerr
	}
	fn := &funcNode{name: name}
	// First argument is always a path for these functions.
	path, aerr := p.parsePath()
	if aerr != nil {
		return nil, aerr
	}
	fn.path = path
	if name != "attribute_exists" && name != "attribute_not_exists" {
		if _, aerr := p.expect(tokComma, ", between arguments"); aerr != nil {
			return nil, aerr
		}
		arg, aerr := p.parseOperand()
		if aerr != nil {
			return nil, aerr
		}
		fn.arg = arg
	}
	if _, aerr := p.expect(tokRParen, ")"); aerr != nil {
		return nil, aerr
	}
	return fn, nil
}

// ---- operands ----

// operand yields a value (or absence) from an item.
type operand interface {
	value(it item.Item) (item.Value, bool, *awshttp.APIError)
	describe() string
}

type pathOperand struct{ path Path }

func (o pathOperand) value(it item.Item) (item.Value, bool, *awshttp.APIError) {
	v, ok := o.path.Get(it)
	return v, ok, nil
}
func (o pathOperand) describe() string { return o.path.String() }

type valueOperand struct {
	ref string
	v   item.Value
}

func (o valueOperand) value(item.Item) (item.Value, bool, *awshttp.APIError) {
	return o.v, true, nil
}
func (o valueOperand) describe() string { return o.ref }

// sizeOperand is size(path).
type sizeOperand struct{ path Path }

func (o sizeOperand) value(it item.Item) (item.Value, bool, *awshttp.APIError) {
	v, ok := o.path.Get(it)
	if !ok {
		return item.Value{}, false, nil
	}
	n := 0
	switch v.Type {
	case item.TypeS:
		n = len(v.S)
	case item.TypeB:
		n = len(v.B)
	case item.TypeL:
		n = len(v.L)
	case item.TypeM:
		n = len(v.M)
	case item.TypeSS:
		n = len(v.SS)
	case item.TypeNS:
		n = len(v.NS)
	case item.TypeBS:
		n = len(v.BS)
	default:
		return item.Value{}, false, errSyntax("size() is not defined for type %s", v.Type)
	}
	d, _ := item.ParseDecimal(itoa(n))
	return item.Value{Type: item.TypeN, N: d}, true, nil
}
func (o sizeOperand) describe() string { return "size(" + o.path.String() + ")" }

func (p *parser) parseOperand() (operand, *awshttp.APIError) {
	t := p.peek()
	switch t.kind {
	case tokValueRef:
		p.next()
		v, aerr := p.env.resolveValue(t.text)
		if aerr != nil {
			return nil, aerr
		}
		return valueOperand{ref: t.text, v: v}, nil
	case tokIdent:
		if strings.EqualFold(t.text, "size") {
			p.next()
			if p.peek().kind == tokLParen {
				p.next()
				path, aerr := p.parsePath()
				if aerr != nil {
					return nil, aerr
				}
				if _, aerr := p.expect(tokRParen, ")"); aerr != nil {
					return nil, aerr
				}
				return sizeOperand{path: path}, nil
			}
			p.backup() // plain attribute actually named "size"
		}
		fallthrough
	case tokNameRef:
		path, aerr := p.parsePath()
		if aerr != nil {
			return nil, aerr
		}
		return pathOperand{path: path}, nil
	}
	return nil, errSyntax("expected an operand at offset %d, got %s", t.pos, fmtToken(t))
}

// ---- evaluation nodes ----

type andNode struct{ l, r condNode }

func (n *andNode) eval(it item.Item) (bool, *awshttp.APIError) {
	l, aerr := n.l.eval(it)
	if aerr != nil || !l {
		return false, aerr
	}
	return n.r.eval(it)
}

type orNode struct{ l, r condNode }

func (n *orNode) eval(it item.Item) (bool, *awshttp.APIError) {
	l, aerr := n.l.eval(it)
	if aerr != nil {
		return false, aerr
	}
	if l {
		return true, nil
	}
	return n.r.eval(it)
}

type notNode struct{ inner condNode }

func (n *notNode) eval(it item.Item) (bool, *awshttp.APIError) {
	v, aerr := n.inner.eval(it)
	return !v, aerr
}

type groupNode struct{ inner condNode }

func (n *groupNode) eval(it item.Item) (bool, *awshttp.APIError) { return n.inner.eval(it) }

type cmpNode struct {
	op          string
	left, right operand
}

func (n *cmpNode) eval(it item.Item) (bool, *awshttp.APIError) {
	l, lok, aerr := n.left.value(it)
	if aerr != nil {
		return false, aerr
	}
	r, rok, aerr := n.right.value(it)
	if aerr != nil {
		return false, aerr
	}
	if !lok || !rok {
		return false, nil // absent operands never match
	}
	switch n.op {
	case "=":
		return item.Equal(l, r), nil
	case "<>":
		return !item.Equal(l, r), nil
	}
	// Ordered comparison: same type, S/N/B only.
	if l.Type != r.Type {
		return false, nil
	}
	var cmp int
	switch l.Type {
	case item.TypeS:
		cmp = strings.Compare(l.S, r.S)
	case item.TypeN:
		cmp = item.Compare(l.N, r.N)
	case item.TypeB:
		cmp = strings.Compare(string(l.B), string(r.B))
	default:
		return false, nil
	}
	switch n.op {
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case ">=":
		return cmp >= 0, nil
	}
	return false, errSyntax("unknown comparator %q", n.op)
}

type betweenNode struct{ val, lo, hi operand }

func (n *betweenNode) eval(it item.Item) (bool, *awshttp.APIError) {
	ge := &cmpNode{op: ">=", left: n.val, right: n.lo}
	le := &cmpNode{op: "<=", left: n.val, right: n.hi}
	a, aerr := ge.eval(it)
	if aerr != nil || !a {
		return false, aerr
	}
	return le.eval(it)
}

type inNode struct {
	val  operand
	opts []operand
}

func (n *inNode) eval(it item.Item) (bool, *awshttp.APIError) {
	v, ok, aerr := n.val.value(it)
	if aerr != nil || !ok {
		return false, aerr
	}
	for _, o := range n.opts {
		ov, ook, aerr := o.value(it)
		if aerr != nil {
			return false, aerr
		}
		if ook && item.Equal(v, ov) {
			return true, nil
		}
	}
	return false, nil
}

type funcNode struct {
	name string
	path Path
	arg  operand
}

func (n *funcNode) eval(it item.Item) (bool, *awshttp.APIError) {
	v, exists := n.path.Get(it)
	switch n.name {
	case "attribute_exists":
		return exists, nil
	case "attribute_not_exists":
		return !exists, nil
	case "attribute_type":
		if !exists {
			return false, nil
		}
		want, _, aerr := n.arg.value(it)
		if aerr != nil {
			return false, aerr
		}
		if want.Type != item.TypeS {
			return false, errSyntax("attribute_type expects a string type name")
		}
		return string(v.Type) == want.S, nil
	case "begins_with":
		if !exists {
			return false, nil
		}
		prefix, _, aerr := n.arg.value(it)
		if aerr != nil {
			return false, aerr
		}
		switch {
		case v.Type == item.TypeS && prefix.Type == item.TypeS:
			return strings.HasPrefix(v.S, prefix.S), nil
		case v.Type == item.TypeB && prefix.Type == item.TypeB:
			return strings.HasPrefix(string(v.B), string(prefix.B)), nil
		}
		return false, nil
	case "contains":
		if !exists {
			return false, nil
		}
		needle, _, aerr := n.arg.value(it)
		if aerr != nil {
			return false, aerr
		}
		switch v.Type {
		case item.TypeS:
			return needle.Type == item.TypeS && strings.Contains(v.S, needle.S), nil
		case item.TypeSS:
			for _, s := range v.SS {
				if needle.Type == item.TypeS && s == needle.S {
					return true, nil
				}
			}
		case item.TypeNS:
			for _, d := range v.NS {
				if needle.Type == item.TypeN && item.Compare(d, needle.N) == 0 {
					return true, nil
				}
			}
		case item.TypeBS:
			for _, b := range v.BS {
				if needle.Type == item.TypeB && string(b) == string(needle.B) {
					return true, nil
				}
			}
		case item.TypeL:
			for _, lv := range v.L {
				if item.Equal(lv, needle) {
					return true, nil
				}
			}
		}
		return false, nil
	}
	return false, errSyntax("unknown function %q", n.name)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
