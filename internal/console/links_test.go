package console_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// A link built from a resource's name survives the characters the service
// allows in it. The templates assembled "?name=%s" with printf, which html/template
// treats as an already-built URL and does not query-escape, so a "+" arrived as
// a space and a "#" cut the link short. Secrets Manager allows both.

var hrefRE = regexp.MustCompile(`(?:href|hx-get)="([^"]*)"`)

// linksTo returns the page's links (unescaped from HTML) that contain needle.
func linksTo(body, needle string) []string {
	var out []string
	for _, m := range hrefRE.FindAllStringSubmatch(body, -1) {
		if l := html.UnescapeString(m[1]); strings.Contains(l, needle) {
			out = append(out, l)
		}
	}
	return out
}

func TestALinkFromANameSurvivesItsCharacters(t *testing.T) {
	h := newConsole(t)
	for _, c := range []struct {
		svc, createPath, listPath, openPath string
		form                                url.Values
		name                                string
	}{
		{"sm", "/_console/sm/create", "/_console/sm", "/_console/sm/secret", url.Values{"value": {"x"}}, "app/db+prod=1"},
		{"logs", "/_console/logs/create", "/_console/logs", "/_console/logs/group", url.Values{}, "/app/a#b"},
	} {
		t.Run(c.svc, func(t *testing.T) {
			form := c.form
			form.Set("name", c.name)
			if rec := req(t, h, "POST", c.createPath, form); rec.Code != http.StatusSeeOther {
				t.Fatalf("create: %d\n%s", rec.Code, rec.Body)
			}

			// The list pane, and every tab link on the detail page.
			for _, page := range []string{c.listPath, c.openPath + "?name=" + url.QueryEscape(c.name)} {
				body := req(t, h, "GET", page, nil).Body.String()
				links := linksTo(body, c.openPath+"?name=")
				if len(links) == 0 {
					t.Fatalf("%s carries no link to %s", page, c.openPath)
				}
				for _, l := range links {
					// Compared as a parsed query: the escaping's hex case is not
					// the point, what the link NAMES is.
					u, err := url.Parse(l)
					if err != nil || u.Query().Get("name") != c.name {
						t.Errorf("%s: link %q names %q, want %q", page, l, u.Query().Get("name"), c.name)
					}
				}
			}

			// And what the link names is found: the page it opens is that resource's.
			rec := req(t, h, "GET", c.openPath+"?name="+url.QueryEscape(c.name), nil)
			if rec.Code != http.StatusOK || !strings.Contains(html.UnescapeString(rec.Body.String()), c.name) {
				t.Errorf("opening %q: %d, and the page does not name it", c.name, rec.Code)
			}
		})
	}
}
