package console_test

// The rail links every surface, so a page that exists but is unreachable from
// the nav is only half-delivered.
//
// This checks the RENDERED page, where registration_test.go checks the
// template source. Both are worth having: the source check says the markup
// exists, this one says it survived rendering with a working href.
//
// It used to name three hrefs by hand, and separately maintained a list of
// every surface path for a render sweep. That sweep is gone — it had drifted,
// missing /logs and /cw — and lives in registration_render_test.go now,
// driven by the catalogue.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/console"
)

func TestRailLinksEveryCatalogService(t *testing.T) {
	c := newConsole(t)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_console/", nil))
	body := rec.Body.String()
	for _, e := range console.RegisteredServices() {
		href := `href="/_console` + e.Path + `"`
		if !strings.Contains(body, href) {
			t.Errorf("the rail has no link to %s — the page exists but nothing "+
				"navigates to it", e.Path)
		}
	}
}
