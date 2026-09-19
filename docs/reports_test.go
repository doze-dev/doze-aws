package docs

// A dated report is allowed to be stale. It is not allowed to be stale
// silently.
//
// # The problem these had
//
// docs/reports/ holds eight phase reports written between 2026-07-10 and
// 2026-07-12, and they are worth keeping: they are the record of how the thing
// was built, and the reasoning in them is still good. What went wrong is that
// they read as current. PHASE_8_REPORT.md said "the only remaining deferral is
// DynamoDB Streams", which was true the day it was written and became false the
// moment Streams shipped; six and seven describe a repo with ten services,
// which is now seventeen.
//
// Correcting them would be worse than leaving them — a phase report edited to
// match today is no longer a record of anything. So each one says what it is,
// at the top, and this test is what stops the next one being written without
// that line.
//
// # Why the date is checked and not just the sentence
//
// A required sentence that is always the same is a sentence people paste, and a
// pasted one carries whatever date it was copied from. Checking it against the
// report's own `Date:` line makes the note say something true about the file it
// is in rather than something true about the file it came from.
//
// # Why an embed
//
// Same reason ledger.go gives: //go:embed with no matches is a compile error,
// so renaming or emptying this directory cannot turn the check into a silent
// pass. A filepath.Glob that returns nothing looks exactly like eight files
// that all pass.

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

//go:embed reports/*.md
var reportFS embed.FS

var (
	reportDate     = regexp.MustCompile(`(?m)^Date: (\d{4}-\d{2}-\d{2})\s*$`)
	historicalNote = regexp.MustCompile(`(?m)^> \*\*Historical\.\*\* A snapshot as of (\d{4}-\d{2}-\d{2}),`)
)

func TestEveryPhaseReportSaysItIsHistorical(t *testing.T) {
	entries, err := reportFS.ReadDir("reports")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no reports: this check would pass vacuously")
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			raw, err := reportFS.ReadFile("reports/" + name)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)

			dm := reportDate.FindStringSubmatch(text)
			if dm == nil {
				t.Fatalf("no `Date: YYYY-MM-DD` line.\n" +
					"  Every report carries one, and the historical note below is " +
					"checked against it.\n  Without a date there is nothing to say " +
					"the report is a snapshot OF.")
				return
			}
			nm := historicalNote.FindStringSubmatch(text)
			if nm == nil {
				t.Fatalf("no historical note.\n%s", wantNote(dm[1]))
			}
			if nm[1] != dm[1] {
				t.Errorf("the note says %s and the report is dated %s.\n"+
					"  A note pasted from another report carries that report's date, "+
					"which is how this\n  line stops meaning anything. It has to name "+
					"the day THIS report describes.", nm[1], dm[1])
			}

			// Near the top, before the body. A disclaimer under the third
			// heading is one the reader meets after the stale claim.
			if i := strings.Index(text, "\n## "); i >= 0 && strings.Index(text, nm[0]) > i {
				t.Errorf("the historical note sits below the first `## ` heading.\n" +
					"  It has to come before the content it qualifies, or a reader " +
					"meets the stale\n  claim first and the note second.")
			}
		})
	}
}

func wantNote(date string) string {
	return fmt.Sprintf("  Add this immediately after the `Date:` line:\n\n"+
		"> **Historical.** A snapshot as of %s, kept as written rather than\n"+
		"> corrected. Counts, scope and any claim about what \"remains\" describe the\n"+
		"> repo on that day — for current state see [docs/api-support](../api-support)\n"+
		"> and [docs/not-built.md](../not-built.md).\n\n"+
		"  These reports are the record of how this was built and are worth keeping.\n"+
		"  Editing one to match today destroys what it is; saying what it is does not.",
		date)
}
