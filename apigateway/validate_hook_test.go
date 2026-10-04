package apigateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/restroute"
)

// probe serves a request through a router built from the same specs the
// server uses, with every handler replaced by one that records what the
// router matched and the labels it read.
func probe(specs []restroute.Route, table []route, method, path string) (op string, labels map[string]string, ok bool) {
	routed := append([]restroute.Route(nil), specs...)
	for i := range routed {
		routed[i].Handler = func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			ok = true
			op = restroute.Op(r)
			labels = map[string]string{}
			for _, rt := range table {
				if rt.Op != op {
					continue
				}
				for _, name := range rt.Labels {
					if name != "" {
						labels[name] = labelValue(r, name)
					}
				}
			}
			return nil
		}
	}
	rt := restroute.Build(routed, restroute.Options{
		OnError:          func(http.ResponseWriter, *http.Request, *awshttp.APIError) {},
		Tolerant:         true,
		NotFound:         notFound,
		MethodNotAllowed: notAllowed,
	})
	rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	return op, labels, ok
}

// TestEveryRouteMatchesItsOwnTemplate is the guard the model's table needs:
// every operation is found at its own method and path, under its own name, with
// each label read from the segment that carries it. A route shadowed by a less
// specific one would select the wrong constraint table, and the audit cannot
// see it — a missing constraint looks like a permissive service, not a broken
// router.
func TestEveryRouteMatchesItsOwnTemplate(t *testing.T) {
	for _, plane := range []struct {
		name  string
		specs []restroute.Route
		table []route
	}{{"v1", v1Specs(), routes}, {"v2", v2Specs(), routesV2}} {
		for _, rt := range plane.table {
			// A concrete path, each label given a value that could not be
			// mistaken for a literal segment.
			parts := make([]string, len(rt.Segs))
			for i, seg := range rt.Segs {
				if seg == "" {
					parts[i] = "L-" + rt.Labels[i]
					continue
				}
				parts[i] = seg
			}
			path := "/" + strings.Join(parts, "/")

			op, labels, ok := probe(plane.specs, plane.table, rt.Method, path)
			if !ok {
				t.Errorf("%s %s %s matched no route", plane.name, rt.Method, path)
				continue
			}
			if op != rt.Op {
				t.Errorf("%s %s %s resolved to %s, want %s", plane.name, rt.Method, path, op, rt.Op)
				continue
			}
			for _, name := range rt.Labels {
				if name != "" && labels[name] != "L-"+name {
					t.Errorf("%s %s: label %s = %q, want %q", plane.name, rt.Op, name, labels[name], "L-"+name)
				}
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

// TestUnroutedPathsAreLeftAlone: the operations doze-aws does not implement
// are refused by family, not claimed as an operation — validation would
// otherwise check a request against the wrong table.
func TestUnroutedPathsAreLeftAlone(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{"GET", "/restapis/abc/requestvalidators"},
		{"POST", "/restapis/abc/models"},
		{"GET", "/domainnames"},
		{"GET", "/"},
	} {
		if op := OperationFor(httptest.NewRequest(tc.method, tc.path, nil)); op != "" {
			t.Errorf("%s %s claimed by %s, but doze-aws does not route it", tc.method, tc.path, op)
		}
	}
}

// Every route has a handler and every handler a route, in both planes.
func TestEveryRouteHasAHandlerAndEveryHandlerARoute(t *testing.T) {
	s := &Server{}
	for _, plane := range []struct {
		name     string
		specs    []restroute.Route
		handlers map[string]restroute.Handler
	}{{"v1", v1Specs(), s.v1Handlers()}, {"v2", v2Specs(), s.v2Handlers()}} {
		routed := map[string]bool{}
		for _, rt := range plane.specs {
			if rt.Handler != nil {
				continue // a refused family carries its own
			}
			routed[rt.Op] = true
			if plane.handlers[rt.Op] == nil {
				t.Errorf("%s: %s %s (%s) has no handler", plane.name, rt.Method, rt.Pattern, rt.Op)
			}
		}
		for op := range plane.handlers {
			if !routed[op] {
				t.Errorf("%s: handler %s has no route", plane.name, op)
			}
		}
	}
}
