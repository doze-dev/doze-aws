package eventbridge

import (
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	cases := []struct {
		expr string
		want time.Duration
		ok   bool
	}{
		{"rate(5 minutes)", 5 * time.Minute, true},
		{"rate(1 minute)", time.Minute, true},
		{"rate(2 hours)", 2 * time.Hour, true},
		{"rate(1 day)", 24 * time.Hour, true},
		{" rate(3 minutes) ", 3 * time.Minute, true},
		{"cron(0 12 * * ? *)", 0, false}, // cron not driven locally
		{"rate(0 minutes)", 0, false},
		{"rate(5)", 0, false},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := parseRate(c.expr)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseRate(%q) = %v,%v want %v,%v", c.expr, got, ok, c.want, c.ok)
		}
	}
}

// TestFireDueSchedules exercises the due-firing logic deterministically: a rate
// rule fires only after its interval has elapsed since the previous tick.
func TestFireDueSchedules(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a store")
	}
	s, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A scheduled rule with no targets: firing is a no-op delivery, but the
	// due-logic (first-sighting arms, then fires after the interval) is what we
	// assert via the lastFired bookkeeping.
	if err := s.store.PutRule(Rule{Bus: DefaultBus, Name: "tick", Schedule: "rate(1 minute)", State: "ENABLED"}); err != nil {
		t.Fatal(err)
	}
	lastFired := map[string]time.Time{}
	s.fireDueSchedules(lastFired) // first sighting: arms, records now
	key := DefaultBus + "\x00tick"
	armed, ok := lastFired[key]
	if !ok {
		t.Fatal("rule was not armed on first sighting")
	}
	// Backdate the arm time past the interval; the next pass must re-fire (update).
	lastFired[key] = armed.Add(-2 * time.Minute)
	s.fireDueSchedules(lastFired)
	if !lastFired[key].After(armed.Add(-2 * time.Minute)) {
		t.Fatal("due rule did not fire (lastFired not advanced)")
	}
}
