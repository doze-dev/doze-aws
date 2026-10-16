package console_test

// The list pane and the key bar, as the design draws them (plan/design: Sqs-02-Pick,
// Sqs-03-Create, Main, Wire-Empty, Wire-Inspect; plan/03-shell-fidelity.md 2.3
// and 2.4, build pieces S4 and S5, decision D10). The browser specs drive the
// keys; these pin what the server writes: the words in the pane's header, the
// colour a count may carry, and the key set each page declares.
//
// "warn" is a class on a row's count, or on a span inside it: SQS puts it
// inside, because the count's slot is patched out of band as innerHTML and the
// colour has to follow the number.

import (
	"html"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// What each service's list pane calls its things, as its Home board writes it.
// IAM's board heads its pane "IAM" (the pane holds principals, policies and an
// access log, not one kind), so that is what the page says.
var boardHeadings = map[string]string{
	"s3": "Buckets", "ddb": "Tables", "sqs": "Queues", "sns": "Topics", "eb": "Event buses",
	"kinesis": "Streams", "lambda": "Functions", "apigw": "APIs", "sfn": "State machines",
	"logs": "Log groups", "cw": "Alarms", "kms": "Keys", "sm": "Secrets", "ssm": "Parameters",
	"iam": "IAM", "cfn": "Stacks",
}

var (
	lpName  = regexp.MustCompile(`<span class="lp-name"[^>]*>([^<]*)</span>`)
	newLink = regexp.MustCompile(`(?s)<a class="new-link[^"]*"[^>]*>(.*?)</a>`)
	keysAtt = regexp.MustCompile(`data-keys="([^"]*)"`)
)

func declaredKeys(page string) []string {
	var out []string
	for _, m := range keysAtt.FindAllStringSubmatch(page, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

// S4: the heading is the board's plural noun, with no count in it (the count is
// in the rail); the action is a text button; the pane has no key line at its
// foot (its content is the key bar's now).
func TestTheListPaneHeaderIsTheBoards(t *testing.T) {
	h := newConsole(t)
	for svc, want := range boardHeadings {
		page := req(t, h, "GET", "/_console/"+svc, nil).Body.String()
		// The header runs from the pane to its filter. (Not to the first </div>:
		// the fidelity button inside it carries a dialog.)
		i, j := strings.Index(page, `<aside class="listpane`), strings.Index(page, `class="filter"`)
		if i < 0 || j < i {
			t.Errorf("%s: no list pane header", svc)
			continue
		}
		head := []string{"", page[i:j]}
		name := lpName.FindStringSubmatch(head[1])
		if name == nil || name[1] != want {
			t.Errorf("%s: the heading is %v, the board writes %q", svc, name, want)
		}
		if strings.Contains(head[1], "lp-ct") {
			t.Errorf("%s: the heading carries a count; the count is in the rail", svc)
		}
		nl := newLink.FindStringSubmatch(head[1])
		if nl == nil || !strings.Contains(nl[1], "New") || strings.Contains(nl[1], "<svg") {
			t.Errorf("%s: New is not a text button: %v", svc, nl)
		}
		if strings.Contains(page, `class="lp-f"`) || strings.Contains(page, "move · ") {
			t.Errorf("%s: the list pane still ends with its own key line", svc)
		}
	}
}

// The filter stays, deliberately: the page's navigation for a long list.
func TestTheListPaneKeepsItsFilter(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/sqs", nil).Body.String()
	if !strings.Contains(page, `class="filter"`) {
		t.Errorf("the SQS list pane lost its filter")
	}
}

// A count is neutral unless the service says it needs a look. SQS says so for a
// dead-letter queue that holds a message (boards Sqs-02, Queue: the DLQ's 3 in
// warn). The colour is a class on the count, "warn", which any service's list
// can ask for; the count's element is replaced whole by the poll so the colour
// follows the number.
func TestACountIsWarnForADeadLetterQueueHoldingMessages(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}})
	home := req(t, h, "GET", "/_console/sqs", nil).Body.String()
	if !regexp.MustCompile(`<span class="sb" id="lr-sqs-work-dlq">0</span>`).MatchString(home) {
		t.Errorf("an empty dead-letter queue's count is not neutral:\n%s", home)
	}
	req(t, h, "POST", "/_console/sqs/work-dlq/send", url.Values{"body": {`{"n":1}`}})
	home = req(t, h, "GET", "/_console/sqs", nil).Body.String()
	if !regexp.MustCompile(`<span class="sb" id="lr-sqs-work-dlq"><span class="warn">1</span></span>`).MatchString(home) {
		t.Errorf("a dead-letter queue holding a message is not warn:\n%s", home)
	}
	if !regexp.MustCompile(`<span class="sb" id="lr-sqs-work">0</span>`).MatchString(home) {
		t.Errorf("the queue that feeds it was coloured")
	}
}

// S5: the bar is one element, outside the workspace (a boosted navigation swaps
// the workspace and the bar is updated out of band), on every page.
func TestEveryPageHasOneKeyBarOutsideTheWorkspace(t *testing.T) {
	h := newConsole(t)
	paths := []string{"/_console/", "/_console/traffic", "/_console/connect", "/_console/nowhere", "/_console/sqs", "/_console/sqs/create", "/_console/s3"}
	for _, p := range paths {
		page := req(t, h, "GET", p, nil).Body.String()
		if n := strings.Count(page, `id="keybar"`); n != 1 {
			t.Errorf("%s: %d key bars, want 1", p, n)
			continue
		}
		bar, ws := strings.Index(page, `id="keybar"`), strings.Index(page, `</main>`)
		if ws < 0 || bar < ws {
			t.Errorf("%s: the key bar is inside the workspace, where a swap would drop it", p)
		}
		if !strings.Contains(page, `class="keys"`) {
			t.Errorf("%s: the bar is not the design's .keys", p)
		}
	}
}

// Each page declares its own keys, from its template, as data: the list
// pane for a list page, the form for a create page, the feed and the inspector
// for the wire. "?" is not among them (D10): no board draws what it shows.
func TestEachPageDeclaresTheKeysItAnswersTo(t *testing.T) {
	h := newConsole(t)
	// A list with nothing in it has no row to move over, open or filter (board
	// Sqs-01-None: "c new queue", "⌘K jump or act"): the bar names only keys
	// that do something.
	cases := []struct{ path, want string }{
		{"/_console/sqs", "c=new queue|⌘K=jump or act"},
		{"/_console/s3", "c=new bucket|⌘K=jump or act"},
		{"/_console/eb", "j k=bus|⏎=open|/=filter|c=new bus|⌘K=jump or act"}, // the default bus is always there,
		{"/_console/iam", "j k=principal|⏎=open|/=filter|c=new principal|⌘K=jump or act"},
		{"/_console/sqs/create", sqsCreateKeys},
		{"/_console/s3/create", otherCreateKeys},
	}
	for _, c := range cases {
		page := req(t, h, "GET", c.path, nil).Body.String()
		got := declaredKeys(page)
		if len(got) == 0 || got[len(got)-1] != c.want {
			t.Errorf("%s declares %q, want last %q", c.path, got, c.want)
		}
	}
	for _, p := range []string{"/_console/connect", "/_console/nowhere"} {
		for _, k := range declaredKeys(req(t, h, "GET", p, nil).Body.String()) {
			if k != "⌘K=jump or act" {
				t.Errorf("%s declares %q; a page with nothing of its own answers only to the palette", p, k)
			}
		}
	}
	// The wire with nothing on it: Wire-Empty draws "⌘K jump or act" and nothing else.
	for _, k := range declaredKeys(req(t, h, "GET", "/_console/", nil).Body.String()) {
		if k != "⌘K=jump or act" {
			t.Errorf("the first-run wire declares %q", k)
		}
	}
}

// Escape cancels a create page. The attribute is what shell.js reads; no
// template ever set it, so Escape did nothing on a create page.
func TestEveryCreatePageSaysEscapeCancels(t *testing.T) {
	h := newConsole(t)
	for svc, path := range map[string]string{
		"s3": "/s3/create", "ddb": "/ddb/create", "sqs": "/sqs/create", "sns": "/sns/create", "eb": "/eb/create-bus",
		"kinesis": "/kinesis/create", "lambda": "/lambda/create", "apigw": "/apigw/create", "sfn": "/sfn/create",
		"logs": "/logs/create", "cw": "/cw/create", "kms": "/kms/create", "sm": "/sm/create", "ssm": "/ssm/create",
		"iam": "/iam/create", "cfn": "/cfn/create",
	} {
		rec := req(t, h, "GET", "/_console"+path, nil)
		page := rec.Body.String()
		if rec.Code != 200 {
			t.Errorf("%s: %s answered %d", svc, path, rec.Code)
			continue
		}
		if !strings.Contains(page, "data-esc-back") {
			t.Errorf("%s: the create page does not say Escape cancels (data-esc-back)", svc)
		}
		want := otherCreateKeys
		if svc == "sqs" {
			want = sqsCreateKeys
		}
		got := declaredKeys(page)
		if len(got) == 0 || got[len(got)-1] != want {
			t.Errorf("%s: the create page declares %q", svc, got)
		}
	}
}

var capEntry = regexp.MustCompile(`(?m)^    "([^"]+)":`)

// Every key any page names is a key shell.js binds. The browser spec drives
// each one; this stops a template naming a key the script does not know, which
// is the bar lying before it is ever pressed.
func TestEveryKeyAPageNamesIsOneTheScriptBinds(t *testing.T) {
	h := newConsole(t)
	src, err := os.ReadFile("static/shell.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "var KEYBAR = {")
	if i < 0 {
		t.Fatal("shell.js has no KEYBAR table of the keys it binds")
	}
	end := strings.Index(s[i:], "\n  };")
	bound := map[string]bool{}
	for _, m := range capEntry.FindAllStringSubmatch(s[i:i+end], -1) {
		bound[m[1]] = true
	}
	if len(bound) == 0 {
		t.Fatal("KEYBAR holds no keys")
	}
	if bound["?"] {
		t.Errorf("shell.js binds ?, which no board draws a sheet for (D10)")
	}
	create(t, h, "/_console/sqs/create", url.Values{"name": {"bound"}, "dlq_mode": {"none"}})
	pages := []string{"/_console/", "/_console/connect", "/_console/nowhere", "/_console/iam", "/_console/iam/create", "/_console/sqs/create", "/_console/s3/create",
		"/_console/sqs/bound", "/_console/sqs/bound?tab=consume", "/_console/sqs/bound?tab=config", "/_console/sqs/bound?tab=tags"}
	for svc := range boardHeadings {
		pages = append(pages, "/_console/"+svc)
	}
	for _, p := range pages {
		for _, decl := range declaredKeys(req(t, h, "GET", p, nil).Body.String()) {
			for _, item := range strings.Split(decl, "|") {
				caps, _, ok := strings.Cut(item, "=")
				if !ok {
					t.Errorf("%s: %q is not caps=label", p, item)
					continue
				}
				for _, cap := range strings.Fields(caps) {
					if cap == "?" || !bound[cap] {
						t.Errorf("%s names %q, which shell.js does not bind", p, cap)
					}
				}
			}
		}
	}
}
