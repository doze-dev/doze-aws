// Package docs embeds the per-service API-support ledgers and reads them.
//
// # The ledger is shipped code, not just documentation
//
// docs/api-support/*.md records every operation each service implements and at
// what tier — F functional, C cosmetic, S honest stub — and that table is
// parsed at RUNTIME to render the console's fidelity panel. For an emulator it
// is the most useful table a UI can show: real AWS never has to answer "is this
// call real here". So the format is an interface, and a change to it is a
// change to the product.
//
// # Why one parser and not two
//
// There were two, with different rules. console/info.go's kept every tier and
// had no guard against prose; console/coverage_test.go's kept F only, split
// bundled cells, and rejected a cell whose tokens do not look like operation
// names — which is what stops sns.md's "Mobile push (Platform
// applications/endpoints)" becoming an operation called "Mobile".
//
// Two readings of one file is how a format change breaks only one of them, and
// the one it breaks is whichever nobody ran. They live here now, and the two
// console tests and the runtime panel all go through the same code.
//
// It also removes a hole. Both console tests used filepath.Glob("../docs/...")
// and SKIPPED when it matched nothing, so renaming this directory turned three
// ratchets into three silent passes. Reading through the embedded FS cannot do
// that: //go:embed with no matches is a compile error.
package docs

import (
	"embed"
	"regexp"
	"sort"
	"strings"
)

//go:embed api-support/*.md
var FS embed.FS

// TierLegend is the one wording every ledger carries.
//
// There were two, nine files each — a long form that defines the tiers and a
// short one that assumes you already know them. The long form wins because the
// reader this is for is a stranger deciding whether to trust the project, and
// byte-equality is the right check because a legend is a key, not prose.
const TierLegend = "**F** = functional (real local semantics, SDK-observable behavior matches AWS) · " +
	"**C** = cosmetic (accepted and round-tripped, no local effect) · " +
	"**S** = stub (clean error; emulating it locally would be a lie)"

// Row is one ledger line: the operation cell as written (which may bundle
// several operations), its tier, and the maintainer's note.
type Row struct {
	Ops, Tier, Note string
}

// TierLabel spells the letter out, for a UI that has room for a word.
func (r Row) TierLabel() string {
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

var (
	tableHead = regexp.MustCompile(`(?i)^\|\s*Operation\s*\|\s*Tier\s*\|`)
	tableRow  = regexp.MustCompile(`^\|([^|]*)\|([^|]*)\|([^|]*)\|?`)
	opToken   = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
)

// verbFragments are the bare verbs a bundled ledger row splits into.
//
// A bare verb is a fragment of a bundled row ("Put/Get/Update/List/
// DeleteFunctionEventInvokeConfig"), never an operation — only the token
// carrying the full name checks anything. Left in, the fragments got "covered"
// by whatever stray literal said "Get". Publish IS a real operation (SNS), so
// it is deliberately not in the set.
var verbFragments = map[string]bool{
	"Put": true, "Get": true, "Update": true, "List": true, "Delete": true, "Create": true,
}

// Services is every service with a ledger, sorted.
func Services() []string {
	entries, err := FS.ReadDir("api-support")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(out)
	return out
}

// Read returns a ledger's raw text.
func Read(svc string) (string, error) {
	b, err := FS.ReadFile("api-support/" + svc + ".md")
	return string(b), err
}

// Rows parses one service's operation tables, keeping every tier, ordered
// functional first — the question a reader has is "what is real here", so real
// leads.
func Rows(svc string) []Row {
	text, err := Read(svc)
	if err != nil {
		return nil
	}
	var rows []Row
	forEachTableRow(text, func(m []string) {
		tier := normaliseTier(m[2])
		if tier == "" {
			return
		}
		rows = append(rows, Row{
			Ops:  strings.TrimSpace(strings.ReplaceAll(m[1], "`", "")),
			Tier: tier,
			Note: strings.TrimSpace(strings.ReplaceAll(m[3], "`", "")),
		})
	})
	sort.SliceStable(rows, func(i, j int) bool {
		rank := map[string]int{"F": 0, "C": 1, "S": 2}
		return rank[rows[i].Tier] < rank[rows[j].Tier]
	})
	return rows
}

// FTierOps is every functional operation named in a ledger, with bundled cells
// split out and prose cells rejected whole.
func FTierOps(svc string) []string {
	text, err := Read(svc)
	if err != nil {
		return nil
	}
	var ops []string
	forEachTableRow(text, func(m []string) {
		if normaliseTier(m[2]) != "F" {
			return
		}
		cell := strings.NewReplacer("`", "", "*", "").Replace(m[1])
		var toks []string
		for _, tok := range strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || r == ',' }) {
			tok = strings.TrimSpace(tok)
			if tok == "" || verbFragments[tok] {
				continue
			}
			if !opToken.MatchString(tok) {
				// A cell with prose in it is not a list of operations at all,
				// so the whole cell is dropped rather than the odd token —
				// half-reading "Mobile push (Platform applications/endpoints)"
				// invents an operation called Mobile.
				return
			}
			toks = append(toks, tok)
		}
		ops = append(ops, toks...)
	})
	return ops
}

// normaliseTier reads a tier cell, or returns "" for anything that is not one.
// "C→F" means "today C, planned F"; today is what both readings report.
func normaliseTier(cell string) string {
	tier := strings.TrimSpace(strings.NewReplacer("*", "", "`", "").Replace(cell))
	if i := strings.Index(tier, "→"); i >= 0 {
		tier = strings.TrimSpace(tier[:i])
	}
	switch tier {
	case "F", "C", "S":
		return tier
	}
	return ""
}

// forEachTableRow walks the operation tables in a ledger.
func forEachTableRow(text string, fn func(m []string)) {
	inTable := false
	for _, line := range strings.Split(text, "\n") {
		if tableHead.MatchString(line) {
			inTable = true
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			inTable = false
			continue
		}
		if !inTable || strings.HasPrefix(trimmed, "|-") {
			continue
		}
		if m := tableRow.FindStringSubmatch(line); m != nil {
			fn(m)
		}
	}
}

// Section returns the body of a "## heading" section, up to the next heading of
// the same level. Used to find the paragraph a claim lives in without caring
// where in the file it sits.
func Section(svc, heading string) string {
	text, err := Read(svc)
	if err != nil {
		return ""
	}
	want := "## " + heading
	var b strings.Builder
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, want):
			in = true
		case in && strings.HasPrefix(line, "## "):
			return b.String()
		case in:
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}
