package console_test

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A link built from a resource's name survives the characters the service
// allows in it. The templates assembled "?name=%s" with printf, which html/template
// treats as an already-built URL and does not query-escape, so a "+" arrived as
// a space and a "#" cut the link short. Secrets Manager allows both.
//
// html/template escapes for a URL only inside an attribute it knows to be one
// (href, action, src). hx-get, hx-post and data-live are plain text to it, so
// a name written into them is HTML-escaped and nothing more; and inside an
// href it normalises a path rather than escaping it, so a "/", "#" or "?" in a
// name still cuts the path. Every name a template puts in a URL therefore
// passes through pathesc (a path segment) or urlquery (a query value), and
// every one a handler puts in a redirect through seg or urlQuery.

var (
	// An opening tag with its attributes; a ">" inside a quoted value (an
	// Alpine expression) does not end it.
	tagRE  = regexp.MustCompile(`<(\w+)((?:[^<>"']|"[^"]*"|'[^']*')*)>`)
	linkRE = regexp.MustCompile(`\s(href|hx-get|data-live)="([^"]*)"`)
)

type pageLink struct {
	attr, raw string
}

// linksOn returns the console links a page carries, HTML-unescaped: what the
// browser holds before it resolves them. A form's hx-get is not one: it is
// sent with the form's fields, and bare it is a different request.
func linksOn(body string) []pageLink {
	var out []pageLink
	for _, tag := range tagRE.FindAllStringSubmatch(body, -1) {
		if tag[1] == "form" {
			continue
		}
		for _, m := range linkRE.FindAllStringSubmatch(tag[2], -1) {
			l := html.UnescapeString(m[2])
			if !strings.HasPrefix(l, "/_console/") || strings.HasPrefix(l, "/_console/static/") {
				continue
			}
			out = append(out, pageLink{m[1], l})
		}
	}
	return out
}

// asABrowserSendsIt is the request target a browser makes of an attribute's
// value: the fragment is cut off and never sent, a space is percent-encoded,
// and everything else goes as written. A link that only works because the
// test escaped it for the page is not a link that works.
func asABrowserSendsIt(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	target := u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + strings.ReplaceAll(u.RawQuery, " ", "%20")
	}
	return target, nil
}

// fetch follows a link the way the browser would, as a navigation (href) or as
// the htmx request the attribute makes.
func fetch(t *testing.T, h http.Handler, l pageLink) (*httptest.ResponseRecorder, string) {
	t.Helper()
	target, err := asABrowserSendsIt(l.raw)
	if err != nil {
		t.Errorf("%s=%q is not a URL a browser can follow: %v", l.attr, l.raw, err)
		return nil, ""
	}
	u, err := url.ParseRequestURI(target)
	if err != nil {
		t.Errorf("%s=%q becomes %q, which is not a request target: %v", l.attr, l.raw, target, err)
		return nil, ""
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.URL, r.RequestURI = u, target
	if l.attr == "href" {
		r.Header.Set("Accept", "text/html")
	} else {
		r.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec, target
}

// names reports whether a request target carries the name, in its path or in
// a query value.
func names(target, name string) bool {
	u, err := url.ParseRequestURI(target)
	if err != nil {
		return false
	}
	if strings.Contains(u.Path, name) {
		return true
	}
	for _, vs := range u.Query() {
		for _, v := range vs {
			if strings.Contains(v, name) {
				return true
			}
		}
	}
	return false
}

var hxValsRE = regexp.MustCompile(`\shx-vals='([^']*)'`)

// crawl opens the start pages and every link, hx-get and live region on them,
// then does the same on each page it reached that names the resource (its tabs,
// its drawers). Every one answers 200, and every hx-vals on the way is the
// JSON htmx will parse. It returns the targets it fetched.
func crawl(t *testing.T, h http.Handler, name string, refused string, start ...string) map[string]int {
	t.Helper()
	seen := map[string]int{}
	queue := make([]pageLink, 0, len(start))
	for _, s := range start {
		queue = append(queue, pageLink{"href", s})
	}
	for len(queue) > 0 {
		l := queue[0]
		queue = queue[1:]
		target, err := asABrowserSendsIt(l.raw)
		if err == nil {
			if _, done := seen[target]; done {
				continue
			}
		}
		rec, target := fetch(t, h, l)
		if rec == nil {
			continue
		}
		seen[target] = rec.Code
		if refused != "" && strings.HasPrefix(target, refused) && rec.Code == http.StatusBadRequest &&
			strings.Contains(rec.Body.String(), "ValidationException") {
			continue
		}
		if rec.Code != http.StatusOK {
			t.Errorf("%s=%q (sent as %s): %d, want 200\n%.300s", l.attr, l.raw, target, rec.Code, rec.Body)
			continue
		}
		body := rec.Body.String()
		for _, m := range hxValsRE.FindAllStringSubmatch(body, -1) {
			vals := html.UnescapeString(m[1])
			var v map[string]any
			if err := json.Unmarshal([]byte(vals), &v); err != nil {
				t.Errorf("%s carries hx-vals that is not JSON (%v): %s", target, err, vals)
			}
		}
		// Only a page about this resource is read for more links: the rest of
		// the console is another test's.
		if !names(target, name) && !containsString(start, l.raw) {
			continue
		}
		queue = append(queue, linksOn(body)...)
	}
	return seen
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// The names below use the characters each service's own name rule allows
// (.audit-models/<svc>.json), not characters the service would refuse:
//
//	Secrets Manager  SecretName: letters, digits and /_+=.@-
//	CloudWatch Logs  LogGroupName: [.\-_/#A-Za-z0-9]+
//	SSM              PSParameterName: a /-separated path of [a-zA-Z0-9_.-]
//	IAM              userNameType: [\w+=,.@-]+
//	S3               ObjectKey: any UTF-8, length >= 1
//	CloudWatch       AlarmName: any 1-255 characters
//	DynamoDB         TableName: [a-zA-Z0-9_.-]+; a key's value is any string
//	Step Functions   Name: anything but whitespace and <>{}[]?*"#%\^|~`$&,;:/
//	EventBridge      RuleName and NonPartnerEventBusName: [.\-_A-Za-z0-9]+
func TestALinkFromANameSurvivesItsCharacters(t *testing.T) {
	h, gateway := newConsoleStack(t)
	post := func(t *testing.T, path string, form url.Values) {
		t.Helper()
		if rec := req(t, h, "POST", path, form); rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Fatalf("POST %s: %d\n%s", path, rec.Code, rec.Body)
		}
	}
	const sfnRole = "arn:aws:iam::000000000000:role/stepfunctions"

	for _, c := range []struct {
		svc   string
		name  string // what the links must carry
		setup func(t *testing.T)
		// pages the crawl starts from; open is the path a link to the
		// resource's own page begins with.
		pages []string
		open  string
		// refused is a request the service itself turns down for this name,
		// which no escaping in the console changes.
		refused string
	}{
		{svc: "sm", name: "app/db+prod=1",
			setup: func(t *testing.T) {
				post(t, "/_console/sm/create", url.Values{"name": {"app/db+prod=1"}, "value": {"x"}})
			},
			pages: []string{"/_console/sm"}, open: "/_console/sm/secret?name="},
		{svc: "logs", name: "/app/a#b",
			setup: func(t *testing.T) { post(t, "/_console/logs/create", url.Values{"name": {"/app/a#b"}}) },
			pages: []string{"/_console/logs"}, open: "/_console/logs/group?name=",
			// A log group may have a "#" in its name and an ARN may not
			// (AmazonResourceName, [\w+=/:,.@-]*), so ListTagsForResource cannot
			// address this group, here or on AWS. The link reaches the service
			// with the name whole; the service refuses the ARN made from it.
			refused: "/_console/tags/view?svc=logs&id=%2Fapp%2Fa%23b"},
		{svc: "ssm", name: "/app/cfg/db.url_1-x",
			setup: func(t *testing.T) {
				post(t, "/_console/ssm/create", url.Values{"name": {"/app/cfg/db.url_1-x"}, "type": {"String"}, "value": {"v"}})
			},
			pages: []string{"/_console/ssm"}, open: "/_console/ssm/param?name="},
		{svc: "iam", name: "user+a=b@c,d",
			setup: func(t *testing.T) {
				post(t, "/_console/iam/create", url.Values{"kind": {"user"}, "name": {"user+a=b@c,d"}})
			},
			pages: []string{"/_console/iam"}, open: "/_console/iam/user/"},
		{svc: "s3", name: `a+b #c&d=e?f%g"h.txt`,
			setup: func(t *testing.T) {
				post(t, "/_console/s3/create", url.Values{"name": {"link-bucket"}})
				for _, key := range []string{`a+b #c&d=e?f%g"h.txt`, `dir +x&y#z/a+b #c&d=e?f%g"h.txt`} {
					r := httptest.NewRequest("PUT", "/link-bucket/"+(&url.URL{Path: key}).EscapedPath(), strings.NewReader("hello"))
					rec := httptest.NewRecorder()
					gateway.ServeHTTP(rec, r)
					if rec.Code != http.StatusOK {
						t.Fatalf("PutObject %q: %d\n%s", key, rec.Code, rec.Body)
					}
				}
			},
			pages: []string{"/_console/s3/link-bucket", "/_console/s3/link-bucket?prefix=" + url.QueryEscape("dir +x&y#z/")},
			open:  "/_console/s3/link-bucket/meta?key="},
		{svc: "cw", name: "cpu/high #1? 50%+a&b=c",
			setup: func(t *testing.T) {
				loc := create(t, h, "/_console/cw/create-alarm", url.Values{"name": {"cpu/high #1? 50%+a&b=c"}, "metric": {"Custom|Hits"},
					"statistic": {"Sum"}, "operator": {"LessThanThreshold"}, "threshold": {"3"}, "period": {"60"},
					"evaluation": {"1"}, "missing": {"notBreaching"}})
				// The redirect after create is a link too.
				if rec, target := fetch(t, h, pageLink{"href", loc}); rec == nil || rec.Code != http.StatusOK {
					t.Errorf("the redirect after create, %q (sent as %s), does not open the alarm", loc, target)
				}
			},
			pages: []string{"/_console/cw"}, open: "/_console/cw/alarm/"},
		{svc: "ddb", name: "link.table-1_x",
			setup: func(t *testing.T) {
				post(t, "/_console/ddb/create", url.Values{"name": {"link.table-1_x"}, "hash_key": {"id"}, "hash_type": {"S"}})
				post(t, "/_console/ddb/link.table-1_x/put", url.Values{"item": {`{"id":"a+b #c&d/e?f%g\"h","n":1}`}})
			},
			pages: []string{"/_console/ddb"}, open: "/_console/ddb/link.table-1_x"},
		{svc: "sfn", name: "run+1=a@b!(c)",
			setup: func(t *testing.T) {
				post(t, "/_console/sfn/create", url.Values{"name": {"flow+v1=a@b"}, "role": {sfnRole},
					"definition": {`{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":60,"End":true}}}`}})
				loc := create(t, h, "/_console/sfn/"+url.PathEscape("flow+v1=a@b")+"/start", url.Values{"name": {"run+1=a@b!(c)"}})
				if rec, target := fetch(t, h, pageLink{"href", loc}); rec == nil || rec.Code != http.StatusOK {
					t.Errorf("the redirect after start, %q (sent as %s), does not open the execution", loc, target)
				}
			},
			pages: []string{"/_console/sfn", "/_console/sfn/" + url.PathEscape("flow+v1=a@b")},
			open:  "/_console/sfn/flow+v1=a@b/execution/"},
		{svc: "eb", name: "on.order-1_x",
			setup: func(t *testing.T) {
				// CreateEventBus takes NonPartnerEventBusName, which has no "/".
				post(t, "/_console/eb/create-bus", url.Values{"name": {"team.bus-1_x"}})
				post(t, "/_console/eb/team.bus-1_x/create-rule",
					url.Values{"name": {"on.order-1_x"}, "pattern": {`{"source":["shop"]}`}})
			},
			pages: []string{"/_console/eb", "/_console/eb/team.bus-1_x"},
			open:  "/_console/eb/team.bus-1_x/rule/"},
	} {
		t.Run(c.svc, func(t *testing.T) {
			c.setup(t)
			seen := crawl(t, h, c.name, c.refused, c.pages...)

			// A link to the resource's own page was on the way, it carries the
			// name whole, and the page it opens is that resource's.
			opened := 0
			for target, code := range seen {
				if !strings.HasPrefix(target, c.open) && !strings.HasPrefix(target, strings.ReplaceAll(c.open, "+", "%2B")) {
					continue
				}
				if !names(target, c.name) {
					continue
				}
				opened++
				if code != http.StatusOK {
					continue // already reported by the crawl
				}
				// A page, not a fragment of one: a polled region under the
				// resource's path need not repeat its name.
				rec, _ := fetch(t, h, pageLink{"href", target})
				if body := rec.Body.String(); strings.Contains(body, "<!DOCTYPE") && !strings.Contains(html.UnescapeString(body), c.name) {
					t.Errorf("%s answers 200 and does not name %q", target, c.name)
				}
			}
			if opened == 0 {
				t.Errorf("no link under %s names %q; the crawl reached:\n%s", c.open, c.name, strings.Join(keysOf(seen), "\n"))
			}
		})
	}
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The actions on a CloudWatch alarm agree with its links about how its name is
// written into a path: state, actions and delete all reach the alarm, and each
// sends the browser back to a page that exists.
func TestAnAlarmWithAnyNameCanBeChangedAndDeleted(t *testing.T) {
	h := newConsole(t)
	const name = "cpu/high #1? 50%+a&b=c"
	create(t, h, "/_console/cw/create-alarm", url.Values{"name": {name}, "metric": {"Custom|Hits"},
		"statistic": {"Sum"}, "operator": {"LessThanThreshold"}, "threshold": {"3"}, "period": {"60"},
		"evaluation": {"1"}, "missing": {"notBreaching"}})

	page := req(t, h, "GET", "/_console/cw/alarm/"+url.PathEscape(name), nil)
	if page.Code != http.StatusOK {
		t.Fatalf("alarm page: %d", page.Code)
	}
	posts := regexp.MustCompile(`hx-post="([^"]*/cw/alarm/[^"]*)"`).FindAllStringSubmatch(page.Body.String(), -1)
	if len(posts) < 3 {
		t.Fatalf("the alarm page carries %d actions on the alarm, want state, actions and delete", len(posts))
	}
	follow := func(t *testing.T, action string, form url.Values) {
		t.Helper()
		target, err := asABrowserSendsIt(html.UnescapeString(action))
		if err != nil {
			t.Fatalf("hx-post=%q: %v", action, err)
		}
		rec := req(t, h, "POST", target, form)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST %s: %d\n%.300s", target, rec.Code, rec.Body)
		}
		if back, sent := fetch(t, h, pageLink{"href", rec.Header().Get("Location")}); back == nil || back.Code != http.StatusOK {
			t.Errorf("POST %s sends the browser to %s, which does not open", target, sent)
		}
	}
	for _, m := range posts {
		switch {
		case strings.HasSuffix(m[1], "/state"):
			follow(t, m[1], url.Values{"state": {"ALARM"}, "reason": {"by hand"}})
		case strings.HasSuffix(m[1], "/actions"):
			follow(t, m[1], url.Values{"enabled": {"false"}})
		}
	}
	for _, m := range posts {
		if strings.HasSuffix(m[1], "/delete") {
			follow(t, m[1], nil)
		}
	}
	if gone := req(t, h, "GET", "/_console/cw", nil).Body.String(); strings.Contains(html.UnescapeString(gone), name) {
		t.Errorf("the alarm is still listed after delete")
	}
}

// What the templates write into a URL is escaped where it is written, so the
// crawl above is not the only thing between a new link and a name with a "#"
// in it. Three shapes are read off the template sources:
//
//   - an attribute htmx or the browser treats as a URL, starting at the
//     console's prefix: every value after the prefix goes through pathesc
//     (before the "?") or urlquery (after it);
//   - a URL assembled with printf from the prefix: every further argument is
//     (pathesc X) or (urlquery X);
//   - an hx-vals object written out in the template: every value goes through
//     jsonstr, since a name with a quote in it is otherwise not JSON. (One a
//     handler marshalled whole, hx-vals='{{.NextVals}}', already is.)
var (
	urlAttrRE   = regexp.MustCompile(`\s(href|action|src|hx-get|hx-post|hx-put|hx-patch|hx-delete|data-live)="(\{\{\$?\.Prefix\}\}/(?:[^"{]|\{\{(?:[^}"]|"[^"]*")*\}\})*)"`)
	actionRE    = regexp.MustCompile(`\{\{((?:[^}"]|"[^"]*")*)\}\}`)
	bareValueRE = regexp.MustCompile(`^[.$][\w.$]*$`)
	printfURLRE = regexp.MustCompile(`\(printf "(%s/[^"]*)" \$?\.Prefix((?: [^()\s]+| \([^()]*\))*)\)`)
	hxValsSrcRE = regexp.MustCompile(`\shx-vals='(\{"[^']*)'`)
	printfArgRE = regexp.MustCompile(` ([^()\s]+|\([^()]*\))`)
)

// Templates another change is rewriting at the time of writing; the names they
// put in a path are a queue's ([A-Za-z0-9_-], ".fifo") and, in the rule form,
// an event bus's. Remove an entry here and the test says what is left to do.
var urlGuardSkips = map[string]bool{"sqs.html": true, "create.html": true}

func TestEveryNameATemplatePutsInAURLIsEscaped(t *testing.T) {
	files, _ := filepath.Glob("templates/*.html")
	if len(files) == 0 {
		t.Fatal("no templates found")
	}
	for _, f := range files {
		if urlGuardSkips[filepath.Base(f)] {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			at := f + ":" + strconv.Itoa(i+1)
			for _, m := range urlAttrRE.FindAllStringSubmatch(line, -1) {
				value := m[2]
				query := false
				rest := value[strings.Index(value, "}}")+2:]
				for len(rest) > 0 {
					loc := actionRE.FindStringSubmatchIndex(rest)
					if loc == nil {
						break
					}
					if strings.Contains(rest[:loc[0]], "?") {
						query = true
					}
					expr := strings.TrimSpace(rest[loc[2]:loc[3]])
					want := "pathesc"
					if query {
						want = "urlquery"
					}
					if bareValueRE.MatchString(expr) {
						t.Errorf("%s: %s=%q writes {{%s}} into a URL unescaped; write {{%s %s}}", at, m[1], value, expr, want, expr)
					}
					rest = rest[loc[1]:]
				}
			}
			for _, m := range printfURLRE.FindAllStringSubmatch(line, -1) {
				format, args := m[1], printfArgRE.FindAllStringSubmatch(m[2], -1)
				verbs := strings.Split(format, "%")[2:] // after the prefix's own %s
				query := false
				for n, a := range args {
					if n < len(verbs) && n > 0 && strings.Contains(verbs[n-1], "?") {
						query = true
					}
					if n == 0 && strings.Contains(strings.SplitN(format, "%", 3)[1], "?") {
						query = true
					}
					if bareValueRE.MatchString(a[1]) {
						want := "pathesc"
						if query {
							want = "urlquery"
						}
						t.Errorf("%s: %s builds a URL from %s unescaped; write (%s %s)", at, m[0], a[1], want, a[1])
					}
				}
			}
			for _, m := range hxValsSrcRE.FindAllStringSubmatch(line, -1) {
				for _, a := range actionRE.FindAllStringSubmatch(m[1], -1) {
					if expr := strings.TrimSpace(a[1]); bareValueRE.MatchString(expr) {
						t.Errorf("%s: hx-vals='%s' writes {{%s}} into JSON unescaped; write {{jsonstr %s}}", at, m[1], expr, expr)
					}
				}
			}
		}
	}
}
