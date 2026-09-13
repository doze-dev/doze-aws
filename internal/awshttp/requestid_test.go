package awshttp

// The request-id plumbing, at the level where a fault can be produced directly
// rather than contrived through a service.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestWithRequestIDPutsTheSameIDOnBothSides(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	w, r, id := WithRequestID(rec, r)
	if id == "" {
		t.Fatal("no id minted")
	}
	if got := ResponseID(w); got != id {
		t.Errorf("ResponseID = %q, want %q", got, id)
	}
	if got := RequestIDFrom(r.Context()); got != id {
		t.Errorf("RequestIDFrom = %q, want %q — the log and the wire would disagree", got, id)
	}
}

// Asking twice is the same answer. Ten writers ask, and each used to mint.
func TestResponseIDIsStable(t *testing.T) {
	w, _, id := WithRequestID(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	for i := range 5 {
		if got := ResponseID(w); got != id {
			t.Fatalf("call %d returned %q, want %q", i, got, id)
		}
	}
}

// nopWrapper stands in for a middleware that wraps the writer without knowing
// about request ids — the console's status recorder is one.
type nopWrapper struct{ http.ResponseWriter }

func (n nopWrapper) Unwrap() http.ResponseWriter { return n.ResponseWriter }

func TestResponseIDSeesThroughAWrapper(t *testing.T) {
	w, _, id := WithRequestID(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got := ResponseID(nopWrapper{w}); got != id {
		t.Errorf("through a wrapper: %q, want %q", got, id)
	}
}

// A writer that never saw the gateway still answers with an id, because a
// service package used directly is a supported way to run one.
func TestResponseIDMintsWhenThereIsNoneToFind(t *testing.T) {
	got := ResponseID(httptest.NewRecorder())
	if len(got) != 36 {
		t.Errorf("ResponseID on a bare writer = %q, want a UUID-shaped id", got)
	}
}

func TestNoteFaultReportsOnlyServerFaults(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	SetFaultResponseHandler(func(id string, e *APIError) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, id+" "+e.Code)
	})
	t.Cleanup(func() { SetFaultResponseHandler(nil) })

	NoteFault("id-400", &APIError{Status: 400, Code: "ValidationError"})
	NoteFault("id-404", &APIError{Status: 404, Code: "NoSuchBucket"})
	NoteFault("id-500", &APIError{Status: 500, Code: "InternalFailure"})
	NoteFault("id-503", &APIError{Status: 503, Code: "ServiceUnavailable"})
	NoteFault("id-nil", nil)

	mu.Lock()
	defer mu.Unlock()
	want := []string{"id-500 InternalFailure", "id-503 ServiceUnavailable"}
	if len(seen) != len(want) {
		t.Fatalf("reported %v, want %v — a 4xx logging here would be noise on every typo", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("reported[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
}
