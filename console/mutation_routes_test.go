package console_test

// Every mutation route, checked for the one property that broke: a handler
// that redirects must send the browser somewhere inside the console, and that
// somewhere must render.
//
// The routes are read out of console.go rather than listed here, so a route
// added later is covered without anyone remembering to add it. The check does
// not require the mutation to succeed — a handler given bad input renders an
// error instead of redirecting, and that is fine. What is never fine is doing
// the work and then sending the browser to a 404, which is what 27 handlers
// did while returning a perfectly correct 303.
//
// Only a minority of the console's mutations redirect at all: most are htmx
// handlers that render a fragment in place, and for those there is no redirect
// target to get wrong. So the sweep splits the routes in two and holds every
// route that CAN redirect to the property. That capability is also read from
// the source, so a handler that gains or loses a redirect moves between the
// two populations on its own.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// redirectFloor is how many routes this sweep is known to drive as far as a
// redirect. It is a ratchet: raising it as fixtures improve is welcome,
// dropping below it means something regressed. It also stops the capability
// analysis below from making the sweep vacuous — if it ever classified every
// handler as partial-only, "all capable routes verified" would pass trivially.
const redirectFloor = 45

// splitPoint is 2^127 — the midpoint of the hash space, so it falls strictly
// inside the single shard of a freshly created stream.
const splitPoint = "170141183460469231731687303715884105728"

var postRoutePattern = regexp.MustCompile(`"POST "\+p\+"([^"]*)",\s*c\.(\w+)\)`)

// postRoutes reads the registered mutation routes, and the handler each one
// dispatches to, from the router itself.
func postRoutes(t *testing.T) (routes []string, handlers map[string]string) {
	t.Helper()
	src, err := os.ReadFile("console.go")
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	handlers = map[string]string{}
	for _, m := range postRoutePattern.FindAllStringSubmatch(string(src), -1) {
		routes = append(routes, m[1])
		handlers[m[1]] = m[2]
	}
	if len(routes) < 50 {
		t.Fatalf("only found %d POST routes; the router's shape must have changed", len(routes))
	}
	sort.Strings(routes)
	return routes, handlers
}

var getRoutePattern = regexp.MustCompile(`"GET "\+p\+"([^"]*)",\s*c\.(\w+)\)`)

// getRoutes returns the GET patterns that serve a specific page, dropping the
// subtree ones. This matters more than it looks: the router registers
// "GET /" as a catch-all, so *any* path under the console renders the flows
// page with 200 — which would make "the target must render" true by
// construction. Requiring the target to match a route that actually exists is
// what turns that half of the property back into a check.
func getRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("console.go")
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	var out []string
	for _, m := range getRoutePattern.FindAllStringSubmatch(string(src), -1) {
		if !strings.HasSuffix(m[1], "/") {
			out = append(out, m[1])
		}
	}
	if len(out) < 30 {
		t.Fatalf("only found %d specific GET routes; the router's shape must have changed", len(out))
	}
	return out
}

// servedByARoute reports whether a redirect target resolves to one of those
// routes. A {param} stands for exactly one segment, as it does in the router.
func servedByARoute(path string, routes []string) bool {
	if path == "" || path == "/" {
		return true // the console root
	}
	want := strings.Split(strings.Trim(path, "/"), "/")
	for _, r := range routes {
		got := strings.Split(strings.Trim(r, "/"), "/")
		if len(got) != len(want) {
			continue
		}
		ok := true
		for i := range got {
			if strings.HasPrefix(got[i], "{") {
				ok = want[i] != ""
			} else {
				ok = got[i] == want[i]
			}
			if !ok {
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

var (
	funcPattern = regexp.MustCompile(`(?m)^func \(c \*Console\) (\w+)\(`)
	callPattern = regexp.MustCompile(`c\.(\w+)\(`)
)

// consoleFuncBodies returns every Console method in the package, keyed by name.
func consoleFuncBodies(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package: %v", err)
	}
	bodies := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(raw)
		for _, m := range funcPattern.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			body := src[m[0]:]
			if end := strings.Index(body, "\n}\n"); end >= 0 {
				body = body[:end]
			}
			bodies[name] = body
		}
	}
	if len(bodies) < 50 {
		t.Fatalf("only found %d Console methods; the handler shape must have changed", len(bodies))
	}
	return bodies
}

// canRedirect reports whether a handler is able to redirect at all, following
// the helpers it calls. c.redirect is the common path; a handful of handlers
// set the htmx header themselves.
func canRedirect(name string, bodies map[string]string, seen map[string]bool) bool {
	if seen[name] {
		return false
	}
	seen[name] = true
	body, ok := bodies[name]
	if !ok {
		return false
	}
	if strings.Contains(body, "c.redirect(") ||
		strings.Contains(body, "HX-Redirect") ||
		strings.Contains(body, "http.Redirect(") {
		return true
	}
	for _, m := range callPattern.FindAllStringSubmatch(body, -1) {
		if canRedirect(m[1], bodies, seen) {
			return true
		}
	}
	return false
}

// fixtures names a real resource for each path parameter, so routes are
// exercised against something that exists wherever that is cheap to arrange.
// A parameter with no fixture still gets a value: the handler then fails to
// find it, which exercises the error path — also a place redirects happen.
var fixtures = map[string]string{
	"{bucket}": "fixture-bucket",
	"{queue}":  "fixture-queue",
	"{topic}":  "fixture-topic",
	"{table}":  "fixture-table",
	"{stream}": "fixture-stream",
	"{fn}":     "fixture-fn",
	"{key}":    "", // filled at run time from the seeded key
	"{bus}":    "fixture-bus",
	"{stack}":  "fixture-stack",
	"{api}":    "fixture-api",
	"{rule}":   "fixture-rule",
	"{kind}":   "user",
	"{name}":   "fixture-user",
	"{shard}":  shardID(0),
}

// discovered holds values that cannot be written down in advance because they
// only exist once something has been created — an access key id, the local
// directory a function's code is read from.
var discovered = map[string]string{}

func shardID(n int) string { return fmt.Sprintf("shardId-%012d", n) }

// policyDoc is a syntactically valid policy; nothing here evaluates it.
const policyDoc = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`

// seedFixtures creates one resource of each kind the routes address. Creating
// something that already exists is refused harmlessly, so this is safe to call
// before every route — which it has to be, because the sweep includes the
// delete routes and subtests run in sorted order.
func seedFixtures(t *testing.T, c http.Handler) {
	t.Helper()
	seeds := []struct {
		path string
		form url.Values
	}{
		{"/s3/create", url.Values{"name": {"fixture-bucket"}}},
		{"/sqs/create", url.Values{"name": {"fixture-queue"}}},
		{"/sns/create", url.Values{"name": {"fixture-topic"}}},
		{"/kinesis/create", url.Values{"name": {"fixture-stream"}, "shards": {"1"}}},
		{"/iam/create", url.Values{"kind": {"user"}, "name": {"fixture-user"}}},
		{"/eb/create-bus", url.Values{"name": {"fixture-bus"}}},
		{"/sm/create", url.Values{"name": {"fixture-secret"}, "value": {"v"}}},
		{"/ssm/create", url.Values{"name": {"/fixture/param"}, "type": {"String"}, "value": {"v"}}},
		{"/ddb/create", url.Values{
			"name": {"fixture-table"}, "hash_key": {"pk"}, "hash_type": {"S"},
		}},
		{"/kms/create", url.Values{
			"spec": {"SYMMETRIC_DEFAULT"}, "usage": {"ENCRYPT_DECRYPT"}, "alias": {"fixture-key"},
		}},
		// A principal for the delete route to consume. It cannot share the one
		// above: "attach" sorts before "delete", so by the time the sweep gets
		// there the shared user carries policies IAM refuses to orphan.
		{"/iam/create", url.Values{"kind": {"user"}, "name": {"fixture-doomed"}}},
		// A customer-managed policy. The AWS-managed arn the attach routes use
		// cannot be deleted, which is the whole point of it.
		{"/iam/create", url.Values{
			"kind": {"policy"}, "name": {"fixture-policy"}, "document": {policyDoc},
		}},
		{"/eb/fixture-bus/create-rule", url.Values{
			"name": {"fixture-rule"}, "pattern": {`{"source":["demo"]}`},
		}},
		// Reshaping closes the shards it operates on, so merge and split each
		// get a stream nothing else in the sweep touches.
		{"/kinesis/create", url.Values{"name": {"fixture-merge"}, "shards": {"2"}}},
		{"/kinesis/create", url.Values{"name": {"fixture-split"}, "shards": {"1"}}},
		{"/lambda/create", url.Values{
			"name": {"fixture-fn"}, "runtime": {"provided.al2"},
			"handler": {"bootstrap"}, "code": {discovered["code"]},
		}},
	}
	for _, s := range seeds {
		postForm(t, c, s.path, s.form)
	}
	// The KMS routes take a key id in the path. An alias would carry a slash
	// and never match the route pattern, so the id comes from where the create
	// redirect points.
	if fixtures["{key}"] == "" {
		rec := postForm(t, c, "/kms/create", url.Values{
			"spec": {"SYMMETRIC_DEFAULT"}, "usage": {"ENCRYPT_DECRYPT"},
		})
		if m := keyID.FindStringSubmatch(flashOf(rec)); m != nil {
			fixtures["{key}"] = m[1]
		}
	}
	// An access key id is only ever legible in the response that creates it.
	if discovered["accessKey"] == "" {
		rec := postForm(t, c, "/iam/user/fixture-user/keys", nil)
		if id := accessKeyID.FindString(flashOf(rec)); id != "" {
			discovered["accessKey"] = id
		}
	}
}

// keyID matches the UUID a KMS key is named by.
var keyID = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// accessKeyID matches the id half of a freshly minted access key.
var accessKeyID = regexp.MustCompile(`AKIA[A-Za-z0-9]+`)

// flashOf returns wherever a handler said to go next, by either mechanism.
func flashOf(rec *httptest.ResponseRecorder) string {
	return rec.Header().Get("Location") + rec.Header().Get("HX-Redirect")
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

func fillParams(route string, override map[string]string) string {
	return pathParam.ReplaceAllStringFunc(route, func(p string) string {
		if v, ok := override[p]; ok {
			return v
		}
		if v, ok := fixtures[p]; ok {
			return v
		}
		return "fixture"
	})
}

// overrideFor adapts the shared fixtures and form for the routes that cannot
// use them — because the route consumes what it addresses, or because it needs
// a value that only exists once something has been created.
func overrideFor(route string) (path map[string]string, form url.Values) {
	switch route {
	case "/iam/{kind}/{name}/delete":
		return map[string]string{"{name}": "fixture-doomed"}, nil
	case "/iam/policy/delete":
		return nil, url.Values{"arn": {"arn:aws:iam::000000000000:policy/fixture-policy"}}
	case "/iam/user/{name}/keys/delete":
		return nil, url.Values{"id": {discovered["accessKey"]}}
	case "/kinesis/{stream}/encryption":
		// The key has to be one KMS will admit to holding.
		return nil, url.Values{"key": {fixtures["{key}"]}}
	case "/kinesis/{stream}/merge":
		return map[string]string{"{stream}": "fixture-merge"},
			url.Values{"left": {shardID(0)}, "right": {shardID(1)}}
	case "/kinesis/{stream}/split":
		return map[string]string{"{stream}": "fixture-split"},
			url.Values{"shard": {shardID(0)}, "at": {splitPoint}}
	case "/lambda/create":
		// A function needs somewhere real to read its code from, even though
		// nothing here invokes it.
		return nil, url.Values{
			"name": {"fixture-made-fn"}, "runtime": {"provided.al2"},
			"handler": {"bootstrap"}, "code": {discovered["code"]},
		}
	}
	return nil, nil
}

// mutationForm carries a superset of the fields the handlers read. Unknown
// fields are ignored, so one form serves every route and the test stays about
// redirects rather than about each handler's arguments.
func mutationForm() url.Values {
	return url.Values{
		"name": {"fixture-made"}, "shards": {"1"}, "hours": {"48"},
		"kind": {"user"}, "policy": {"fixture-policy"},
		"document": {policyDoc},
		"trust":    {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`},
		"arn":      {"arn:aws:iam::aws:policy/ReadOnlyAccess"},
		"body":     {"{}"}, "data": {"{}"}, "value": {"v"}, "key": {"k"},
		"partitionKey": {"pk"}, "mode": {"PROVISIONED"}, "stage": {"dev"},
		"method": {"GET"}, "path": {"/"}, "limit": {"10"},
		"left": {"a"}, "right": {"b"}, "at": {"1"},
		// tags, subscriptions, items and rules each need a couple of fields
		// before the handler will get as far as redirecting.
		"svc": {"s3"}, "id": {"fixture-bucket"},
		"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:fixture-queue"},
		"item":    {`{"pk":{"S":"x"}}`},
		"pattern": {`{"source":["demo"]}`},
		"type":    {"String"}, "description": {"fixture"},
		"hash_key": {"pk"}, "hash_type": {"S"},
		"spec": {"SYMMETRIC_DEFAULT"}, "usage": {"ENCRYPT_DECRYPT"},
		"alias": {"fixture-alias"}, "label": {"fixture-label"},
	}
}

func TestEveryMutationRedirectStaysInTheConsole(t *testing.T) {
	c := newConsole(t)
	// Owned by the parent so it outlives every subtest that seeds a function.
	discovered["code"] = t.TempDir()

	routes, handlers := postRoutes(t)
	pages := getRoutes(t)
	bodies := consoleFuncBodies(t)
	capable := map[string]bool{}
	for _, route := range routes {
		capable[route] = canRedirect(handlers[route], bodies, map[string]bool{})
	}

	// Counted because a sweep that passes with nothing redirecting proves
	// nothing. Subtests here run in order, so plain slices are enough.
	var checked, partial, missed []string

	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			// Re-seed per route. Subtests run in order and the sweep includes
			// the delete routes, so without this every route sorting after
			// "delete" would operate on something already removed and never
			// reach the redirect it is here to check.
			seedFixtures(t, c)
			pathOverride, formOverride := overrideFor(route)
			path := fillParams(route, pathOverride) // re-resolved: ids are discovered
			form := mutationForm()
			for k, v := range formOverride {
				form[k] = v
			}
			rec := postForm(t, c, path, form)

			// A redirect can arrive either way: htmx uses a header, a plain
			// form submit uses 303 + Location.
			loc := rec.Header().Get("Location")
			if h := rec.Header().Get("HX-Redirect"); h != "" {
				loc = h
			}
			if loc == "" {
				// No redirect. For a handler that renders a fragment that is
				// the whole story; for one that can redirect, the sweep failed
				// to give it what it needed and the target goes unchecked.
				if rec.Code >= 500 {
					t.Fatalf("POST %s = %d: %s", path, rec.Code, truncate(rec.Body.String()))
				}
				entry := fmt.Sprintf("%s (%d)", route, rec.Code)
				if capable[route] {
					missed = append(missed, entry+" "+truncate(rec.Body.String()))
				} else {
					partial = append(partial, entry)
				}
				return
			}
			checked = append(checked, route)
			if !strings.HasPrefix(loc, "/_console/") && loc != "/_console" {
				t.Fatalf("POST %s redirected out of the console: %s", path, loc)
			}
			target := strings.TrimPrefix(loc, "/_console")
			if i := strings.IndexAny(target, "?#"); i >= 0 {
				target = target[:i]
			}
			if !servedByARoute(target, pages) {
				t.Fatalf("POST %s redirected to %s, which no console route serves — "+
					"the subtree catch-all renders it as the home page, so the user "+
					"lands somewhere they did not ask for", path, loc)
			}
			follow := httptest.NewRecorder()
			c.ServeHTTP(follow, httptest.NewRequest(http.MethodGet, loc, nil))
			if follow.Code != http.StatusOK {
				t.Fatalf("POST %s redirected to %s, which returned %d", path, loc, follow.Code)
			}
			if body := follow.Body.String(); strings.Contains(body, "template:") {
				i := strings.Index(body, "template:")
				t.Fatalf("POST %s redirected to %s, which rendered a template error: %s",
					path, loc, truncate(body[i:]))
			}
		})
	}

	// Say plainly what each population is, so the headline number is not read
	// as coverage of something it was never about.
	var capableCount int
	for _, ok := range capable {
		if ok {
			capableCount++
		}
	}
	t.Logf("verified the redirect target of %d/%d redirect-capable routes", len(checked), capableCount)
	t.Logf("%d routes render a partial and never redirect (the property does not apply)", len(partial))

	if len(missed) > 0 {
		t.Errorf("%d route(s) can redirect but the sweep never got one out of them — "+
			"a fixture is wrong or a handler stopped redirecting:\n  %s",
			len(missed), strings.Join(missed, "\n  "))
	}
	if len(checked) < redirectFloor {
		t.Fatalf("only %d routes reached a redirect, below the %d previously verified: "+
			"a fixture has broken or a handler stopped redirecting", len(checked), redirectFloor)
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
