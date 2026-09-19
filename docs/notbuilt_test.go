package docs

// The register and the ledgers reconcile, in both directions.
//
// This is TestExemptionsAreReal's shape, applied to emulator coverage rather
// than console reachability — and it is the check that would have caught
// docs/reports/PHASE_8_REPORT.md claiming DynamoDB Streams was "the only
// remaining deferral" the moment Streams became F-tier.

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const registerPath = "not-built.md"

func TestEveryStubIsInTheRegister(t *testing.T) {
	seen := map[string]bool{}
	for _, svc := range Services() {
		for _, r := range Rows(svc) {
			if r.Tier != "S" {
				continue
			}
			k := key(svc, r.Ops)
			seen[k] = true
			e, ok := notBuilt[k]
			if !ok {
				t.Errorf("%s has no entry in docs/notbuilt.go.\n"+
					"  An absence with no verdict is one nobody decided — give it one of %v.\n"+
					"  The reason stays in the ledger; this only says which argument it is.",
					k, verdicts)
				continue
			}
			if !valid(e.verdict) {
				t.Errorf("%s has verdict %q, which is not one of %v", k, e.verdict, verdicts)
			}
			// A refusal with no reason is a dead end for the reader, and the
			// register leans on the ledger carrying one.
			if strings.TrimSpace(r.Note) == "" {
				t.Errorf("%s is refused with no reason in its ledger — the register "+
					"points at that note, so an empty one leaves the page saying nothing", k)
			}
		}
	}
	// The other direction: an entry for a row that no longer exists. Usually
	// the row became F-tier, which is the good case and the one most likely to
	// leave a stale claim behind.
	var stale []string
	for k := range notBuilt {
		if !seen[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	for _, k := range stale {
		t.Errorf("%s is in the register and is no longer an S-tier row — it was "+
			"implemented, or its wording changed. Either way the entry is stale.", k)
	}
}

// TestNotYetEntriesSayWhatItWouldTake keeps the two sections honest about each
// other. "Declined" and "not yet" are different promises, and a declined entry
// carrying a roadmap, or a deferred one carrying none, blurs them.
func TestNotYetEntriesSayWhatItWouldTake(t *testing.T) {
	for k, e := range notBuilt {
		switch {
		case e.verdict == notYet && strings.TrimSpace(e.takes) == "":
			t.Errorf("%s is listed notYet with nothing about what it would take — "+
				"which makes it indistinguishable from declined", k)
		case e.verdict != notYet && strings.TrimSpace(e.takes) != "":
			t.Errorf("%s is declined (%s) but carries a 'what it would take' — "+
				"either it is notYet or the roadmap is a decision nobody made", k, e.verdict)
		}
	}
}

// TestTheRegisterPageMatchesTheVerdicts holds docs/not-built.md to the table.
//
// The page is generated from notbuilt.go and the ledgers, so the prose that
// matters — the preamble and the verdict definitions — is hand-written and the
// list underneath cannot drift from the code. Regenerate with:
//
//	go test ./docs/ -run TestTheRegisterPageMatches -register.update
func TestTheRegisterPageMatchesTheVerdicts(t *testing.T) {
	want := renderRegister()
	if *updateRegister {
		if err := os.WriteFile(registerPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", registerPath)
		return
	}
	got, err := os.ReadFile(registerPath)
	if err != nil {
		t.Fatalf("%v\n  Generate it with: go test ./docs/ -run TestTheRegisterPageMatches -register.update", err)
	}
	if string(got) == want {
		return
	}
	t.Errorf("docs/not-built.md is out of date with docs/notbuilt.go and the ledgers.\n"+
		"  Regenerate: go test ./docs/ -run TestTheRegisterPageMatches -register.update\n"+
		"  (%d bytes on disk, %d generated)", len(got), len(want))
}

// bundleSize reads the "(N operations)" a bundled ledger cell states about
// itself.
var bundleSize = regexp.MustCompile(`\((\d+) operations?\)`)

// countOps is how many operations a ledger cell stands for: the count it
// states, or the number of names it lists, or one.
//
// Approximate on purpose, and the page says so. An exact number would need the
// dispatch table rather than the prose, and the figure is here to convey scale
// — "about nine hundred" versus "eighty-one" is the difference between an
// evaluator understanding the shape of what is declined and badly
// underestimating it.
func countOps(cell string) int {
	if m := bundleSize.FindStringSubmatch(cell); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	n := 0
	for _, tok := range strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || r == ',' }) {
		if opToken.MatchString(strings.TrimSpace(tok)) {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

func valid(v verdict) bool {
	for _, w := range verdicts {
		if v == w {
			return true
		}
	}
	return false
}

// renderRegister builds the page: hand-written preamble, then one table per
// verdict, with each row's reason read from the ledger it already lives in.
func renderRegister() string {
	var b strings.Builder
	b.WriteString(registerPreamble)

	type row struct{ svc, ops, note, takes string }
	byVerdict := map[verdict][]row{}
	total := 0
	for _, svc := range Services() {
		for _, r := range Rows(svc) {
			if r.Tier != "S" {
				continue
			}
			e := notBuilt[key(svc, r.Ops)]
			byVerdict[e.verdict] = append(byVerdict[e.verdict], row{svc, r.Ops, r.Note, e.takes})
			total++
		}
	}

	ops := 0
	for _, rows := range byVerdict {
		for _, r := range rows {
			ops += countOps(r.ops)
		}
	}
	fmt.Fprintf(&b, "\n%d entries below, covering about %d operations across %d services. "+
		"A row is one decision, which is often a family — \"Custom domains and base path "+
		"mappings (12 operations)\" is one argument, not twelve.\n",
		total, ops, len(Services()))

	for _, v := range verdicts {
		rows := byVerdict[v]
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].svc != rows[j].svc {
				return rows[i].svc < rows[j].svc
			}
			return rows[i].ops < rows[j].ops
		})
		if v == notYet {
			b.WriteString("\n---\n\n## Not yet\n\nNo argument against these. " +
				"What each would take:\n\n")
			fmt.Fprintf(&b, "| Service | What is absent | What it would take |\n|---|---|---|\n")
			for _, r := range rows {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", r.svc, r.ops, r.takes)
			}
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", v.heading())
		fmt.Fprintf(&b, "| Service | What is absent | Why |\n|---|---|---|\n")
		for _, r := range rows {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", r.svc, r.ops, r.note)
		}
	}
	b.WriteString(registerFooter)
	return b.String()
}
