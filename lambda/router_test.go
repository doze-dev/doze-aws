package lambda

import (
	"net/http/httptest"
	"testing"
)

// The router is built from the model's table and a handler map. A route with no
// handler would answer 404 for an operation the model says exists; a handler
// with no route is dead code that looks like a feature.
func TestEveryRouteHasAHandlerAndEveryHandlerARoute(t *testing.T) {
	handlers := (&Server{}).handlers()
	routed := map[string]bool{}
	for _, rt := range routeSpecs() {
		routed[rt.Op] = true
		if handlers[rt.Op] == nil {
			t.Errorf("%s %s (%s) has no handler", rt.Method, rt.Pattern, rt.Op)
		}
	}
	for op := range handlers {
		if !routed[op] {
			t.Errorf("handler %s has no route", op)
		}
	}
}

// Every route in the model's table is found at its own template, under its own
// operation — the property the old matchRoute held by construction.
func TestEveryModelRouteMatchesItsOwnTemplate(t *testing.T) {
	for _, rt := range routes {
		path := ""
		for i, seg := range rt.Segs {
			if seg == "" {
				seg = "L-" + rt.Labels[i]
			}
			path += "/" + seg
		}
		req := httptest.NewRequest(rt.Method, path, nil)
		// The one pair that shares a path is told apart by ?Arn=.
		if got := OperationFor(req); got != rt.Op {
			t.Errorf("%s %s = %q, want %s", rt.Method, path, got, rt.Op)
		}
	}
}

func TestPathsTheModelDoesNotListAreStillServed(t *testing.T) {
	for _, c := range []struct{ method, path, op string }{
		{"GET", "/2016-08-19/account-settings", "GetAccountSettings"},
		{"GET", "/2015-03-31/functions/f/doze-runtime", "DozeRuntime"},
		{"DELETE", "/2015-03-31/functions/f/policy", "RemovePermission"},
		{"DELETE", "/2015-03-31/functions/f/policy/sid", "RemovePermission"},
		{"POST", "/2018-10-31/layers/l/versions/1/policy", "AddLayerVersionPermission"},
		{"GET", "/2018-10-31/layers/l/versions/1/policy", "GetLayerVersionPolicy"},
		{"DELETE", "/2018-10-31/layers/l/versions/1/policy/sid", "RemoveLayerVersionPermission"},
		{"GET", "/2018-10-31/layers?find=LayerVersion&Arn=arn:aws:lambda:us-east-1:000000000000:layer:l:1", "GetLayerVersionByArn"},
		{"GET", "/2018-10-31/layers", "ListLayers"},
		// A trailing slash, as the v1 Go SDK sends it.
		{"GET", "/2015-03-31/functions/", "ListFunctions"},
	} {
		if got := OperationFor(httptest.NewRequest(c.method, c.path, nil)); got != c.op {
			t.Errorf("%s %s = %q, want %s", c.method, c.path, got, c.op)
		}
	}
}

// A path that exists with another method is 405, and one that does not exist
// is 404, in Lambda's own error shape.
func TestWrongMethodIs405AndUnknownPathIs404(t *testing.T) {
	s, _ := New(Options{DataDir: t.TempDir()})
	t.Cleanup(func() { s.Close() })
	for _, c := range []struct {
		method, path string
		status       int
		code         string
	}{
		{"PATCH", "/2015-03-31/functions", 405, "MethodNotAllowed"},
		{"GET", "/2015-03-31/nothing-here", 404, "ResourceNotFoundException"},
		{"GET", "/2015-03-31", 404, "ResourceNotFoundException"},
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.status {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.status)
		}
		if got := rec.Header().Get("x-amzn-errortype"); got != c.code {
			t.Errorf("%s %s error type = %q, want %s", c.method, c.path, got, c.code)
		}
	}
}
