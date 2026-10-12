package dozetest

import (
	"html"
	"net/http"
	"strings"
	"testing"
)

// WholeRuns is how many times a request is sent to show that a refusal does
// not depend on the order a map happened to iterate in. The failure it is for
// (one element of a list kept, chosen by iteration order) was accepted on a
// quarter to nearly all of eight runs, so twenty in a row is not luck.
const WholeRuns = 20

// RefusedEveryTime sends a Query request WholeRuns times and requires, on every
// one, a 400 ValidationError that names the place the rule was broken.
//
// send is given the run number so a request that would create something can
// use a new name each time: a run that is wrongly accepted must not turn the
// next one into a different request.
//
// ValidationError is the code the Query services answer a broken member
// constraint with. Their models declare no error shape for it on any
// operation; it is what the same violation in a list's first element was
// already refused with.
func RefusedEveryTime(t *testing.T, what, at string, send func(run int) (status int, body string)) {
	t.Helper()
	for run := 0; run < WholeRuns; run++ {
		status, body := send(run)
		if status != http.StatusBadRequest || !strings.Contains(body, "<Code>ValidationError</Code>") {
			t.Fatalf("%s: run %d of %d was not refused as a ValidationError: %d %s", what, run+1, WholeRuns, status, body)
		}
		if !strings.Contains(html.UnescapeString(body), "'"+at+"'") {
			t.Fatalf("%s: run %d was refused somewhere other than '%s': %s", what, run+1, at, body)
		}
	}
}
