package docs

// The shape every section of docs/SUPPORT.md has to have.
//
// # What this is and is not
//
// Eighteen sections, each written by hand, together the thing an evaluator
// reads to decide whether to trust this project. The prose in them is the
// asset and is deliberately NOT generated — endpoints.md carries a published
// retraction of a promise it once made, and no template produces that.
//
// So this enforces STRUCTURE and VOCABULARY, never wording. A heading has to be
// there; what you write under it is yours. The one exception is the tier
// legend, which is byte-equal against the const, because a legend is a key
// rather than prose: two wordings of it means two readers get two different
// definitions of what "S" promises. That check is now document-wide rather
// than per-service — see TestTheTierLegendIsWrittenOnce.
//
// # The weak check, named as weak
//
// `### Verified against` cites files. This asserts those files EXIST, which
// proves the citation is not stale and proves nothing about whether the cited
// test covers the claim. That is worth having and worth being honest about: a
// path that has been renamed is a lie the reader cannot detect, and a path that
// exists is merely not that particular lie.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	ledgerH1     = regexp.MustCompile(`^## (.+?) — API support\s*$`)
	citedPath    = regexp.MustCompile("`([a-zA-Z0-9_./-]+\\.(?:go|json|md))`")
	totalsClaim  = regexp.MustCompile(`\d+/\d+ model-derived constraints enforced across`)
	validTiers   = map[string]bool{"F": true, "C": true, "S": true}
	requiredHead = []string{"Input validation", "Verified against", "Differences from AWS"}
)

func TestEveryLedgerHasTheSameShape(t *testing.T) {
	services := Services()
	if len(services) == 0 {
		t.Fatal("no ledgers: every check here would pass vacuously")
	}
	for _, svc := range services {
		t.Run(svc, func(t *testing.T) {
			text, err := Read(svc)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(text, "\n")
			if !ledgerH1.MatchString(first) {
				t.Errorf("first line is %q, want `## <Service> — API support`.\n"+
					"  README is matched to this section by that name, so the heading "+
					"is load-bearing.", first)
			}
			for _, h := range requiredHead {
				if Section(svc, h) == "" {
					t.Errorf("no `### %s` section.", h)
				}
			}
			if !totalsClaim.MatchString(flatten(Section(svc, "Input validation"))) {
				t.Errorf("`### Input validation` carries no totals sentence.\n" +
					"  The parity suite asserts it through dozetest.AssertLedgerTotals; " +
					"without the\n  sentence there is nothing for it to assert against.")
			}
			checkRows(t, svc)
			checkCitations(t, svc)
		})
	}
}

// TestTheTierLegendIsWrittenOnce is what the eighteen per-file byte-equality
// checks collapsed into.
//
// The legend used to be repeated in every ledger and asserted in every one,
// because eighteen copies of a key is eighteen chances to reword one. There is
// one copy now, so there is one assertion: it is present, byte-equal to the
// const, and it is not present twice.
func TestTheTierLegendIsWrittenOnce(t *testing.T) {
	doc := flatten(support())
	switch n := strings.Count(doc, flatten(TierLegend)); n {
	case 1:
		return
	case 0:
		t.Errorf("the tier legend does not appear verbatim.\n"+
			"  It is byte-equal on purpose: a legend is a key, not prose, and two "+
			"wordings\n  means two readers get two definitions of what a stub "+
			"promises. Paste:\n\n%s", TierLegend)
	default:
		t.Errorf("the tier legend appears %d times, want 1.\n"+
			"  Collapsing eighteen copies into one was the point; a second copy is "+
			"the drift\n  starting again.", n)
	}
}

// checkRows holds the operation table to its vocabulary: every tier cell is one
// of three letters, and every stub says why.
func checkRows(t *testing.T, svc string) {
	t.Helper()
	rows := Rows(svc)
	if len(rows) == 0 {
		t.Fatal("the operation table is empty or unparseable")
	}
	var stubs int
	for _, r := range rows {
		if !validTiers[r.Tier] {
			t.Errorf("%q has tier %q, want F, C or S.\n"+
				"  An unclassifiable cell is a row the console's coverage check "+
				"skips silently.", r.Ops, r.Tier)
		}
		if r.Tier != "S" {
			continue
		}
		stubs++
		if strings.TrimSpace(r.Note) == "" {
			t.Errorf("%q is a stub with no note.\n"+
				"  Every absence in this project is supposed to have an argument, "+
				"and docs/not-built.md\n  is built out of these notes — an empty "+
				"one becomes an entry that says nothing.", r.Ops)
		}
	}
	t.Logf("%d rows, %d stubs", len(rows), stubs)
}

// checkCitations proves the files `### Verified against` names are still there.
func checkCitations(t *testing.T, svc string) {
	t.Helper()
	section := Section(svc, "Verified against")
	if section == "" {
		return // already reported
	}
	cited := citedPath.FindAllStringSubmatch(section, -1)
	if len(cited) == 0 {
		t.Errorf("`### Verified against` cites no file.\n" +
			"  The section exists to point at what backs the claims above it; " +
			"without a path it is\n  an assertion about itself.")
		return
	}
	for _, m := range cited {
		path := m[1]
		// Cited relative to the service's own package unless the path already
		// names a directory, which is how the ledgers write them.
		candidates := []string{
			filepath.Join("..", path),
			filepath.Join("..", svc, path),
		}
		var found bool
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("`### Verified against` cites %s, which is not on disk.\n"+
				"  Looked in the repo root and in %s/. A renamed file leaves a "+
				"citation that reads\n  as evidence and is not.", path, svc)
		}
	}
}

// flatten collapses runs of whitespace, so a sentence that wraps across lines
// in markdown compares equal to the one-line form it is checked against.
//
// Every check here that matches a SENTENCE has to go through it. Both that
// forgot — the legend and the totals claim — failed on files that were correct,
// for the same reason, and the second one only looked like a real finding
// because the first had already been fixed.
func flatten(s string) string { return strings.Join(strings.Fields(s), " ") }
