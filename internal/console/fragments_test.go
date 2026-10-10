package console_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// A fragment that redraws a detail pane after an action shows what the page it
// replaces shows. Each handler used to pass its own subset of the page's data,
// so after a save the version count was wrong and a SecureString was drawn in
// clear. The check is the plainest one: the fragment, and the same region of a
// full reload, are the same markup.

var (
	detailSection = regexp.MustCompile(`(?s)<section class="detail">(.*?)</section>`)
	spaces        = regexp.MustCompile(`\s+`)
)

// region is the detail pane of a full page, normalised for comparison.
func region(t *testing.T, page string) string {
	t.Helper()
	m := detailSection.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no detail section in the page")
	}
	return normalise(m[1])
}

// normalise drops whitespace differences and the parts of a pane that
// legitimately differ between two renders of the same state: ids minted per
// render, and the relative age of a timestamp.
func normalise(s string) string {
	s = spaces.ReplaceAllString(s, " ")
	s = regexp.MustCompile(`\b\d+ (?:seconds?|minutes?|hours?) ago\b|just now|\bnow\b`).ReplaceAllString(s, "AGO")
	return strings.TrimSpace(s)
}

func sameMarkup(t *testing.T, what, fragment, reload string) {
	t.Helper()
	f, r := normalise(fragment), region(t, reload)
	if f == r {
		return
	}
	// Report where they part, not two walls of markup.
	i := 0
	for i < len(f) && i < len(r) && f[i] == r[i] {
		i++
	}
	from := max(0, i-60)
	t.Errorf("%s: the fragment and a full reload differ at byte %d\n  fragment: …%s\n  reload:   …%s",
		what, i, f[from:min(len(f), i+120)], r[from:min(len(r), i+120)])
}

func TestSecretsManagerFragmentsMatchAFullReload(t *testing.T) {
	h := newConsole(t)
	const name = "app/db"
	req(t, h, "POST", "/_console/sm/create", url.Values{"name": {name}, "value": {"v1"}})
	page := "/_console/sm/secret?name=" + url.QueryEscape(name)

	// A new version: the count, the stages and the policy all have to survive.
	put := req(t, h, "POST", "/_console/sm/put", url.Values{"name": {name}, "value": {"v2"}}).Body.String()
	sameMarkup(t, "after saving a new version", put, req(t, h, "GET", page, nil).Body.String())

	// Rotation off is a no-op to configure, and still redraws the strip.
	rot := req(t, h, "POST", "/_console/sm/rotation", url.Values{"name": {name}, "lambda": {""}}).Body.String()
	sameMarkup(t, "after configuring rotation", rot, req(t, h, "GET", page, nil).Body.String())
}

func TestParameterStoreFragmentsMatchAFullReload(t *testing.T) {
	h := newConsole(t)
	for _, c := range []struct{ name, typ string }{{"/app/plain", "String"}, {"/app/secret", "SecureString"}} {
		t.Run(c.typ, func(t *testing.T) {
			req(t, h, "POST", "/_console/ssm/create", url.Values{"name": {c.name}, "type": {c.typ}, "value": {"hunter2"}})
			page := "/_console/ssm/param?name=" + url.QueryEscape(c.name)

			put := req(t, h, "POST", "/_console/ssm/put", url.Values{"name": {c.name}, "value": {"hunter3"}}).Body.String()
			sameMarkup(t, "after saving", put, req(t, h, "GET", page, nil).Body.String())
		})
	}
}
