package console_test

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A name is data, not code. Interpolated into a script expression it is code:
// html/template escapes it for HTML, and the HTML parser decodes that back to the
// quote before Alpine reads the attribute, so a name with a ' in it ends the string
// literal and whatever follows runs. The sidebar filter did exactly this, in 18
// places, with a name that nothing stopped being (for API Gateway, anything).
//
// The rule is that user data reaches a script expression through a data-* attribute
// and is read back with $el.dataset. This holds the templates to it.
func TestNoTemplateInterpolatesDataIntoAScriptExpression(t *testing.T) {
	// Interpolations that are not user data: the mount prefix (an operator's flag),
	// and values from a fixed set (a service name, an authorization type).
	safe := regexp.MustCompile(`^\{\{\s*\.(Prefix|Service|C\.AuthType)\s*\}\}$`)
	attr := regexp.MustCompile(`\s(x-[\w:.-]+|@[\w:.-]+|:[\w-]+|on[a-z]+)="([^"]*)"`)
	quoted := regexp.MustCompile(`'(\{\{[^}]*\}\})'`)
	files, err := filepath.Glob("templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range attr.FindAllStringSubmatch(string(src), -1) {
			for _, q := range quoted.FindAllStringSubmatch(a[2], -1) {
				if !safe.MatchString(q[1]) {
					t.Errorf("%s: %s=%q puts %s inside a script string; pass it through data-* and read $el.dataset", f, a[1], shorten(a[2], 70), q[1])
				}
			}
		}
	}
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// An API Gateway API's name is free text, so it is where a hostile or merely
// unlucky name actually reaches a template. It must arrive in the sidebar as data.
func TestAPINameWithQuotesIsDataInTheSidebar(t *testing.T) {
	h := newConsole(t)
	hostile := `x'+(window.__pwned=1)+'"<b>`
	loc := create(t, h, "/_console/apigw/create", url.Values{"name": {hostile}})
	if loc == "" {
		t.Fatal("the API was not created")
	}
	page := req(t, h, "GET", "/_console/apigw", nil)
	body := page.Body.String()
	if page.Code != 200 {
		t.Fatalf("list: %d", page.Code)
	}
	// The name is in an attribute, escaped for HTML, and nowhere in a script expression.
	if !strings.Contains(body, `data-name="x&#39;&#43;(window.__pwned=1)&#43;&#39;&#34;&lt;b&gt;"`) {
		t.Errorf("the name is not carried as an escaped data-name attribute:\n%s", truncateBody(body))
	}
	for _, m := range regexp.MustCompile(`x-show="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], "__pwned") {
			t.Errorf("the name reached a script expression: x-show=%q", m[1])
		}
	}
	if !strings.Contains(body, `$el.dataset.name.toLowerCase().includes($store.filter.q.toLowerCase())`) {
		t.Error("the sidebar filter should read the name from the element's dataset")
	}
}
