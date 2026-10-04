package lambdaruntime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Runtime API is three calls with a method each. The router refuses what
// is not one of them, where the mux it replaced answered 202 to anything under
// /invocation/ and 400 to the rest.
func TestTheRuntimeAPIIsThreeCallsWithAMethodEach(t *testing.T) {
	h := (&Runner{}).routes()
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/2018-06-01/runtime/invocation/abc/bogus", http.StatusNotFound},
		{"GET", "/2018-06-01/runtime/invocation/abc/response", http.StatusMethodNotAllowed},
		{"POST", "/2018-06-01/runtime/invocation/next", http.StatusMethodNotAllowed},
		{"GET", "/2018-06-01/runtime/init/error", http.StatusMethodNotAllowed},
		{"GET", "/2018-06-01/runtime/nothing", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
