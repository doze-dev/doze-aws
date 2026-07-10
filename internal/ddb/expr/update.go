package expr

import (
	"slices"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// Update is a parsed update expression: SET / REMOVE / ADD / DELETE clauses.
type Update struct {
	sets    []setAction
	removes []Path
	adds    []addAction
	deletes []deleteAction
}

type setAction struct {
	path Path
	expr setExpr
}

// setExpr is the right-hand side of SET: operand, operand + operand,
// operand - operand, list_append(a, b), if_not_exists(path, operand).
type setExpr interface {
	value(it item.Item) (item.Value, *awshttp.APIError)
}

type addAction struct {
	path Path
	ref  operand
}

type deleteAction struct {
	path Path
	ref  operand
}

// ParseUpdate parses an update expression.
func ParseUpdate(src string, env *Env) (*Update, *awshttp.APIError) {
	toks, aerr := lex(src)
	if aerr != nil {
		return nil, aerr
	}
	p := &parser{toks: toks, env: env}
	out := &Update{}
	seen := map[string]bool{}
	for !p.atEOF() {
		kw, ok := p.keyword("SET", "REMOVE", "ADD", "DELETE")
		if !ok {
			return nil, errSyntax("expected SET, REMOVE, ADD, or DELETE at offset %d, got %s",
				p.peek().pos, fmtToken(p.peek()))
		}
		if seen[kw] {
			return nil, errSyntax("the %s clause may appear at most once", kw)
		}
		seen[kw] = true
		var aerr *awshttp.APIError
		switch kw {
		case "SET":
			aerr = p.parseSetClause(out)
		case "REMOVE":
			aerr = p.parseRemoveClause(out)
		case "ADD":
			aerr = p.parseAddClause(out)
		case "DELETE":
			aerr = p.parseDeleteClause(out)
		}
		if aerr != nil {
			return nil, aerr
		}
	}
	if len(out.sets)+len(out.removes)+len(out.adds)+len(out.deletes) == 0 {
		return nil, errSyntax("update expression has no actions")
	}
	// Overlapping paths conflict (a coarse root-level check catches the
	// common SDK-visible case: two actions on one document path).
	paths := map[string]bool{}
	addPath := func(pth Path) *awshttp.APIError {
		key := pth.String()
		if paths[key] {
			return errSyntax("two document paths overlap with each other: %s", key)
		}
		paths[key] = true
		return nil
	}
	for _, a := range out.sets {
		if aerr := addPath(a.path); aerr != nil {
			return nil, aerr
		}
	}
	for _, pth := range out.removes {
		if aerr := addPath(pth); aerr != nil {
			return nil, aerr
		}
	}
	for _, a := range out.adds {
		if aerr := addPath(a.path); aerr != nil {
			return nil, aerr
		}
	}
	for _, a := range out.deletes {
		if aerr := addPath(a.path); aerr != nil {
			return nil, aerr
		}
	}
	return out, nil
}

func (p *parser) parseSetClause(out *Update) *awshttp.APIError {
	for {
		path, aerr := p.parsePath()
		if aerr != nil {
			return aerr
		}
		if t := p.next(); t.kind != tokCompare || t.text != "=" {
			return errSyntax("SET requires = at offset %d", t.pos)
		}
		rhs, aerr := p.parseSetExpr()
		if aerr != nil {
			return aerr
		}
		out.sets = append(out.sets, setAction{path: path, expr: rhs})
		if p.peek().kind == tokComma {
			p.next()
			continue
		}
		return nil
	}
}

// parseSetExpr parses `term (('+'|'-') term)?`.
func (p *parser) parseSetExpr() (setExpr, *awshttp.APIError) {
	left, aerr := p.parseSetTerm()
	if aerr != nil {
		return nil, aerr
	}
	switch p.peek().kind {
	case tokPlus, tokMinus:
		op := p.next()
		right, aerr := p.parseSetTerm()
		if aerr != nil {
			return nil, aerr
		}
		return arithmeticExpr{op: op.text, left: left, right: right}, nil
	}
	return left, nil
}

// parseSetTerm parses operand | list_append(...) | if_not_exists(...).
func (p *parser) parseSetTerm() (setExpr, *awshttp.APIError) {
	if t := p.peek(); t.kind == tokIdent {
		switch strings.ToLower(t.text) {
		case "list_append":
			p.next()
			if _, aerr := p.expect(tokLParen, "( after list_append"); aerr != nil {
				return nil, aerr
			}
			a, aerr := p.parseSetTerm()
			if aerr != nil {
				return nil, aerr
			}
			if _, aerr := p.expect(tokComma, ", in list_append"); aerr != nil {
				return nil, aerr
			}
			b, aerr := p.parseSetTerm()
			if aerr != nil {
				return nil, aerr
			}
			if _, aerr := p.expect(tokRParen, ")"); aerr != nil {
				return nil, aerr
			}
			return listAppendExpr{a: a, b: b}, nil
		case "if_not_exists":
			p.next()
			if _, aerr := p.expect(tokLParen, "( after if_not_exists"); aerr != nil {
				return nil, aerr
			}
			path, aerr := p.parsePath()
			if aerr != nil {
				return nil, aerr
			}
			if _, aerr := p.expect(tokComma, ", in if_not_exists"); aerr != nil {
				return nil, aerr
			}
			def, aerr := p.parseSetTerm()
			if aerr != nil {
				return nil, aerr
			}
			if _, aerr := p.expect(tokRParen, ")"); aerr != nil {
				return nil, aerr
			}
			return ifNotExistsExpr{path: path, def: def}, nil
		}
	}
	o, aerr := p.parseOperand()
	if aerr != nil {
		return nil, aerr
	}
	return operandExpr{o}, nil
}

func (p *parser) parseRemoveClause(out *Update) *awshttp.APIError {
	for {
		path, aerr := p.parsePath()
		if aerr != nil {
			return aerr
		}
		out.removes = append(out.removes, path)
		if p.peek().kind == tokComma {
			p.next()
			continue
		}
		return nil
	}
}

func (p *parser) parseAddClause(out *Update) *awshttp.APIError {
	for {
		path, aerr := p.parsePath()
		if aerr != nil {
			return aerr
		}
		val, aerr := p.parseOperand()
		if aerr != nil {
			return aerr
		}
		out.adds = append(out.adds, addAction{path: path, ref: val})
		if p.peek().kind == tokComma {
			p.next()
			continue
		}
		return nil
	}
}

func (p *parser) parseDeleteClause(out *Update) *awshttp.APIError {
	for {
		path, aerr := p.parsePath()
		if aerr != nil {
			return aerr
		}
		val, aerr := p.parseOperand()
		if aerr != nil {
			return aerr
		}
		out.deletes = append(out.deletes, deleteAction{path: path, ref: val})
		if p.peek().kind == tokComma {
			p.next()
			continue
		}
		return nil
	}
}

// ---- SET expression evaluation ----

type operandExpr struct{ o operand }

func (e operandExpr) value(it item.Item) (item.Value, *awshttp.APIError) {
	v, ok, aerr := e.o.value(it)
	if aerr != nil {
		return item.Value{}, aerr
	}
	if !ok {
		return item.Value{}, errSyntax("the document path %s does not exist", e.o.describe())
	}
	return v, nil
}

type arithmeticExpr struct {
	op          string
	left, right setExpr
}

func (e arithmeticExpr) value(it item.Item) (item.Value, *awshttp.APIError) {
	l, aerr := e.left.value(it)
	if aerr != nil {
		return item.Value{}, aerr
	}
	r, aerr := e.right.value(it)
	if aerr != nil {
		return item.Value{}, aerr
	}
	if l.Type != item.TypeN || r.Type != item.TypeN {
		return item.Value{}, errSyntax("arithmetic requires Number operands, got %s and %s", l.Type, r.Type)
	}
	if e.op == "+" {
		return item.Value{Type: item.TypeN, N: item.Add(l.N, r.N)}, nil
	}
	return item.Value{Type: item.TypeN, N: item.Sub(l.N, r.N)}, nil
}

type listAppendExpr struct{ a, b setExpr }

func (e listAppendExpr) value(it item.Item) (item.Value, *awshttp.APIError) {
	a, aerr := e.a.value(it)
	if aerr != nil {
		return item.Value{}, aerr
	}
	b, aerr := e.b.value(it)
	if aerr != nil {
		return item.Value{}, aerr
	}
	if a.Type != item.TypeL || b.Type != item.TypeL {
		return item.Value{}, errSyntax("list_append requires two lists, got %s and %s", a.Type, b.Type)
	}
	out := make([]item.Value, 0, len(a.L)+len(b.L))
	out = append(out, a.L...)
	out = append(out, b.L...)
	return item.Value{Type: item.TypeL, L: out}, nil
}

type ifNotExistsExpr struct {
	path Path
	def  setExpr
}

func (e ifNotExistsExpr) value(it item.Item) (item.Value, *awshttp.APIError) {
	if v, ok := e.path.Get(it); ok {
		return v, nil
	}
	return e.def.value(it)
}

// Apply runs the update against a copy of the item and returns the new item.
// The caller re-validates key immutability and size afterward.
func (u *Update) Apply(orig item.Item) (item.Item, *awshttp.APIError) {
	it := deepCopy(orig)
	for _, a := range u.sets {
		v, aerr := a.expr.value(it)
		if aerr != nil {
			return nil, aerr
		}
		if aerr := a.path.Set(it, v); aerr != nil {
			return nil, aerr
		}
	}
	for _, pth := range u.removes {
		if aerr := pth.Remove(it); aerr != nil {
			return nil, aerr
		}
	}
	for _, a := range u.adds {
		if aerr := applyAdd(it, a); aerr != nil {
			return nil, aerr
		}
	}
	for _, a := range u.deletes {
		if aerr := applyDelete(it, a); aerr != nil {
			return nil, aerr
		}
	}
	return it, nil
}

// applyAdd implements ADD: numeric addition or set union; creates the
// attribute when absent.
func applyAdd(it item.Item, a addAction) *awshttp.APIError {
	inc, ok, aerr := a.ref.value(it)
	if aerr != nil {
		return aerr
	}
	if !ok {
		return errSyntax("ADD operand is missing")
	}
	cur, exists := a.path.Get(it)
	if !exists {
		switch inc.Type {
		case item.TypeN, item.TypeSS, item.TypeNS, item.TypeBS:
			return a.path.Set(it, inc)
		}
		return errSyntax("ADD requires a Number or set operand, got %s", inc.Type)
	}
	switch {
	case cur.Type == item.TypeN && inc.Type == item.TypeN:
		return a.path.Set(it, item.Value{Type: item.TypeN, N: item.Add(cur.N, inc.N)})
	case cur.Type == item.TypeSS && inc.Type == item.TypeSS:
		merged := append([]string(nil), cur.SS...)
		for _, s := range inc.SS {
			if !containsStr(merged, s) {
				merged = append(merged, s)
			}
		}
		return a.path.Set(it, item.Value{Type: item.TypeSS, SS: merged})
	case cur.Type == item.TypeNS && inc.Type == item.TypeNS:
		merged := append([]item.Decimal(nil), cur.NS...)
		for _, d := range inc.NS {
			dup := false
			for _, e := range merged {
				if item.Compare(d, e) == 0 {
					dup = true
					break
				}
			}
			if !dup {
				merged = append(merged, d)
			}
		}
		return a.path.Set(it, item.Value{Type: item.TypeNS, NS: merged})
	case cur.Type == item.TypeBS && inc.Type == item.TypeBS:
		merged := append([][]byte(nil), cur.BS...)
		for _, b := range inc.BS {
			dup := false
			for _, e := range merged {
				if string(e) == string(b) {
					dup = true
					break
				}
			}
			if !dup {
				merged = append(merged, b)
			}
		}
		return a.path.Set(it, item.Value{Type: item.TypeBS, BS: merged})
	}
	return errSyntax("ADD type mismatch: %s vs %s", cur.Type, inc.Type)
}

// applyDelete implements DELETE: set subtraction. Empty results remove the
// attribute (DynamoDB semantics).
func applyDelete(it item.Item, a deleteAction) *awshttp.APIError {
	sub, ok, aerr := a.ref.value(it)
	if aerr != nil {
		return aerr
	}
	if !ok {
		return errSyntax("DELETE operand is missing")
	}
	cur, exists := a.path.Get(it)
	if !exists {
		return nil
	}
	switch {
	case cur.Type == item.TypeSS && sub.Type == item.TypeSS:
		var kept []string
		for _, s := range cur.SS {
			if !containsStr(sub.SS, s) {
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			return a.path.Remove(it)
		}
		return a.path.Set(it, item.Value{Type: item.TypeSS, SS: kept})
	case cur.Type == item.TypeNS && sub.Type == item.TypeNS:
		var kept []item.Decimal
		for _, d := range cur.NS {
			drop := false
			for _, s := range sub.NS {
				if item.Compare(d, s) == 0 {
					drop = true
					break
				}
			}
			if !drop {
				kept = append(kept, d)
			}
		}
		if len(kept) == 0 {
			return a.path.Remove(it)
		}
		return a.path.Set(it, item.Value{Type: item.TypeNS, NS: kept})
	case cur.Type == item.TypeBS && sub.Type == item.TypeBS:
		var kept [][]byte
		for _, b := range cur.BS {
			drop := false
			for _, s := range sub.BS {
				if string(b) == string(s) {
					drop = true
					break
				}
			}
			if !drop {
				kept = append(kept, b)
			}
		}
		if len(kept) == 0 {
			return a.path.Remove(it)
		}
		return a.path.Set(it, item.Value{Type: item.TypeBS, BS: kept})
	}
	return errSyntax("DELETE requires matching set types, got %s and %s", cur.Type, sub.Type)
}

func containsStr(list []string, s string) bool {
	return slices.Contains(list, s)
}

// deepCopy clones an item so updates never alias stored state.
func deepCopy(it item.Item) item.Item {
	out := make(item.Item, len(it))
	for k, v := range it {
		out[k] = copyValue(v)
	}
	return out
}

func copyValue(v item.Value) item.Value {
	switch v.Type {
	case item.TypeM:
		m := make(map[string]item.Value, len(v.M))
		for k, mv := range v.M {
			m[k] = copyValue(mv)
		}
		v.M = m
	case item.TypeL:
		l := make([]item.Value, len(v.L))
		for i, lv := range v.L {
			l[i] = copyValue(lv)
		}
		v.L = l
	case item.TypeB:
		v.B = append([]byte(nil), v.B...)
	case item.TypeSS:
		v.SS = append([]string(nil), v.SS...)
	case item.TypeNS:
		v.NS = append([]item.Decimal(nil), v.NS...)
	case item.TypeBS:
		bs := make([][]byte, len(v.BS))
		for i, b := range v.BS {
			bs[i] = append([]byte(nil), b...)
		}
		v.BS = bs
	}
	return v
}
