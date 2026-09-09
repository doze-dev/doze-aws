package awscron

import (
	"testing"
	"time"
)

// A Tuesday, 09:31 UTC.
var after = time.Date(2026, time.September, 8, 9, 31, 0, 0, time.UTC)

func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func TestNextMatchesAWSDocumentation(t *testing.T) {
	cases := []struct {
		expr string
		want time.Time
	}{
		{"cron(0 10 * * ? *)", at(2026, 9, 8, 10, 0)},            // 10:00 every day
		{"cron(15 12 * * ? *)", at(2026, 9, 8, 12, 15)},          // 12:15 every day
		{"cron(0 18 ? * MON-FRI *)", at(2026, 9, 8, 18, 0)},      // 18:00 weekdays
		{"cron(0 8 1 * ? *)", at(2026, 10, 1, 8, 0)},             // 08:00 first of the month
		{"cron(0/15 * * * ? *)", at(2026, 9, 8, 9, 45)},          // every 15 minutes
		{"cron(0/10 * ? * MON-FRI *)", at(2026, 9, 8, 9, 40)},    // every 10 minutes on weekdays
		{"cron(0 9 ? * 2#1 *)", at(2026, 10, 5, 9, 0)},           // 09:00 first Monday (Sep 7 has passed)
		{"cron(0 0 L * ? *)", at(2026, 9, 30, 0, 0)},             // midnight on the last day
		{"cron(0 12 ? * SAT *)", at(2026, 9, 12, 12, 0)},         // Saturday noon
		{"cron(30 9 15W * ? *)", at(2026, 9, 15, 9, 30)},         // the 15th is a Tuesday
		{"cron(0 7 LW * ? *)", at(2026, 9, 30, 7, 0)},            // Sep 30 2026 is a Wednesday
		{"cron(0 6 ? * 6L *)", at(2026, 9, 25, 6, 0)},            // last Friday of September
		{"cron(0 0 1 JAN ? 2027)", at(2027, 1, 1, 0, 0)},         // a named month and a fixed year
		{"cron(5 4 ? * * 2026-2027)", at(2026, 9, 9, 4, 5)},      // a year range
		{"cron(0 12 29 FEB ? *)", at(2028, 2, 29, 12, 0)},        // a leap day
		{"cron(0/30 20-2 ? * MON-FRI *)", at(2026, 9, 8, 20, 0)}, // AWS's own wrap-around example
		{"cron(0 1 ? * FRI-MON *)", at(2026, 9, 11, 1, 0)},       // a wrap-around weekday range
		{"cron(0 12 ? * L *)", at(2026, 9, 12, 12, 0)},           // bare L is every Saturday
		{"cron(0 12 ? * 7L *)", at(2026, 9, 26, 12, 0)},          // 7L is the last Saturday

		// The W flag's whole job is landing on a weekday, and every one of
		// nearestWeekday's four adjustments was dead: the two existing W
		// cases (the 15th, and LW in September) both fall on weekdays
		// already, so the function was only ever asked to do nothing.
		// AWS: W never crosses a month boundary, which is why the first and
		// last of a month move forwards and backwards respectively.
		{"cron(0 9 12W * ? *)", at(2026, 9, 11, 9, 0)},  // the 12th is a Saturday: back to Friday the 11th
		{"cron(0 9 13W * ? *)", at(2026, 9, 14, 9, 0)},  // the 13th is a Sunday: on to Monday the 14th
		{"cron(0 7 LW * ? 2026)", at(2026, 9, 30, 7, 0)}, // Sep 30 is a Wednesday, unmoved
		{"cron(0 7 31W OCT ? 2026)", at(2026, 10, 30, 7, 0)}, // Oct 31 is a Saturday: back to Friday the 30th
		{"cron(0 7 LW OCT ? 2027)", at(2027, 10, 29, 7, 0)},  // Oct 31 2027 is a Sunday: back TWO days, not forward into November
		{"cron(0 7 1W MAY ? 2027)", at(2027, 5, 3, 7, 0)},    // May 1 2027 is a Saturday: forward TWO days, not back into April
		{"cron(0 7 1W NOV ? 2026)", at(2026, 11, 2, 7, 0)},   // Nov 1 2026 is a Sunday: forward one to Monday
	}
	for _, c := range cases {
		e, err := Parse(c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got, ok := e.Next(after)
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: Next = %v (ok=%v), want %v", c.expr, got, ok, c.want)
		}
	}
}

func TestParseRefusals(t *testing.T) {
	bad := []string{
		"cron(0 10 * * * *)",       // both day fields set
		"cron(0 10 ? * ? *)",       // neither day field set
		"cron(? 10 * * ? *)",       // ? outside a day field
		"cron(0 10 * * ? 1969)",    // year below range
		"cron(60 10 * * ? *)",      // minute out of range
		"cron(0 10 32 * ? *)",      // day out of range
		"cron(0 10 * 13 ? *)",      // month out of range
		"cron(0 10 ? * 8 *)",       // weekday out of range
		"cron(0 10 ? * 2#6 *)",     // ordinal out of range
		"cron(0 10 * * ?)",         // five fields
		"cron(0 10 * * ? * *)",     // seven fields
		"cron(0 10 ? * 2#1,4#2 *)", // more than one ordinal
		"cron(0/0 10 * * ? *)",     // zero step
		"cron(0 10 * FOO ? *)",     // unknown month name
		"rate(5 minutes)",          // not a cron
	}
	for _, expr := range bad {
		if _, err := Parse(expr); err == nil {
			t.Errorf("%s should be refused", expr)
		}
	}
}

func TestNextExhaustsTheYearField(t *testing.T) {
	e, err := Parse("cron(0 0 1 1 ? 2020)")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := e.Next(after); ok {
		t.Errorf("a 2020-only expression has no next firing after 2026, got %v", got)
	}
}

// TestWDayPastTheMonthNeverFires: a day the month does not have has no
// nearest weekday, so the expression simply never matches in that month —
// the same as AWS, where there is no 30th of February to move away from.
//
// nearestWeekday clamps n to the last day of the month, but the day loop
// never offers it a day the month does not contain, so that clamp is
// defensive and unreachable. Written down here so the next person reading
// the 90.9% does not go looking for the case that would cover it.
func TestWDayPastTheMonthNeverFires(t *testing.T) {
	e, err := Parse("cron(0 7 30W FEB ? 2027)")
	if err != nil {
		t.Fatalf("30W is a legal expression even in February: %v", err)
	}
	if got, ok := e.Next(after); ok {
		t.Errorf("February has no 30th, so nothing should fire; got %v", got)
	}
}

func TestNextIsStrictlyAfter(t *testing.T) {
	e, _ := Parse("cron(31 9 * * ? *)")
	got, _ := e.Next(after) // 09:31 exactly is not after 09:31
	if !got.Equal(at(2026, 9, 9, 9, 31)) {
		t.Errorf("Next at the firing minute should be the next day, got %v", got)
	}
}
