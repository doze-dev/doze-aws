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

// infoSvc maps console service keys onto ledger filenames where they differ.
var infoSvc = map[string]string{
	"ddb":   "dynamodb",
	"eb":    "eventbridge",
	"sm":    "secretsmanager",
	"apigw": "apigateway",
	"cfn":   "cloudformation",
	"sfn":   "stepfunctions",
}

// svcInfo renders one service's fidelity table.
func (c *Console) svcInfo(w http.ResponseWriter, r *http.Request) {
	svc := r.PathValue("svc")
	if mapped, ok := infoSvc[svc]; ok {
		svc = mapped
	}
	rows := docs.Rows(svc)
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Tier]++
	}
	c.partial(w, "info_panel", map[string]any{
		"Svc": svc, "Rows": rows,
		"F": counts["F"], "C": counts["C"], "S": counts["S"],
	})
}
