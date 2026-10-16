package console_test

// The shell's top bar and rail, as the design draws them (plan/design: Main,
// Anatomy, Sqs-02-Pick; plan/03-shell-fidelity.md 2.1 and 2.2, decisions D1 and
// D9). The browser specs measure what is drawn; these pin what the server
// decides: which words are in the markup, on which page, and what the rail's
// status dots are made from.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/peers"
)

// chromeConsole builds a console over a stack in the given IAM mode, with the
// instance name the binary would pass and a recorder, and counts every
// DozeAccessLog call the console makes to IAM.
func chromeConsole(t *testing.T, mode iam.Mode, id awsident.Identity) (c http.Handler, rec *console.Recorder, accessLogCalls *atomic.Int64) {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t), IAMMode: mode, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	rec = console.NewRecorder(stack.Handler(), id)
	accessLogCalls = new(atomic.Int64)
	dir := peers.InProcess(func(service string) http.Handler {
		h := stack.Service(service)
		if service != "iam" || h == nil {
			return h
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), "DozeAccessLog") {
				accessLogCalls.Add(1)
			}
			r.Body = io.NopCloser(strings.NewReader(string(b)))
			h.ServeHTTP(w, r)
		})
	})
	c, err = console.New(console.Options{Peers: dir, Identity: id, Recorder: rec, Name: "harbour"})
	if err != nil {
		t.Fatal(err)
	}
	return c, rec, accessLogCalls
}

// topBar returns the <header class="topbar"> element's markup.
func topBar(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<header class="topbar"`)
	j := strings.Index(page, `</header>`)
	if i < 0 || j < i {
		t.Fatalf("no top bar in the page:\n%.400s", page)
	}
	return page[i:j]
}

func TestTheTopBarIsTheDesigns(t *testing.T) {
	c, _, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	for _, path := range []string{"/_console/", "/_console/sqs", "/_console/connect", "/_console/s3/create", "/_console/nowhere"} {
		bar := topBar(t, req(t, c, "GET", path, nil).Body.String())
		for _, want := range []string{
			// the brand: the product, then this instance's name in a muted weight
			`doze-aws`, `class="inst"`, `harbour`,
			// the jump box, in the design's words
			`Jump to a resource, or do something…`, `⌘K`,
			// the endpoint chip, with its dot
			`class="live"`,
			// the region, a static chip: no board draws a selector
			`us-east-1`,
		} {
			if !strings.Contains(bar, want) {
				t.Errorf("%s: the top bar lacks %q:\n%s", path, want, bar)
			}
		}
		// "Jump or act…" is the narrow board's text, drawn only at 1100px and below.
		if strings.Contains(bar, "console</span>") || strings.Contains(bar, `placeholder`) ||
			strings.Index(bar, "Jump or act") < strings.Index(bar, "Jump to a resource") {
			t.Errorf("%s: the top bar still says the old words:\n%s", path, bar)
		}
		if strings.Contains(bar, "<select") {
			t.Errorf("%s: a region selector is a control that cannot work", path)
		}
	}
}

func TestTheTopBarShowsTheRegionItWasStartedWith(t *testing.T) {
	c, _, _ := chromeConsole(t, iam.ModeSoft, awsident.Identity{Region: "eu-west-1", AccountID: "111122223333"})
	bar := topBar(t, req(t, c, "GET", "/_console/sqs", nil).Body.String())
	if !strings.Contains(bar, "eu-west-1") || strings.Contains(bar, "us-east-1") {
		t.Errorf("the region chip is not the instance's:\n%s", bar)
	}
}

var pill = regexp.MustCompile(`<span class="pill iam[^"]*"[^>]*>([^<]*)</span>`)

// The IAM mode is on every page, always, in one wording: "IAM soft",
// "IAM enforce", "IAM off". The board's "· recording" suffix is dropped.
func TestTheIAMModePillIsOnEveryPageInOneWording(t *testing.T) {
	for _, tc := range []struct {
		mode iam.Mode
		want string
	}{{iam.ModeSoft, "IAM soft"}, {iam.ModeEnforce, "IAM enforce"}, {iam.ModeOff, "IAM off"}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			c, _, _ := chromeConsole(t, tc.mode, awsident.Default())
			for _, path := range []string{"/_console/", "/_console/traffic", "/_console/sqs", "/_console/s3", "/_console/iam",
				"/_console/connect", "/_console/sqs/create", "/_console/nowhere"} {
				m := pill.FindStringSubmatch(topBar(t, req(t, c, "GET", path, nil).Body.String()))
				if m == nil {
					t.Errorf("%s: no IAM pill in the top bar", path)
					continue
				}
				if m[1] != tc.want {
					t.Errorf("%s: the pill says %q, want %q", path, m[1], tc.want)
				}
			}
		})
	}
}

// The mode cannot change while the process runs, so reading it is one call to
// the service, not one per page render. Idle weight is a feature.
func TestTheIAMModeIsReadOnceNotPerRender(t *testing.T) {
	c, _, calls := chromeConsole(t, iam.ModeEnforce, awsident.Default())
	for i := 0; i < 12; i++ {
		req(t, c, "GET", "/_console/sqs", nil)
		req(t, c, "GET", "/_console/", nil)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("24 renders read the IAM access log %d times; want exactly one, the mode is cached after the first read", n)
	}
}

var htmlTag = regexp.MustCompile(`<html [^>]*>`)

// The rail's default is the design's rule (Anatomy, rule 1): labelled on the
// wire, marks only inside a service. The server says which, on <html> so the
// first paint is right and on #workspace so a boosted navigation can change it.
func TestTheRailDefaultIsLabelledOnTheWireAndMarksInsideAService(t *testing.T) {
	c, _, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	for path, want := range map[string]string{
		"/_console/": "wide", "/_console/traffic": "wide",
		"/_console/sqs": "slim", "/_console/s3": "slim", "/_console/connect": "slim",
		"/_console/sqs/create": "slim", "/_console/iam": "slim", "/_console/nowhere": "slim",
	} {
		page := req(t, c, "GET", path, nil).Body.String()
		if tag := htmlTag.FindString(page); !strings.Contains(tag, `data-rail="`+want+`"`) {
			t.Errorf("%s: <html> is %s, want data-rail=%q", path, tag, want)
		}
		if !strings.Contains(page, `id="workspace" class="workspace" data-rail-default="`+want+`"`) {
			t.Errorf("%s: #workspace does not carry data-rail-default=%q", path, want)
		}
	}
}

func TestTheRailCaptionsAreTheDesignsWords(t *testing.T) {
	c, _, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	page := req(t, c, "GET", "/_console/", nil).Body.String()
	for _, want := range []string{">Storage and data<", ">Messaging<", ">Compute and APIs<", ">Config and secrets<", ">Stack-wide<",
		">CloudWatch Logs<", ">Secrets Manager<", ">Parameter Store<"} {
		if !strings.Contains(page, want) {
			t.Errorf("the rail lacks %q", want)
		}
	}
}

func counts(t *testing.T, c http.Handler) (map[string]float64, map[string]string) {
	t.Helper()
	body := req(t, c, "GET", "/_console/api/counts", nil).Body.Bytes()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("/api/counts is not JSON: %v\n%s", err, body)
	}
	n := map[string]float64{}
	var warn map[string]string
	for k, v := range raw {
		if k == "warn" {
			if err := json.Unmarshal(v, &warn); err != nil {
				t.Fatalf("warn is not a map of strings: %s", v)
			}
			continue
		}
		var f float64
		if err := json.Unmarshal(v, &f); err != nil {
			t.Fatalf("count %q is not a number: %s", k, v)
		}
		n[k] = f
	}
	return n, warn
}

// A service that has something to say gets a dot: the DLQ that holds a message
// (warn), an alarm in ALARM (bad), a call refused or failed in the last minute
// (warn, bad for a 5xx). Nothing else: a quiet stack has no dots.
func TestCountsCarryTheRailsStatusDots(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())

	if _, warn := counts(t, c); len(warn) != 0 {
		t.Fatalf("a fresh stack has dots: %v", warn)
	}

	// A DLQ with a message in it: SQS warns.
	create(t, c, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}})
	if _, warn := counts(t, c); len(warn) != 0 {
		t.Fatalf("an empty DLQ is not news: %v", warn)
	}
	req(t, c, "POST", "/_console/sqs/work-dlq/send", url.Values{"body": {`{"n":1}`}})
	n, warn := counts(t, c)
	if warn["sqs"] != "warn" {
		t.Errorf("a DLQ holding a message: warn = %v, want sqs=warn", warn)
	}
	if n["sqs"] != 2 {
		t.Errorf("the counts changed shape: sqs = %v, want 2", n["sqs"])
	}
	if len(warn) != 1 {
		t.Errorf("only SQS has something to say: %v", warn)
	}

	// An alarm in ALARM: CloudWatch is bad.
	create(t, c, "/_console/cw/create-alarm", url.Values{
		"name": {"low"}, "metric": {"Shop|Checkouts|"}, "statistic": {"Sum"},
		"operator": {"LessThanThreshold"}, "threshold": {"3"},
		"period": {"60"}, "evaluation": {"1"}, "missing": {"notBreaching"}})
	if _, warn := counts(t, c); warn["cw"] != "" {
		t.Errorf("an alarm that is not firing has a dot: %v", warn)
	}
	req(t, c, "POST", "/_console/cw/alarm/low/state", url.Values{"state": {"ALARM"}, "reason": {"by hand"}})
	if _, warn := counts(t, c); warn["cw"] != "bad" {
		t.Errorf("an alarm in ALARM: warn = %v, want cw=bad", warn)
	}

	// A call the wire shows refused, in the last minute: that service warns.
	call := func(target, body string) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("X-Amz-Target", target)
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		rec.ServeHTTP(httptest.NewRecorder(), r)
	}
	call("DynamoDB_20120810.DescribeTable", `{"TableName":"nope"}`)
	if _, warn := counts(t, c); warn["ddb"] != "warn" {
		t.Errorf("a refused call in the last minute: warn = %v, want ddb=warn", warn)
	}
}

// A missing key is the ordinary answer to "is it there?", so a 404 alone is
// not a warning: HeadObject on an absent key is how SDKs ask.
func TestANotFoundIsNotAWarning(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	create(t, c, "/_console/s3/create", url.Values{"name": {"files"}})
	r := httptest.NewRequest("GET", "/files/absent.txt", nil)
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=x")
	w := httptest.NewRecorder()
	rec.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("setup: GET of an absent key = %d", w.Code)
	}
	if _, warn := counts(t, c); warn["s3"] != "" {
		t.Errorf("a 404 put a dot on S3: %v", warn)
	}
}

// The dot is in the markup of the first paint, not only in the poll.
func TestTheRailDrawsTheDotsInTheMarkup(t *testing.T) {
	c, _, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	create(t, c, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}})
	if page := req(t, c, "GET", "/_console/", nil).Body.String(); strings.Contains(page, `class="ri-dot`) && strings.Contains(page, `ri-dot warn`) {
		t.Errorf("a quiet stack draws a warning dot")
	}
	req(t, c, "POST", "/_console/sqs/work-dlq/send", url.Values{"body": {`{"n":1}`}})
	page := req(t, c, "GET", "/_console/", nil).Body.String()
	if !regexp.MustCompile(`<span class="ri-dot warn"[^>]*data-dot="sqs"`).MatchString(page) {
		t.Errorf("the SQS row has no warn dot after a DLQ filled:\n%.300s", page)
	}
}
