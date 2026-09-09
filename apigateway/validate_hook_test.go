package apigateway

import (
	"strings"
	"testing"
)

// TestEveryRouteMatchesItsOwnTemplate is the guard the ordering needs. Routes
// are sorted most-specific first so that /methods/{m}/integration wins over
// /methods/{m}, and nothing enforces that order but the generator — if it ever
// emitted them the other way round, PutIntegration's constraints would be
// checked against PutMethod's table and the audit would still pass, because a
// missing constraint looks like a permissive service, not a broken matcher.
func TestEveryRouteMatchesItsOwnTemplate(t *testing.T) {
	for _, rt := range routes {
		// Build a concrete path from the template, giving each label a value
		// that could not be mistaken for a literal segment.
		parts := make([]string, len(rt.Segs))
		for i, seg := range rt.Segs {
			if seg == "" {
				parts[i] = "L-" + rt.Labels[i]
				continue
			}
			parts[i] = seg
		}
		path := "/" + strings.Join(parts, "/")

		op, labels, ok := matchRoute(rt.Method, path)
		if !ok {
			t.Errorf("%s %s matched no route", rt.Method, path)
			continue
		}
		if op != rt.Op {
			t.Errorf("%s %s resolved to %s, want %s\n"+
				"  A route shadowed by a less specific one selects the wrong "+
				"constraint table, and the audit cannot see it.",
				rt.Method, path, op, rt.Op)
			continue
		}
		for i, name := range rt.Labels {
			if name == "" {
				continue
			}
			if got := labels[name]; got != "L-"+name {
				t.Errorf("%s: label %s = %q, want %q (segment %d)",
					rt.Op, name, got, "L-"+name, i)
			}
		}
	}
}

// TestEveryConstraintTableHasARoute keeps the two generated tables in step. A
// table nothing routes to is dead: its operation would never be validated, and
// the only symptom would be an audit that quietly stopped covering it.
func TestEveryConstraintTableHasARoute(t *testing.T) {
	routed := map[string]bool{}
	for _, rt := range routes {
		routed[rt.Op] = true
	}
	for op := range constraintTables {
		if !routed[op] {
			t.Errorf("%s has constraints and no route — nothing would ever check them", op)
		}
	}
}

// TestUnroutedPathsAreLeftAlone: the data plane and the operations doze-aws
// does not implement must not be claimed by the matcher.
func TestUnroutedPathsAreLeftAlone(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{"GET", "/restapis/abc/requestvalidators"},
		{"POST", "/restapis/abc/models"},
		{"GET", "/apikeys"},
		{"GET", "/"},
	} {
		if op, _, ok := matchRoute(tc.method, tc.path); ok {
			t.Errorf("%s %s claimed by %s, but doze-aws does not route it",
				tc.method, tc.path, op)
		}
	}
}
