package console_test

// Every page in the catalogue renders — the half of the registration check
// that needs a booted Stack, and so cannot live in-package.
//
// It replaces three hand-written path lists that had each drifted: the one in
// surfaces_render_test.go and the two in console_test.go were between them
// missing /logs, /cw and eight of the sixteen create pages. A list of paths
// beside a list of services is the same mirror this whole file exists to
// remove, so the paths come from the catalogue.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/console"
)

func TestEveryCatalogPageRenders(t *testing.T) {
	c := newConsole(t)
	for _, e := range console.RegisteredServices() {
		t.Run(e.Key, func(t *testing.T) {
			assertRenders(t, c, "/_console"+e.Path)
			if e.CreatePath != "" {
				assertRenders(t, c, "/_console"+e.CreatePath)
			}
		})
	}
}

func TestEverySurfaceRenders(t *testing.T) {
	c := newConsole(t)
	for _, e := range console.RegisteredSurfaces() {
		t.Run(e.Key, func(t *testing.T) { assertRenders(t, c, "/_console"+e.Path) })
	}
}

// assertRenders fails on the debris a template error leaves behind rather than
// on the status code.
//
// Status alone proves nothing here: html/template has already written the
// <head> by the time it fails, so a page that dies mid-render still answers
// 200. That is exactly how the Lambda create page shipped broken behind a
// green status sweep — the comment at handlers_create.go still describes it.
func assertRenders(t *testing.T, h http.Handler, path string) {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET %s = %d, want 200", path, w.Code)
		return
	}
	body := w.Body.String()
	for _, bad := range []string{"template:", "can't evaluate", "<no value>"} {
		if strings.Contains(body, bad) {
			t.Errorf("GET %s rendered %q — the template failed mid-render and "+
				"answered 200 anyway", path, bad)
		}
	}
	// A page that died early is short; the shell alone is several KB.
	if len(body) < 2000 {
		t.Errorf("GET %s returned %d bytes — too short to be a rendered page",
			path, len(body))
	}
}
