package expr

import (
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// Env carries the request's name/value substitutions and tracks which were
// actually referenced, so unused ones can be rejected like real DynamoDB.
type Env struct {
	Names  map[string]string     // "#n" -> attribute name
	Values map[string]item.Value // ":v" -> value
	usedN  map[string]bool
	usedV  map[string]bool
}

// NewEnv builds an evaluation environment.
func NewEnv(names map[string]string, values map[string]item.Value) *Env {
	return &Env{Names: names, Values: values, usedN: map[string]bool{}, usedV: map[string]bool{}}
}

// resolveName maps a #ref to its attribute name.
func (e *Env) resolveName(ref string) (string, *awshttp.APIError) {
	name, ok := e.Names[ref]
	if !ok {
		return "", errSyntax("expression attribute name %s is not defined", ref)
	}
	e.usedN[ref] = true
	return name, nil
}

// resolveValue maps a :ref to its value.
func (e *Env) resolveValue(ref string) (item.Value, *awshttp.APIError) {
	v, ok := e.Values[ref]
	if !ok {
		return item.Value{}, errSyntax("expression attribute value %s is not defined", ref)
	}
	e.usedV[ref] = true
	return v, nil
}

// CheckAllUsed errors if any provided substitution went unreferenced — the
// SDK-observable ValidationException real DynamoDB raises.
func (e *Env) CheckAllUsed() *awshttp.APIError {
	for ref := range e.Names {
		if !e.usedN[ref] {
			return awshttp.Errf(400, "ValidationException",
				"Value provided in ExpressionAttributeNames unused in expressions: keys: {%s}", ref)
		}
	}
	for ref := range e.Values {
		if !e.usedV[ref] {
			return awshttp.Errf(400, "ValidationException",
				"Value provided in ExpressionAttributeValues unused in expressions: keys: {%s}", ref)
		}
	}
	return nil
}

// pathSeg is one document-path step.
type pathSeg struct {
	attr  string // attribute name (empty for index segments)
	index int    // list index when attr == ""
}

// Path is a parsed document path (a.b[0].c).
type Path struct {
	segs []pathSeg
}

// Root returns the top-level attribute name.
func (p Path) Root() string { return p.segs[0].attr }

// String renders the path for error messages.
func (p Path) String() string {
	var b strings.Builder
	for i, s := range p.segs {
		if s.attr == "" {
			b.WriteString("[" + strconv.Itoa(s.index) + "]")
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.attr)
	}
	return b.String()
}

// parsePath parses `segment (('.' segment) | '[' n ']')*`.
func (p *parser) parsePath() (Path, *awshttp.APIError) {
	var out Path
	seg, aerr := p.parsePathHead()
	if aerr != nil {
		return Path{}, aerr
	}
	out.segs = append(out.segs, seg)
	for {
		switch p.peek().kind {
		case tokDot:
			p.next()
			seg, aerr := p.parsePathHead()
			if aerr != nil {
				return Path{}, aerr
			}
			out.segs = append(out.segs, seg)
		case tokLBracket:
			p.next()
			numTok, aerr := p.expect(tokNumber, "a list index")
			if aerr != nil {
				return Path{}, aerr
			}
			if _, aerr := p.expect(tokRBracket, "]"); aerr != nil {
				return Path{}, aerr
			}
			n, _ := strconv.Atoi(numTok.text)
			out.segs = append(out.segs, pathSeg{index: n})
		default:
			return out, nil
		}
	}
}

func (p *parser) parsePathHead() (pathSeg, *awshttp.APIError) {
	t := p.next()
	switch t.kind {
	case tokIdent:
		return pathSeg{attr: t.text}, nil
	case tokNameRef:
		name, aerr := p.env.resolveName(t.text)
		if aerr != nil {
			return pathSeg{}, aerr
		}
		return pathSeg{attr: name}, nil
	}
	return pathSeg{}, errSyntax("expected an attribute name at offset %d, got %s", t.pos, fmtToken(t))
}

// Get resolves the path against an item; ok=false when any step is missing.
func (p Path) Get(it item.Item) (item.Value, bool) {
	if len(p.segs) == 0 {
		return item.Value{}, false
	}
	cur, ok := it[p.segs[0].attr]
	if !ok {
		return item.Value{}, false
	}
	for _, seg := range p.segs[1:] {
		if seg.attr != "" {
			if cur.Type != item.TypeM {
				return item.Value{}, false
			}
			cur, ok = cur.M[seg.attr]
			if !ok {
				return item.Value{}, false
			}
			continue
		}
		if cur.Type != item.TypeL || seg.index < 0 || seg.index >= len(cur.L) {
			return item.Value{}, false
		}
		cur = cur.L[seg.index]
	}
	return cur, true
}

// Set writes a value at the path, creating intermediate maps for missing map
// segments (DynamoDB requires parents to exist EXCEPT the final segment; we
// match that: missing intermediate levels error).
func (p Path) Set(it item.Item, v item.Value) *awshttp.APIError {
	if len(p.segs) == 1 {
		it[p.segs[0].attr] = v
		return nil
	}
	parent, aerr := p.parent(it)
	if aerr != nil {
		return aerr
	}
	last := p.segs[len(p.segs)-1]
	if last.attr != "" {
		if parent.Type != item.TypeM {
			return errSyntax("document path %s traverses a non-map value", p)
		}
		parent.M[last.attr] = v
		return nil
	}
	if parent.Type != item.TypeL {
		return errSyntax("document path %s indexes a non-list value", p)
	}
	if last.index >= len(parent.L) {
		// Appending past the end appends to the list, like DynamoDB.
		parent.L = append(parent.L, v)
		return p.storeParent(it, parent)
	}
	parent.L[last.index] = v
	return p.storeParent(it, parent)
}

// Remove deletes the value at the path (missing paths are a no-op).
func (p Path) Remove(it item.Item) *awshttp.APIError {
	if len(p.segs) == 1 {
		delete(it, p.segs[0].attr)
		return nil
	}
	parent, aerr := p.parent(it)
	if aerr != nil {
		return nil // missing parents make REMOVE a no-op
	}
	last := p.segs[len(p.segs)-1]
	if last.attr != "" {
		if parent.Type == item.TypeM {
			delete(parent.M, last.attr)
		}
		return nil
	}
	if parent.Type == item.TypeL && last.index >= 0 && last.index < len(parent.L) {
		parent.L = append(parent.L[:last.index], parent.L[last.index+1:]...)
		return p.storeParent(it, parent)
	}
	return nil
}

// parent resolves the path up to (not including) the final segment.
func (p Path) parent(it item.Item) (item.Value, *awshttp.APIError) {
	parentPath := Path{segs: p.segs[:len(p.segs)-1]}
	v, ok := parentPath.Get(it)
	if !ok {
		return item.Value{}, errSyntax("document path %s has missing intermediate attributes", p)
	}
	return v, nil
}

// storeParent writes a mutated list parent back (lists are values, not
// references, when reslicing changes the header).
func (p Path) storeParent(it item.Item, parent item.Value) *awshttp.APIError {
	parentPath := Path{segs: p.segs[:len(p.segs)-1]}
	return parentPath.Set(it, parent)
}
