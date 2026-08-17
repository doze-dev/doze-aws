package console

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestTemplateClassesAreStyled guards against the referenced-but-never-wired
// bug class: a template ships a class that no stylesheet defines, and the
// element silently renders unstyled. It has happened twice — .panel-head (the
// SQS peek header stacked as cramped blocks) and .form-page (create pages had
// a width but no padding, gluing titles to the pane border).
//
// Every class token used in the templates must either appear in a stylesheet
// or be listed in hookClasses with a reason.
func TestTemplateClassesAreStyled(t *testing.T) {
	// Classes that intentionally carry no styles: pure JS/layout hooks.
	hookClasses := map[string]string{
		"rt-cd":       "countdown text node — shell.js writes its textContent every second",
		"rt-cold":     "idle state IS the .rt-badge base look; only rt-warm adds rules",
		"sqs-peek":    "grid cell wrapper — the .sqs-work grid places it; no styles of its own",
		"sqs-compose": "grid cell wrapper — the .sqs-work grid places it; no styles of its own",
	}

	css := readAll(t, "static/app.css") + readAll(t, filepath.Join("static", "cm", "codemirror.min.css"))

	actionRe := regexp.MustCompile(`(?s){{.*?}}`)
	// Whitespace before class= keeps Alpine's bound :class="expr" (a JS
	// expression, not a class list) out of the scan.
	classRe := regexp.MustCompile(`\sclass="([^"]*)"|\sclass='([^']*)'`)

	seen := map[string][]string{} // class -> templates using it
	files, err := os.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".html") {
			continue
		}
		// Strip template actions from the WHOLE file first — actions can carry
		// quoted arguments ({{if eq .State "ENABLED"}}) that would otherwise
		// truncate the class-attribute capture at the inner quote. Replacing
		// with a space also keeps tokens on either side of an {{if}}/{{else}}
		// boundary from fusing into one.
		src := actionRe.ReplaceAllString(readAll(t, filepath.Join("templates", f.Name())), " ")
		for _, m := range classRe.FindAllStringSubmatch(src, -1) {
			for _, tok := range strings.Fields(m[1] + m[2]) {
				if len(seen[tok]) == 0 || seen[tok][len(seen[tok])-1] != f.Name() {
					seen[tok] = append(seen[tok], f.Name())
				}
			}
		}
	}

	var missing []string
	for tok, users := range seen {
		if _, ok := hookClasses[tok]; ok {
			continue
		}
		if styled(css, tok) {
			continue
		}
		missing = append(missing, tok+" (used in "+strings.Join(users, ", ")+")")
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("class .%s is referenced by a template but styled nowhere — add CSS or allowlist it in hookClasses with a reason", m)
	}
}

// styled reports whether class tok appears as a selector-ish token in css.
// A token ending in "-" is a dynamic prefix (class="fl-k-{{.Kind}}") and
// matches if any selector continues it.
func styled(css, tok string) bool {
	if strings.HasSuffix(tok, "-") {
		return strings.Contains(css, "."+tok)
	}
	re := regexp.MustCompile(`\.` + regexp.QuoteMeta(tok) + `([^a-zA-Z0-9_-]|$)`)
	return re.MatchString(css)
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestTemplateTokensAreDefined guards the other half of the styling contract.
// TestTemplateClassesAreStyled catches a class with no rules; this catches an
// inline style reaching for a custom property that does not exist.
//
// It has already happened twice in one week, both times from renaming a token
// in the stylesheet: --accent-blue became --accent and two templates kept the
// old name, and the hsl()-to-hex change left eleven templates passing hex into
// hsl(). Neither failed a test, neither failed a build, and both rendered as a
// silently inherited colour — the most expensive kind of wrong, because it
// looks deliberate.
func TestTemplateTokensAreDefined(t *testing.T) {
	css := readAll(t, "static/app.css")
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(css, -1) {
		defined[m[1]] = true
	}

	files, err := os.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	varRe := regexp.MustCompile(`var\((--[a-z0-9-]+)`)
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".html") {
			continue
		}
		src := readAll(t, filepath.Join("templates", f.Name()))
		for _, m := range varRe.FindAllStringSubmatch(src, -1) {
			tok := m[1]
			// Service tokens can be built by interpolation (--svc-{{.Svc}});
			// those resolve at render time and the prefix is what matters.
			if strings.HasPrefix(tok, "--svc-") {
				continue
			}
			if !defined[tok] {
				t.Errorf("%s uses var(%s), which app.css does not define", f.Name(), tok)
			}
		}
	}
}
