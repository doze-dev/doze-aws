package console

// The fidelity Info panel.
//
// docs/SUPPORT.md records every operation each service implements and at
// what tier: F = functional (real local semantics), C = cosmetic (accepted,
// stored maybe, no local effect), S = stub (clean refusal). For an emulator
// this is the most useful table a console page can show — real AWS cannot offer
// it, because real AWS never has to answer "is this call real here".
//
// The parsing used to live here, and a second, differently-behaved copy lived
// in coverage_test.go. Both now call docs.Rows / docs.FTierOps, so the panel and
// the ratchets read the ledger identically and a format change cannot break
// only the one nobody ran.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/docs"
)

// infoSection is one ledger section of a console service's panel.
type infoSection struct {
	// Key is the ledger's own name for the section (docs/SUPPORT.md anchor).
	Key string
	// Title is shown above the table when a service has more than one.
	Title string
	Rows  []docs.Row
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
		s.Rows = docs.Rows(s.Key)
		out = append(out, s)
	}
	return out
}

// svcInfo renders one service's fidelity table.
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
