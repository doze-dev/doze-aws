package console_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A page load that fails is a page. The handlers answered a failed GET with the
// bare error fragment the actions use, which as a document has no chrome, no
// rail and no way back. Every service has a page for a resource that is not
// there, so every service is asked for one.
func TestAMissingResourceIsAPageInTheShell(t *testing.T) {
	h := newConsole(t)
	for _, c := range []struct{ svc, path string }{
		{"s3", "/_console/s3/no-such-bucket"},
		{"ddb", "/_console/ddb/no-such-table"},
		{"sqs", "/_console/sqs/no-such-queue"},
		{"sns", "/_console/sns/no-such-topic"},
		{"eb", "/_console/eb/no-such-bus"},
		{"kinesis", "/_console/kinesis/no-such-stream"},
		{"lambda", "/_console/lambda/no-such-fn"},
		{"apigw", "/_console/apigw/zzzzzzzzzz"},
		{"sfn", "/_console/sfn/no-such-machine"},
		{"logs", "/_console/logs/group?name=/no/such/group"},
		{"cw", "/_console/cw/alarm/no-such-alarm"},
		{"kms", "/_console/kms/00000000-0000-0000-0000-000000000000"},
		{"sm", "/_console/sm/secret?name=no-such-secret"},
		{"ssm", "/_console/ssm/param?name=/no/such/param"},
		{"cfn", "/_console/cfn/no-such-stack"},
	} {
		t.Run(c.svc, func(t *testing.T) {
			r := httptest.NewRequest("GET", c.path, nil)
			r.Header.Set("Accept", "text/html,application/xhtml+xml")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			body := w.Body.String()
			if w.Code < 400 || w.Code >= 500 {
				t.Fatalf("status %d, want a 4xx\n%s", w.Code, clip(body))
			}
			if !strings.HasPrefix(strings.TrimSpace(body), "<!DOCTYPE html>") {
				t.Fatalf("not a page:\n%s", clip(body))
			}
			// Either the failure in place, or the not-found page's own pointer to
			// the section: both are a page in the shell with a way on.
			if !strings.Contains(body, `id="toasts"`) {
				t.Errorf("not inside the shell:\n%s", clip(body))
			}
			if !strings.Contains(body, "Back to ") && !strings.Contains(body, "Did you mean") && !strings.Contains(body, "the wire") {
				t.Errorf("no way back:\n%s", clip(body))
			}
		})
	}
}

// A16: the page for a thing that is not there still has the service's list pane
// beside it, with what is there. Without it the only way on was the back link,
// and the one thing a person looking for a name needs is the list of names.
func TestAMissingResourceKeepsItsListPane(t *testing.T) {
	h := newConsole(t)
	discovered["code"] = t.TempDir()
	seedFixtures(t, h)
	for _, c := range []struct{ svc, path string }{
		{"s3", "/_console/s3/no-such-bucket"},
		{"ddb", "/_console/ddb/no-such-table"},
		{"sqs", "/_console/sqs/no-such-queue"},
		{"sns", "/_console/sns/no-such-topic"},
		{"kinesis", "/_console/kinesis/no-such-stream"},
		{"lambda", "/_console/lambda/no-such-fn"},
		{"apigw", "/_console/apigw/zzzzzzzzzz"},
		{"sfn", "/_console/sfn/no-such-machine"},
		{"logs", "/_console/logs/group?name=/no/such/group"},
		{"cw", "/_console/cw/alarm/no-such-alarm"},
		{"kms", "/_console/kms/00000000-0000-0000-0000-000000000000"},
		{"sm", "/_console/sm/secret?name=no-such-secret"},
		{"ssm", "/_console/ssm/param?name=/no/such/param"},
		{"cfn", "/_console/cfn/no-such-stack"},
		{"iam", "/_console/iam/user/no-such-user"},
	} {
		t.Run(c.svc, func(t *testing.T) {
			r := httptest.NewRequest("GET", c.path, nil)
			r.Header.Set("Accept", "text/html")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			body := w.Body.String()
			if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
				t.Fatalf("status %d\n%s", w.Code, clip(body))
			}
			i := strings.Index(body, `<aside class="listpane`)
			if i < 0 {
				t.Fatalf("no list pane beside the failure:\n%s", clip(body))
			}
			pane := body[i : i+strings.Index(body[i:], "</aside>")]
			if !strings.Contains(pane, "fixture") {
				t.Errorf("the pane does not list what the stack has:\n%s", clip(pane))
			}
			if strings.Contains(pane, `class="li on"`) {
				t.Errorf("the pane marks a resource open, but the page is for one that is not there")
			}
		})
	}
}

func clip(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}

// An htmx request keeps the fragment: it has a listener that places it.
func TestAnHTMXFailureStaysAFragment(t *testing.T) {
	h := newConsole(t)
	r := httptest.NewRequest("GET", "/_console/sqs/no-such-queue", nil)
	r.Header.Set("HX-Request", "true")
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("HX-Doze-Error") != "1" || w.Code != http.StatusBadRequest {
		t.Errorf("status %d, HX-Doze-Error %q: want the placed fragment", w.Code, w.Header().Get("HX-Doze-Error"))
	}
}

// CloudFormation names a missing stack in its message, not its code, and a
// missing stack is still a page that says Not found.
func TestAMissingStackIsNotFound(t *testing.T) {
	h := newConsole(t)
	r := httptest.NewRequest("GET", "/_console/cfn/no-such-stack", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Not found") {
		t.Errorf("status %d, want 404 and a Not found heading:\n%s", w.Code, clip(w.Body.String()))
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type %q: the page relied on sniffing", ct)
	}
}

// A boosted link is a navigation: htmx swaps the workspace out of the page it
// gets, so a failure must be a page. The fragment it used to get was placed on
// top of whatever resource was still on screen.
func TestABoostedLinkToAMissingResourceGetsAPage(t *testing.T) {
	h := newConsole(t)
	r := httptest.NewRequest("GET", "/_console/sqs/no-such-queue", nil)
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-Boosted", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Header().Get("HX-Doze-Error") != "" || !strings.HasPrefix(strings.TrimSpace(body), "<!DOCTYPE html>") {
		t.Errorf("a boosted failure was a fragment (HX-Doze-Error %q):\n%s", w.Header().Get("HX-Doze-Error"), clip(body))
	}
	if !strings.Contains(body, `id="workspace"`) {
		t.Errorf("the page has no workspace for htmx to swap in")
	}
}
