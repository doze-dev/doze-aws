package restroute_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/restroute"
)

// reply writes what the handler saw, so a test reads it off the response.
func reply(w http.ResponseWriter, r *http.Request, parts ...string) *awshttp.APIError {
	w.Write([]byte(restroute.Op(r) + "|" + strings.Join(parts, "|")))
	return nil
}

func options(t *testing.T) restroute.Options {
	return restroute.Options{
		OnError: func(w http.ResponseWriter, _ *http.Request, e *awshttp.APIError) {
			w.WriteHeader(e.Status)
			w.Write([]byte(e.Code))
		},
		NotFound: func(*http.Request) *awshttp.APIError {
			return awshttp.Errf(404, "NotFound", "no such route")
		},
		MethodNotAllowed: func(*http.Request) *awshttp.APIError {
			return awshttp.Errf(405, "MethodNotAllowed", "wrong method")
		},
	}
}

func do(h http.Handler, method, target string) (int, string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec.Code, rec.Body.String()
}

func TestPatternFromAModelTemplate(t *testing.T) {
	for _, c := range []struct {
		segs, labels []string
		greedy       bool
		want         string
	}{
		{nil, nil, false, "/"},
		{[]string{""}, []string{"Bucket"}, false, "/{Bucket}"},
		{[]string{"2015-03-31", "functions", "", "aliases", ""},
			[]string{"", "", "FunctionName", "", "Name"}, false,
			"/2015-03-31/functions/{FunctionName}/aliases/{Name}"},
		{[]string{"", ""}, []string{"Bucket", "Key"}, true, "/{Bucket}/*"},
	} {
		if got := restroute.Pattern(c.segs, c.labels, c.greedy); got != c.want {
			t.Errorf("Pattern(%q) = %q, want %q", c.segs, got, c.want)
		}
	}
}

func TestARequestIsMatchedOnceAndCarriesItsOperation(t *testing.T) {
	rt := restroute.Build([]restroute.Route{
		{Op: "GetAlias", Method: "GET", Pattern: "/f/{Fn}/aliases/{Name}", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return reply(w, r, restroute.Param(r, "Fn"), restroute.Param(r, "Name"))
		}},
		{Op: "DeleteAlias", Method: "DELETE", Pattern: "/f/{Fn}/aliases/{Name}", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return reply(w, r)
		}},
	}, options(t))

	if code, body := do(rt, "GET", "/f/orders/aliases/live"); code != 200 || body != "GetAlias|orders|live" {
		t.Errorf("GET = %d %q", code, body)
	}
	if got := rt.Op(httptest.NewRequest("DELETE", "/f/orders/aliases/live", nil)); got != "DeleteAlias" {
		t.Errorf("Op = %q, want DeleteAlias", got)
	}
	if got := rt.Op(httptest.NewRequest("GET", "/nowhere", nil)); got != "" {
		t.Errorf("Op of an unrouted path = %q, want empty", got)
	}
}

func TestAKnownPathWithTheWrongMethodIs405AndAnUnknownPathIs404(t *testing.T) {
	rt := restroute.Build([]restroute.Route{
		{Op: "GetAlias", Method: "GET", Pattern: "/f/{Fn}", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return reply(w, r)
		}},
	}, options(t))
	if code, body := do(rt, "PUT", "/f/orders"); code != 405 || body != "MethodNotAllowed" {
		t.Errorf("wrong method = %d %q, want 405 MethodNotAllowed", code, body)
	}
	if code, body := do(rt, "GET", "/g/orders"); code != 404 || body != "NotFound" {
		t.Errorf("unknown path = %d %q, want 404 NotFound", code, body)
	}
}

// S3 tells operations apart by a query flag or a header on the same method
// and path. Candidates are tried in the order given; one with no Pick is the
// fallback.
func TestOperationsSharingAPathAreToldApartByTheirPick(t *testing.T) {
	has := func(flag string) func(*http.Request) bool {
		return func(r *http.Request) bool { _, ok := r.URL.Query()[flag]; return ok }
	}
	h := func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return reply(w, r) }
	rt := restroute.Build([]restroute.Route{
		{Op: "GetBucketTagging", Method: "GET", Pattern: "/{Bucket}", Pick: has("tagging"), Handler: h},
		{Op: "GetBucketVersioning", Method: "GET", Pattern: "/{Bucket}", Pick: has("versioning"), Handler: h},
		{Op: "ListObjects", Method: "GET", Pattern: "/{Bucket}", Handler: h},
	}, options(t))
	for target, want := range map[string]string{
		"/b?tagging":    "GetBucketTagging",
		"/b?versioning": "GetBucketVersioning",
		"/b":            "ListObjects",
		"/b?prefix=x":   "ListObjects",
	} {
		_, body := do(rt, "GET", target)
		if !strings.HasPrefix(body, want+"|") {
			t.Errorf("GET %s served %q, want %s", target, body, want)
		}
		if got := rt.Op(httptest.NewRequest("GET", target, nil)); got != want {
			t.Errorf("Op(GET %s) = %q, want %s", target, got, want)
		}
	}
}

// A pick that nothing satisfies, with no fallback, is a refusal rather than a
// silent pick of the wrong operation.
func TestNoCandidateAndNoFallbackIsNotFound(t *testing.T) {
	rt := restroute.Build([]restroute.Route{
		{Op: "GetBucketTagging", Method: "GET", Pattern: "/{Bucket}",
			Pick:    func(r *http.Request) bool { return r.URL.Query().Has("tagging") },
			Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return reply(w, r) }},
	}, options(t))
	if code, _ := do(rt, "GET", "/b"); code != 404 {
		t.Errorf("GET /b = %d, want 404", code)
	}
}

// chi routes on the escaped path, so an encoded slash is part of its segment
// and Param decodes it; with no escaping on the wire the path is already
// decoded and must not be decoded twice.
func TestEscapedSlashesStayInsideTheirSegment(t *testing.T) {
	rt := restroute.Build([]restroute.Route{
		{Op: "GetObject", Method: "GET", Pattern: "/{Bucket}/*", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return reply(w, r, restroute.Param(r, "Bucket"), restroute.Wildcard(r))
		}},
		{Op: "ListTags", Method: "GET", Pattern: "/tags/{Resource}", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return reply(w, r, restroute.Param(r, "Resource"))
		}},
	}, options(t))
	for target, want := range map[string]string{
		"/b/a/b/c":                    "GetObject|b|a/b/c",
		"/b/a%2Fb":                    "GetObject|b|a/b",
		"/b/dir/":                     "GetObject|b|dir/",
		"/b/50%2541":                  "GetObject|b|50%41", // a literal %41 in the key
		"/tags/arn:aws:lambda:x%2Ffn": "ListTags|arn:aws:lambda:x/fn",
	} {
		if _, body := do(rt, "GET", target); body != want {
			t.Errorf("GET %s = %q, want %q", target, body, want)
		}
	}
}

func TestAMiddlewareRunsAfterMatchingSoItCanReadTheOperation(t *testing.T) {
	o := options(t)
	var seen []string
	o.Use = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, restroute.Op(r))
			next.ServeHTTP(w, r)
		})
	}}
	rt := restroute.Build([]restroute.Route{
		{Op: "Invoke", Method: "POST", Pattern: "/f/{Fn}/invocations", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return reply(w, r) }},
	}, o)
	do(rt, "POST", "/f/orders/invocations")
	do(rt, "POST", "/nowhere")
	if len(seen) != 1 || seen[0] != "Invoke" {
		t.Errorf("middleware saw %q, want [Invoke] and only for the matched route", seen)
	}
}

func TestRoutesListsWhatWasRegistered(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return reply(w, r) }
	rt := restroute.Build([]restroute.Route{
		{Op: "A", Method: "GET", Pattern: "/a", Handler: h},
		{Op: "B", Method: "PUT", Pattern: "/a", Handler: h},
		{Op: "C", Method: "GET", Pattern: "/a", Pick: func(*http.Request) bool { return false }, Handler: h},
	}, options(t))
	got := strings.Join(rt.Routes(), ",")
	if got != "GET /a,PUT /a" {
		t.Errorf("Routes() = %q, want each method+pattern once, in order", got)
	}
}

func TestSetParamLetAHandlerRedispatch(t *testing.T) {
	rt := restroute.Build([]restroute.Route{
		{Op: "List", Method: "GET", Pattern: "/q", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			restroute.SetParam(r, "queue", "first")
			return reply(w, r, restroute.Param(r, "queue"))
		}},
	}, options(t))
	if _, body := do(rt, "GET", "/q"); body != "List|first" {
		t.Errorf("body = %q", body)
	}
}

// The JSON services have always forgiven a trailing slash and read an empty
// label as a label with no value, which validation then refuses; S3 must not,
// because a key may end in a slash and hold two in a row.
func TestTolerantRoutersTrimSlashesAndReadEmptyLabelsAsEmpty(t *testing.T) {
	o := options(t)
	o.Tolerant = true
	rt := restroute.Build([]restroute.Route{
		{Op: "ListFunctions", Method: "GET", Pattern: "/f", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return reply(w, r) }},
		{Op: "CreateAlias", Method: "POST", Pattern: "/f/{Fn}/aliases", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return reply(w, r, "["+restroute.Param(r, "Fn")+"]")
		}},
	}, o)
	if code, body := do(rt, "GET", "/f/"); code != 200 || body != "ListFunctions|" {
		t.Errorf("trailing slash = %d %q", code, body)
	}
	if code, body := do(rt, "POST", "/f//aliases"); code != 200 || body != "CreateAlias|[]" {
		t.Errorf("empty label = %d %q, want CreateAlias with an empty Fn", code, body)
	}
	if got := rt.Op(httptest.NewRequest("POST", "/f//aliases", nil)); got != "CreateAlias" {
		t.Errorf("Op of an empty label = %q", got)
	}
}

// A family a service refuses as a whole is one route for every method; and two
// methods may name the same position differently (CreateResource's {parentId}
// beside DeleteResource's {resourceId}).
func TestAnyMethodRoutesAndPerMethodLabelNames(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
		return reply(w, r, restroute.Param(r, "parentId")+restroute.Param(r, "resourceId"))
	}
	rt := restroute.Build([]restroute.Route{
		{Op: "CreateResource", Method: "POST", Pattern: "/r/{parentId}", Handler: h},
		{Op: "DeleteResource", Method: "DELETE", Pattern: "/r/{resourceId}", Handler: h},
		{Method: "", Pattern: "/gone/*", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return awshttp.Errf(501, "NotImplemented", "no")
		}},
	}, options(t))
	if _, body := do(rt, "POST", "/r/abc"); body != "CreateResource|abc" {
		t.Errorf("POST = %q", body)
	}
	if _, body := do(rt, "DELETE", "/r/xyz"); body != "DeleteResource|xyz" {
		t.Errorf("DELETE = %q", body)
	}
	for _, m := range []string{"GET", "PUT", "PATCH", "DELETE"} {
		if code, _ := do(rt, m, "/gone/a/b"); code != 501 {
			t.Errorf("%s /gone/a/b = %d, want 501", m, code)
		}
	}
}

// A handler that calls another service in-process passes its request context
// along, route context and all. The callee is a separate router and must route
// the call it was made, not the one its caller was serving.
func TestARouterIgnoresARouteContextItInheritedFromACaller(t *testing.T) {
	callee := restroute.Build([]restroute.Route{
		{Op: "Invoke", Method: "POST", Pattern: "/functions/{Fn}/invocations", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return reply(w, r, restroute.Param(r, "Fn"))
		}},
	}, options(t))
	caller := restroute.Build([]restroute.Route{
		{Op: "PutObject", Method: "PUT", Pattern: "/{Bucket}/*", Handler: func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			// The notification: a new request carrying this request's context.
			req := httptest.NewRequest("POST", "/functions/sink/invocations", nil).WithContext(r.Context())
			callee.ServeHTTP(w, req)
			return nil
		}},
	}, options(t))
	if code, body := do(caller, "PUT", "/bucket/key"); code != 200 || body != "Invoke|sink" {
		t.Errorf("callee answered %d %q, want its own route", code, body)
	}
}

// Match says what a request names without serving it: the operation and its
// labels, decoded as a handler would read them.
func TestMatchReturnsTheOperationAndItsLabels(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return nil }
	rt := restroute.Build([]restroute.Route{
		{Op: "GetAlias", Method: "GET", Pattern: "/f/{Fn}/aliases/{Name}", Handler: h},
		{Op: "ListTags", Method: "GET", Pattern: "/tags/*", Handler: h},
	}, options(t))
	op, labels := rt.Match(httptest.NewRequest("GET", "/f/orders%3Av1/aliases/live", nil))
	if op != "GetAlias" || labels["Fn"] != "orders:v1" || labels["Name"] != "live" {
		t.Errorf("Match = %q %v", op, labels)
	}
	if op, labels := rt.Match(httptest.NewRequest("GET", "/tags/arn%3Aaws%3Alambda%3A%3Afunction%2Fx", nil)); op != "ListTags" || labels["*"] != "arn:aws:lambda::function/x" {
		t.Errorf("greedy label = %q %v", op, labels)
	}
	if op, labels := rt.Match(httptest.NewRequest("GET", "/nothing", nil)); op != "" || labels != nil {
		t.Errorf("no route = %q %v", op, labels)
	}
}
