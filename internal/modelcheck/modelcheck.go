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
	Pat  *Pat
	// Blob marks a KindLength on a blob member. On the JSON protocols a blob
	// arrives as base64 text, and the model's length is of the decoded BYTES:
	// KMS's Plaintext may be 4096 bytes, which is 5464 characters. Without
	// this the check measured the text, and refused a 3,073 to 4,096 byte
	// plaintext that AWS accepts.
	Blob bool
}

// Pat is a constraint's regular expression, compiled the first time something
// is actually matched against it.
//
// 527 of these are written across the service tables, and regexp.MustCompile in
// a package-level var meant every one of them was compiled during init. That is
// the single largest thing an idle doze-aws holds: a heap profile of a process
// with nothing to do put regexp.Compile at 3.1MB of a 6.7MB live heap, 46% of
// it, for patterns belonging to APIs the process may never be asked to serve.
//
// Compiling on demand costs one atomic load per check once warm. The tables are
// still declared the same way; only MustCompile becomes Pattern.
type Pat struct {
	src  string
	once sync.Once
	re   *regexp.Regexp
	err  error
}

// Pattern declares a constraint's regular expression without compiling it.
func Pattern(src string) *Pat { return &Pat{src: src} }

func (p *Pat) compile() {
	p.once.Do(func() { p.re, p.err = regexp.Compile(p.src) })
}

// MatchString reports whether the value satisfies the pattern.
//
// A pattern that does not compile matches nothing, and the walker refuses what
// does not match — so a bad entry refuses EVERY request that sets the member,
// valid ones included. Fail-closed, which is the right direction, and loud
// enough that each service's rejection-parity suite catches it: those assert
// the baseline is accepted before mutating it, and a pattern refusing
// everything fails that first.
//
// TestEveryPatternCompiles (patterns_test.go) covers the rest — a pattern on a
// member no baseline happens to set, which nothing else would exercise.
func (p *Pat) MatchString(s string) bool {
	p.compile()
	return p.err == nil && p.re.MatchString(s)
}

// String is the pattern's source, which is what AWS quotes back in a refusal.
func (p *Pat) String() string { return p.src }

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
// call: profiling put SplitSegment and strings.Split at over half the
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
		name, markers := SplitSegment(p)
		segs = append(segs, segment{name: name, lower: lowerFirst(name), markers: markers})
	}
	pathCache.Store(path, segs)
	return segs
}

// SplitSegment separates a segment into its member name and its markers.
//
// Exported only so the cross-check in internal/shapecheck can reach it: this
// grammar has three implementations in the tree and nothing confirmed they
// agree. See TestTheThreePathParsersAgree.
func SplitSegment(seg string) (name string, markers []string) {
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
		if c.Blob {
			if str, isStr := s.val.(string); isStr {
				n = decodedLen(str)
			}
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
// decodedLen is the number of bytes a base64 string stands for, worked out
// from its length and padding without decoding it. A string that is not valid
// base64 is the caller's decode error to report; what is returned for one is
// only a number that will not wrongly refuse a valid blob.
func decodedLen(s string) int {
	n := len(s)
	for pad := 0; pad < 2 && n > 0 && s[n-1] == '='; pad++ {
		n--
	}
	return n * 6 / 8
}

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
