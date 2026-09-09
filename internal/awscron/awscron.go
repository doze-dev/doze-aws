// Package awscron parses EventBridge's six-field cron expressions and
// computes their next firing time. The grammar is AWS's, not Vixie cron's:
//
//	cron(Minutes Hours Day-of-month Month Day-of-week Year)
//
// Minutes 0–59, hours 0–23, day-of-month 1–31 (with L, W), month 1–12 or
// JAN–DEC, day-of-week 1–7 or SUN–SAT where 1 is Sunday (with L, #), year
// 1970–2199. Each field takes `*`, a value, a list `a,b`, a range `a-b`,
// and a step `a/n` or `*/n`. Exactly one of day-of-month and day-of-week
// must be `?`. Times are UTC, as EventBridge evaluates them.
//
// Written in-tree rather than pulled in: the project keeps three runtime
// dependencies, and AWS's dialect differs from every cron library's.
package awscron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Expression is a parsed cron expression.
type Expression struct {
	minutes [60]bool
	hours   [24]bool
	months  [13]bool // 1-based
	years   map[int]bool
	anyYear bool

	// Day rules: exactly one side is active (the other was `?`).
	byDOM bool
	dom   [32]bool // 1-based
	domL  bool     // L: last day of month
	domW  []int    // nW: nearest weekday to day n
	domLW bool     // LW: last weekday
	dow   [8]bool  // 1-based, 1 = Sunday
	dowL  int      // nL: last weekday n of the month (0 = none; 8 = plain L, Saturday)
	dowN  []dowNth // n#k: the k-th weekday n
}

type dowNth struct{ day, nth int }

// Parse takes the six fields between `cron(` and `)`, or the whole
// `cron(...)` form.
func Parse(expr string) (*Expression, error) {
	inner := strings.TrimSpace(expr)
	if strings.HasPrefix(inner, "cron(") && strings.HasSuffix(inner, ")") {
		inner = inner[len("cron(") : len(inner)-1]
	}
	fields := strings.Fields(inner)
	if len(fields) != 6 {
		return nil, fmt.Errorf("a cron expression has six fields, got %d", len(fields))
	}
	e := &Expression{years: map[int]bool{}}
	var err error
	if err = parseSet(fields[0], 0, 59, nil, func(v int) { e.minutes[v] = true }); err != nil {
		return nil, fmt.Errorf("minutes: %w", err)
	}
	if err = parseSet(fields[1], 0, 23, nil, func(v int) { e.hours[v] = true }); err != nil {
		return nil, fmt.Errorf("hours: %w", err)
	}
	if err = parseSet(fields[3], 1, 12, monthNames, func(v int) { e.months[v] = true }); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if err = e.parseYears(fields[5]); err != nil {
		return nil, fmt.Errorf("year: %w", err)
	}
	domQ, dowQ := fields[2] == "?", fields[4] == "?"
	if domQ == dowQ {
		return nil, fmt.Errorf("exactly one of day-of-month and day-of-week must be ?")
	}
	if domQ {
		if err = e.parseDOW(fields[4]); err != nil {
			return nil, fmt.Errorf("day-of-week: %w", err)
		}
	} else {
		e.byDOM = true
		if err = e.parseDOM(fields[2]); err != nil {
			return nil, fmt.Errorf("day-of-month: %w", err)
		}
	}
	return e, nil
}

var monthNames = map[string]int{"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6, "JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12}
var dayNames = map[string]int{"SUN": 1, "MON": 2, "TUE": 3, "WED": 4, "THU": 5, "FRI": 6, "SAT": 7}

// parseSet handles `*`, values, lists, ranges and steps over [lo, hi].
func parseSet(field string, lo, hi int, names map[string]int, set func(int)) error {
	if field == "" {
		return fmt.Errorf("empty field")
	}
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("bad step in %q", part)
			}
			step, part = n, part[:i]
		}
		start, end := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err error
			if start, err = atoiNamed(a, names); err != nil {
				return err
			}
			if end, err = atoiNamed(b, names); err != nil {
				return err
			}
			if start > end {
				// A wrap-around range (20-2 hours, FRI-MON): everything from
				// start to the top, then the bottom to end, as AWS's own
				// examples use.
				for v := start; v <= hi; v += step {
					set(v)
				}
				for v := lo; v <= end; v += step {
					set(v)
				}
				continue
			}
		default:
			v, err := atoiNamed(part, names)
			if err != nil {
				return err
			}
			start = v
			if step == 1 {
				end = v
			}
		}
		if start < lo || end > hi {
			return fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for v := start; v <= end; v += step {
			set(v)
		}
	}
	return nil
}

func atoiNamed(s string, names map[string]int) (int, error) {
	if names != nil {
		if v, ok := names[strings.ToUpper(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a value", s)
	}
	return v, nil
}

func (e *Expression) parseYears(field string) error {
	if field == "*" {
		e.anyYear = true
		return nil
	}
	return parseSet(field, 1970, 2199, nil, func(v int) { e.years[v] = true })
}

func (e *Expression) parseDOM(field string) error {
	for _, part := range strings.Split(field, ",") {
		switch {
		case part == "L":
			e.domL = true
		case part == "LW":
			e.domLW = true
		case strings.HasSuffix(part, "W"):
			n, err := strconv.Atoi(strings.TrimSuffix(part, "W"))
			if err != nil || n < 1 || n > 31 {
				return fmt.Errorf("%q is not a weekday-nearest day", part)
			}
			e.domW = append(e.domW, n)
		default:
			if err := parseSet(part, 1, 31, nil, func(v int) { e.dom[v] = true }); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Expression) parseDOW(field string) error {
	for _, part := range strings.Split(field, ",") {
		switch {
		case part == "L":
			// Bare L in the day-of-week field is Saturday, every week; "7L"
			// is the last Saturday of the month.
			e.dow[7] = true
		case strings.HasSuffix(part, "L"):
			d, err := atoiNamed(strings.TrimSuffix(part, "L"), dayNames)
			if err != nil || d < 1 || d > 7 {
				return fmt.Errorf("%q is not a last-weekday", part)
			}
			e.dowL = d
		case strings.Contains(part, "#"):
			a, b, _ := strings.Cut(part, "#")
			d, err := atoiNamed(a, dayNames)
			if err != nil || d < 1 || d > 7 {
				return fmt.Errorf("%q is not a weekday", a)
			}
			n, err := strconv.Atoi(b)
			if err != nil || n < 1 || n > 5 {
				return fmt.Errorf("%q is not an ordinal 1-5", b)
			}
			if len(e.dowN) > 0 {
				return fmt.Errorf("only one #-expression is allowed in the day-of-week field")
			}
			e.dowN = append(e.dowN, dowNth{d, n})
		default:
			if err := parseSet(part, 1, 7, dayNames, func(v int) { e.dow[v] = true }); err != nil {
				return err
			}
		}
	}
	return nil
}

// dayMatches says whether a calendar day satisfies the day rules.
func (e *Expression) dayMatches(t time.Time) bool {
	day := t.Day()
	last := daysIn(t.Year(), t.Month())
	if e.byDOM {
		if e.dom[day] || (e.domL && day == last) {
			return true
		}
		if e.domLW && day == nearestWeekday(t.Year(), t.Month(), last) {
			return true
		}
		for _, n := range e.domW {
			if n <= last && day == nearestWeekday(t.Year(), t.Month(), n) {
				return true
			}
		}
		return false
	}
	wd := int(t.Weekday()) + 1 // 1 = Sunday
	if e.dow[wd] {
		return true
	}
	if e.dowL != 0 && wd == e.dowL && day+7 > last {
		return true
	}
	for _, n := range e.dowN {
		if wd == n.day && (day-1)/7+1 == n.nth {
			return true
		}
	}
	return false
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// nearestWeekday is the Mon–Fri day closest to n inside the same month.
func nearestWeekday(year int, month time.Month, n int) int {
	last := daysIn(year, month)
	if n > last {
		n = last
	}
	switch time.Date(year, month, n, 0, 0, 0, 0, time.UTC).Weekday() {
	case time.Saturday:
		if n > 1 {
			return n - 1
		}
		return n + 2
	case time.Sunday:
		if n < last {
			return n + 1
		}
		return n - 2
	}
	return n
}

// Next is the first firing strictly after `after`, in UTC. ok is false
// when the year field is exhausted (or a bounded search finds nothing).
func (e *Expression) Next(after time.Time) (time.Time, bool) {
	t := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := 2200
	if !e.anyYear {
		limit = 0
		for y := range e.years {
			if y > limit {
				limit = y
			}
		}
	}
	for t.Year() <= limit {
		if !e.anyYear && !e.years[t.Year()] {
			t = time.Date(t.Year()+1, 1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !e.months[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !e.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !e.hours[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if !e.minutes[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t, true
	}
	return time.Time{}, false
}
