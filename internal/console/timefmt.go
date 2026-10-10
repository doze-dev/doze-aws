package console

import "time"

// Every time the console shows, shown one way.
//
// A time on a page is in the viewer's local zone. The console runs on the
// developer's machine, so "local" is the clock on their wall, which is the one
// they compare it to. Before this the formats were scattered across thirty
// sites and several of them wrote UTC with no zone beside a filter labelled
// "local time", so two columns of one page could disagree by the zone's offset
// and say nothing about it.
//
// Wire values (a request's StartTime, a test event's time) are not displayed
// and stay in the form the API takes; they do not come through here.
//
// The layouts live in this file and nowhere else. timefmt_test.go reads the
// package and fails on a display layout written anywhere beside it.

const (
	layoutStamp       = "2006-01-02 15:04:05"
	layoutStampMinute = "2006-01-02 15:04"
	layoutStampMillis = "2006-01-02 15:04:05.000 MST"
	layoutDay         = "2006-01-02"
	layoutClock       = "15:04:05"
	layoutClockMillis = "15:04:05.000"
	layoutClockTenths = "15:04:05.0"
)

// stamp is a date and a time to the second: a row's "created" column.
func stamp(t time.Time) string { return t.Local().Format(layoutStamp) }

// stampMinute is a date and a time to the minute, for a column that is read at a
// glance rather than compared.
func stampMinute(t time.Time) string { return t.Local().Format(layoutStampMinute) }

// stampMillis is the precise form with the zone's name, for a history where the
// order of events a millisecond apart is the question.
func stampMillis(t time.Time) string { return t.Local().Format(layoutStampMillis) }

// day is a calendar date.
func day(t time.Time) string { return t.Local().Format(layoutDay) }

// clock is a time of day to the second: a feed row, a record's arrival.
func clock(t time.Time) string { return t.Local().Format(layoutClock) }

// clockMillis is a time of day to the millisecond.
func clockMillis(t time.Time) string { return t.Local().Format(layoutClockMillis) }

// clockTenths is a time of day to a tenth of a second, for the tightest column.
func clockTenths(t time.Time) string { return t.Local().Format(layoutClockTenths) }

// zoned is what a displayed time says on hover: the same instant with its zone
// named and its offset, and in UTC. The page shows the local wall clock because
// that is the one the reader compares it to; a log line from somewhere else, or
// AWS's own console, is in UTC, and this is where the two are put side by side.
//
// It takes the string a formatter above produced and reads it back, so a
// template can add the hover to a value it already has without the view model
// carrying the instant twice. A value that is not a time (a dash, an empty
// cell) has no hover, and says so with "".
func zoned(shown string) string {
	for _, layout := range []string{layoutStampMillis, layoutStamp, layoutStampMinute} {
		if t, err := time.ParseInLocation(layout, shown, time.Local); err == nil {
			return t.Format("2006-01-02 15:04:05 MST") + " (UTC" + t.Format("-07:00") + ") · " +
				t.UTC().Format("2006-01-02 15:04:05") + " UTC"
		}
	}
	// A time of day alone is today's: a feed row, a record's arrival.
	now := time.Now().In(time.Local)
	for _, layout := range []string{layoutClockMillis, layoutClockTenths, layoutClock} {
		if c, err := time.ParseInLocation(layout, shown, time.Local); err == nil {
			t := time.Date(now.Year(), now.Month(), now.Day(), c.Hour(), c.Minute(), c.Second(), c.Nanosecond(), time.Local)
			return t.Format("15:04:05 MST") + " (UTC" + t.Format("-07:00") + ") · " + t.UTC().Format("15:04:05") + " UTC"
		}
	}
	return ""
}
