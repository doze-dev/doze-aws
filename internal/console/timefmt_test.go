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
