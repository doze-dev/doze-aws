package cloudformation

import (
	"testing"
	"time"
)

// Events in one deploy are told apart by EventId. With a clock that has not
// moved between two events — a coarse timer, or an injected one — they used to
// share an id, and SAM's progress display, which skips ids it has seen, never
// reached the stack's own CREATE_COMPLETE.
func TestIdsMintedInOneTickDiffer(t *testing.T) {
	s := newStore(nil)
	frozen := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return frozen }
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := s.newID()
		if seen[id] {
			t.Fatalf("id %s minted twice", id)
		}
		seen[id] = true
	}
}

func TestEventTimeCarriesMilliseconds(t *testing.T) {
	at := time.Date(2026, 10, 10, 1, 2, 3, 456_000_000, time.UTC)
	if got := eventTime(stackEvent{Timestamp: at.Unix(), TimeMs: at.UnixMilli()}); got != "2026-10-10T01:02:03.456Z" {
		t.Errorf("eventTime = %q", got)
	}
	// An event written before milliseconds were kept still renders.
	if got := eventTime(stackEvent{Timestamp: at.Unix()}); got != "2026-10-10T01:02:03Z" {
		t.Errorf("legacy eventTime = %q", got)
	}
}
