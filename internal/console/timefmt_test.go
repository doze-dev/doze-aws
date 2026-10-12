package console

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// One formatter for the times the console shows: the layouts are written in
// timefmt.go and nowhere else, and nothing displayed is formatted in UTC.
func TestDisplayedTimesGoThroughOneFormatter(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	layout := regexp.MustCompile(`\.Format\("(?:2006-01-02|15:04)`)
	utcDisplay := regexp.MustCompile(`\.UTC\(\)\.Format\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "timefmt.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if layout.MatchString(line) {
				t.Errorf("%s:%d writes a display layout; use the formatters in timefmt.go:\n\t%s", f, i+1, strings.TrimSpace(line))
			}
			// RFC3339 on the wire is not display, and is spelled with the
			// constant rather than a layout string.
			if utcDisplay.MatchString(line) && !strings.Contains(line, "time.RFC3339") {
				t.Errorf("%s:%d formats a time in UTC for display:\n\t%s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// Every time a template shows says its zone on hover: the element that holds it
// carries title="{{zoned X}}" for the same X. A relative time ("3 h ago") most
// of all, since it shows no instant at all.
//
// Two rules, read off the templates:
//
//  1. every {{ago X}} sits in an element whose opening tag has
//     title="{{zoned X}}";
//  2. a field whose name says it is a time (timeField below), written as text
//     rather than inside an attribute, sits in such an element too.
//
// Rule 2 knows a time by its field's name, which is all a template gives; a
// time under a name not listed is not seen. The list is every such name the
// view models carry today.
var (
	agoCall   = regexp.MustCompile(`\{\{ago ([^}]+)\}\}`)
	timeField = regexp.MustCompile(`\{\{((?:\$|\.)[A-Za-z.$]*\.?(?:Created|Updated|Modified|Changed|Started|Stopped|When|Time|Clock|LastEvent|DeletedAt|LastModified|LastRotated|Arrived|SentAt|RedriveDate))\}\}`)
)

// enclosingTag returns the opening tag of the element the text at idx sits in:
// the nearest "<name ...>" before it that is not a closing tag. inAttr reports
// that idx is inside a tag, so the value is an attribute's and not shown text.
func enclosingTag(src string, idx int) (tag string, inAttr bool) {
	before := src[:idx]
	open := strings.LastIndex(before, "<")
	for open >= 0 && strings.HasPrefix(before[open:], "</") {
		open = strings.LastIndex(before[:open], "<")
	}
	if open < 0 {
		return "", false
	}
	end := strings.Index(before[open:], ">")
	if end < 0 {
		return before[open:], true
	}
	return before[open : open+end+1], false
}

func TestEveryDisplayedTimeHasAZoneHover(t *testing.T) {
	files, _ := filepath.Glob("templates/*.html")
	if len(files) == 0 {
		t.Fatal("no templates found")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		lineOf := func(idx int) int { return strings.Count(src[:idx], "\n") + 1 }
		check := func(idx int, expr, shown string) {
			tag, inAttr := enclosingTag(src, idx)
			if inAttr {
				return
			}
			if !strings.Contains(tag, `title="{{zoned `+expr+`}}"`) {
				t.Errorf("%s:%d shows %s with no zone on hover; its element needs title=\"{{zoned %s}}\" (it is in %s)",
					f, lineOf(idx), shown, expr, tag)
			}
		}
		for _, m := range agoCall.FindAllStringSubmatchIndex(src, -1) {
			check(m[0], src[m[2]:m[3]], src[m[0]:m[1]])
		}
		for _, m := range timeField.FindAllStringSubmatchIndex(src, -1) {
			check(m[0], src[m[2]:m[3]], src[m[0]:m[1]])
		}
	}
}

func TestFormattersAreLocal(t *testing.T) {
	loc := time.FixedZone("test", 5*3600+1800) // an offset no UTC reading can match
	prev := time.Local
	time.Local = loc
	defer func() { time.Local = prev }()

	at := time.Date(2026, 10, 10, 6, 30, 45, 123_000_000, time.UTC)
	for name, got := range map[string]string{
		"stamp":       stamp(at),
		"stampMinute": stampMinute(at),
		"clock":       clock(at),
		"clockMillis": clockMillis(at),
		"day":         day(at),
	} {
		want := map[string]string{
			"stamp": "2026-10-10 12:00:45", "stampMinute": "2026-10-10 12:00", "clock": "12:00:45",
			"clockMillis": "12:00:45.123", "day": "2026-10-10",
		}[name]
		if got != want {
			t.Errorf("%s = %q, want %q (local, not UTC)", name, got, want)
		}
	}
}

func TestTheHoverNamesTheZoneAndGivesUTC(t *testing.T) {
	prev := time.Local
	time.Local = time.FixedZone("TST", 5*3600+1800)
	defer func() { time.Local = prev }()

	at := time.Date(2026, 10, 10, 6, 30, 45, 0, time.UTC)
	want := "2026-10-10 12:00:45 TST (UTC+05:30) · 2026-10-10 06:30:45 UTC"
	if got := zoned(stamp(at)); got != want {
		t.Errorf("zoned(stamp) = %q, want %q", got, want)
	}
	if got := zoned(stampMinute(at)); got != "2026-10-10 12:00:00 TST (UTC+05:30) · 2026-10-10 06:30:00 UTC" {
		t.Errorf("zoned(stampMinute) = %q", got)
	}
	if got := zoned(stampMillis(at)); got != want {
		t.Errorf("zoned(stampMillis) = %q", got)
	}
	if got := zoned(clock(at)); got != "12:00:45 TST (UTC+05:30) · 06:30:45 UTC" {
		t.Errorf("zoned(clock) = %q", got)
	}
	for _, notATime := range []string{"", "—", "just now", "3 days"} {
		if got := zoned(notATime); got != "" {
			t.Errorf("zoned(%q) = %q, want no hover", notATime, got)
		}
	}
}
