package console

// The fidelity Info panel.
//
// docs/api-support/*.md records every operation each service implements and
// at what tier: F = functional (real local semantics), C = cosmetic (accepted,
// stored maybe, no local effect), S = stub (clean refusal). For an emulator
// this is the most useful table a console page can show — real AWS cannot
// offer it, because real AWS never has to answer "is this call real here".
// The same ledger drives the coverage ratchet; this is its read-only face.

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/doze-dev/doze-aws/docs"
)

// infoRow is one ledger line: the operation cell as written (which may bundle
// several operations), its tier, and the maintainer's note.
type infoRow struct {
	Ops, Tier, Note string
}

// TierLabel spells the letter out for the panel header chips.
func (r infoRow) TierLabel() string {
	switch r.Tier {
	case "F":
		return "functional"
	case "C":
		return "cosmetic"
	case "S":
		return "stub"
	}
	return r.Tier
}

// infoSvc maps console service keys onto ledger filenames where they differ.
var infoSvc = map[string]string{
	"ddb":   "dynamodb",
	"eb":    "eventbridge",
	"sm":    "secretsmanager",
	"apigw": "apigateway",
	"cfn":   "cloudformation",
	"sfn":   "stepfunctions",
}

var (
	infoOnce  sync.Once
	infoCache map[string][]infoRow

	infoTableHead = regexp.MustCompile(`(?i)^\|\s*Operation\s*\|\s*Tier\s*\|`)
	infoTableRow  = regexp.MustCompile(`^\|([^|]*)\|([^|]*)\|([^|]*)\|?`)
)

// ledgerRows parses one service's operation tables, keeping every tier.
func ledgerRows(svc string) []infoRow {
	infoOnce.Do(func() {
		infoCache = map[string][]infoRow{}
		entries, err := docs.FS.ReadDir("api-support")
		if err != nil {
			return
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			b, err := docs.FS.ReadFile("api-support/" + e.Name())
			if err != nil {
				continue
			}
			var rows []infoRow
			inTable := false
			for _, line := range strings.Split(string(b), "\n") {
				if infoTableHead.MatchString(line) {
					inTable = true
					continue
				}
				if !strings.HasPrefix(strings.TrimSpace(line), "|") {
					inTable = false
					continue
				}
				if !inTable || strings.HasPrefix(strings.TrimSpace(line), "|---") || strings.HasPrefix(strings.TrimSpace(line), "|-") {
					continue
				}
				m := infoTableRow.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				tier := strings.TrimSpace(strings.NewReplacer("*", "", "`", "").Replace(m[2]))
				// "C→F" means "today C, planned F"; today is what the panel
				// reports, the same reading the ratchet takes.
				if i := strings.Index(tier, "→"); i >= 0 {
					tier = strings.TrimSpace(tier[:i])
				}
				if tier != "F" && tier != "C" && tier != "S" {
					continue
				}
				rows = append(rows, infoRow{
					Ops:  strings.TrimSpace(strings.ReplaceAll(m[1], "`", "")),
					Tier: tier,
					Note: strings.TrimSpace(strings.ReplaceAll(m[3], "`", "")),
				})
			}
			// Functional first, then cosmetic, then stubs — the question the
			// panel answers is "what is real here", so real leads.
			sort.SliceStable(rows, func(i, j int) bool {
				rank := map[string]int{"F": 0, "C": 1, "S": 2}
				return rank[rows[i].Tier] < rank[rows[j].Tier]
			})
			infoCache[name] = rows
		}
	})
	if mapped, ok := infoSvc[svc]; ok {
		svc = mapped
	}
	return infoCache[svc]
}

// svcInfo renders one service's fidelity table.
func (c *Console) svcInfo(w http.ResponseWriter, r *http.Request) {
	svc := r.PathValue("svc")
	rows := ledgerRows(svc)
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Tier]++
	}
	c.partial(w, "info_panel", map[string]any{
		"Svc": svc, "Rows": rows,
		"F": counts["F"], "C": counts["C"], "S": counts["S"],
	})
}
