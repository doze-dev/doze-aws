package console

// A2: the links under "Refused here" and "Not built", and the anchors in the
// fidelity dialog they point at.

import (
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/peers"
)

func TestSplitOpsKeepsEveryCharacterAndAnchorsOnlyWholeOperations(t *testing.T) {
	for _, tc := range []struct {
		cell string
		ops  []string // the anchors, in order
	}{
		{"SendMessage", []string{"SendMessage"}},
		{"SendMessage / SendMessageBatch", []string{"SendMessage", "SendMessageBatch"}},
		{"CreateQueue, DeleteQueue,GetQueueUrl", []string{"CreateQueue", "DeleteQueue", "GetQueueUrl"}},
		// A bare verb is the fragment of a bundled row, never an operation.
		{"Put/Get/DeleteFunctionEventInvokeConfig", []string{"DeleteFunctionEventInvokeConfig"}},
		// A phrase is not an operation, and what follows it still is.
		{"Mobile push (Platform applications), Publish", []string{"Publish"}},
		{"", nil},
	} {
		var text strings.Builder
		var ops []string
		for _, p := range splitOps(tc.cell) {
			text.WriteString(p.Text)
			if p.Op != "" {
				ops = append(ops, p.Op)
			}
		}
		if text.String() != tc.cell {
			t.Errorf("%q: the parts read back as %q", tc.cell, text.String())
		}
		if strings.Join(ops, ",") != strings.Join(tc.ops, ",") {
			t.Errorf("%q: anchors %v, want %v", tc.cell, ops, tc.ops)
		}
	}
}

// Every row of every service's ledger reads back whole in the dialog (the anchors
// add markup, not words), and each operation a row names has its anchor.
func TestTheDialogAnchorsEveryOperationAndLosesNoWord(t *testing.T) {
	c, err := New(Options{Peers: peers.InProcess(func(string) http.Handler { return nil })})
	if err != nil {
		t.Fatal(err)
	}
	cell := regexp.MustCompile(`(?s)<td class="fid-op">(.*?)</td>`)
	anchor := regexp.MustCompile(`<span class="fid-opn" id="op-([a-z0-9]+)-([A-Za-z0-9]+)" data-op="([A-Za-z0-9]+)">`)
	for _, e := range catalog {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest("GET", "/_console/info/"+e.Key, nil))
		body := w.Body.String()
		cells := cell.FindAllStringSubmatch(body, -1)
		var want []string
		for _, s := range infoSections(e.Key) {
			for _, r := range s.Rows {
				want = append(want, r.Ops)
			}
		}
		if len(cells) != len(want) {
			t.Errorf("%s: %d operation cells drawn, the ledger has %d rows", e.Key, len(cells), len(want))
			continue
		}
		for i, m := range cells {
			got := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(m[1], ""))
			if got != html.UnescapeString(html.EscapeString(want[i])) && got != want[i] {
				t.Errorf("%s: row %d reads %q, the ledger says %q", e.Key, i, got, want[i])
			}
		}
		for _, a := range anchor.FindAllStringSubmatch(body, -1) {
			if a[2] != a[3] {
				t.Errorf("%s: anchor id names %s but data-op %s", e.Key, a[2], a[3])
			}
		}
	}
}

// The ones the refusals link: the operation SQS refuses or does not build is in
// the SQS ledger, with an anchor the link can open at.
func TestTheSQSDialogHasTheAnchorsARefusalLinks(t *testing.T) {
	c, err := New(Options{Peers: peers.InProcess(func(string) http.Handler { return nil })})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/_console/info/sqs", nil))
	for _, op := range []string{"CreateQueue", "SendMessage", "SendMessageBatch", "ReceiveMessage"} {
		if !strings.Contains(w.Body.String(), `<span class="fid-opn" id="op-sqs-`+op+`" data-op="`+op+`">`+op+`</span>`) {
			t.Errorf("no anchor for %s in the SQS dialog", op)
		}
	}
	// API Gateway has two ledgers: the same operation name in each must not share an id.
	w = httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/_console/info/apigw", nil))
	ids := map[string]int{}
	for _, m := range regexp.MustCompile(`id="(op-[^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1) {
		ids[m[1]]++
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("the API Gateway dialog has %d elements with id %s", n, id)
		}
	}
	if len(ids) == 0 {
		t.Error("the API Gateway dialog has no operation anchors")
	}
}

func TestWhichFailuresPointAtTheLedger(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		state, detail, svc, op string
		wantNil                bool
		wantText, wantPath     string
	}{
		{name: "refused here on an operation in the ledger", state: "refusedhere", svc: "sqs", op: "CreateQueue",
			wantText: "The ledger row for CreateQueue →", wantPath: "/sqs?fidelity=CreateQueue"},
		{name: "not built on an operation in the ledger", state: "notbuilt", detail: "declined", svc: "sqs", op: "ListQueueTags",
			wantText: "The ledger row for ListQueueTags →", wantPath: "/sqs?fidelity=ListQueueTags"},
		// A name the ledger does not have is not a row: the link opens the service's ledger.
		{name: "an operation the ledger does not name", state: "refusedhere", svc: "sqs", op: "NoSuchOperation",
			wantText: "The SQS ledger →", wantPath: "/sqs?fidelity="},
		{name: "a path-routed service names no operation", state: "refusedhere", svc: "s3",
			wantText: "The S3 ledger →", wantPath: "/s3?fidelity="},
		{name: "the stack lacks the service: a choice about the stack, not a ledger row", state: "refusedhere", detail: serviceNotRunning, svc: "sqs", op: "CreateQueue", wantNil: true},
		{name: "a service the console has no page for", state: "notbuilt", svc: "sts", op: "GetCallerIdentity", wantNil: true},
		{name: "refused", state: "refused", svc: "sqs", op: "CreateQueue", wantNil: true},
		{name: "denied", state: "denied", svc: "sqs", op: "CreateQueue", wantNil: true},
		{name: "failed", state: "error", svc: "sqs", op: "CreateQueue", wantNil: true},
		{name: "threw", state: "threw", svc: "lambda", op: "Invoke", wantNil: true},
	} {
		got := ledgerRefFor(tc.state, tc.detail, tc.svc, tc.op)
		if tc.wantNil {
			if got != nil {
				t.Errorf("%s: linked %+v", tc.name, got)
			}
			continue
		}
		if got == nil || got.Text != tc.wantText || got.Path != tc.wantPath {
			t.Errorf("%s: got %+v, want %q %q", tc.name, got, tc.wantText, tc.wantPath)
		}
	}
	if notBuiltRefFor("refusedhere", "") != nil || notBuiltRefFor("refused", "declined") != nil {
		t.Error("only a not-built operation links docs/not-built.md")
	}
}

// Every operation a console action can be refused on that the ledger names is a
// row the link can open at: the operations the console's own JSON and Query calls
// make are read off the request.
func TestAConsoleCallNamesItsOperation(t *testing.T) {
	for _, tc := range []struct {
		name, target, ct, body, want string
	}{
		{"a JSON service", "AmazonSQS.CreateQueue", "application/x-amz-json-1.0", `{}`, "CreateQueue"},
		{"DynamoDB's dated prefix", "DynamoDB_20120810.PutItem", "application/x-amz-json-1.0", `{}`, "PutItem"},
		{"a Query service", "", "application/x-www-form-urlencoded", "Action=CreateTopic&Name=a", "CreateTopic"},
		{"a path-routed service says nothing", "", "", "", ""},
	} {
		r, _ := http.NewRequest("POST", "http://peer.invalid/", strings.NewReader(tc.body))
		if tc.target != "" {
			r.Header.Set("X-Amz-Target", tc.target)
		}
		r.Header.Set("Content-Type", tc.ct)
		if got := operationOf(r); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
