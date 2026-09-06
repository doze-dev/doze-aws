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
	"time"
)

// redirectFloor is how many routes this sweep is known to drive as far as a
// redirect. It is a ratchet: raising it as fixtures improve is welcome,
// dropping below it means something regressed. It also stops the capability
// analysis below from making the sweep vacuous — if it ever classified every
// handler as partial-only, "all capable routes verified" would pass trivially.
const redirectFloor = 89

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
	"{bucket}":  "fixture-bucket",
	"{queue}":   "fixture-queue",
	"{topic}":   "fixture-topic",
	"{table}":   "fixture-table",
	"{stream}":  "fixture-stream",
	"{fn}":      "fixture-fn",
	"{key}":     "", // filled at run time from the seeded key
	"{bus}":     "fixture-bus",
	"{stack}":   "fixture-stack",
	"{cs}":      "fixture-cs",
	"{api}":     "", // discovered — API Gateway ids are generated
	"{rule}":    "fixture-rule",
	"{kind}":    "user",
	"{name}":    "fixture-user",
	"{shard}":   shardID(0),
	"{machine}": "fixture-machine",
	"{exec}":    "fixture-exec",
	"{alias}":   "fixture-alias",
	// A version nothing holds: DeleteStateMachineVersion on an absent
	// version succeeds, as on AWS, and the seeded alias pins version 1 so
	// deleting THAT would be refused — a refusal is not a redirect.
	"{n}":        "999",
	"{activity}": "fixture-activity",
}

// discovered holds values that cannot be written down in advance because they
// only exist once something has been created — an access key id, the local
// directory a function's code is read from.
var discovered = map[string]string{}

func shardID(n int) string { return fmt.Sprintf("shardId-%012d", n) }

// cfnTemplate1/2 differ by one resource, so a change set between them has a
// real diff — an identical pair would land the set in FAILED ("didn't contain
// changes") and the execute route could never redirect.
const cfnTemplate1 = `{"Resources":{"FixtureQueue":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"fixture-cfn-q"}}}}`

const cfnTemplate2 = `{"Resources":{"FixtureQueue":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"fixture-cfn-q"}},"FixtureTopic":{"Type":"AWS::SNS::Topic","Properties":{"TopicName":"fixture-cfn-t"}}}}`

// sfnDefinition is a runnable machine — one Pass into a Succeed — so a
// seeded execution finishes on its own and the stop route exercises the
// already-finished error path rather than racing the engine.
const sfnDefinition = `{"StartAt":"A","States":{"A":{"Type":"Pass","Next":"B"},"B":{"Type":"Succeed"}}}`

// sfnRole satisfies the model's required roleArn; nothing local evaluates it.
const sfnRole = "arn:aws:iam::000000000000:role/stepfunctions"

// sfnSlowDefinition waits long enough to be stopped mid-state, which is what
// makes its execution REDRIVABLE — a finished Pass → Succeed never is.
const sfnSlowDefinition = `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":300,"End":true}}}`

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
		{"/iam/create", url.Values{"kind": {"user"}, "name": {"fixture-renameme"}}},
		{"/iam/create", url.Values{"kind": {"role"}, "name": {"fixture-role"}}},
		{"/iam/create", url.Values{"kind": {"group"}, "name": {"fixture-group"}}},
		{"/iam/create", url.Values{"kind": {"profile"}, "name": {"fixture-profile"}}},
		// A DEDICATED policy for the version routes — the policy-delete
		// subtest consumes fixture-policy, which sorts before delete-version.
		// Its non-default second version is what delete-version may delete
		// (the default refuses). Versions pile up one per seed run; the
		// emulator has no five-version cap to trip.
		{"/iam/create", url.Values{
			"kind": {"policy"}, "name": {"fixture-vpolicy"}, "document": {policyDoc},
		}},

		{"/eb/create-bus", url.Values{"name": {"fixture-bus"}}},
		{"/sm/create", url.Values{"name": {"fixture-secret"}, "value": {"v"}}},
		{"/ssm/create", url.Values{"name": {"/fixture/param"}, "type": {"String"}, "value": {"v"}}},
		{"/logs/create", url.Values{"name": {"/fixture/logs"}, "days": {"7"}}},
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
		// The stack, then a change set over it with a genuine one-resource
		// diff. Once the execute subtest deploys template2 the re-seeded set
		// lands in FAILED (empty diff) — harmless, nothing later executes it.
		{"/cfn/create", url.Values{"name": {"fixture-stack"}, "template": {cfnTemplate1}}},
		{"/cfn/fixture-stack/update", url.Values{
			"review": {"1"}, "changeset": {"fixture-cs"}, "template": {cfnTemplate2},
		}},
		{"/eb/fixture-bus/create-rule", url.Values{
			"name": {"fixture-rule"}, "pattern": {`{"source":["demo"]}`},
		}},
		// The machine, and one execution of it for the execution routes to
		// address. It finishes on its own; re-seeding it afterwards is refused
		// with ExecutionAlreadyExists, which the seeder tolerates, and
		// stopping a finished execution answers with when it stopped.
		{"/sfn/create", url.Values{"name": {"fixture-machine"}, "type": {"STANDARD"}, "role": {sfnRole}, "definition": {sfnDefinition}}},
		{"/sfn/fixture-machine/start", url.Values{"name": {"fixture-exec"}}},
		// A version for the alias to route to (publishing an unchanged
		// revision answers the existing version, so this never piles up),
		// the alias itself (idempotent on the same routing), and an activity.
		{"/sfn/fixture-machine/publish", url.Values{"description": {"fixture"}}},
		{"/sfn/fixture-machine/alias/create", url.Values{"name": {"fixture-alias"}, "v1": {"1"}, "w1": {"100"}}},
		{"/sfn/activities/create", url.Values{"name": {"fixture-activity"}}},
		// The redrive route needs an execution that stopped short: a Wait,
		// started and then aborted (see below, after the seeds).
		{"/sfn/create", url.Values{"name": {"fixture-slow"}, "type": {"STANDARD"}, "role": {sfnRole}, "definition": {sfnSlowDefinition}}},
		{"/sfn/fixture-slow/start", url.Values{"name": {"fixture-halted"}}},
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
	// Abort the slow execution once it is parked in its Wait — stopping it
	// before the engine has entered a state leaves nothing to redrive. The
	// history is polled the way the page polls it; a re-seed after the
	// redrive subtest finds it RUNNING again and aborts it again.
	for i := 0; i < 40; i++ {
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_console/sfn/fixture-slow/execution/fixture-halted/history", nil))
		if strings.Contains(rec.Body.String(), "WaitStateEntered") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	postForm(t, c, "/sfn/fixture-slow/execution/fixture-halted/stop", url.Values{"error": {"Fixture"}})
	// The API Gateway fixture is id-addressed and the sweep's own delete
	// route consumes it, so it is probed and re-created (with the fresh id
	// re-discovered from the create redirect) rather than written down.
	if fixtures["{api}"] == "" || !pageOK(c, "/apigw/"+fixtures["{api}"]) {
		rec := postForm(t, c, "/apigw/create", url.Values{"name": {"fixture-api"}})
		if m := apigwID.FindStringSubmatch(flashOf(rec)); m != nil {
			fixtures["{api}"] = m[1]
		}
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
	// The version-routes policy gets its second (non-default) version ONCE —
	// per-seed would hit the five-version cap before the new-version subtest
	// gets its turn.
	if discovered["vpolicySeeded"] == "" {
		postForm(t, c, "/iam/policy/new-version", url.Values{
			"arn": {"arn:aws:iam::000000000000:policy/fixture-vpolicy"}, "document": {policyDoc},
		})
		discovered["vpolicySeeded"] = "yes"
	}
	// A second user + key pair for the toggle route (see overrideFor).
	if discovered["accessKey2"] == "" {
		postForm(t, c, "/iam/create", url.Values{"kind": {"user"}, "name": {"fixture-keyuser"}})
		rec := postForm(t, c, "/iam/user/fixture-keyuser/keys", nil)
		if id := accessKeyID.FindString(flashOf(rec)); id != "" {
			discovered["accessKey2"] = id
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

// pageOK reports whether a console GET renders 200 — the existence probe for
// discovered fixtures that a delete route may have consumed.
func pageOK(c http.Handler, path string) bool {
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_console"+path, nil))
	return rec.Code == http.StatusOK
}

// apigwID matches the generated id in the create redirect's target.
var apigwID = regexp.MustCompile(`/apigw/([a-z0-9]{6,})`)

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
	case "/iam/group/{name}/member":
		return map[string]string{"{name}": "fixture-group"}, url.Values{"user": {"fixture-user"}}
	case "/iam/group/{name}/rename":
		return map[string]string{"{name}": "fixture-group"}, url.Values{"new": {"fixture-group"}}
	case "/iam/user/{name}/join-group":
		return nil, url.Values{"group": {"fixture-group"}}
	case "/iam/user/{name}/rename":
		return map[string]string{"{name}": "fixture-renameme"}, url.Values{"new": {"fixture-renamed"}}
	case "/iam/user/{name}/keys/toggle":
		// A key on a user nothing else touches: the keys/delete subtest
		// consumes fixture-user's discovered key before toggle's turn.
		return map[string]string{"{name}": "fixture-keyuser"},
			url.Values{"id": {discovered["accessKey2"]}, "active": {"1"}}
	case "/iam/profile/{name}/role":
		return map[string]string{"{name}": "fixture-profile"}, url.Values{"role": {"fixture-role"}}
	case "/iam/role/{name}/trust", "/iam/role/{name}/meta":
		return map[string]string{"{name}": "fixture-role"}, nil
	case "/iam/policy/new-version", "/iam/policy/set-default", "/iam/policy/delete-version":
		v := url.Values{"arn": {"arn:aws:iam::000000000000:policy/fixture-vpolicy"}, "document": {policyDoc}}
		if route == "/iam/policy/set-default" {
			v.Set("version", "v1")
		}
		if route == "/iam/policy/delete-version" {
			v.Set("version", "v2")
		}
		return nil, v
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
	case "/sfn/create":
		// The shared form's "type" is SSM's String; a machine is STANDARD.
		return nil, url.Values{"name": {"fixture-made-machine"}, "type": {"STANDARD"}, "role": {sfnRole}, "definition": {sfnDefinition}}
	case "/sfn/{machine}/definition":
		return nil, url.Values{"definition": {sfnDefinition}}
	case "/sfn/{machine}/alias/create":
		// A second alias on the seeded version; the seeded one is the
		// delete and update routes' to consume.
		return nil, url.Values{"name": {"fixture-made-alias"}, "v1": {"1"}, "w1": {"100"}}
	case "/sfn/{machine}/alias/{alias}/update":
		return nil, url.Values{"v1": {"1"}, "w1": {"100"}, "description": {"fixture, updated"}}
	case "/sfn/{machine}/execution/{exec}/redrive":
		// The finished fixture execution SUCCEEDED, which is the one status
		// that can never be redriven; the aborted Wait can.
		return map[string]string{"{machine}": "fixture-slow", "{exec}": "fixture-halted"}, nil
	case "/apigw/{api}/update-stage":
		// The stage the deploy subtest created; mutationForm's generic name
		// would PATCH a stage that does not exist.
		return nil, url.Values{"name": {"dev"}, "deployment": {"repointed"}}
	case "/logs/retention":
		// The group the create route makes is deleted by the delete route
		// before this runs (alphabetical order), so it addresses the seed.
		return nil, url.Values{"name": {"/fixture/logs"}, "days": {"14"}}
	case "/logs/delete-stream":
		return nil, url.Values{"name": {"/fixture/logs"}, "stream": {"never-written"}}
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
		"template":   {cfnTemplate1},
		"deployment": {"nonesuch"}, "part": {"fixture-part"},
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
