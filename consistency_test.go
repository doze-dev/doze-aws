package dozeaws_test

// README's service table against the ledgers it summarises.
//
// # The drift edge this closes
//
// Every parity suite now asserts its own ledger's numbers, so
// docs/SUPPORT.md (<svc>) cannot fall behind the code. README carries the
// SAME numbers a second time — "21 of 22 dispatched operations · 48/48
// constraint cases", seventeen times — and nothing checked those at all. The
// drift the ledger work removed at one edge was still wide open at the next
// one along, and README is the file an evaluator reads first.
//
// # Why this is not in docs/
//
// The plan called it docs/consistency_test.go and that is not possible:
// README.md lives at the repo root and //go:embed cannot reach outside its own
// package directory. Reading it as ../README.md would reintroduce exactly the
// path-relative hole that docs/ledger.go was written to remove — a glob that
// matches nothing looks identical to a file that passes. So the test lives
// where the file it reads lives, and gets the same compile-time guarantee.
//
// # One parser, not two
//
// The ledger's claim is read through dozetest.LedgerTotals, which is the same
// regex the parity suites assert against. A second parser for the same
// sentence is how one of them ends up accepting a wording the other rejects.

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/docs"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

//go:embed README.md
var readmeMD string

var (
	// A row of the service table: name | operations | input validation.
	readmeRow = regexp.MustCompile(`(?m)^\| ([^|]+?) \| (?:[^|]*) \| (.+?) \|\s*$`)
	// The two numbers a row carries, worded as the ledgers word them.
	readmeCases = regexp.MustCompile(`(\d+)/(\d+) constraint cases`)
	readmeOps   = regexp.MustCompile(`(?:all |the )?(?:(\d+) of (?:all |the )?)?(\d+) (?:dispatched |routed )?operations`)
	ledgerTitle = regexp.MustCompile(`^## (.+?) — API support`)
)

// titleToService maps a ledger's H1 to its filename, derived rather than
// hand-listed: seventeen hand-written pairs is itself a thing that drifts.
func titleToService(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, svc := range docs.Services() {
		text, err := docs.Read(svc)
		if err != nil {
			t.Fatalf("%s: %v", svc, err)
		}
		m := ledgerTitle.FindStringSubmatch(text)
		if m == nil {
			t.Errorf("docs/SUPPORT.md (%s) has no `## <Service> — API support` "+
				"heading, so README cannot be matched to it by name", svc)
			continue
		}
		out[m[1]] = svc
	}
	return out
}

// readmeAliases are the two places README's wording and the ledger's H1
// deliberately differ. A closed list, so a THIRD divergence fails rather than
// being silently skipped as an unrecognised row.
var readmeAliases = map[string]string{
	// README names the slice that is implemented; the ledger names the service.
	// Only Parameter Store is served, and saying so in the table is worth more
	// than matching the filename.
	"SSM Parameter Store": "SSM",
}

func TestREADMEAgreesWithTheLedgers(t *testing.T) {
	byTitle := titleToService(t)
	seen := map[string]bool{}

	for _, row := range readmeRow.FindAllStringSubmatch(readmeMD, -1) {
		name, cell := strings.TrimSpace(row[1]), row[2]
		if name == "Service" || strings.HasPrefix(name, "-") {
			continue // header and separator
		}
		if alias, ok := readmeAliases[name]; ok {
			name = alias
		}
		svc, ok := byTitle[name]
		if !ok {
			continue // not a service row; README has other tables
		}
		seen[svc] = true

		want, ok := dozetest.LedgerTotals(svc)
		if !ok {
			t.Errorf("%s: docs/SUPPORT.md (%s) has no totals sentence to check "+
				"README against", name, svc)
			continue
		}
		t.Run(svc, func(t *testing.T) {
			cm := readmeCases.FindStringSubmatch(cell)
			if cm == nil {
				t.Fatalf("README's row has no \"N/M constraint cases\".\n"+
					"  The ledger says %d/%d.", want.Enforced, want.Cases)
			}
			if atoi(cm[1]) != want.Enforced || atoi(cm[2]) != want.Cases {
				t.Errorf("constraint cases: README says %s/%s, "+
					"docs/SUPPORT.md (%s) says %d/%d.\n"+
					"  The ledger is the one the parity suite asserts, so it is "+
					"the one that is right.",
					cm[1], cm[2], svc, want.Enforced, want.Cases)
			}

			om := readmeOps.FindStringSubmatch(cell)
			if om == nil {
				t.Fatalf("README's row has no operation count.\n"+
					"  The ledger says %s.", describeReadmeOps(want))
			}
			gotAudited, gotDispatched := atoi(om[2]), atoi(om[2])
			if om[1] != "" {
				gotAudited = atoi(om[1])
			}
			if gotAudited != want.AuditedOps || gotDispatched != want.DispatchedOps {
				t.Errorf("operations: README says %s, docs/SUPPORT.md (%s) says %s.",
					strings.TrimSpace(om[0]), svc, describeReadmeOps(want))
			}
		})
	}

	// Bidirectional, or a row quietly disappearing from README reads as a pass.
	for _, svc := range docs.Services() {
		if seen[svc] || svc == "apigatewayv2" {
			continue
		}
		t.Errorf("docs/SUPPORT.md (%s) has no row in README's service table.\n"+
			"  Every ledger is summarised there except apigatewayv2, which shares "+
			"the API Gateway row\n  because the two ship as one feature.", svc)
	}
}

// TestTheServiceCountIsNotFolklore pins the number three documents state in
// prose against the list the binary actually serves.
func TestTheServiceCountIsNotFolklore(t *testing.T) {
	const want = 17
	if got := len(dozeaws.Implemented); got != want {
		t.Errorf("dozeaws.Implemented has %d services, and README, "+
			"docs/README.md and docs/not-built.md all say %d.\n"+
			"  Adding a service means saying so in those three; this is the test "+
			"that makes it unavoidable.", got, want)
	}
	for _, svc := range dozeaws.Implemented {
		if _, err := docs.Read(svc); err != nil {
			t.Errorf("%s is implemented and has no section in docs/SUPPORT.md: %v",
				svc, err)
		}
	}
}

func describeReadmeOps(t dozetest.Totals) string {
	if t.AuditedOps == t.DispatchedOps {
		return fmt.Sprintf("all %d", t.DispatchedOps)
	}
	return fmt.Sprintf("%d of %d", t.AuditedOps, t.DispatchedOps)
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
