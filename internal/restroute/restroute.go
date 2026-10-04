// Package restroute serves the REST-style AWS services (Lambda, API Gateway,
// S3) from a chi router built out of AWS's own service model.
//
// Each of those services used to dispatch by splitting the path into segments
// and switching on the method, while a separate table generated from the model
// said which operation a request was — for validation and for naming the call
// on the console's wire page — and a third matcher in the IAM guard guessed
// the permission. Three answers to one question, and they disagreed. Here the
// model's table is the router: a request is matched once, the operation it
// matched is on its context, and validation, the guard, logging and the
// handler all read it from there.
//
// chi cannot match on a query string or a header, and S3 tells some
// operations apart only by those (?tagging, ?versioning, x-amz-copy-source).
// A Route that shares its method and path with another carries a Pick
// function; the candidates are tried in the order they were given, most
// specific first, as LocalStack's op_router does with a score.
package restroute

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Handler is what every service's handlers already are: it writes its success
// response itself and returns the API error to answer with otherwise.
type Handler func(w http.ResponseWriter, r *http.Request) *awshttp.APIError

// Route is one operation's binding.
type Route struct {
	// Op is the AWS operation name.
	Op string
	// Method is the HTTP method; empty means every method (a family the
	// service refuses as a whole).
	Method string
	// Pattern is a chi pattern; Pattern converts a model template to one.
	Pattern string
	// Pick, when set, must return true for this route to serve a request that
	// matched its method and pattern. Routes sharing a method and pattern are
	// tried in order, and one without a Pick is the fallback.
	Pick func(*http.Request) bool
	// Handler serves it. Nil leaves the route to Options.Unhandled.
	Handler Handler
}

// Options says how a service answers what the router cannot.
type Options struct {
	// OnError writes (and logs) the API error a Handler returned, and the
	// ones the router raises itself. Required.
	OnError func(w http.ResponseWriter, r *http.Request, e *awshttp.APIError)
	// NotFound and MethodNotAllowed are the router's own refusals, in the
	// service's wire format. Required.
	NotFound         func(r *http.Request) *awshttp.APIError
	MethodNotAllowed func(r *http.Request) *awshttp.APIError
	// Tolerant makes the router forgive what the REST/JSON services always
	// have: a trailing slash is trimmed (the v1 Go SDK sends
	// /2015-03-31/functions/), and an empty path label is a label with no
	// value rather than a missing route — /functions//aliases reaches
	// CreateAlias with an empty FunctionName, which validation refuses as the
	// model says it must. S3 keeps its slashes: a key may end in one, and may
	// contain two in a row.
	Tolerant bool
	// Unmatched, when set, handles the router's own refusals — no route, the
	// wrong method, no candidate that picks — in place of OnError, for a
	// service whose checks must run on a request that matched nothing too (S3's
	// IAM guard and its unimplemented-sub-resource refusal both did, before the
	// router). It receives the refusal the router would have written.
	Unmatched func(w http.ResponseWriter, r *http.Request, e *awshttp.APIError)
	// Use wraps every route, innermost last. It runs after the route has
	// matched, so Op(r) is already set — which is why validation and the IAM
	// guard are here and not on the router: before matching there is no
	// operation to read.
	Use []func(http.Handler) http.Handler
}

// anyMethod is the key a route with no Method is held under.
const anyMethod = "*"

// Router is a built chi router that can also say which operation a request is
// without serving it.
type Router struct {
	mux      *chi.Mux
	tolerant bool
	// byKey holds every route by "METHOD pattern", in the order given, for Op.
	byKey map[string][]Route
	keys  []string
}

// Build makes the router. It panics on a route chi cannot register (a
// duplicate method and pattern without a Pick on both, a malformed pattern):
// those are programming errors in a table that is fixed at build time, and a
// test builds every service's router.
func Build(routes []Route, o Options) *Router {
	rt := &Router{mux: chi.NewRouter(), byKey: map[string][]Route{}, tolerant: o.Tolerant}
	if o.Tolerant {
		rt.mux.Use(normalize)
	}

	refuse := func(w http.ResponseWriter, r *http.Request, e *awshttp.APIError) {
		if o.Unmatched != nil {
			o.Unmatched(w, r, e)
			return
		}
		o.OnError(w, r, e)
	}

	wrap := func(rr Route) http.Handler {
		var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rr.Handler == nil {
				refuse(w, r, o.NotFound(r))
				return
			}
			if e := rr.Handler(w, r); e != nil {
				o.OnError(w, r, e)
			}
		})
		for i := len(o.Use) - 1; i >= 0; i-- {
			h = o.Use[i](h)
		}
		op := rr.Op
		inner := h
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner.ServeHTTP(w, withOp(r, op))
		})
	}

	for _, rr := range routes {
		if rr.Method == "" {
			rr.Method = anyMethod
		}
		key := rr.Method + " " + rr.Pattern
		if _, seen := rt.byKey[key]; !seen {
			rt.keys = append(rt.keys, key)
		}
		rt.byKey[key] = append(rt.byKey[key], rr)
	}
	for _, key := range rt.keys {
		cands := rt.byKey[key]
		method, pattern := cands[0].Method, cands[0].Pattern
		chains := make([]http.Handler, len(cands))
		for i, c := range cands {
			chains[i] = wrap(c)
		}
		if len(cands) == 1 && cands[0].Pick == nil {
			if method == anyMethod {
				rt.mux.Handle(pattern, chains[0])
			} else {
				rt.mux.Method(method, pattern, chains[0])
			}
			continue
		}
		rt.mux.Method(method, pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for i, c := range cands {
				if c.Pick == nil || c.Pick(r) {
					chains[i].ServeHTTP(w, r)
					return
				}
			}
			refuse(w, r, o.NotFound(r))
		}))
	}
	rt.mux.NotFound(func(w http.ResponseWriter, r *http.Request) { refuse(w, r, o.NotFound(r)) })
	rt.mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { refuse(w, r, o.MethodNotAllowed(r)) })
	return rt
}

// ServeHTTP serves the request.
//
// A Router is a service, never a sub-router. chi treats a request whose
// context already holds a route context as a call from a parent router and
// reuses its method and path — so a handler that calls a sibling service
// in-process (S3 telling Lambda about an upload) would hand it S3's route and
// be answered 405. The inherited context is cleared first.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if chi.RouteContext(r.Context()) != nil {
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, (*chi.Context)(nil)))
	}
	rt.mux.ServeHTTP(w, r)
}

// Op is the operation r addresses, or "" when no route matches. It matches
// without serving, so it can name a call from outside the handler chain — the
// console's wire page does.
func (rt *Router) Op(r *http.Request) string {
	op, _ := rt.Match(r)
	return op
}

// Match is Op with the path labels the request carries, decoded as a handler
// would read them (an empty label is "", and a greedy one is under "*"). It is
// how a caller with no handler chain learns what a request names: the IAM
// guard asks it for the function a Lambda call is addressed to.
func (rt *Router) Match(r *http.Request) (op string, labels map[string]string) {
	rctx := chi.NewRouteContext()
	path := routePath(r)
	if rt.tolerant {
		path = tidy(path)
	}
	if !rt.mux.Match(rctx, r.Method, path) {
		return "", nil
	}
	cands := rt.byKey[r.Method+" "+rctx.RoutePattern()]
	if len(cands) == 0 {
		cands = rt.byKey[anyMethod+" "+rctx.RoutePattern()]
	}
	for _, c := range cands {
		if c.Pick == nil || c.Pick(r) {
			labels = make(map[string]string, len(rctx.URLParams.Keys))
			for i, k := range rctx.URLParams.Keys {
				labels[k] = decodeLabel(rctx.URLParams.Values[i], r.URL.RawPath != "")
			}
			return c.Op, labels
		}
	}
	return "", nil
}

// Routes lists every registered "METHOD pattern", in registration order. A
// test walks it to hold a router to the model it was built from.
func (rt *Router) Routes() []string { return append([]string(nil), rt.keys...) }

// routePath is the path chi routes on: the escaped form when the request has
// one, so a %2F inside an S3 key or an ARN in a tag path stays one segment.
func routePath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	return r.URL.Path
}

// emptySeg stands in, while routing, for a path segment with nothing in it.
// chi will not match an empty label (and matches one inconsistently), so the
// router is given something to match and Param gives it back as empty.
const emptySeg = "\x00"

// tidy trims a trailing slash and marks interior empty segments.
func tidy(p string) string {
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimRight(p, "/")
		if p == "" {
			return "/"
		}
	}
	if !strings.Contains(p, "//") {
		return p
	}
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			parts[i] = emptySeg
		}
	}
	return strings.Join(parts, "/")
}

// normalize applies tidy before routing, by setting the path chi routes on.
func normalize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			rctx.RoutePath = tidy(routePath(r))
		}
		next.ServeHTTP(w, r)
	})
}
