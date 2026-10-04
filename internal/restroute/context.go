package restroute

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

type opKey struct{}

func withOp(r *http.Request, op string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), opKey{}, op))
}

// Op is the operation the router matched for r, or "" outside a route.
func Op(r *http.Request) string {
	op, _ := r.Context().Value(opKey{}).(string)
	return op
}

// Param is a path label's value, as the client meant it. chi routes on the
// escaped path when there is one and hands back the escaped text, so a
// %2F stays inside its segment and then has to be unescaped here — but only
// then: with no RawPath the router saw the decoded path, and decoding it
// again would turn a literal "%41" into "A".
func Param(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if v == emptySeg {
		return ""
	}
	if r.URL.RawPath == "" {
		return v
	}
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}

// Wildcard is what a trailing `*` matched: S3's greedy {Key+}.
func Wildcard(r *http.Request) string { return Param(r, "*") }

// SetParam sets a path label on the route context. A handler that re-dispatches
// to another handler — a list page that opens its first item — uses it where
// it used to call SetPathValue.
func SetParam(r *http.Request, name, value string) {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		rctx.URLParams.Add(name, value)
	}
}
