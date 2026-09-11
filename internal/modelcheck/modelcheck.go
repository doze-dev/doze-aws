package modelcheck

// Package modelcheck refuses the inputs AWS's own service models say are
// invalid.
//
// A service supplies a table of constraints — generated from `dzaudit`, not
// transcribed — and this package walks a request body against it. Each service
// pairs its table with a rejection-parity test that replays a violating value
// for every constraint, so the table refuses and the test proves the refusal
// happened for the right reason rather than by accident.
//
// It lives here rather than in one service because the shape is identical
// everywhere: the DynamoDB audit built it, and the eight services still to be
// audited need exactly the same walker.
//
// # Why the raw body
//
// The checks run against the body decoded to `any` rather than a typed request,
// because most constrained members have no local effect and so were never
// decoded — which is precisely why they were accepted. A member doze-aws
// ignores still has to be refused when it is invalid, or code that would fail
// on deploy passes here, which is the whole failure this exists to catch.
//
// # Why paths
//
// Two thirds of the model's constraints live inside structures rather than on
// top-level members. A flat member->rule map cannot express
// GlobalSecondaryIndexes[].Projection.ProjectionType, so a table is keyed by
// path and a walker resolves it. A segment is a member name followed by
// container markers, applied left to right:
//
//	Tags[]                    every element of a list
//	RequestItems{}            every value of a map
//	RequestItems{}[].PutRequest   a map of lists, which is BatchWriteItem's shape
//
// A path whose enclosing structure was not sent yields nothing to check, which
// is what makes a constraint on an optional structure's member apply only when
// that structure is actually present.

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// NoMax is an unbounded upper bound — the model's "range 1..-".
var NoMax = math.Inf(1)

type Kind int

const (
	KindRequired Kind = iota
	KindEnum
	KindLength
	KindRange
	KindPattern
)

// constraint is one rule the model states about one input path.
type Constraint struct {
	Path string
	Kind Kind
	Enum []string
	Min  float64
	Max  float64
	Pat  *regexp.Regexp
}

// site is one concrete location a path resolved to: the value found there,
// whether it was present at all, and how AWS would spell that location.
type site struct {
	val     any
	present bool
	disp    string
}

var markerRE = regexp.MustCompile(`^([A-Za-z0-9]+)((?:\[\]|\{\})*)$`)

// segment is one dot-separated part of a constraint path, already split into
// its member name, its markers, and the lower-cased spelling used in the
// message a refusal carries.
type segment struct {
	name    string
	lower   string
	markers []string
}

// pathCache holds each constraint path parsed into segments.
//
// A path is a compile-time constant — "MetricData[].Dimensions[].Name" is
// written in a table and never changes — but it used to be split on "." and
// run through markerRE on EVERY request, for every constraint in the table.
// Validation runs on every request of every service, and Lambda's table has
// 464 constraints, so that was 464 regex matches and 464 string splits per
// call: profiling put splitSegment and strings.Split at over half the
// allocations in the whole validator.
//
// Parsed once here instead. A sync.Map rather than a plain map because tables
// are read concurrently by every in-flight request and never written after
// init; the only writes are first-sight parses, which converge immediately.
var pathCache sync.Map // string -> []segment

// parsePath splits a constraint path into segments, once per distinct path.
func parsePath(path string) []segment {
	if got, ok := pathCache.Load(path); ok {
		return got.([]segment)
	}
	parts := strings.Split(path, ".")
	segs := make([]segment, 0, len(parts))
	for _, p := range parts {
		name, markers := splitSegment(p)
		segs = append(segs, segment{name: name, lower: lowerFirst(name), markers: markers})
	}
	pathCache.Store(path, segs)
	return segs
}

// splitSegment separates a segment into its member name and its markers.
func splitSegment(seg string) (name string, markers []string) {
	m := markerRE.FindStringSubmatch(seg)
	if m == nil {
		return seg, nil
	}
	for i := 0; i+1 < len(m[2]); i += 2 {
		markers = append(markers, m[2][i:i+2])
	}
	return m[1], markers
}

// element is one value reached while expanding a segment's markers, with the
// display path naming its position.
type element struct {
	v    any
	disp []string
}

// expand applies a segment's markers in order, fanning one value out to every
// element (or map value) it contains.
func expand(start element, markers []string) []element {
	level := []element{start}
	for _, mk := range markers {
		var next []element
		for _, el := range level {
			switch mk {
			case "[]":
				lst, ok := el.v.([]any)
				if !ok {
					continue
				}
				for i, item := range lst {
					// AWS indexes list positions from 1 in validation messages.
					next = append(next, element{v: item,
						disp: append(append([]string{}, el.disp...), strconv.Itoa(i+1))})
				}
			case "{}":
				m, ok := el.v.(map[string]any)
				if !ok {
					continue
				}
				// Sorted, so a body with several keys produces the same message
				// every run rather than map-iteration roulette.
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				for _, k := range keys {
					next = append(next, element{v: m[k],
						disp: append(append([]string{}, el.disp...), k)})
				}
			}
		}
		level = next
	}
	return level
}

// sites resolves a path over the body, appending what it finds to buf.
//
// The caller passes a buffer and reads the result before the next call, so a
// whole table is walked with one allocation rather than one per constraint —
// which matters because the walk runs on every request of every service and
// Lambda's table has 464 constraints. Nothing retains a site.
func sites(root map[string]any, path string, buf []site) []site {
	return descend(root, parsePath(path), nil, buf[:0])
}

func descend(cur map[string]any, segs []segment, prefix []string, out []site) []site {
	seg := segs[0]
	markers := seg.markers
	v, present := cur[seg.name]

	// The leaf. Without markers it is the member itself; with them, the
	// constraint is on each element or map value — AttributesToGet[] bounds
	// the strings in the list, not the list.
	if len(segs) == 1 && (len(markers) == 0 || !present) {
		// Most constraints in a table are a top-level member with no markers,
		// so this case is the one worth not allocating for: the display path
		// is just the member's own lower-cased name, already parsed.
		disp := seg.lower
		if len(prefix) > 0 {
			disp = strings.Join(prefix, ".") + "." + seg.lower
		}
		return append(out, site{val: v, present: present, disp: disp})
	}

	here := append(append([]string{}, prefix...), seg.lower)
	if len(segs) == 1 {
		for _, el := range expand(element{v: v, disp: here}, markers) {
			out = append(out, site{val: el.v, present: true, disp: strings.Join(el.disp, ".")})
		}
		return out
	}

	if !present {
		return out // the structure was not sent: nothing inside it to check
	}
	for _, el := range expand(element{v: v, disp: here}, markers) {
		m, ok := el.v.(map[string]any)
		if !ok {
			continue
		}
		out = descend(m, segs[1:], el.disp, out)
	}
	return out
}

// check applies one constraint at one site, returning the error AWS would.
func (c Constraint) check(s site, code string) *awshttp.APIError {
	if c.Kind == KindRequired {
		if !s.present || s.val == nil {
			return validationErr(code, "Value null at '%s' failed to satisfy constraint: "+
				"Member must not be null", s.disp)
		}
		return nil
	}
	if !s.present || s.val == nil {
		return nil // absent is the @required check's business, not this one's
	}

	switch c.Kind {
	case KindEnum:
		str, ok := s.val.(string)
		if !ok {
			return nil
		}
		if !slices.Contains(c.Enum, str) {
			return validationErr(code, "Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [%s]",
				str, s.disp, strings.Join(c.Enum, ", "))
		}
	case KindLength:
		n, ok := lengthOf(s.val)
		if !ok {
			return nil
		}
		if float64(n) < c.Min {
			return validationErr(code, "Value at '%s' failed to satisfy constraint: "+
				"Member must have length greater than or equal to %d", s.disp, int(c.Min))
		}
		if float64(n) > c.Max {
			return validationErr(code, "Value at '%s' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", s.disp, int(c.Max))
		}
	case KindRange:
		f, ok := toNumber(s.val)
		if !ok {
			return nil
		}
		if f < c.Min {
			return validationErr(code, "Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must have value greater than or equal to %d",
				trimNum(f), s.disp, int(c.Min))
		}
		if f > c.Max {
			return validationErr(code, "Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must have value less than or equal to %d",
				trimNum(f), s.disp, int(c.Max))
		}
	case KindPattern:
		str, ok := s.val.(string)
		if !ok {
			return nil
		}
		if !c.Pat.MatchString(str) {
			return validationErr(code, "Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: %s", str, s.disp, c.Pat.String())
		}
	}
	return nil
}

// lengthOf measures whatever the model bounds the length of: a string, a list
// or a map. @length on a map member bounds the collection, not its values.
func lengthOf(v any) (int, bool) {
	switch t := v.(type) {
	case string:
		return len(t), true
	case []any:
		return len(t), true
	case map[string]any:
		return len(t), true
	}
	return 0, false
}

// validate runs a constraint table over a raw request body.
//
// Order is the table's order, so a request breaking several rules reports the
// same one every run rather than whichever the map iterated to first.
func Validate(body []byte, table []Constraint) *awshttp.APIError {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil // the caller's own decode reports this
	}
	return ValidateMap(raw, table)
}

// ValidateMap is Validate for a body the caller has already decoded, which
// several services have by the time they dispatch. Re-marshalling just to
// re-parse would be the only alternative.
func ValidateMap(raw map[string]any, table []Constraint) *awshttp.APIError {
	return ValidateMapAs(raw, table, CodeJSON)
}

// ValidateMapAs is ValidateMap with the error code the service's protocol uses.
func ValidateMapAs(raw map[string]any, table []Constraint, code string) *awshttp.APIError {
	var buf []site
	for _, c := range table {
		buf = sites(raw, c.Path, buf)
		for _, s := range buf {
			if err := c.check(s, code); err != nil {
				return err
			}
		}
	}
	return nil
}

// The two protocol families name this error differently, and clients branch on
// the code: the awsJson services answer ValidationException, the Query ones
// (STS, SNS, EC2) answer ValidationError. Same message, different envelope.
const (
	CodeJSON  = "ValidationException"
	CodeQuery = "ValidationError"
)

func validationErr(code, format string, args ...any) *awshttp.APIError {
	if code == "" {
		code = CodeJSON
	}
	return awshttp.Errf(400, code, "%s",
		"1 validation error detected: "+fmt.Sprintf(format, args...))
}

// toNumber accepts the shapes a number arrives in.
//
// A numeric string counts, because the Query protocol has no types: every value
// in a form is a string, and the model's own @range trait is what says a member
// is a number. Refusing to read it would leave every range constraint on a
// Query service silently unchecked.
func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// trimNum renders a number the way it was written, so an integer bound does
// not come back as "0.000000" in the message.
func trimNum(f float64) string {
	if f == math.Trunc(f) && !math.IsInf(f, 0) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// lowerFirst renders a member the way AWS names it in a validation message.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// ---- the query protocol ----

// FromQuery un-flattens Query-protocol form values into the nested shape the
// constraint paths describe, so one walker serves both protocol families.
//
// The Query protocol spells a list as prefix.member.1, prefix.member.2 and a
// structure as parent.child, which is the same information as a JSON body with
// the nesting written into the key. Rebuilding the nesting is strictly cheaper
// than teaching every constraint path a second spelling — and it means the test
// harness that builds violating requests works unchanged too.
//
// Values stay strings. Everything in a form is a string, and the model's own
// trait is what says whether a member is a number; see toNumber.
func FromQuery(vals map[string][]string) map[string]any {
	root := map[string]any{}
	// Which containers were spelled with the MAP marker. Both markers vanish
	// during the walk, and only this says which one was there.
	entries := map[string]bool{}
	for key, vs := range vals {
		if len(vs) == 0 {
			continue
		}
		insertQuery(root, strings.Split(key, "."), vs[0], "", entries)
	}
	collapseEntries(root, "", entries)
	return root
}

// childPath is the dotted path of one member, used to line up what
// insertQuery recorded with what collapseEntries is looking at. A list's
// elements share their container's path: the index is not part of it.
func childPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// collapseEntries turns the Query protocol's spelling of a MAP back into one.
//
// A map arrives as a numbered list of pairs — MessageAttributes.entry.1.Name
// and .entry.1.Value.DataType — which the positional pass above rebuilds as a
// list of {Name, Value} structures. The model calls that member a map
// (MessageAttributes{}.DataType), so without this the walker looks for a map,
// finds a list, and every constraint underneath passes vacuously.
//
// It collapses ONLY containers spelled `.entry.`, which is why insertQuery
// records them. The shape alone cannot tell you: AWS flattens a
// list<Struct{Name,Value}> exactly like a map<String,String>, so
// CloudWatch's `Dimensions.member.1.Name/.Value` — a genuine LIST — is
// indistinguishable from SNS's `Attributes.entry.1.Name/.Value` once the
// marker is gone. Collapsing on shape turned that list into a map, and every
// constraint written `Dimensions[].Name` then found no sites and passed
// vacuously — the same failure this function exists to prevent, inverted.
func collapseEntries(node map[string]any, path string, entries map[string]bool) {
	for k, v := range node {
		here := childPath(path, k)
		switch t := v.(type) {
		case map[string]any:
			collapseEntries(t, here, entries)
		case []any:
			if entries[here] {
				if m, ok := entriesToMap(t); ok {
					node[k] = m
					collapseEntries(m, here, entries)
					continue
				}
			}
			for _, el := range t {
				if em, ok := el.(map[string]any); ok {
					collapseEntries(em, here, entries)
				}
			}
		}
	}
}

// entriesToMap converts [{Name:k, Value:v}, ...] into {k: v}, covering both
// spellings in use: Name/Value (SNS message attributes, SQS attributes) and
// key/value (SNS topic attributes).
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

// insertQuery walks one flattened key into the tree, creating containers as it
// goes. "member" is skipped: it is the protocol's list marker, not a member
// name, and the index that follows is what selects the element.
func insertQuery(cur map[string]any, segs []string, val string, path string, entries map[string]bool) {
	for i := 0; i < len(segs); i++ {
		seg := segs[i]

		// prefix.member.N, prefix.entry.N or prefix.N — a list index follows.
		// "entry" is the Query protocol's marker for a MAP, which arrives as a
		// numbered list of Name/Value pairs and is collapsed back into a map by
		// collapseEntries once the tree is built.
		if (seg == "member" || seg == "entry") && i+1 < len(segs) {
			continue
		}
		if idx, err := strconv.Atoi(seg); err == nil && idx >= 1 {
			// The parent key already holds the list; this segment selects an
			// element, so it is handled by the branch below rather than here.
			_ = idx
			continue
		}

		last := i == len(segs)-1
		// Look ahead: a list marker or index after this segment makes it a
		// list. marker remembers WHICH marker, because that is the only thing
		// that distinguishes a map from a list of Name/Value pairs.
		isList, marker := false, ""
		for j := i + 1; j < len(segs); j++ {
			if segs[j] == "member" || segs[j] == "entry" {
				marker = segs[j]
				continue
			}
			if _, err := strconv.Atoi(segs[j]); err == nil {
				isList = true
			}
			break
		}

		switch {
		case last:
			cur[seg] = val
		case isList:
			here := childPath(path, seg)
			if marker == "entry" {
				entries[here] = true
			}
			lst, _ := cur[seg].([]any)
			// One element is enough: a constraint applies to every element, so
			// checking the first is checking the rule.
			if len(lst) == 0 {
				lst = []any{}
			}
			rest := segs[i+1:]
			// Skip the marker and index to see whether elements are scalars.
			k := 0
			for k < len(rest) && (rest[k] == "member" || rest[k] == "entry" || isIndex(rest[k])) {
				k++
			}
			if k == len(rest) {
				if len(lst) == 0 {
					lst = append(lst, val)
				}
				cur[seg] = lst
				return
			}
			var elem map[string]any
			if len(lst) > 0 {
				elem, _ = lst[0].(map[string]any)
			}
			if elem == nil {
				elem = map[string]any{}
				lst = append(lst, elem)
			}
			cur[seg] = lst
			insertQuery(elem, rest[k:], val, here, entries)
			return
		default:
			next, _ := cur[seg].(map[string]any)
			if next == nil {
				next = map[string]any{}
				cur[seg] = next
			}
			cur = next
			path = childPath(path, seg)
		}
	}
}

func isIndex(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1
}
