package console

// The console follows the design, and a test is what keeps it following.
//
// The design's stylesheet (plan/design/boards/con.css) is the one place the
// colours and the typeface are decided. The console's app.css used to carry its
// own near-miss of both (#5c5c5c against #565b63, Inter against IBM Plex), and
// they stayed apart because nothing compared them. These tests compare them:
// the token values are parsed out of both files, so changing one without the
// other is a failing test rather than a thing somebody notices on a board.
//
// The font checks guard the other half of "works offline": no external font
// URL, and every file the stylesheet names is actually in the embedded tree
// (a missing font does not fail anything — the browser quietly falls back, which
// is how the console ran without its typeface for a long time).

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const designCSS = "../../plan/design/boards/con.css"

var cssComments = regexp.MustCompile(`(?s)/\*.*?\*/`)

// cssDecls parses "--a: 1; --b: 2" into a map, keeping only custom properties.
func cssDecls(body string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(d, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if ok && strings.HasPrefix(k, "--") {
			out[k] = strings.ToLower(v)
		}
	}
	return out
}

// ruleBody returns the declarations of the first rule whose selector is exactly
// sel, searching from the first occurrence of marker (so the :root inside the
// prefers-color-scheme block can be told from the bare one).
func ruleBody(t *testing.T, css, marker, sel string) map[string]string {
	t.Helper()
	from := 0
	if marker != "" {
		from = strings.Index(css, marker)
		if from < 0 {
			t.Fatalf("marker %q not found", marker)
		}
	}
	re := regexp.MustCompile(`(?:^|[};{])\s*` + regexp.QuoteMeta(sel) + `\s*\{([^{}]*)\}`)
	m := re.FindStringSubmatch(css[from:])
	if m == nil {
		t.Fatalf("no rule %q after %q", sel, marker)
	}
	return cssDecls(m[1])
}

// The design's palette, token by token.
var designTokens = []string{"--bg", "--card", "--ink", "--mut", "--line", "--line2", "--sel", "--well",
	"--ok", "--warn", "--bad", "--deny", "--okbg", "--warnbg", "--badbg", "--denybg"}

func TestTokensEqualTheDesigns(t *testing.T) {
	design := cssComments.ReplaceAllString(readAll(t, designCSS), " ")
	app := cssComments.ReplaceAllString(readAll(t, "static/app.css"), " ")

	light := ruleBody(t, design, "", ".app")
	dark := ruleBody(t, design, "", ".app.dark")

	// Every place the console decides a colour: the default, the system
	// preference, and the two explicit choices. "System" is the absence of the
	// attribute, so a token missing from one of these four is a theme that
	// silently inherits the wrong one.
	cases := []struct {
		where  string
		got    map[string]string
		want   map[string]string
		darkly bool
	}{
		{":root", ruleBody(t, app, "", ":root"), light, false},
		{`:root[data-theme="light"]`, ruleBody(t, app, "", `:root[data-theme="light"]`), light, false},
		{"@media (prefers-color-scheme: dark) :root", ruleBody(t, app, "@media (prefers-color-scheme: dark)", ":root"), dark, true},
		{`:root[data-theme="dark"]`, ruleBody(t, app, "", `:root[data-theme="dark"]`), dark, true},
	}
	for _, c := range cases {
		for _, tok := range designTokens {
			want, ok := c.want[tok]
			if !ok {
				// The design's dark block only restates what changes; the light
				// block is the complete list, and a dark theme inherits the rest.
				want, ok = light[tok]
			}
			if !ok {
				t.Fatalf("design defines no %s", tok)
			}
			if got := c.got[tok]; got != want {
				t.Errorf("%s: %s is %q, the design (con.css) says %q", c.where, tok, got, want)
			}
		}
	}
}

// The console's own token names are aliases of the design's, so the 400 places
// that say var(--muted-foreground) follow the design without being edited.
func TestConsoleTokensAliasTheDesigns(t *testing.T) {
	app := cssComments.ReplaceAllString(readAll(t, "static/app.css"), " ")
	root := ruleBody(t, app, "", ":root")
	alias := map[string]string{
		"--background": "var(--bg)", "--foreground": "var(--ink)", "--card-foreground": "var(--ink)",
		"--muted": "var(--sel)", "--muted-foreground": "var(--mut)",
		"--primary": "var(--ink)", "--primary-foreground": "var(--card)",
		"--accent": "var(--ink)", "--ring": "var(--ink)",
		"--success": "var(--ok)", "--warning": "var(--warn)",
		"--destructive": "var(--bad)", "--refused": "var(--deny)",
		"--border": "var(--line)",
	}
	for tok, want := range alias {
		if root[tok] != want {
			t.Errorf(":root: %s is %q, want the alias %q", tok, root[tok], want)
		}
	}
	// The alias lives in :root only. A copy in a theme block would pin a value
	// there and stop following the design's token underneath it.
	for _, sel := range []string{`:root[data-theme="light"]`, `:root[data-theme="dark"]`} {
		body := ruleBody(t, app, "", sel)
		for tok := range alias {
			if _, ok := body[tok]; ok {
				t.Errorf("%s redefines %s; it is an alias set once in :root", sel, tok)
			}
		}
	}
}

// The console's type ladder is the design's. con.css sets 13/20 body, .h1 18/24,
// .h2 14/20, .cap 10px with .09em tracking, .m and .note 12px, tabular figures.
func TestTypeScaleIsTheDesigns(t *testing.T) {
	design := cssComments.ReplaceAllString(readAll(t, designCSS), " ")

	designSizes := map[string]bool{}
	for _, m := range regexp.MustCompile(`font:\s*(?:italic\s+)?\d+\s+(\d+)px`).FindAllStringSubmatch(design, -1) {
		designSizes[m[1]+"px"] = true
	}
	for _, m := range regexp.MustCompile(`font-size:\s*(\d+px)`).FindAllStringSubmatch(design, -1) {
		designSizes[m[1]] = true
	}
	for sz := range typeLadder {
		if !designSizes[sz] {
			t.Errorf("the console ladder holds %s, which con.css never sets", sz)
		}
	}

	for _, want := range []string{
		`.cap{font:600 10px/16px`, `letter-spacing:.09em`,
		`.h1{font:600 18px/24px`, `.h2{font:600 14px/20px`,
		`font:400 13px/20px`, `font-feature-settings:'tnum' 1`,
	} {
		if !strings.Contains(strings.ReplaceAll(design, "\n", ""), want) {
			t.Fatalf("con.css no longer says %q; update this test with the design", want)
		}
	}

	app := cssComments.ReplaceAllString(readAll(t, "static/app.css"), " ")
	body := ruleBody2(t, app, "body")
	for k, v := range map[string]string{"font-size": "13px", "line-height": "20px"} {
		if body[k] != v {
			t.Errorf("body %s is %q, the design's body is %q", k, body[k], v)
		}
	}
	if !strings.Contains(body["font-feature-settings"], `"tnum"`) {
		t.Errorf("body font-feature-settings is %q; the design sets tabular figures", body["font-feature-settings"])
	}
	if strings.Contains(body["letter-spacing"], "-") {
		t.Errorf("body letter-spacing is %q; the design does not track body text", body["letter-spacing"])
	}
}

// ruleBody2 is ruleBody for ordinary properties.
func ruleBody2(t *testing.T, css, sel string) map[string]string {
	t.Helper()
	re := regexp.MustCompile(`(?:^|[};{])\s*` + regexp.QuoteMeta(sel) + `\s*\{([^{}]*)\}`)
	m := re.FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no rule %q", sel)
	}
	out := map[string]string{}
	for _, d := range strings.Split(m[1], ";") {
		k, v, ok := strings.Cut(d, ":")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func TestNoInterAndNoExternalFont(t *testing.T) {
	css := cssComments.ReplaceAllString(readAll(t, "static/app.css"), " ")
	inter := regexp.MustCompile(`(?i)\binter(var|variable)?\b`)
	if m := inter.FindString(css); m != "" {
		t.Errorf("app.css still names %q; the typeface is IBM Plex", m)
	}
	if !strings.Contains(css, "IBM Plex Sans") || !strings.Contains(css, "IBM Plex Mono") {
		t.Error("app.css does not name IBM Plex Sans and IBM Plex Mono")
	}

	// No remote anything in the stylesheet: the console works offline.
	if m := regexp.MustCompile(`(?i)(@import|url\(\s*["']?(https?:)?//)`).FindString(css); m != "" {
		t.Errorf("app.css reaches off the machine: %q", m)
	}

	files, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := fs.ReadFile(templateFS, "templates/"+f.Name())
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		for _, bad := range []string{"fonts.googleapis", "fonts.gstatic", "InterVar", "typekit", "use.fontawesome"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s mentions %q", f.Name(), bad)
			}
		}
		if m := inter.FindString(regexp.MustCompile(`font-family:[^;"}]*`).FindString(s)); m != "" {
			t.Errorf("%s sets an inline font-family naming %q", f.Name(), m)
		}
		if m := regexp.MustCompile(`<link[^>]+href="https?://[^"]*"[^>]*>`).FindString(s); m != "" {
			t.Errorf("%s links an external resource: %s", f.Name(), m)
		}
	}
}

func TestEveryFontTheCSSNamesIsEmbedded(t *testing.T) {
	css := cssComments.ReplaceAllString(readAll(t, "static/app.css"), " ")
	faces := regexp.MustCompile(`(?s)@font-face\s*\{([^{}]*)\}`).FindAllStringSubmatch(css, -1)
	if len(faces) == 0 {
		t.Fatal("app.css has no @font-face rule")
	}
	have := map[string]bool{} // "family weight style" actually declared
	var missing []string
	for _, f := range faces {
		body := f[1]
		fam := regexp.MustCompile(`font-family:\s*["']?([^"';]+)`).FindStringSubmatch(body)
		wt := regexp.MustCompile(`font-weight:\s*([^;]+)`).FindStringSubmatch(body)
		st := regexp.MustCompile(`font-style:\s*([^;]+)`).FindStringSubmatch(body)
		if fam == nil || wt == nil {
			t.Errorf("@font-face without family or weight: %s", strings.TrimSpace(body))
			continue
		}
		style := "normal"
		if st != nil {
			style = strings.TrimSpace(st[1])
		}
		have[strings.TrimSpace(fam[1])+" "+strings.TrimSpace(wt[1])+" "+style] = true
		if !strings.Contains(body, "font-display: swap") {
			t.Errorf("@font-face %s %s lacks font-display: swap", fam[1], wt[1])
		}
		for _, u := range regexp.MustCompile(`url\(\s*["']?([^"')]+)["']?\s*\)`).FindAllStringSubmatch(body, -1) {
			if strings.Contains(u[1], "//") || strings.HasPrefix(u[1], "data:") {
				t.Errorf("@font-face url %q is not an embedded file", u[1])
				continue
			}
			// app.css is served from /static/, so a relative url resolves there.
			p := path.Join("static", u[1])
			if _, err := fs.Stat(staticFS, p); err != nil {
				missing = append(missing, p)
			}
		}
	}
	sort.Strings(missing)
	for _, p := range missing {
		t.Errorf("app.css names %s, which is not in the embedded tree", p)
	}

	for _, want := range []string{
		"IBM Plex Sans 400 normal", "IBM Plex Sans 500 normal", "IBM Plex Sans 600 normal", "IBM Plex Sans 400 italic",
		"IBM Plex Mono 400 normal", "IBM Plex Mono 500 normal", "IBM Plex Mono 700 normal",
	} {
		if !have[want] {
			t.Errorf("no @font-face for %s", want)
		}
	}

	// The licence travels with the fonts.
	for _, p := range []string{"static/fonts/OFL-IBM-Plex-Sans.txt", "static/fonts/OFL-IBM-Plex-Mono.txt"} {
		b, err := fs.ReadFile(staticFS, p)
		if err != nil || !strings.Contains(string(b), "SIL OPEN FONT LICENSE") {
			t.Errorf("%s: the OFL text must ship beside the fonts (%v)", p, err)
		}
	}
	if _, err := fs.Stat(staticFS, "static/InterVariable.woff2"); err == nil {
		t.Error("static/InterVariable.woff2 is still embedded; nothing uses it")
	}
}

// The first-run wire draws its heading at 28/34 and the line under it at 15/22
// (board Wire-Empty, inline: con.css has neither). They are not steps of the
// ladder, because the design uses them once, so they are tokens of their own;
// this holds the tokens to the board, so that they are the design's and not a
// size somebody liked.
func TestFirstRunSizesAreTheBoards(t *testing.T) {
	board := strings.ReplaceAll(readAll(t, "../../plan/design/boards/Wire-Empty.dc.html"), "\n", "")
	for _, want := range []string{`font: 600 28px/34px`, `font-size: 15px; line-height: 22px`} {
		if !strings.Contains(board, want) {
			t.Fatalf("Wire-Empty no longer says %q; update this test with the board", want)
		}
	}
	app := cssComments.ReplaceAllString(readAll(t, "static/app.css"), " ")
	root := ruleBody(t, app, "", ":root")
	if root["--text-first"] != "28px" || root["--text-lede"] != "15px" {
		t.Errorf("--text-first = %q, --text-lede = %q, want 28px and 15px", root["--text-first"], root["--text-lede"])
	}
	if h := ruleBody2(t, app, ".wf-h h1"); h["line-height"] != "34px" || h["font-size"] != "var(--text-first)" {
		t.Errorf(".wf-h h1 = %v, want the heading's 28/34", h)
	}
	if l := ruleBody2(t, app, ".wf-lede"); l["line-height"] != "22px" || l["font-size"] != "var(--text-lede)" {
		t.Errorf(".wf-lede = %v, want the line's 15/22", l)
	}
}
