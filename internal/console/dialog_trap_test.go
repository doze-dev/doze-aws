package console

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A dialog's focus trap is switched by an Alpine effect, and an effect re-runs
// only for what it READ the first time. shell.js loads after Alpine, so on the
// first run window.dozeTrap is undefined, and `window.dozeTrap && dozeTrap($el,
// open)` stops before it reads `open`: the effect never runs again and the
// dialog never traps focus. The effect has to read its flag first.
func TestADialogsFocusTrapReadsItsFlagFirst(t *testing.T) {
	files, _ := filepath.Glob("templates/*.html")
	if len(files) == 0 {
		t.Fatal("no templates found")
	}
	short := regexp.MustCompile(`x-effect="window\.dozeTrap &&`)
	traps := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "dozeTrap($el") {
				traps++
			}
			if short.MatchString(line) {
				t.Errorf("%s:%d: the effect tests window.dozeTrap before it reads the dialog's flag, so it never re-runs; write x-effect=\"[flag, window.dozeTrap && dozeTrap($el, flag)]\"", f, i+1)
			}
		}
	}
	if traps < 10 {
		t.Errorf("found %s dialogs with a focus trap; the check is not looking at what it thinks it is", strconv.Itoa(traps))
	}
}
