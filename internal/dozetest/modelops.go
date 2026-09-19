package dozetest

// Every operation AWS documents is accounted for, from a fixture rather than a
// transcription.
//
// # What was here before
//
// Five services carried a `modelOperations` list — about three hundred
// operation names typed by hand out of `dzaudit list` — pinned by a magic
// count:
//
//	if len(modelOperations) != 37 { t.Fatalf("the frozen list has %d …") }
//
// It works, and it has two problems. The count only fails once somebody runs
// the tool and pastes a new list, which model-drift.yml's own header admits:
// "would fail — but only once someone ran the tool." And thirteen services
// never got one, while README and CONTRIBUTING said all of them had.
//
// `dzaudit ops <model>` emits the list now and it is committed per service, so
// the transcription is gone and the weekly drift job regenerates it. An
// operation AWS adds is a red build with nobody in the loop.

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// Table is one dispatch destination — handled, staged, or refused by name.
//
// It carries the operation NAMES rather than a predicate, because the check
// runs both ways: an operation in the model that reaches no table is a gap, and
// an operation in a table that the model does not document is a typo or a
// spelling AWS has retired. A predicate answers the first question and cannot
// answer the second.
type Table struct {
	Name string
	Ops  []string
}

func (tbl Table) has(op string) bool {
	for _, o := range tbl.Ops {
		if o == op {
			return true
		}
	}
	return false
}

// Names lists a dispatch map's keys, whatever it maps to — handler funcs,
// reasons, bare bools.
func Names[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ModelOps reads a committed ops_*.json.
func ModelOps(t testing.TB, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the model operation list: %v\n"+
			"  Regenerate it with: go run ./cmd/dzaudit ops <model> > %s", err, path)
	}
	var ops []string
	if err := json.Unmarshal(b, &ops); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if len(ops) == 0 {
		// A fixture that is empty would make every assertion below pass
		// vacuously, which is the failure mode this whole file exists to avoid.
		t.Fatalf("%s is empty", path)
	}
	sort.Strings(ops)
	return ops
}

// Coverage is one service's dispatch surface, measured against the model.
type Coverage struct {
	// Ops is the model's operation list, from ModelOps.
	Ops []string
	// Tables are the dispatch destinations: handled, staged, refused by name.
	Tables []Table
	// LocalOnly are operations doze-aws serves that AWS does not document —
	// deliberate extensions rather than mistakes. DozePeek reads an SQS queue
	// without consuming from it, which no AWS API offers because no AWS
	// console needs to; DozeAccessLog and DozeGeneratePolicy back the IAM
	// panel. They are named here so the check can tell an extension from a
	// misspelling, which otherwise look identical from the model's side.
	LocalOnly []string
	// Unreached is the burn-down list: operations the model documents that
	// reach no table and so answer a generic InvalidAction.
	//
	// Frozen in BOTH directions, the same shape as console/coverage_test.go's
	// uncovered. Growing means AWS added an operation nobody noticed — which is
	// how all of these got here. Shrinking means somebody closed a gap and the
	// list should say so.
	Unreached []string
}

// AssertCoverage checks that every operation the model documents appears in
// exactly one dispatch table, or is a listed gap.
//
// EXACTLY one, in both directions. Zero means the operation answers
// InvalidAction, which reads to a caller like a typo in their own code rather
// than a gap in this one — the distinction the refusal tables exist to
// preserve. More than one means two answers are possible and which you get
// depends on lookup order.
func AssertCoverage(t testing.TB, c Coverage) {
	t.Helper()
	allowed := map[string]bool{}
	for _, op := range c.LocalOnly {
		allowed[op] = true
	}
	expectedGap := map[string]bool{}
	for _, op := range c.Unreached {
		expectedGap[op] = true
	}
	assertCoverage(t, c.Ops, allowed, expectedGap, c.Tables)
}

func assertCoverage(t testing.TB, ops []string, localOnly, expectedGap map[string]bool, tables []Table) {
	t.Helper()
	documented := map[string]bool{}
	for _, op := range ops {
		documented[op] = true
	}
	var unaccounted, doubled []string
	for _, op := range ops {
		var in []string
		for _, tbl := range tables {
			if tbl.has(op) {
				in = append(in, tbl.Name)
			}
		}
		switch len(in) {
		case 0:
			unaccounted = append(unaccounted, op)
		case 1:
		default:
			doubled = append(doubled, op+" ("+joinNames(in)+")")
		}
	}
	// The gap list is frozen both ways, so a new hole and a closed one are
	// equally loud.
	var newGaps, closedGaps []string
	for _, op := range unaccounted {
		if !expectedGap[op] {
			newGaps = append(newGaps, op)
		}
	}
	reached := map[string]bool{}
	for _, op := range unaccounted {
		reached[op] = true
	}
	for op := range expectedGap {
		if !reached[op] {
			closedGaps = append(closedGaps, op)
		}
	}
	sort.Strings(closedGaps)
	if len(newGaps) > 0 {
		t.Errorf("%d operation(s) in the service model reach no dispatch table, so they "+
			"answer a generic InvalidAction — which reads as a typo in the caller "+
			"rather than a gap here:\n  %v\n"+
			"Handle them, refuse them by name with what they would need, or add them to "+
			"Unreached so the gap is written down rather than discovered.",
			len(newGaps), newGaps)
	}
	if len(closedGaps) > 0 {
		t.Errorf("%d operation(s) are listed in Unreached but now reach a table:\n  %v\n"+
			"Good — shorten the list.", len(closedGaps), closedGaps)
	}
	if len(doubled) > 0 {
		t.Errorf("%d operation(s) appear in more than one dispatch table, so the answer "+
			"depends on lookup order:\n  %v", len(doubled), doubled)
	}
	// The other direction: a table entry the model does not document. Usually a
	// typo, occasionally a spelling AWS has retired — either way the entry is
	// dead and the operation it was meant to cover is not covered.
	for _, tbl := range tables {
		var unknown []string
		for _, op := range tbl.Ops {
			if !documented[op] && !localOnly[op] {
				unknown = append(unknown, op)
			}
		}
		if len(unknown) > 0 {
			t.Errorf("%s names %d operation(s) the service model does not document:\n  %v\n"+
				"Either a misspelling — which covers nothing, so the real operation still "+
				"falls through — or a spelling AWS has retired, or a deliberate doze-aws "+
				"extension, which belongs in LocalOnly.",
				tbl.Name, len(unknown), unknown)
		}
	}
}

func joinNames(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += " and "
		}
		out += s
	}
	return out
}
