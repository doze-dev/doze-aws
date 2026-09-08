package eventbridge

import (
	"context"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/internal/awscron"
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
	compiled := map[string]*awscron.Expression{}
	s.fireDueSchedules(lastFired, compiled) // first sighting: arms, records now
	key := DefaultBus + "\x00tick"
	armed, ok := lastFired[key]
	if !ok {
		t.Fatal("rule was not armed on first sighting")
	}
	// Backdate the arm time past the interval; the next pass must re-fire (update).
	lastFired[key] = armed.Add(-2 * time.Minute)
	s.fireDueSchedules(lastFired, compiled)
	if !lastFired[key].After(armed.Add(-2 * time.Minute)) {
		t.Fatal("due rule did not fire (lastFired not advanced)")
	}
}

// A cron rule fires when the expression's next time after the last firing
// has passed, and not before — driven by the server's clock.
func TestFireDueSchedulesCron(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a store")
	}
	now := time.Date(2026, time.September, 8, 9, 31, 0, 0, time.UTC) // a Tuesday
	s, err := New(Options{DataDir: t.TempDir(), Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.store.PutRule(Rule{Bus: DefaultBus, Name: "daily", Schedule: "cron(0 10 * * ? *)", State: "ENABLED"}); err != nil {
		t.Fatal(err)
	}
	lastFired := map[string]time.Time{}
	compiled := map[string]*awscron.Expression{}
	key := DefaultBus + "\x00daily"
	s.fireDueSchedules(lastFired, compiled) // arms at 09:31
	if _, ok := lastFired[key]; !ok {
		t.Fatal("cron rule was not armed")
	}
	// 09:59: not yet.
	now = now.Add(28 * time.Minute)
	s.fireDueSchedules(lastFired, compiled)
	if !lastFired[key].Equal(time.Date(2026, time.September, 8, 9, 31, 0, 0, time.UTC)) {
		t.Fatal("the rule fired before 10:00")
	}
	// 10:00: due.
	now = now.Add(time.Minute)
	s.fireDueSchedules(lastFired, compiled)
	if !lastFired[key].Equal(now) {
		t.Fatalf("the rule should have fired at 10:00, lastFired = %v", lastFired[key])
	}
	// 10:01: fired for today; next is tomorrow.
	now = now.Add(time.Minute)
	s.fireDueSchedules(lastFired, compiled)
	if !lastFired[key].Equal(now.Add(-time.Minute)) {
		t.Fatal("the rule fired twice in one day")
	}
	if _, ok := compiled["cron(0 10 * * ? *)"]; !ok {
		t.Error("the expression should be compiled once and cached")
	}
}

// A malformed cron is refused at PutRule with AWS's message; a good one and
// a rate are accepted.
func TestPutRuleRefusesMalformedCron(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a store")
	}
	s, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, ok := range []string{"cron(0 12 * * ? *)", "rate(5 minutes)", "cron(0 18 ? * MON-FRI *)"} {
		if _, aerr := s.putRule(ctx, map[string]any{"Name": "r", "ScheduleExpression": ok}); aerr != nil {
			t.Errorf("%s should be accepted: %v", ok, aerr)
		}
	}
	for _, bad := range []string{"cron(0 12 * * * *)", "cron(nope)", "every 5 minutes"} {
		_, aerr := s.putRule(ctx, map[string]any{"Name": "r", "ScheduleExpression": bad})
		if aerr == nil || aerr.Code != "ValidationException" || aerr.Message != "Parameter ScheduleExpression is not valid." {
			t.Errorf("%s should be refused with AWS's message, got %v", bad, aerr)
		}
	}
}
