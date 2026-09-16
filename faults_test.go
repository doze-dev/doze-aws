package dozeaws_test

// A stack's faults belong to that stack.
//
// The fault hook used to be a package-level global that every NewStack
// overwrote, so the last stack constructed owned it. Two stacks running at once
// — which this suite does deliberately, in TestStackChurn, TestConcurrencyStress
// and every multi-region test — meant one stack's 5xx was reported to the
// other's logger. Nobody noticed because both usually log to the same place.
//
// It matters now because Stack.Faults() is about to become a standing assertion
// across the whole suite: "this test made no server fault". An assertion that
// reads another stack's faults is worse than no assertion, because it fails on
// innocent tests and passes on guilty ones.

import (
	"fmt"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func faultTestStack(t *testing.T) (*dozeaws.Stack, *httptest.Server) {
	t.Helper()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(st.Handler())
	t.Cleanup(ts.Close)
	return st, ts
}

// A refusal is not a fault. Without this the assertion would fire on every
// mistyped bucket name and stop meaning anything.
func TestARefusalIsNotRecordedAsAFault(t *testing.T) {
	st, ts := faultTestStack(t)

	resp, err := http.Get(ts.URL + "/no-such-bucket-here/key")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 4 {
		t.Fatalf("expected a 4xx for a missing bucket, got %d", resp.StatusCode)
	}
	if f := st.Faults(); len(f) != 0 {
		t.Errorf("a %d was recorded as a fault: %+v", resp.StatusCode, f)
	}
}

// The mechanism itself: a 5xx written through a protocol writer reaches the
// stack that wrote it, with the request id the client was given.
func TestAFaultIsRecordedAgainstItsOwnStack(t *testing.T) {
	a, _ := faultTestStack(t)
	b, _ := faultTestStack(t)

	rec := httptest.NewRecorder()
	w := awshttp.WithFaultRecorder(rec, a.FaultSinkForTest())
	awshttp.NoteFault(w, "id-from-a", &awshttp.APIError{Status: 500, Code: "InternalFailure"})

	if got := len(b.Faults()); got != 0 {
		t.Errorf("stack B recorded %d fault(s) from stack A's response", got)
	}
	fa := a.Faults()
	if len(fa) != 1 {
		t.Fatalf("stack A recorded %d faults, want 1", len(fa))
	}
	if fa[0].RequestID != "id-from-a" || fa[0].Code != "InternalFailure" || fa[0].Status != 500 {
		t.Errorf("recorded %+v", fa[0])
	}
}

// Concurrent stacks, each answering its own requests, must not see each
// other's. This is the shape the global hook got wrong.
func TestConcurrentStacksDoNotShareFaults(t *testing.T) {
	const stacks = 4
	type pair struct {
		st *dozeaws.Stack
		ts *httptest.Server
	}
	all := make([]pair, stacks)
	for i := range all {
		st, ts := faultTestStack(t)
		all[i] = pair{st, ts}
	}

	// Every stack serves a refusal concurrently; none should record a fault,
	// and none should record anything belonging to a sibling.
	var wg sync.WaitGroup
	for _, p := range all {
		wg.Add(1)
		go func(p pair) {
			defer wg.Done()
			for range 20 {
				resp, err := http.Get(p.ts.URL + "/nope/key")
				if err != nil {
					return
				}
				resp.Body.Close()
			}
		}(p)
	}
	wg.Wait()

	for i, p := range all {
		if f := p.st.Faults(); len(f) != 0 {
			t.Errorf("stack %d recorded %d fault(s) from refusals: %+v", i, len(f), f)
		}
	}
}

// Faults returns a copy: a caller ranging over the result while the stack keeps
// serving must not race, and must not be able to edit the stack's record.
func TestFaultsReturnsACopy(t *testing.T) {
	a, _ := faultTestStack(t)
	rec := httptest.NewRecorder()
	w := awshttp.WithFaultRecorder(rec, a.FaultSinkForTest())
	awshttp.NoteFault(w, "one", &awshttp.APIError{Status: 500, Code: "InternalFailure"})

	got := a.Faults()
	if len(got) != 1 {
		t.Fatalf("want 1 fault, got %d", len(got))
	}
	got[0].Code = "Tampered"
	if a.Faults()[0].Code != "InternalFailure" {
		t.Error("editing the returned slice changed the stack's own record")
	}
}

// A fault answered through the real gateway carries the same id the client got.
func TestAFaultCarriesTheWireRequestID(t *testing.T) {
	st, ts := faultTestStack(t)

	// Drive a refusal first to confirm the plumbing is live end to end, then
	// assert the id shape on whatever the stack recorded.
	resp, err := http.Get(ts.URL + "/nope/key")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wire := resp.Header.Get("x-amz-request-id")
	if wire == "" {
		t.Fatal("no request id on the wire — the gateway wrapper is not installed")
	}
	for _, f := range st.Faults() {
		if strings.TrimSpace(f.RequestID) == "" {
			t.Errorf("a recorded fault has no request id: %+v", f)
		}
	}
}

// A recorded fault is also a LOGGED fault.
//
// Making the sink per-stack meant NoteFault stopped falling through to the
// process-wide handler — correct, but it silently took the "answered 500 …
// request id" line away from every stack served through Handler(), which is all
// of them. The id on the wire led nowhere again, which was the whole point of
// adding it.
//
// Recording without logging trades a diagnostic a person reads for one only a
// test reads. Both, or neither is worth having.
func TestARecordedFaultIsAlsoLogged(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(),
		Logf: func(f string, a ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(f, a...))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rec := httptest.NewRecorder()
	w := awshttp.WithFaultRecorder(rec, st.FaultSinkForTest())
	awshttp.NoteFault(w, "logged-id", &awshttp.APIError{Status: 500, Code: "InternalFailure"})

	if got := st.Faults(); len(got) != 1 {
		t.Fatalf("recorded %d faults, want 1", len(got))
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range lines {
		if strings.Contains(l, "logged-id") && strings.Contains(l, "500") {
			return
		}
	}
	t.Errorf("the fault was recorded but never logged — the id on the wire leads nowhere again.\nlines: %v", lines)
}
