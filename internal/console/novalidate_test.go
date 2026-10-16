package console

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A hint is never a gate (the Teaching board), and the refusal the user reads
// is the service's, beside the control, with what they typed still in it (the
// States board, card 2).
//
// A form the browser validates is the other way round: min, max, pattern and
// required stop the submit and show the browser's own bubble ("Please match the
// requested format."), so the service is never asked and the sentence AWS would
// have said never appears. The States card's own example, 90000 in a field
// with max="43200", could not be produced from the page.
//
// So every form that submits through htmx says novalidate. The attributes stay
// on the inputs: the spinner stops at them, and the teaching line prints them.
func TestEveryFormThatSubmitsLetsTheServiceRefuse(t *testing.T) {
	// Template actions can hold a ">" ({{if gt .N 1}}), so they go first, as
	// cssguard does; then a form's start tag runs to the next ">".
	actionRe := regexp.MustCompile(`(?s){{.*?}}`)
	startRe := regexp.MustCompile(`(?s)<form\b[^>]*>`)
	submitRe := regexp.MustCompile(`\bhx-(post|put|patch|delete)\b`)

	files, err := os.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	forms := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".html") {
			continue
		}
		src := actionRe.ReplaceAllString(readAll(t, filepath.Join("templates", f.Name())), " ")
		for _, m := range startRe.FindAllString(src, -1) {
			if !submitRe.MatchString(m) {
				continue // a GET filter: nothing is refused by a service
			}
			forms++
			if !regexp.MustCompile(`\snovalidate\b`).MatchString(m) {
				one := strings.Join(strings.Fields(m), " ")
				if len(one) > 110 {
					one = one[:110] + "…"
				}
				bad = append(bad, f.Name()+": "+one)
			}
		}
	}
	if forms < 100 {
		t.Fatalf("found %d submitting forms; the scan is broken (there were about 170)", forms)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("a form the browser would validate before the service sees it: %s", b)
	}
}
