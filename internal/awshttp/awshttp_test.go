package awshttp

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestErrfAndAs(t *testing.T) {
	e := Errf(400, "ValidationException", "bad %s", "field")
	if e.Status != 400 || e.Code != "ValidationException" || e.Message != "bad field" {
		t.Fatalf("Errf = %+v", e)
	}
	if e.Error() == "" {
		t.Fatal("Error() empty")
	}
	// AsAPIError round-trips an *APIError and wraps a plain error.
	if got := AsAPIError(e); got != e {
		t.Fatalf("AsAPIError(apiErr) = %+v", got)
	}
	plain := AsAPIError(errors.New("boom"))
	if plain == nil || plain.Code == "" {
		t.Fatalf("AsAPIError(plain) = %+v", plain)
	}
	if AsAPIErrorOrNil(nil) != nil {
		t.Fatal("AsAPIErrorOrNil(nil) should be nil")
	}
	if AsAPIErrorOrNil(e) != e {
		t.Fatal("AsAPIErrorOrNil(apiErr) should return it")
	}
}

// An unexpected error becomes an opaque 500, which is right — internal detail
// must not reach a client. It used to become nothing at all in the LOG, which
// is not: across 400-odd call sites, any unforeseen failure produced
// "InternalFailure: internal error" on the wire and silence in the terminal,
// so the one person who could act on it learned nothing.
func TestAnInternalFaultIsReportedSomewhere(t *testing.T) {
	var seen []error
	OnInternalFault = func(err error) { seen = append(seen, err) }
	t.Cleanup(func() { OnInternalFault = nil })

	boom := errors.New("bbolt: database not open")
	got := AsAPIError(boom)

	if len(seen) != 1 || !errors.Is(seen[0], boom) {
		t.Fatalf("the discarded error was not reported: %v", seen)
	}
	// ...and still does not reach the client.
	if got.Status != 500 || got.Code != "InternalFailure" {
		t.Errorf("wire error = %+v, want an opaque 500", got)
	}
	if strings.Contains(got.Message, "bbolt") {
		t.Errorf("internal detail leaked onto the wire: %q", got.Message)
	}

	// An *APIError is a deliberate, client-visible answer — not a fault, and
	// reporting one would drown the real ones.
	seen = nil
	AsAPIError(Errf(400, "ValidationException", "bad field"))
	if len(seen) != 0 {
		t.Errorf("a deliberate API error was reported as an internal fault: %v", seen)
	}
	// Nil hook is the library default and must not panic.
	OnInternalFault = nil
	_ = AsAPIError(boom)
}

func TestRequestIDUnique(t *testing.T) {
	a, b := RequestID(), RequestID()
	if a == "" || a == b {
		t.Fatalf("RequestID not unique: %q %q", a, b)
	}
}

func TestISO8601(t *testing.T) {
	got := ISO8601(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	if got == "" || got[:4] != "2026" {
		t.Fatalf("ISO8601 = %q", got)
	}
}
