package awshttp

import (
	"errors"
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
