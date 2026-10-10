package dozeaws_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The claims tables are read as a contract: every row has a recognised source
// and a scenario that exists, or says plainly that it does not yet.
//
// What this is for is the difference between "a scenario drives the call" and
// "AWS was asked". A claim resting on memory is a thing the code may not rely
// on, and the table is where that is counted.
func TestEveryClaimNamesARealScenarioOrSaysItIsToBeWritten(t *testing.T) {
	files, err := filepath.Glob("conformance/claims/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("no claims files: %v", err)
	}
	scenario := regexp.MustCompile(`(?m)^def (test_\w+)\(`)
	row := regexp.MustCompile(`^\|\s*(.+?)\s*\|\s*(model|doc|memory)\s*\|\s*(.+?)\s*\|$`)
	for _, f := range files {
		if filepath.Base(f) == "README.md" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rows := 0
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "| Claim") || strings.HasPrefix(line, "|---") {
				continue
			}
			m := row.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("%s:%d is not a claim row (| claim | model/doc/memory | scenario |): %s", f, i+1, line)
				continue
			}
			rows++
			sc := m[3]
			if sc == "(to write)" {
				continue
			}
			mod, fn, ok := strings.Cut(sc, "::")
			if !ok {
				t.Errorf("%s:%d: scenario %q is neither module::function nor (to write)", f, i+1, sc)
				continue
			}
			src, err := os.ReadFile(filepath.Join("conformance/tests", mod+".py"))
			if err != nil {
				t.Errorf("%s:%d: no conformance/tests/%s.py", f, i+1, mod)
				continue
			}
			found := false
			for _, d := range scenario.FindAllStringSubmatch(string(src), -1) {
				if d[1] == fn {
					found = true
				}
			}
			if !found {
				t.Errorf("%s:%d: %s has no scenario %s", f, i+1, mod, fn)
			}
		}
		if rows == 0 {
			t.Errorf("%s holds no claims", f)
		}
	}
}
