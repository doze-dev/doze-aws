package console_test

// What the independent re-audit of the SQS console (2026-10-16) found, where a
// Go test can hold it. The browser half is e2e/tests/sqs-audit.spec.ts.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// gw makes one SQS JSON call on the gateway, the way a client does.
func gw(t *testing.T, gateway http.Handler, target, body string) (int, string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("X-Amz-Target", "AmazonSQS."+target)
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

// redrive reads a queue's RedrivePolicy back through the API.
func redrive(t *testing.T, gateway http.Handler, queue string) (arn string, count int) {
	t.Helper()
	code, body := gw(t, gateway, "GetQueueAttributes", `{"QueueUrl":"http://x/000000000000/`+queue+`","AttributeNames":["RedrivePolicy"]}`)
	if code != 200 {
		t.Fatalf("GetQueueAttributes %s: %d %s", queue, code, body)
	}
	var out struct{ Attributes map[string]string }
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var rp struct {
		Arn   string `json:"deadLetterTargetArn"`
		Count int    `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(out.Attributes["RedrivePolicy"]), &rp); err != nil {
		t.Fatalf("no RedrivePolicy on %s: %q", queue, out.Attributes["RedrivePolicy"])
	}
	return rp.Arn, rp.Count
}

// Item 1. With the dead-letter queue left on "keep current", changing "After
// this many receives" used to toast "Queue settings saved" and change nothing.
// The count applies to the policy the queue has: the target stays, the count
// changes. A count the service refuses (Sqs-21) is refused, and nothing moves.
func TestEditingTheReceiveCountWithTheDeadLetterQueueKept(t *testing.T) {
	h, gateway := newConsoleStack(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}, "max_receive": {"3"}})
	arn, n := redrive(t, gateway, "work")
	if n != 3 || !strings.HasSuffix(arn, ":work-dlq") {
		t.Fatalf("setup: the policy is %s after %d", arn, n)
	}

	saved := req(t, h, "POST", "/_console/sqs/work/attributes", url.Values{"dlq": {"keep"}, "max_receive": {"7"}})
	if saved.Code != 200 {
		t.Fatalf("saving the count: %d\n%s", saved.Code, saved.Body)
	}
	if arn2, n2 := redrive(t, gateway, "work"); n2 != 7 || arn2 != arn {
		t.Errorf("the count was not applied with the target kept: policy is %s after %d (was %s after 3, want %s after 7)", arn2, n2, arn, arn)
	}

	// The same value again is not a change, and not an error.
	if again := req(t, h, "POST", "/_console/sqs/work/attributes", url.Values{"dlq": {"keep"}, "max_receive": {"7"}}); again.Code != 200 {
		t.Errorf("saving the count that is already stored: %d\n%s", again.Code, again.Body)
	}

	// A blank count leaves the policy as it is.
	req(t, h, "POST", "/_console/sqs/work/attributes", url.Values{"dlq": {"keep"}, "max_receive": {""}})
	if _, n3 := redrive(t, gateway, "work"); n3 != 7 {
		t.Errorf("a blank count changed the policy to %d", n3)
	}

	// Sqs-21: the service refuses 5000, as the service says it, and the stored count stands.
	refused := req(t, h, "POST", "/_console/sqs/work/attributes", url.Values{"dlq": {"keep"}, "max_receive": {"5000"}})
	if refused.Code != 400 || !strings.Contains(refused.Body.String(), "InvalidAttributeValue") || !strings.Contains(refused.Body.String(), "between 1 and 1000") {
		t.Errorf("5000 was not refused by the service: %d\n%s", refused.Code, refused.Body)
	}
	if _, n4 := redrive(t, gateway, "work"); n4 != 7 {
		t.Errorf("a refused count changed the policy to %d", n4)
	}

	// A queue with no dead-letter queue has no count to change: nothing is invented.
	create(t, h, "/_console/sqs/create", url.Values{"name": {"plain"}, "dlq_mode": {"none"}})
	req(t, h, "POST", "/_console/sqs/plain/attributes", url.Values{"dlq": {"keep"}, "max_receive": {"4"}})
	if code, body := gw(t, gateway, "GetQueueAttributes", `{"QueueUrl":"http://x/000000000000/plain","AttributeNames":["RedrivePolicy"]}`); code != 200 || strings.Contains(body, "RedrivePolicy") {
		t.Errorf("a queue with no dead-letter queue was given a policy: %s", body)
	}
}

// Item 4. Consume sends what was typed, so the service refuses what it refuses
// (SQS-5: Max 0 and 11; a wait over 20) and the refusal is the page's to show.
func TestConsumeSendsWhatWasTypedAndTheServiceRefuses(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"drain"}, "dlq_mode": {"none"}})
	for _, tc := range []struct {
		name string
		form url.Values
	}{
		{"Max 11", url.Values{"max": {"11"}}},
		{"Max 0", url.Values{"max": {"0"}}},
		{"Wait 99", url.Values{"max": {"10"}, "wait": {"99"}}},
		{"Wait 21", url.Values{"max": {"10"}, "wait": {"21"}}},
	} {
		got := req(t, h, "POST", "/_console/sqs/drain/receive", tc.form)
		if got.Code != 400 || got.Header().Get("HX-Doze-Error") == "" {
			t.Errorf("%s: the console clamped it instead of letting the service refuse (%d)\n%s", tc.name, got.Code, got.Body)
		}
	}
	for _, ok := range []url.Values{{"max": {"10"}, "wait": {"0"}}, {"max": {"1"}, "wait": {"20"}}} {
		if got := req(t, h, "POST", "/_console/sqs/drain/receive", ok); got.Code != 200 {
			t.Errorf("%v was refused: %d\n%s", ok, got.Code, got.Body)
		}
	}
}

// Item 11. The console's own client speaks JSON, and its code for a queue that
// is not there is QueueDoesNotExist; NonExistentQueue is the Query wire's.
func TestDeleteQueueConfirmNamesTheCodeTheConsoleShows(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"doomed"}, "dlq_mode": {"none"}})
	page := req(t, h, "GET", "/_console/sqs/doomed", nil).Body.String()
	if strings.Contains(page, "NonExistentQueue") {
		t.Errorf("the Delete confirm names the Query wire's code")
	}
	if !strings.Contains(page, "gets QueueDoesNotExist.") {
		t.Errorf("the Delete confirm does not name QueueDoesNotExist")
	}
}

// Item 12. The fidelity button names the service as people do, not by its key.
func TestFidelityButtonNamesTheService(t *testing.T) {
	h := newConsole(t)
	for path, want := range map[string]string{"/_console/sqs": "What of SQS is real here", "/_console/ddb": "What of DynamoDB is real here", "/_console/iam": "What of IAM is real here"} {
		if page := req(t, h, "GET", path, nil).Body.String(); !strings.Contains(page, want) {
			t.Errorf("%s: the fidelity button does not say %q", path, want)
		}
	}
}

// Item 13. After a send the toast names the queue (States board: "Sent to <queue>").
func TestSendToastNamesTheQueue(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"inbox"}, "dlq_mode": {"none"}})
	got := req(t, h, "POST", "/_console/sqs/inbox/send", url.Values{"body": {"hello"}})
	if got.Code != 200 || !strings.Contains(got.Header().Get("HX-Trigger"), `"Sent to inbox"`) {
		t.Errorf("the toast after a send: %d %q", got.Code, got.Header().Get("HX-Trigger"))
	}
}

// Item 5 and 6. A page names only keys that do something, and each state of a
// queue's page names the set its board draws. The browser spec
// (shell.spec.ts, "the key bar") presses every one of them.
func TestTheQueuePageDeclaresTheKeysOfItsState(t *testing.T) {
	h := newConsole(t)
	last := func(path string) string {
		t.Helper()
		got := declaredKeys(req(t, h, "GET", path, nil).Body.String())
		if len(got) == 0 {
			t.Fatalf("%s declares no keys", path)
		}
		return strings.Join(got, "  ||  ")
	}
	if got := last("/_console/sqs"); got != "c=new queue|⌘K=jump or act" {
		t.Errorf("an SQS home with no queue declares %q: j, k and ⏎ have nothing to act on", got)
	}
	create(t, h, "/_console/sqs/create", url.Values{"name": {"keyed"}, "dlq_mode": {"none"}})
	if got := last("/_console/sqs"); got != "j k=queue|⏎=open|/=filter|c=new queue|⌘K=jump or act" {
		t.Errorf("an SQS home with a queue declares %q", got)
	}
	// Each state's declaration is the last one on its page that is on screen at
	// once; the page's own list-pane set comes first and is replaced by it.
	for _, tc := range []struct{ path, want string }{
		{"/_console/sqs/keyed", "j k=queue|⌘⏎=send|/=filter|⌘K=jump or act  ||  ⌘⏎=send|esc=close|⌘K=jump or act"},
		{"/_console/sqs/keyed?tab=consume", "⏎=receive|⌘K=jump or act"},
		{"/_console/sqs/keyed?tab=config", "j k=queue|⌘K=jump or act  ||  tab=next field|esc=cancel|⌘K=jump or act"},
		{"/_console/sqs/keyed?tab=tags", "tab=next field|⌘K=jump or act"},
	} {
		got := last(tc.path)
		if !strings.HasSuffix(got, tc.want) {
			t.Errorf("%s declares\n  %s\nwant it to end\n  %s", tc.path, got, tc.want)
		}
		// ⏎ opens a message on the Queue board, and there is no message cursor.
		for _, d := range declaredKeys(req(t, h, "GET", tc.path, nil).Body.String())[1:] {
			if strings.Contains(d, "⏎=open") {
				t.Errorf("%s names ⏎ as open: there is nothing on this page for it to open", tc.path)
			}
		}
	}
}
