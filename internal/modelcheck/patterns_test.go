package modelcheck_test

// Every regular expression the service tables carry must compile.
//
// modelcheck.go referenced a TestEveryPatternCompiles by name for the whole
// life of the package. It did not exist. This is it — and writing it turned up
// that the comment describing it was wrong about what a bad pattern does, which
// is corrected there.
//
// A pattern that does not compile matches NOTHING, and the walker refuses a
// value that does not match — so a broken entry refuses every request that
// carries the member, valid ones included. That is fail-closed, which is the
// right direction, and it means the rejection-parity suites already catch most
// of them: each one asserts its baseline is ACCEPTED before mutating it, and a
// pattern that refuses everything fails that assertion loudly.
//
// What they do not catch is a pattern on a member no baseline happens to set.
// That is the gap this closes, and it is why the check reads the committed
// case files rather than the Go tables: those files are generated from the same
// models by the same tool, they carry the pattern source verbatim
// ("constraint": "pattern ^[\\w+=,.@-]+$"), and there are eighteen of them in
// one place. Reaching the Go tables instead would mean a near-identical test in
// each of eighteen packages, because every constraintTables is unexported.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type auditCase struct {
	Operation  string `json:"operation"`
	Path       string `json:"path"`
	Constraint string `json:"constraint"`
}

func TestEveryPatternCompiles(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "*", "testdata", "cases_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A glob that silently matches nothing would make this pass vacuously,
	// which is the failure mode the fuzz workflow already guards against by
	// name. Eighteen services carry a model.
	if len(files) < 15 {
		t.Fatalf("found %d case files, expected one per modelled service (18) — "+
			"the glob is wrong, and a vacuous pass here is worse than no test", len(files))
	}

	seen := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var cases []auditCase
		if err := json.Unmarshal(raw, &cases); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, c := range cases {
			src, ok := strings.CutPrefix(c.Constraint, "pattern ")
			if !ok {
				continue
			}
			seen++
			if _, err := regexp.Compile(src); err != nil {
				t.Errorf("%s: %s at %s carries a pattern that does not compile: %v\n"+
					"  pattern: %s\n"+
					"  A pattern that does not compile matches nothing, so this constraint "+
					"refuses every request that sets the member — valid ones included.",
					filepath.Base(f), c.Operation, c.Path, err, src)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no pattern constraints found across any service — the test proved nothing")
	}
	t.Logf("checked %d pattern constraints across %d services", seen, len(files))
}
