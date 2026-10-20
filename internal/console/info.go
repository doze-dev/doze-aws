package console

// The fidelity Info panel.
//
// docs/SUPPORT.md records every operation each service implements and at
// what tier: F = functional (real local semantics), C = cosmetic (accepted,
// stored maybe, no local effect), S = stub (clean refusal). For an emulator
// this is the most useful table a console page can show — real AWS cannot offer
// it, because real AWS never has to answer "is this call real here".
//
// The ledger's "### Differences from AWS" bullets are the dialog's second half
// (boards Fidelity and Kms-19): what is real is the table, what is not the same
// as AWS is the prose beneath it, and both are SUPPORT.md's own words.
//
// The parsing used to live here, and a second, differently-behaved copy lived
// in coverage_test.go. Both now call docs.Rows / docs.FTierOps, so the panel and
// the ratchets read the ledger identically and a format change cannot break
// only the one nobody ran.

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/doze-dev/doze-aws/docs"
)

// infoSection is one ledger section of a console service's panel.
type infoSection struct {
	// Key is the ledger's own name for the section (docs/SUPPORT.md anchor).
	Key string
	// Title is shown above the table when a service has more than one.
	Title string
	Rows  []infoRow
	// Diffs is the ledger's "Differences from AWS", read with the rows.
	Diffs []docs.Difference
}

// infoRow is one ledger row as the dialog draws it: the row itself and its
// operation cell cut into the operations it names. Each operation is an anchor
// (id op-<section>-<Operation>, data-op the operation) that a refusal's link
// opens the dialog at: shell.js (dozeLedgerRow) marks and scrolls to its row.
type infoRow struct {
	docs.Row
	Parts []opPart
}

// opPart is a piece of an operation cell: an operation (Op set) or the
// separators and words between operations.
type opPart struct {
	Text, Op string
}

var (
	opName       = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	verbFragment = map[string]bool{"Put": true, "Get": true, "Update": true, "List": true, "Delete": true, "Create": true}
)

// splitOps cuts a ledger operation cell ("SendMessage / SendMessageBatch",
// "Put/Get/DeleteFunctionEventInvokeConfig", "Mobile push (Platform
// applications)") into its operations and what lies between them. A piece is an
// operation only when it is a whole operation name and not a bare verb, the same
// test the ledger's reader makes (docs.Rows), so a phrase is never an anchor.
func splitOps(cell string) []opPart {
	var out []opPart
	flush := func(tok string) {
		name := strings.TrimSpace(tok)
		switch {
		case tok == "":
		case opName.MatchString(name) && !verbFragment[name]:
			lead := tok[:strings.Index(tok, name)]
			if lead != "" {
				out = append(out, opPart{Text: lead})
			}
			out = append(out, opPart{Text: name, Op: name})
			if trail := tok[len(lead)+len(name):]; trail != "" {
				out = append(out, opPart{Text: trail})
			}
		default:
			out = append(out, opPart{Text: tok})
		}
	}
	start := 0
	for i, r := range cell {
		if r == '/' || r == ',' {
			flush(cell[start:i])
			out = append(out, opPart{Text: string(r)})
			start = i + 1
		}
	}
	flush(cell[start:])
	return out
}

// infoSvc maps a console service key onto the ledger sections it covers.
//
// A key absent from here is its own ledger name. A service the console shows
// as one list can be two ledgers: the API Gateway list holds REST APIs and HTTP
// APIs, which are different services with different operations and different
// tiers, so its panel carries both and names each. Showing only the REST rows
// told someone looking at an HTTP API that most of what it does was not there.
var infoSvc = map[string][]infoSection{
	"ddb":   {{Key: "dynamodb"}},
	"eb":    {{Key: "eventbridge"}},
	"sm":    {{Key: "secretsmanager"}},
	"cfn":   {{Key: "cloudformation"}},
	"sfn":   {{Key: "stepfunctions"}},
	"cw":    {{Key: "cloudwatch"}},
	"apigw": {{Key: "apigateway", Title: "REST APIs"}, {Key: "apigatewayv2", Title: "HTTP APIs"}},
}

// infoSections reads the ledger sections a console service key covers.
func infoSections(svc string) []infoSection {
	secs, ok := infoSvc[svc]
	if !ok {
		secs = []infoSection{{Key: svc}}
	}
	out := make([]infoSection, 0, len(secs))
	for _, s := range secs {
		// The emulator's own calls (DozePeek) come last, with a dash for a tier.
		for _, row := range append(docs.Rows(s.Key), docs.Extensions(s.Key)...) {
			s.Rows = append(s.Rows, infoRow{Row: row, Parts: splitOps(row.Ops)})
		}
		s.Diffs = docs.Differences(s.Key)
		out = append(out, s)
	}
	return out
}

// svcInfo renders one service's fidelity dialog body: the tier counts, the
// table of operations, and the ledger's "Differences from AWS". It runs when a
// dialog is opened, so the ledger is read (docs.Rows, docs.Differences) at that
// moment and held nowhere: an idle console carries none of it.
func (c *Console) svcInfo(w http.ResponseWriter, r *http.Request) {
	secs := infoSections(param(r, "svc"))
	counts := map[string]int{}
	rows := 0
	for _, s := range secs {
		rows += len(s.Rows)
		for _, row := range s.Rows {
			counts[row.Tier]++
		}
	}
	c.partial(w, "info_panel", map[string]any{
		"Sections": secs, "Empty": rows == 0,
		"F": counts["F"], "C": counts["C"], "S": counts["S"],
	})
}
