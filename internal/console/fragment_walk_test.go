package console_test

// A15, walked. fragments_test.go holds the two services that were found
// drawing less than their page after an action. This walks the routes that
// answer an htmx request with a region of a page, asks for the page, and
// compares the two: the fragment must be the same markup as the element with
// its id in a full load (the element is found by the id the fragment's root
// carries, so a fragment with no root id cannot be compared and is reported).
//
// The population is closed by TestEveryPartialIsClassified, which reads the
// source: a template named in a c.partial call is either a region (a case
// below), or an answer to a call (a result, a verdict, a receipt, a drawer, or
// rows a live region polls), with the reason. A partial added later fails
// until it is put in one.
//
// A case that has `adopts` set is EXPECTED to differ today and names the stage
// that moves its handler onto the view function its page uses. When it stops
// differing the test says to remove the mark, so the list only ever shrinks.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fragCase struct {
	tpl    string
	method string
	path   string // under /_console; {x} is replaced from pre's result
	form   url.Values
	page   string // a GET under /_console that draws the region
	// pre runs first and creates what the case addresses. What it returns
	// replaces {x} in path, form values and page.
	pre func(t *testing.T, h http.Handler) map[string]string
	// adopts, when set, says the fragment differs from its page today and which
	// stage moves it onto the page's own view function.
	adopts string
}

// elementByID returns the element whose id attribute is id, outer HTML.
func elementByID(html, id string) (string, bool) {
	at := strings.Index(html, `id="`+id+`"`)
	if at < 0 {
		return "", false
	}
	start := strings.LastIndex(html[:at], "<")
	return elementAt(html, start)
}

var tagName = regexp.MustCompile(`^<([a-zA-Z][a-zA-Z0-9-]*)`)

// elementAt returns the element that opens at html[start], balanced by its own
// tag name.
func elementAt(html string, start int) (string, bool) {
	m := tagName.FindStringSubmatch(html[start:])
	if m == nil {
		return "", false
	}
	name := m[1]
	open, closeTag := "<"+name, "</"+name+">"
	depth, i := 0, start
	for i < len(html) {
		o := strings.Index(html[i:], open)
		c := strings.Index(html[i:], closeTag)
		if c < 0 {
			return "", false
		}
		if o >= 0 && o < c {
			// "<div" must be the tag, not "<divider".
			if j := i + o + len(open); j < len(html) && strings.ContainsRune(" >\n\t/", rune(html[j])) {
				depth++
			}
			i += o + len(open)
			continue
		}
		depth--
		i += c + len(closeTag)
		if depth == 0 {
			return html[start:i], true
		}
	}
	return "", false
}

var rootID = regexp.MustCompile(`^\s*<[a-zA-Z][^>]*?\sid="([^"]+)"`)

func htmxGet(h http.Handler, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/_console"+path, nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func htmxDo(h http.Handler, method, path string, form url.Values) *httptest.ResponseRecorder {
	var r *http.Request
	if method == "GET" {
		r = httptest.NewRequest("GET", "/_console"+path, nil)
	} else {
		r = httptest.NewRequest(method, "/_console"+path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func vals(kv ...string) url.Values {
	out := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i], kv[i+1])
	}
	return out
}

// made runs the creates a case needs, in order, and reads an id out of the last
// redirect when the resource is addressed by a generated one.
func made(t *testing.T, h http.Handler, steps ...[2]any) *httptest.ResponseRecorder {
	t.Helper()
	var rec *httptest.ResponseRecorder
	for _, s := range steps {
		rec = postForm(t, h, s[0].(string), s[1].(url.Values))
	}
	return rec
}

var generatedID = regexp.MustCompile(`/_console/[a-z-]+/([0-9a-f]{8}-[0-9a-f-]{27}|[a-z0-9]{6,12})(?:[?/]|$)`)

func idOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	loc := rec.Header().Get("Location")
	m := generatedID.FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("no generated id in the redirect %q (status %d)\n%s", loc, rec.Code, clip(rec.Body.String()))
	}
	return m[1]
}

const (
	q1      = "arn:aws:sqs:us-east-1:000000000000:q1"
	pattern = `{"source":["demo"]}`
)

// fragmentRegions are the routes that answer with a region of a page.
func fragmentRegions() []fragCase {
	code := func(t *testing.T) string { return t.TempDir() }
	queue := [2]any{"/sqs/create", vals("name", "q1", "dlq_mode", "none")}
	return []fragCase{
		{tpl: "sqs_config", method: "POST", path: "/sqs/q1/attributes", form: vals("visibility", "45"),
			page: "/sqs/q1?tab=config",
			pre:  func(t *testing.T, h http.Handler) map[string]string { made(t, h, queue); return nil }},
		{tpl: "sns_subs", method: "POST", path: "/sns/t1/subscribe", form: vals("protocol", "sqs", "endpoint", q1),
			page: "/sns/t1?tab=subscriptions",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, queue, [2]any{"/sns/create", vals("name", "t1")})
				return nil
			}},
		{tpl: "s3_props", method: "POST", path: "/s3/bkt1/versioning", form: vals("enable", "true"),
			page: "/s3/bkt1?tab=properties",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/s3/create", vals("name", "bkt1")})
				return nil
			}},
		{tpl: "s3_notify", method: "POST", path: "/s3/bkt1/notify-add", form: vals("dest", "sqs:q1", "event", "s3:ObjectCreated:*"),
			page: "/s3/bkt1?tab=properties",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, queue, [2]any{"/s3/create", vals("name", "bkt1")})
				return nil
			}},
		{tpl: "object_table", method: "POST", path: "/s3/bkt1/folder", form: vals("name", "docs"),
			page: "/s3/bkt1",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/s3/create", vals("name", "bkt1")})
				return nil
			}},
		{tpl: "lambda_config", method: "POST", path: "/lambda/fn1/config", form: vals("timeout", "30", "memory", "256", "runtime", "provided.al2", "handler", "bootstrap"),
			page: "/lambda/fn1?tab=config",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/lambda/create", vals("name", "fn1", "runtime", "provided.al2", "handler", "bootstrap", "code", code(t))})
				return nil
			}},
		{tpl: "lambda_triggers", method: "POST", path: "/lambda/fn1/add-mapping", form: vals("queue", "q1", "batch", "5"),
			page: "/lambda/fn1?tab=triggers",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, queue, [2]any{"/lambda/create", vals("name", "fn1", "runtime", "provided.al2", "handler", "bootstrap", "code", code(t))})
				return nil
			}},
		{tpl: "kms_key_detail", method: "POST", path: "/kms/{id}/toggle-rotation",
			page: "/kms/{id}",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				rec := made(t, h, [2]any{"/kms/create", vals("spec", "SYMMETRIC_DEFAULT", "usage", "ENCRYPT_DECRYPT", "alias", "alias-one")})
				return map[string]string{"{id}": idOf(t, rec)}
			}},
		{tpl: "ddb_details", method: "POST", path: "/ddb/tbl1/ttl", form: vals("attr", "expires"),
			page: "/ddb/tbl1?tab=details",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/ddb/create", vals("name", "tbl1", "hash_key", "pk", "hash_type", "S")})
				return nil
			}},
		{tpl: "ddb_item_table", method: "POST", path: "/ddb/tbl1/put", form: vals("item", `{"pk":"a"}`),
			page: "/ddb/tbl1",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/ddb/create", vals("name", "tbl1", "hash_key", "pk", "hash_type", "S")})
				return nil
			}},
		{tpl: "eb_targets", method: "POST", path: "/eb/default/rule/r1/add-target", form: vals("arn", q1),
			page: "/eb/default/rule/r1",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, queue, [2]any{"/eb/default/create-rule", vals("name", "r1", "pattern", pattern)})
				return nil
			}},
		{tpl: "apigw_routes", method: "POST", path: "/apigw/{id}/add-resource", form: vals("part", "orders", "parent", "{root}"),
			page: "/apigw/{id}",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				rec := made(t, h, [2]any{"/apigw/create", vals("name", "api1")})
				id := idOf(t, rec)
				m := regexp.MustCompile(`addRes=\{id:(?:"|&#34;)([a-z0-9]+)`).FindStringSubmatch(htmxGet(h, "/apigw/"+id).Body.String())
				if m == nil {
					t.Fatalf("no root resource on the API page")
				}
				return map[string]string{"{id}": id, "{root}": m[1]}
			}},
		{tpl: "apigw_keys_body", method: "POST", path: "/apigw-keys/create", form: vals("name", "k1"),
			page: "/apigw-keys"},
		{tpl: "message_panel", method: "POST", path: "/sqs/q1/send", form: vals("body", "hello"),
			page: "/sqs/q1",
			pre:  func(t *testing.T, h http.Handler) map[string]string { made(t, h, queue); return nil }},
		{tpl: "apigw_authorizers", method: "POST", path: "/apigw/{id}/create-authorizer",
			form: vals("name", "gate", "type", "TOKEN", "function", "fn1", "source", "method.request.header.Authorization", "ttl", "300"),
			page: "/apigw/{id}?tab=settings",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/lambda/create", vals("name", "fn1", "runtime", "provided.al2", "handler", "bootstrap", "code", code(t))})
				rec := made(t, h, [2]any{"/apigw/create", vals("name", "api1")})
				return map[string]string{"{id}": idOf(t, rec)}
			}},
		{tpl: "apigw_http_routes", method: "POST", path: "/apigw-http/{id}/add-route", form: vals("method", "GET", "path", "/x", "url", "http://127.0.0.1:1/"),
			page: "/apigw-http/{id}",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				rec := made(t, h, [2]any{"/apigw/create", vals("name", "http1", "protocol", "HTTP")})
				return map[string]string{"{id}": idOf(t, rec)}
			}},
		{tpl: "lambda_layers", method: "POST", path: "/lambda/layers/publish", form: vals("name", "lib", "path", "{dir}", "runtimes", "provided.al2"),
			page: "/lambda",
			pre:  func(t *testing.T, h http.Handler) map[string]string { return map[string]string{"{dir}": code(t)} }},
		{tpl: "eb_http_body", method: "POST", path: "/eb/destinations/create-connection",
			form: vals("name", "conn1", "auth_type", "API_KEY", "api_key_name", "x-key", "api_key_value", "s3cret"),
			page: "/eb/destinations"},
		{tpl: "eb_archives_swap", method: "POST", path: "/eb/default/create-archive", form: vals("name", "arc1", "pattern", pattern),
			page: "/eb/default?tab=archives"},
		{tpl: "sfn_executions", method: "GET", path: "/sfn/m1/executions",
			page: "/sfn/m1?tab=executions",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/sfn/create", vals("name", "m1", "type", "STANDARD", "role", sfnRole, "definition", sfnDefinition)},
					[2]any{"/sfn/m1/start", vals("name", "e1")})
				return nil
			}},
		{tpl: "sqs_consumed", method: "POST", path: "/sqs/q1/receive", form: vals("max", "1"),
			page: "/sqs/q1?tab=consume",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, queue, [2]any{"/sqs/q1/send", vals("body", "hello")})
				return nil
			}},
		{tpl: "sfn_history", method: "GET", path: "/sfn/m1/execution/e1/history",
			page: "/sfn/m1/execution/e1",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/sfn/create", vals("name", "m1", "type", "STANDARD", "role", sfnRole, "definition", sfnDefinition)},
					[2]any{"/sfn/m1/start", vals("name", "e1")})
				return nil
			}},
		{tpl: "sfn_graph_live", method: "GET", path: "/sfn/m1/execution/e1/graph",
			page: "/sfn/m1/execution/e1?tab=graph",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/sfn/create", vals("name", "m1", "type", "STANDARD", "role", sfnRole, "definition", sfnDefinition)},
					[2]any{"/sfn/m1/start", vals("name", "e1")})
				return nil
			}},
		{tpl: "log_tail", method: "GET", path: "/lambda/fn1/logs",
			page: "/lambda/fn1?tab=logs",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/lambda/create", vals("name", "fn1", "runtime", "provided.al2", "handler", "bootstrap", "code", code(t))})
				return nil
			}},
		{tpl: "eb_archive_detail", method: "POST", path: "/eb/default/archive/arc1/update", form: vals("pattern", pattern, "retention", "7"),
			page: "frag:/eb/default/archive/arc1",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/eb/default/create-archive", vals("name", "arc1", "pattern", pattern)})
				return nil
			}},
		{tpl: "kinesis_record_table", method: "POST", path: "/kinesis/s1/records/query", form: vals("mode", "latest"),
			page: "/kinesis/s1/records",
			pre: func(t *testing.T, h http.Handler) map[string]string {
				made(t, h, [2]any{"/kinesis/create", vals("name", "s1", "shards", "1")})
				return nil
			}},
		// The editor loads itself into the page from the route that also answers
		// a save, so the page's region is that GET.
		{tpl: "tag_editor", method: "POST", path: "/tags/save", form: vals("svc", "sqs", "id", "q1", "tags", `{"env":"dev"}`),
			page: "frag:/tags/view?svc=sqs&id=q1",
			pre:  func(t *testing.T, h http.Handler) map[string]string { made(t, h, queue); return nil }},
	}
}

var oobPatch = regexp.MustCompile(`(?s)<span hx-swap-oob="innerHTML:#([^"]+)">(.*?)</span>`)

// compareRegions holds each top-level element of a fragment to the element of
// the page that carries its id. A fragment may be several elements (EventBridge
// swaps the archives and the replays together); every one must have an id and a
// twin.
func compareRegions(fragment, page, pageName string) (bool, string) {
	rest := strings.TrimSpace(fragment)
	if rest == "" {
		return true, "the fragment is empty"
	}
	for rest != "" {
		// Out-of-band counters ride along in a hidden element: each patches the
		// inside of an element the page has, so the page's own must read the same.
		if strings.HasPrefix(rest, "<div hidden>") {
			got, ok := elementAt(rest, 0)
			if !ok {
				return true, "the fragment's out-of-band block does not balance: " + clip(rest)
			}
			for _, oob := range oobPatch.FindAllStringSubmatch(got, -1) {
				twin, ok := elementByID(page, oob[1])
				if !ok {
					return true, "the fragment patches #" + oob[1] + ", which page " + pageName + " does not have"
				}
				inner := twin[strings.Index(twin, ">")+1 : strings.LastIndex(twin, "<")]
				if normalise(inner) != normalise(oob[2]) {
					return true, "the fragment patches #" + oob[1] + " to " + clip(oob[2]) + " and the page draws " + clip(inner)
				}
			}
			rest = strings.TrimSpace(rest[len(got):])
			continue
		}
		m := rootID.FindStringSubmatch(rest)
		if m == nil {
			return true, "an element of the fragment has no id, so no region of a page can be named for it: " + clip(rest)
		}
		got, ok := elementAt(rest, strings.Index(rest, "<"))
		if !ok {
			return true, "the fragment's markup does not balance at: " + clip(rest)
		}
		want, ok := elementByID(page, m[1])
		if !ok {
			return true, "page " + pageName + " has no #" + m[1]
		}
		f, r := normalise(got), normalise(want)
		if f != r {
			i := 0
			for i < len(f) && i < len(r) && f[i] == r[i] {
				i++
			}
			from := max(0, i-60)
			return true, "#" + m[1] + " differs at byte " + strconv.Itoa(i) + "\n  fragment: …" + f[from:min(len(f), i+160)] + "\n  page:     …" + r[from:min(len(r), i+160)]
		}
		rest = strings.TrimSpace(rest[strings.Index(rest, got)+len(got):])
	}
	return false, ""
}

func subst(s string, with map[string]string) string {
	for k, v := range with {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

func TestFragmentsAreRegionsOfTheirPage(t *testing.T) {
	for _, c := range fragmentRegions() {
		t.Run(c.tpl, func(t *testing.T) {
			h := newConsole(t)
			with := map[string]string{}
			if c.pre != nil {
				with = c.pre(t, h)
			}
			form := url.Values{}
			for k, v := range c.form {
				form[k] = []string{subst(v[0], with)}
			}
			frag := htmxDo(h, c.method, subst(c.path, with), form)
			if frag.Code != 200 || frag.Header().Get("HX-Doze-Error") != "" || frag.Header().Get("HX-Location") != "" {
				t.Fatalf("%s %s: status %d, error %q, location %q\n%s", c.method, c.path, frag.Code, frag.Header().Get("HX-Doze-Error"), frag.Header().Get("HX-Location"), clip(frag.Body.String()))
			}
			body := strings.TrimSpace(frag.Body.String())
			compare := func(body string) (bool, string) {
				var pageHTML string
				if from, ok := strings.CutPrefix(c.page, "frag:"); ok {
					// The page loads this region from a route of its own.
					g := htmxDo(h, "GET", subst(from, with), nil)
					if g.Code != 200 {
						t.Fatalf("GET %s: %d", from, g.Code)
					}
					pageHTML = g.Body.String()
				} else {
					page := htmxGet(h, subst(c.page, with))
					if page.Code != 200 {
						t.Fatalf("GET %s: %d", c.page, page.Code)
					}
					pageHTML = page.Body.String()
				}
				return compareRegions(body, pageHTML, c.page)
			}
			differs, detail := compare(body)
			// The fragment and the page are two requests. What the clock or a running
			// execution changes between them is transient; a fragment that shows less
			// than its page differs every time. So a GET is looked at again before it
			// is called a difference (a mutating route is never replayed).
			for i := 0; differs && c.method == "GET" && i < 5; i++ {
				time.Sleep(200 * time.Millisecond)
				again := htmxDo(h, c.method, subst(c.path, with), form)
				differs, detail = compare(strings.TrimSpace(again.Body.String()))
			}
			switch {
			case differs && c.adopts == "":
				t.Errorf("%s: the fragment shows less (or other) than its page: %s", c.tpl, detail)
			case !differs && c.adopts != "":
				t.Errorf("%s now matches its page; remove adopts %q", c.tpl, c.adopts)
			case differs:
				t.Logf("known, adopted in %s: %s", c.adopts, detail)
			}
		})
	}
}

// The reasons a partial has no region of a page to be held to.
const (
	noRegionAnswer   = "the answer to a call (a result, a verdict, a receipt, a generated document): the page never draws it"
	noRegionOnDemand = "loaded into a drawer or panel when asked: a page load never draws it"
	noRegionSameFn   = "the update re-renders by calling the GET handler that loads it, so there is one view function by construction"
	noRegionRows     = "rows appended to a table that is itself walked above"
	noRegionLazy     = "loaded into its slot by the page after the load, from the route that answers every refresh"
)

// notRegions is every partial that is not a region of a page, and why. The
// `owed` ones ARE regions of a page and have no case above yet: each names the
// stage that adds the case (and moves the handler onto the page's view function
// if the walk finds a difference).
var notRegions = map[string]string{
	"apigw_result":            noRegionAnswer,
	"sfn_diagnostics":         noRegionAnswer,
	"sfn_test_result":         noRegionAnswer,
	"sfn_sync_result":         noRegionAnswer,
	"kms_out":                 noRegionAnswer,
	"kms_verdict":             noRegionAnswer,
	"kms_crypto_result":       noRegionAnswer,
	"kms_sign_result":         noRegionAnswer,
	"kms_mac_result":          noRegionAnswer,
	"iam_sim_result":          noRegionAnswer,
	"iam_sim_inline":          noRegionAnswer,
	"iam_generated":           noRegionAnswer,
	"iam_sts_keyinfo":         noRegionAnswer,
	"iam_sts_creds":           noRegionAnswer,
	"iam_auth_details":        noRegionAnswer,
	"eb_pattern_verdict":      noRegionAnswer,
	"eb_rule_list":            "the verdicts of a sample event over the rules: the page draws the list with no event, so the two are not the same state",
	"eb_rules_by_target":      noRegionAnswer,
	"sns_receipt":             noRegionAnswer,
	"s3_share_link":           noRegionAnswer,
	"s3_name_check":           noRegionAnswer,
	"connect_result":          noRegionAnswer,
	"cfn_validate_result":     noRegionAnswer,
	"cfn_params":              noRegionAnswer,
	"lambda_result":           noRegionAnswer,
	"ssm_path_result":         noRegionAnswer,
	"value_diff":              noRegionAnswer,
	"sfn_activity_task":       noRegionAnswer,
	"sfn_activity_done":       noRegionAnswer,
	"info_panel":              noRegionOnDemand,
	"eb_kv_detail":            noRegionOnDemand,
	"eb_connection_detail":    noRegionSameFn,
	"eb_destination_detail":   noRegionSameFn,
	"apigw_authorizer_detail": noRegionOnDemand,
	"apigw_key_value":         noRegionOnDemand,
	"kinesis_record_detail":   noRegionOnDemand,
	"cfn_resource_detail":     noRegionOnDemand,
	"lambda_layer_detail":     noRegionOnDemand,
	"lambda_layer_versions":   noRegionOnDemand,
	"object_meta":             noRegionOnDemand,
	"s3_versions":             noRegionOnDemand,
	"s3_mp_uploads":           noRegionOnDemand,
	"sfn_version_detail":      noRegionOnDemand,
	"sfn_map_children":        noRegionOnDemand,
	"traffic_drawer":          noRegionOnDemand,
	"traffic_drawer_gone":     noRegionOnDemand,
	"traffic_rows":            noRegionLazy,
	"res_wire_rows":           noRegionLazy,
	"kinesis_depth":           noRegionOnDemand,
	"lambda_runtime_badge":    "one badge, polled by the page that draws it from this route",
	"ddb_item_rows":           noRegionRows,
	"kinesis_record_rows":     noRegionRows,
	"apigw_method":            "owed: API Gateway's stage (a method's panel is redrawn by five edits; the page opens it by POST /apigw/{id}/method)",
	"sm_secret_detail":        "walked by fragments_test.go (Secrets Manager)",
	"ssm_param_detail":        "walked by fragments_test.go (Parameter Store)",
}

// partialsInSource reads the template names the console hands to c.partial.
func partialsInSource(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`c\.partial\(w, "(\w+)"`)
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			out[m[1]] = f
		}
	}
	if len(out) < 60 {
		t.Fatalf("found %d partials; the source shape must have changed", len(out))
	}
	return out
}

func TestEveryPartialIsClassified(t *testing.T) {
	inSource := partialsInSource(t)
	walked := map[string]bool{}
	for _, c := range fragmentRegions() {
		walked[c.tpl] = true
	}
	var names []string
	for n := range inSource {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		_, not := notRegions[n]
		switch {
		case walked[n] && not:
			t.Errorf("%s is both walked and listed as not a region (%s)", n, notRegions[n])
		case !walked[n] && !not:
			t.Errorf("%s (%s) is a partial nobody has classified: add a case to fragmentRegions if it redraws a region of a page, or to notRegions with the reason it does not", n, inSource[n])
		}
	}
	for n := range notRegions {
		if _, ok := inSource[n]; !ok {
			t.Errorf("%s is classified but no handler renders it any more", n)
		}
	}
	for n := range walked {
		if _, ok := inSource[n]; !ok {
			t.Errorf("%s is walked but no handler renders it any more", n)
		}
	}
}
