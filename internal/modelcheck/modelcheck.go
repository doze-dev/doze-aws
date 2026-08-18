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

// sites resolves a path over the body.
func sites(root map[string]any, path string) []site {
	return descend(root, strings.Split(path, "."), nil)
}

func descend(cur map[string]any, segs []string, prefix []string) []site {
	name, markers := splitSegment(segs[0])
	here := append(append([]string{}, prefix...), lowerFirst(name))
	v, present := cur[name]

	if len(segs) == 1 {
		// The leaf. Without markers it is the member itself; with them, the
		// constraint is on each element or map value — AttributesToGet[] bounds
		// the strings in the list, not the list.
		if len(markers) == 0 || !present {
			return []site{{val: v, present: present, disp: strings.Join(here, ".")}}
		}
		var out []site
		for _, el := range expand(element{v: v, disp: here}, markers) {
			out = append(out, site{val: el.v, present: true, disp: strings.Join(el.disp, ".")})
		}
		return out
	}

	if !present {
		return nil // the structure was not sent: nothing inside it to check
	}
	var out []site
	for _, el := range expand(element{v: v, disp: here}, markers) {
		m, ok := el.v.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, descend(m, segs[1:], el.disp)...)
	}
	return out
}

// check applies one constraint at one site, returning the error AWS would.
func (c Constraint) check(s site) *awshttp.APIError {
	if c.Kind == KindRequired {
		if !s.present || s.val == nil {
			return validationErr("Value null at '%s' failed to satisfy constraint: "+
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
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [%s]",
				str, s.disp, strings.Join(c.Enum, ", "))
		}
	case KindLength:
		n, ok := lengthOf(s.val)
		if !ok {
			return nil
		}
		if float64(n) < c.Min {
			return validationErr("Value at '%s' failed to satisfy constraint: "+
				"Member must have length greater than or equal to %d", s.disp, int(c.Min))
		}
		if float64(n) > c.Max {
			return validationErr("Value at '%s' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", s.disp, int(c.Max))
		}
	case KindRange:
		f, ok := toNumber(s.val)
		if !ok {
			return nil
		}
		if f < c.Min {
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must have value greater than or equal to %d",
				trimNum(f), s.disp, int(c.Min))
		}
		if f > c.Max {
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must have value less than or equal to %d",
				trimNum(f), s.disp, int(c.Max))
		}
	case KindPattern:
		str, ok := s.val.(string)
		if !ok {
			return nil
		}
		if !c.Pat.MatchString(str) {
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
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
	for _, c := range table {
		for _, s := range sites(raw, c.Path) {
			if err := c.check(s); err != nil {
				return err
			}
		}
	}
	return nil
}

func validationErr(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ValidationException", "%s",
		"1 validation error detected: "+fmt.Sprintf(format, args...))
}

// toNumber accepts the shapes a JSON number arrives in.
func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
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
