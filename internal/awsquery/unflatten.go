package awsquery

// Unflatten rebuilds the nested value a Query request describes.
//
// # Why this is not modelcheck.FromQuery
//
// FromQuery does the same walk for a different purpose, and it deliberately
// keeps only ONE element of every list — "a constraint applies to every
// element, so checking the first is checking the rule". That is sound for
// validation and lossy for anything else: a PutMetricData carrying two
// dimensions arrives at a handler with one, and the surviving element is a
// mix of both, because each `member.N` was written over the same slot.
//
// A handler needs every element. So this is the faithful version, and the two
// live apart rather than one growing a flag: a validator that walks a
// thousand-element list to re-check a rule it already checked is a different
// trade from a decoder that must not lose data.
//
// # The shape
//
// AWS flattens a list as `prefix.member.N.field` and a map as
// `prefix.entry.N.key` / `.value`, and structures simply nest with dots. The
// marker word is what separates a list of {Name,Value} structures from a map
// with the same spelling, so it is kept rather than inferred from shape —
// CloudWatch's Dimensions is a list that looks exactly like SNS's Attributes.
//
// Values stay strings. Everything in a form is a string; a caller that knows
// the model coerces.

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Unflatten converts flattened Query parameters into nested maps and lists.
// Keys that are not part of the input shape (Action, Version, and the
// signature parameters) are carried through as plain members; a caller reads
// only what it asks for.
func Unflatten(vals url.Values) map[string]any {
	root := qnode{kind: qMap, members: map[string]*qnode{}}
	for key, vs := range vals {
		if len(vs) == 0 {
			continue
		}
		root.insert(strings.Split(key, "."), vs[0])
	}
	out, _ := root.value().(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

type qkind int

const (
	qLeaf qkind = iota
	qMap
	qList  // built from `.member.N`
	qEntry // built from `.entry.N`, collapses to a map
)

// qnode is one position in the tree while it is being built. Lists are held as
// index→qnode so the elements can be sorted at the end regardless of the order
// the form presented them in.
type qnode struct {
	kind    qkind
	leaf    string
	members map[string]*qnode
	items   map[int]*qnode
}

func newQNode(k qkind) *qnode {
	n := &qnode{kind: k}
	switch k {
	case qMap:
		n.members = map[string]*qnode{}
	case qList, qEntry:
		n.items = map[int]*qnode{}
	}
	return n
}

// insert walks one flattened key into the tree, creating containers as it
// goes. It reads a segment at a time and looks ahead for the marker that says
// what the current member contains.
func (n *qnode) insert(segs []string, val string) {
	if len(segs) == 0 {
		n.kind, n.leaf = qLeaf, val
		return
	}
	name := segs[0]
	rest := segs[1:]

	// `name.member.N...` or `name.entry.N...` — the member is a container.
	if len(rest) >= 2 && (rest[0] == "member" || rest[0] == "entry") {
		if idx, err := strconv.Atoi(rest[1]); err == nil && idx >= 1 {
			k := qList
			if rest[0] == "entry" {
				k = qEntry
			}
			child := n.child(name, k)
			// A container's kind is settled by the first marker seen; a form
			// that spells one member both ways is malformed either way.
			if child.items == nil {
				child.items = map[int]*qnode{}
			}
			elem, ok := child.items[idx]
			if !ok {
				elem = newQNode(qMap)
				child.items[idx] = elem
			}
			elem.insert(rest[2:], val)
			return
		}
	}
	// `name.N...` — the memberless list spelling some services use.
	if len(rest) >= 1 {
		if idx, err := strconv.Atoi(rest[0]); err == nil && idx >= 1 {
			child := n.child(name, qList)
			if child.items == nil {
				child.items = map[int]*qnode{}
			}
			elem, ok := child.items[idx]
			if !ok {
				elem = newQNode(qMap)
				child.items[idx] = elem
			}
			elem.insert(rest[1:], val)
			return
		}
	}
	if len(rest) == 0 {
		n.child(name, qLeaf).insert(nil, val)
		return
	}
	n.child(name, qMap).insert(rest, val)
}

func (n *qnode) child(name string, k qkind) *qnode {
	if n.members == nil {
		n.members = map[string]*qnode{}
		n.kind = qMap
	}
	c, ok := n.members[name]
	if !ok {
		c = newQNode(k)
		n.members[name] = c
		return c
	}
	// A member first seen as a leaf and later as a container (or the reverse)
	// keeps the container: the container carries strictly more.
	if c.kind == qLeaf && k != qLeaf {
		grown := newQNode(k)
		n.members[name] = grown
		return grown
	}
	return c
}

// value renders the built tree as plain Go values.
func (n *qnode) value() any {
	switch n.kind {
	case qLeaf:
		return n.leaf
	case qMap:
		out := make(map[string]any, len(n.members))
		for k, c := range n.members {
			out[k] = c.value()
		}
		return out
	case qList, qEntry:
		idxs := make([]int, 0, len(n.items))
		for i := range n.items {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		if n.kind == qEntry {
			// A map arrives as numbered key/value pairs; collapse it back.
			out := map[string]any{}
			for _, i := range idxs {
				el, ok := n.items[i].value().(map[string]any)
				if !ok {
					continue
				}
				k, v := entryPair(el)
				if k != "" {
					out[k] = v
				}
			}
			return out
		}
		// A scalar list member — TransitiveTagKeys.member.1 rather than
		// Tags.member.1.Key — arrives as an element whose insert had no
		// remaining segments, so it is already a leaf and renders as one.
		out := make([]any, 0, len(idxs))
		for _, i := range idxs {
			out = append(out, n.items[i].value())
		}
		return out
	}
	return nil
}

// entryPair reads the two spellings a Query map uses for its pairs.
func entryPair(el map[string]any) (string, any) {
	for _, pair := range [][2]string{{"Name", "Value"}, {"key", "value"}, {"Key", "Value"}} {
		k, hasK := el[pair[0]].(string)
		v, hasV := el[pair[1]]
		if hasK && hasV {
			return k, v
		}
	}
	return "", nil
}
