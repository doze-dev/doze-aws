package sns

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/eventpattern"
)

// matchFilter reports whether a message with the given attributes satisfies a
// subscription's filter policy. An empty policy always matches. SNS filter
// policies share EventBridge's pattern language (conditions OR'd within an
// attribute, attributes AND'd), so the shared eventpattern matcher backs both —
// giving SNS the full operator set (numeric ranges, prefix/suffix, anything-but,
// exists, wildcard) rather than a weaker re-implementation.
func matchFilter(policyJSON string, attrs map[string]Attr) bool {
	if strings.TrimSpace(policyJSON) == "" {
		return true
	}
	pat, err := eventpattern.Parse([]byte(policyJSON))
	if err != nil {
		return true // a malformed policy shouldn't silently drop everything
	}
	doc := make(map[string]any, len(attrs))
	for name, a := range attrs {
		doc[name] = attrMatchValue(a)
	}
	raw, _ := json.Marshal(doc)
	ok, err := pat.Match(raw)
	if err != nil {
		return true
	}
	return ok
}

// attrMatchValue renders an SNS message attribute as the JSON value the pattern
// matcher compares against: Number as a JSON number (so numeric operators work),
// String.Array as a JSON array (any-element match), everything else as a string.
func attrMatchValue(a Attr) any {
	switch a.DataType {
	case "Number":
		if f, err := strconv.ParseFloat(a.StringValue, 64); err == nil {
			return f
		}
		return a.StringValue
	case "String.Array":
		var arr []any
		if json.Unmarshal([]byte(a.StringValue), &arr) == nil {
			return arr
		}
		return a.StringValue
	default:
		return a.StringValue
	}
}

// MatchPolicy reports whether a message carrying the given string attributes
// would satisfy a subscription's filter policy. Exported so the admin/CLI can
// show per-subscription routing without re-implementing the matcher. An empty
// policy matches everything.
func MatchPolicy(policyJSON string, attrs map[string]string) bool {
	m := make(map[string]Attr, len(attrs))
	for k, v := range attrs {
		m[k] = Attr{DataType: "String", StringValue: v}
	}
	return matchFilter(policyJSON, m)
}
