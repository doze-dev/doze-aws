package sns

import (
	"encoding/json"
	"strings"
)

// matchFilter reports whether a message with the given attributes satisfies a
// subscription's filter policy. An empty policy always matches. Within an
// attribute the conditions are OR'd; across attributes they are AND'd — the SNS
// semantics. Supported conditions: exact string, {"prefix":..}, {"anything-but":
// [..]}, and the {"exists":true/false} operator.
func matchFilter(policyJSON string, attrs map[string]Attr) bool {
	if strings.TrimSpace(policyJSON) == "" {
		return true
	}
	var policy map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
		return true // a malformed policy shouldn't silently drop everything
	}
	for key, conds := range policy {
		a, present := attrs[key]
		if !matchKey(conds, a.StringValue, present) {
			return false
		}
	}
	return true
}

func matchKey(conds []json.RawMessage, value string, present bool) bool {
	for _, c := range conds {
		// Plain string condition.
		var s string
		if json.Unmarshal(c, &s) == nil {
			if present && s == value {
				return true
			}
			continue
		}
		// Operator object.
		var op map[string]json.RawMessage
		if json.Unmarshal(c, &op) != nil {
			continue
		}
		if raw, ok := op["exists"]; ok {
			var want bool
			_ = json.Unmarshal(raw, &want)
			if want == present {
				return true
			}
		}
		if raw, ok := op["prefix"]; ok {
			var p string
			if json.Unmarshal(raw, &p) == nil && present && strings.HasPrefix(value, p) {
				return true
			}
		}
		if raw, ok := op["anything-but"]; ok {
			var list []string
			if json.Unmarshal(raw, &list) == nil && present {
				excluded := false
				for _, x := range list {
					if x == value {
						excluded = true
						break
					}
				}
				if !excluded {
					return true
				}
			}
		}
	}
	return false
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
