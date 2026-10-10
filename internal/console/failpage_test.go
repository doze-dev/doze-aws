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
