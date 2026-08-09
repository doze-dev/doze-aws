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

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// redirectFloor is how many routes this sweep is known to drive as far as a
// redirect. It is a ratchet: raising it as fixtures improve is welcome,
// dropping below it means something regressed.
const redirectFloor = 36

var postRoutePattern = regexp.MustCompile(`"POST "\+p\+"([^"]*)"`)

// postRoutes reads the registered mutation routes from the router itself.
func postRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("console.go")
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	var out []string
	for _, m := range postRoutePattern.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	if len(out) < 50 {
		t.Fatalf("only found %d POST routes; the router's shape must have changed", len(out))
	}
	sort.Strings(out)
	return out
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
	"{shard}":  "shardId-000000000000",
}

// seedFixtures creates one resource of each kind the routes address. Creating
// something that already exists is refused harmlessly, so this is safe to call
// before every route.
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
		loc := rec.Header().Get("Location") + rec.Header().Get("HX-Redirect")
		if m := keyID.FindStringSubmatch(loc); m != nil {
			fixtures["{key}"] = m[1]
		}
	}
}

// keyID matches the UUID a KMS key is named by.
var keyID = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

func fillParams(route string) string {
	return pathParam.ReplaceAllStringFunc(route, func(p string) string {
		if v, ok := fixtures[p]; ok {
			return v
		}
		return "fixture"
	})
}

// mutationForm carries a superset of the fields the handlers read. Unknown
// fields are ignored, so one form serves every route and the test stays about
// redirects rather than about each handler's arguments.
func mutationForm() url.Values {
	return url.Values{
		"name": {"fixture-made"}, "shards": {"1"}, "hours": {"48"},
		"kind": {"user"}, "policy": {"fixture-policy"},
		"document": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
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
	// Counted because a sweep that passes with nothing redirecting proves
	// nothing. Subtests here run in order, so plain slices are enough.
	var checked, skipped []string

	for _, route := range postRoutes(t) {
		t.Run(route, func(t *testing.T) {
			// Re-seed per route. Subtests run in order and the sweep includes
			// the delete routes, so without this every route sorting after
			// "delete" would operate on something already removed and never
			// reach the redirect it is here to check.
			seedFixtures(t, c)
			path := fillParams(route) // re-resolved: the KMS id is discovered
			rec := postForm(t, c, path, mutationForm())

			// A redirect can arrive either way: htmx uses a header, a plain
			// form submit uses 303 + Location.
			loc := rec.Header().Get("Location")
			if h := rec.Header().Get("HX-Redirect"); h != "" {
				loc = h
			}
			if loc == "" {
				// No redirect: the handler rendered something. That is a valid
				// outcome — it just is not what this test is about.
				if rec.Code >= 500 {
					t.Fatalf("POST %s = %d: %s", path, rec.Code, truncate(rec.Body.String()))
				}
				skipped = append(skipped, fmt.Sprintf("%s (%d)", route, rec.Code))
				return
			}
			checked = append(checked, route)
			if !strings.HasPrefix(loc, "/_console/") && loc != "/_console" {
				t.Fatalf("POST %s redirected out of the console: %s", path, loc)
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

	// Say plainly what was and was not reached. The routes below need state
	// this sweep does not build — a receipt handle, a subscription ARN, a
	// deployed function, a multipart upload — so their redirect targets are
	// unverified. Naming them keeps that a visible gap rather than a silent
	// one, and the floor below stops the verified set shrinking unnoticed.
	t.Logf("verified the redirect target of %d/%d mutation routes", len(checked), len(postRoutes(t)))
	if len(skipped) > 0 {
		t.Logf("not exercised (handler refused before redirecting, or never redirects):\n  %s",
			strings.Join(skipped, "\n  "))
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
