package console

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/peers"
)

func routerOnly(t *testing.T) *Console {
	t.Helper()
	c, err := New(Options{Peers: peers.None()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The move from ServeMux to chi must neither drop a route nor invent one.
// testdata/routes.txt is what the ServeMux registered; the router is held to it.
func TestTheRouterServesExactlyTheRoutesTheMuxDid(t *testing.T) {
	raw, err := os.ReadFile("testdata/routes.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The bare prefix and its slash are one route in chi: the group's root.
		if line == "GET (prefix)" {
			continue
		}
		want[line] = true
	}
	got := map[string]bool{}
	for _, line := range strings.Split(routerOnly(t).registeredRoutes(), "\n") {
		if line != "" {
			got[line] = true
		}
	}
	var missing, extra []string
	for r := range want {
		if !got[r] {
			missing = append(missing, r)
		}
	}
	for r := range got {
		if !want[r] {
			extra = append(extra, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("routes the ServeMux served and the router does not:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("routes the router serves and the ServeMux did not:\n  %s", strings.Join(extra, "\n  "))
	}
}

func serve(c *Console, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// A literal and a parameter can share a position, and a request has to reach
// whichever it names: chi prefers the literal and falls back to the parameter
// inside one router, which is why those groups are flat.
func TestLiteralsAndParametersSharingAPositionBothReach(t *testing.T) {
	c := routerOnly(t)
	for _, tc := range []struct{ method, pattern, path string }{
		{"POST", "/iam/user/{name}/keys", "/iam/user/ada/keys"},
		{"POST", "/iam/{kind}/{name}/attach", "/iam/user/ada/attach"},
		{"POST", "/iam/{kind}/{name}/attach", "/iam/role/lambda/attach"},
		{"GET", "/eb/destinations", "/eb/destinations"},
		{"GET", "/eb/{bus}", "/eb/orders"},
		{"POST", "/sfn/activities/create", "/sfn/activities/create"},
		{"POST", "/sfn/{machine}/start", "/sfn/orders/start"},
		{"GET", "/lambda/layers", "/lambda/layers"},
		{"GET", "/lambda/{fn}", "/lambda/orders"},
	} {
		// A request that reaches a handler may fail inside it (nothing is wired
		// here); one that reaches none is the fallback — a 200 wire page for a
		// GET, a 405 for a POST. A POST that answers 405 reached nothing.
		rec := serve(c, tc.method, "/_console"+tc.path)
		if tc.method == "POST" && rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s reached no route (wanted %s)", tc.method, tc.path, tc.pattern)
		}
	}
}

// An unmatched GET is the console's not-found page, at any depth and under
// any group; anything that would change something is a 405.
func TestAnUnmatchedGETIsTheNotFoundPageAndAnUnmatchedPOSTIs405(t *testing.T) {
	c := routerOnly(t)
	for _, p := range []string{"/_console/zzz", "/_console/sqs/nonexistent/deep/path", "/_console/iam/a/b/c/d"} {
		if rec := serve(c, "GET", p); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Not found") {
			t.Errorf("unmatched GET %s = %d, want the not-found page", p, rec.Code)
		}
	}
	if rec := serve(c, "POST", "/_console/no-such-surface"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("unmatched POST = %d, want 405", rec.Code)
	}
	if rec := serve(c, "GET", "/_console"); rec.Code != 200 {
		t.Errorf("the bare prefix = %d, want the wire (200)", rec.Code)
	}
}

// ServeMux answered a HEAD from the GET handler; chi does not unless asked.
func TestHEADIsAnsweredByItsGET(t *testing.T) {
	c := routerOnly(t)
	if rec := serve(c, "HEAD", "/_console/deck"); rec.Code != 200 {
		t.Errorf("HEAD /deck = %d, want 200", rec.Code)
	}
}

// A cross-origin write is refused before any route is looked at.
func TestACrossOriginWriteIsRefusedBeforeRouting(t *testing.T) {
	c := routerOnly(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/_console/sqs/create", nil)
	r.Header.Set("Origin", "http://evil.example")
	c.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d, want 403", rec.Code)
	}
}
