package dozetest

// Holding the ledger's numbers to what the test that produced them found.
//
// # The drift this closes
//
// Seventeen rejection-parity suites end by computing exactly the numbers the
// docs publish — "229/229 model-derived constraints enforced across 33 of the
// 37 operations" — and then throwing them into a t.Logf. Every one of those
// figures in README and in all eighteen `## Input validation` sections got
// there by somebody reading a log line and typing it into markdown.
//
// So regenerating a fixture with more cases kept every test green and silently
// staleified the prose, and three docs were wrong when this was written: logs.md
// claimed 197/197 across 21 operations against a 237-case fixture, and
// eventbridge.md claimed 438/438 across 39 against 448.
//
// # Why the markdown is the golden file
//
// The obvious fix is for each suite to write a committed JSON and for a separate
// test to diff the docs against it. That is worse, for three reasons that are
// not aesthetic:
//
// The number is a test OUTCOME, not a property of a fixture. stepfunctions
// reports 229 from total-gaps-unbuildable over len(ops)-len(needState);
// cases_sfn.json holds 248 cases across 37 operations. You cannot recover 229 by
// reading the fixture, and that is true for six of the eighteen.
//
// It cannot be one file. `go test ./...` runs the eighteen service packages in
// parallel, so eighteen writers on one JSON is a filesystem race; per-service
// files mean eighteen new fixtures plus an -update convention to remember.
//
// And it adds a third copy. test → JSON → prose has two places to drift where
// there is currently one.
//
// The ledger markdown is already committed and already derived, which is what
// the house rule about derived fixtures actually asks for. So the assertion goes
// where the number already is: inside the test that computed it.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/docs"
)

// Totals is what a rejection-parity suite found.
type Totals struct {
	// Enforced and Cases are the "N/M" pair: how many model-derived cases the
	// service refuses, out of how many the fixture holds.
	Enforced, Cases int
	// AuditedOps is how many operations the cases actually cover.
	AuditedOps int
	// DispatchedOps is how many the service dispatches, which is a LARGER and
	// different claim: it counts handlers, not replayable baselines. They
	// diverge when an operation's baseline cannot be replayed — a task token
	// that redeeming spends, a redrive that moves the execution on.
	//
	// ZERO means "this suite does not know". Several do not: cloudformation and
	// iam derive `ops` from the cases they can build, so len(ops) is the
	// audited set and the dispatched total is nowhere in the test. Asserting
	// len(ops) against the ledger's larger number would have "found" a
	// discrepancy that was really my instrumentation, and the tempting fix —
	// editing the doc down — would have destroyed a correct number.
	//
	// The dispatched count gets its own guard from the committed ops_*.json
	// fixtures, where it belongs: it is a fact about the dispatch table, not
	// about this replay.
	DispatchedOps int
}

// totalsSentence matches the claim every ledger makes, in each of the four
// wordings they use:
//
//	48/48 … across 21 of the 22 dispatched operations
//	438/438 … across all 39 dispatched operations
//	183/183 … across the 19 dispatched operations
//	333/333 … across 27 operations
//
// Phrase-anchored rather than position-anchored, deliberately. Fourteen of the
// eighteen open `## Input validation` with a DIFFERENT bolded sentence — "refuses
// what DynamoDB refuses" and friends — so anything that keyed off "the first
// bold paragraph" would read the wrong line in most of the tree.
var totalsSentence = regexp.MustCompile(
	`(\d+)/(\d+) model-derived constraints enforced across (?:all |the )?(?:(\d+) of (?:all |the )?)?(\d+)`)

// LedgerTotals reports what docs/SUPPORT.md (<svc>) claims, and whether it
// claims anything at all.
//
// Exported so README can be checked against the ledgers through the SAME regex
// the parity suites are checked through. A second parser for the same sentence
// is how one of them ends up accepting a wording the other rejects, which is
// the defect docs/ledger.go was created to remove for the operation tables.
func LedgerTotals(svc string) (Totals, bool) {
	section := Section(svc)
	if section == "" {
		return Totals{}, false
	}
	_, m := findTotals(section)
	if m == nil {
		return Totals{}, false
	}
	got := Totals{
		Enforced:      atoi(m[1]),
		Cases:         atoi(m[2]),
		DispatchedOps: atoi(m[4]),
	}
	if m[3] != "" {
		got.AuditedOps = atoi(m[3])
	} else {
		got.AuditedOps = got.DispatchedOps
	}
	return got, true
}

// Section is the ledger's input-validation section, or "".
func Section(svc string) string { return docs.Section(svc, "Input validation") }

// AssertLedgerTotals fails when docs/SUPPORT.md (<svc>) disagrees with what
// the caller measured.
//
// One line at the end of a parity suite, replacing nothing — the t.Logf stays,
// because the breakdown it prints is worth reading even when the headline
// numbers agree.
func AssertLedgerTotals(t testing.TB, svc string, got Totals) {
	t.Helper()
	if _, err := docs.Read(svc); err != nil {
		t.Errorf("no ledger for %s: %v", svc, err)
		return
	}
	// Scoped to the section that makes the claim. Searching the whole file
	// would work today and would happily match a totals sentence quoted in a
	// narrative section tomorrow, reporting the wrong line as stale.
	section := docs.Section(svc, "Input validation")
	if section == "" {
		t.Errorf("docs/SUPPORT.md (%s) has no ### Input validation section", svc)
		return
	}
	line, m := findTotals(section)
	if m == nil {
		t.Errorf("docs/SUPPORT.md (%s) has no totals sentence in its "+
			"## Input validation section.\n"+
			"  Expected the shape: **%d/%d model-derived constraints enforced "+
			"across %s, with `knownGaps` empty.**",
			svc, got.Enforced, got.Cases, describeOps(got))
		return
	}

	want := Totals{
		Enforced:      atoi(m[1]),
		Cases:         atoi(m[2]),
		DispatchedOps: atoi(m[4]),
	}
	// "all 39" and "the 19" mean every dispatched operation is audited; only
	// the "K of J" form distinguishes the two.
	if m[3] != "" {
		want.AuditedOps = atoi(m[3])
	} else {
		want.AuditedOps = want.DispatchedOps
	}

	var wrong []string
	if want.Enforced != got.Enforced || want.Cases != got.Cases {
		wrong = append(wrong, fmt.Sprintf("cases: the ledger says %d/%d, the suite measured %d/%d",
			want.Enforced, want.Cases, got.Enforced, got.Cases))
	}
	if want.AuditedOps != got.AuditedOps {
		wrong = append(wrong, fmt.Sprintf("audited operations: the ledger says %d, the suite measured %d",
			want.AuditedOps, got.AuditedOps))
	}
	if got.DispatchedOps != 0 && want.DispatchedOps != got.DispatchedOps {
		wrong = append(wrong, fmt.Sprintf("dispatched operations: the ledger says %d, the suite measured %d",
			want.DispatchedOps, got.DispatchedOps))
	}
	if len(wrong) == 0 {
		return
	}
	t.Errorf("docs/SUPPORT.md (%s) is out of date:\n  %s\n\nThe line to fix:\n  %s\n\n"+
		"It should read %d/%d … across %s. README's table row carries the same "+
		"numbers; nothing checks that the two agree yet, so change it too.",
		svc, strings.Join(wrong, "\n  "), strings.TrimSpace(line),
		got.Enforced, got.Cases, describeOps(got))
}

// sabotageSentence matches the other claim six ledgers make:
//
//	Removing the constraint table makes 75 of those 100 cases slip through
//
// One wording, unlike the totals sentence above, because these six were
// normalised to it when the measurement behind them was first written. They had
// drifted into four phrasings — "29 of them", "165 of the first 219" — and "the
// first 219" is the tell: it was measured against a partial fixture and then
// never revisited, which is the whole reason this is now a test.
var sabotageSentence = regexp.MustCompile(
	`Removing the constraint table makes (\d+) of those (\d+) cases slip through`)

// AssertSabotageFigure fails when docs/SUPPORT.md (<svc>) disagrees with what
// the caller measured by replaying its cases with constraintTables removed.
//
// This is the only figure in the ledgers that says the audit FOUND something
// rather than covered something — the answer to "is the model-derived table
// doing work the hand-written checks were not". Six ledgers published one and
// none of them had a procedure behind it.
func AssertSabotageFigure(t testing.TB, svc string, slipped, cases int) {
	t.Helper()
	section := docs.Section(svc, "Input validation")
	if section == "" {
		t.Errorf("docs/SUPPORT.md (%s) has no ### Input validation section", svc)
		return
	}
	line, m := findIn(section, sabotageSentence)
	if m == nil {
		t.Errorf("docs/SUPPORT.md (%s) has no sabotage sentence in its "+
			"## Input validation section.\n"+
			"  Expected the shape: Removing the constraint table makes %d of "+
			"those %d cases slip through.", svc, slipped, cases)
		return
	}
	if atoi(m[1]) == slipped && atoi(m[2]) == cases {
		return
	}
	t.Errorf("docs/SUPPORT.md (%s) is out of date:\n"+
		"  the ledger says %s of %s slip through, the replay measured %d of %d\n\n"+
		"The line to fix:\n  %s\n\n"+
		"This number is what the service ACCEPTS with its model-derived table "+
		"removed, so it\nmoves whenever the cases change or a hand-written check "+
		"is added. Both are fine;\nleaving the sentence behind is not.",
		svc, m[1], m[2], slipped, cases, strings.TrimSpace(line))
}

// findTotals returns the line carrying the claim, and its submatches.
func findTotals(text string) (string, []string) {
	return findIn(text, totalsSentence)
}

// findIn returns the paragraph carrying a claim, and its submatches.
//
// The sentence wraps across lines in every ledger, so the match is made against
// a whitespace-collapsed copy while the ORIGINAL paragraph is what gets quoted
// back — a failure naming a line nobody can find in the file is a failure that
// wastes the reader's time.
func findIn(text string, re *regexp.Regexp) (string, []string) {
	for para := range strings.SplitSeq(text, "\n\n") {
		flat := strings.Join(strings.Fields(para), " ")
		if m := re.FindStringSubmatch(flat); m != nil {
			return para, m
		}
	}
	return "", nil
}

// describeOps words the operation half the way the ledgers do.
func describeOps(t Totals) string {
	switch {
	case t.DispatchedOps == 0:
		return fmt.Sprintf("%d audited operations (leave the dispatched count as it is — "+
			"this suite does not measure it)", t.AuditedOps)
	case t.AuditedOps == t.DispatchedOps:
		return fmt.Sprintf("all %d dispatched operations", t.DispatchedOps)
	}
	return fmt.Sprintf("%d of the %d dispatched operations", t.AuditedOps, t.DispatchedOps)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
