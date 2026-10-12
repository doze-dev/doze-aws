package modelcheck

// The Query protocol: a flattened form, rebuilt into the shape the constraint
// paths describe, so one walker serves both protocol families.

import (
	"slices"
	"strconv"
	"strings"
)

// FromQuery un-flattens Query-protocol form values into the nested shape the
// constraint paths describe.
//
// The Query protocol spells a list as prefix.member.1, prefix.member.2 and a
// structure as parent.child, which is the same information as a JSON body with
// the nesting written into the key. Rebuilding the nesting is strictly cheaper
// than teaching every constraint path a second spelling — and it means the test
// harness that builds violating requests works unchanged too.
//
// Values stay strings. Everything in a form is a string, and the model's own
// trait is what says whether a member is a number; see toNumber.
//
// # Every element
//
// A list keeps every element that was sent, in the order of their indices.
// This used to keep one, on the reasoning that a constraint applies to every
// element and so checking one checks the rule. It checks the rule against that
// element. A request is invalid when any element breaks it, and with one kept,
// which one was decided by the order a Go map iterated in: a valid first tag
// and an invalid second was refused on some requests and accepted on others,
// a list was never longer than its bound, and the members of different
// elements were written into the same structure.
//
// Elements are placed by index, so the order the form presents its keys in
// decides nothing. A gap in the indices is compacted: the tree holds the
// elements that were sent and nothing where nothing was sent. Kept as holes, a
// single key naming member 2000000000 would be that many slots, and each hole
// would be a null element for the required check to refuse on a member the
// caller never sent; the model's bound on a list counts its members, which is
// what the compacted length is. What it costs is that a refusal names an
// element by its position among those sent, which is its index whenever the
// request numbers them the way every SDK does.
func FromQuery(vals map[string][]string) map[string]any {
	root := &qnode{}
	for key, vs := range vals {
		if len(vs) == 0 {
			continue
		}
		segs := strings.Split(key, ".")
		// The input itself is a structure. A key that opens with a list
		// position has no member for the list to belong to; the position is
		// dropped and the rest is read as a member, as it always was.
		for len(segs) > 1 && (isMarker(segs[0]) || isIndex(segs[0])) {
			segs = segs[1:]
		}
		root.member(segs[0]).insert(segs[1:], vs[0])
	}
	out, _ := root.value().(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// qnode is one position in the tree while it is being built: a leaf, a
// structure, or a list. A list holds its elements by index until every key has
// been read, because only then is their order known.
type qnode struct {
	leaf    string
	members map[string]*qnode
	items   map[int]*qnode
	// entry records that the list was spelled `.entry.N`, the Query protocol's
	// marker for a MAP. The marker vanishes from the tree, and it is the only
	// thing that says which of the two a numbered list of pairs is.
	entry bool
}

// insert walks the rest of one flattened key into the tree below n.
//
// A list position is `member.N`, `entry.N`, or a bare `N`, the memberless
// spelling some services use. It applies to n itself, so a position directly
// after a position is a list inside a list.
func (n *qnode) insert(segs []string, val string) {
	if len(segs) == 0 {
		n.leaf = val
		return
	}
	seg := segs[0]
	switch {
	case isMarker(seg) && len(segs) > 1 && isIndex(segs[1]):
		idx, _ := strconv.Atoi(segs[1])
		n.item(idx, seg == "entry").insert(segs[2:], val)
	case isMarker(seg) && len(segs) > 1:
		// A marker with no index after it marks nothing. It is the protocol's
		// word and not a member's name, so it is passed over.
		n.insert(segs[1:], val)
	case isIndex(seg):
		idx, _ := strconv.Atoi(seg)
		n.item(idx, false).insert(segs[1:], val)
	default:
		n.member(seg).insert(segs[1:], val)
	}
}

func (n *qnode) member(name string) *qnode {
	if n.members == nil {
		n.members = map[string]*qnode{}
	}
	c := n.members[name]
	if c == nil {
		c = &qnode{}
		n.members[name] = c
	}
	return c
}

func (n *qnode) item(idx int, entry bool) *qnode {
	if n.items == nil {
		n.items = map[int]*qnode{}
	}
	n.entry = n.entry || entry
	c := n.items[idx]
	if c == nil {
		c = &qnode{}
		n.items[idx] = c
	}
	return c
}

// value renders the built tree as the plain values the walker reads.
//
// A form that spells one member two ways (X=a and X.member.1=b) is malformed
// whichever is taken. The container is, because it carries more, and a list
// before a structure; the point is only that the answer does not depend on the
// order the keys were read in.
func (n *qnode) value() any {
	switch {
	case len(n.items) > 0:
		idxs := make([]int, 0, len(n.items))
		for i := range n.items {
			idxs = append(idxs, i)
		}
		slices.Sort(idxs)
		out := make([]any, 0, len(idxs))
		for _, i := range idxs {
			out = append(out, n.items[i].value())
		}
		if n.entry {
			if m, ok := entriesToMap(out); ok {
				return m
			}
		}
		return out
	case n.members != nil:
		out := make(map[string]any, len(n.members))
		for k, c := range n.members {
			out[k] = c.value()
		}
		return out
	}
	return n.leaf
}

// entriesToMap turns the Query protocol's spelling of a MAP back into one.
//
// A map arrives as a numbered list of pairs — MessageAttributes.entry.1.Name
// and .entry.1.Value.DataType — which the positional pass rebuilds as a list of
// {Name, Value} structures. The model calls that member a map
// (MessageAttributes{}.DataType), so without this the walker looks for a map,
// finds a list, and every constraint underneath passes vacuously.
//
// It is applied ONLY to a list spelled `.entry.`. The shape alone cannot tell
// you: AWS flattens a list<Struct{Name,Value}> exactly like a
// map<String,String>, so CloudWatch's `Dimensions.member.1.Name/.Value` — a
// genuine LIST — is indistinguishable from SNS's
// `Attributes.entry.1.Name/.Value` once the marker is gone. Collapsing on shape
// turned that list into a map, and every constraint written `Dimensions[].Name`
// then found no sites and passed vacuously — the same failure this exists to
// prevent, inverted.
//
// Both spellings in use are covered: Name/Value (SNS message attributes, SQS
// attributes) and key/value (SNS topic attributes). The list is in index order,
// so when two entries carry the same name the later index is the one kept,
// every time.
func entriesToMap(list []any) (map[string]any, bool) {
	out := map[string]any{}
	for _, el := range list {
		em, ok := el.(map[string]any)
		if !ok || len(em) != 2 {
			return nil, false
		}
		name, val, found := "", any(nil), false
		for _, pair := range [][2]string{{"Name", "Value"}, {"key", "value"}} {
			n, hasN := em[pair[0]].(string)
			v, hasV := em[pair[1]]
			if hasN && hasV {
				name, val, found = n, v, true
				break
			}
		}
		if !found || name == "" {
			return nil, false
		}
		out[name] = val
	}
	return out, len(out) > 0
}

// isMarker reports the two words the protocol puts before a list index.
func isMarker(s string) bool { return s == "member" || s == "entry" }

func isIndex(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1
}
