package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTheConsoleAndTheGatewayShareAnEndpoint(t *testing.T) {
	tag := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(name + " " + r.URL.Path)) })
	}
	h := withConsole(tag("console"), tag("gateway"))
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/_console"); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/_console/" {
		t.Errorf("/_console = %d %q, want a redirect to /_console/", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get("/_console/sqs/jobs"); rec.Body.String() != "console /_console/sqs/jobs" {
		t.Errorf("a console path = %q", rec.Body)
	}
	// Everything else is the gateway's, exactly as it was sent.
	for _, path := range []string{"/", "/bucket", "/bucket/dir/key", "/bucket/double//slash", "/bucket/trailing/", "/bucket/a/./b"} {
		if rec := get(path); rec.Code != 200 || rec.Body.String() != "gateway "+path {
			t.Errorf("GET %s = %d %q, want the gateway to see it unchanged", path, rec.Code, rec.Body)
		}
	}
}
