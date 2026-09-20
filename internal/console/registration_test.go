package console

// Adding a service to the console means touching twenty-two places. Eight are
// mandatory; the rest degrade silently — a missing empty-state copy reads as
// bland, a missing colour renders the wrong hue, a missing icon is a broken
// image with no error anywhere.
//
// That is a hand-maintained mirror of a list that grows, and this repo has
// now been bitten by that shape four separate times: the CloudWatch parity
// suite's `dispatched` set silently stopped replaying 110 cases, the rail's
// apiCounts drifted from serviceCounts, the CI fuzz matrix never learned
// about the CBOR decoder, and three separate hand-written path lists in this
// package's own tests each stopped covering whatever landed last.
//
// So this file derives everything from `catalog` — the one list — and checks
// both directions. It is in-package because catalog, emptyCopyBySvc and the
// embedded filesystems are unexported; the half that needs a booted Stack
// lives in registration_render_test.go, which cannot be in-package (dozeaws
// imports console, so an in-package test importing dozeaws is a cycle).

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// themeBlocks are the four places a service colour has to be defined. The
// light pair is `:root` and `[data-theme="light"]`; the dark pair is the
// media query and `[data-theme="dark"]`. A service defined in only the light
// pair renders light-tuned hues in dark mode, which is what CloudWatch Logs
// and CloudWatch did until this test was written.
const themeBlocks = 4

func appCSS(t *testing.T) string {
	t.Helper()
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}
	return string(b)
}

func layoutHTML(t *testing.T) string {
	t.Helper()
	b, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("reading layout.html: %v", err)
	}
	return string(b)
}

// TestEveryCatalogServiceIsFullyRegistered walks the one list and asserts each
// entry has everything a working service page needs.
func TestEveryCatalogServiceIsFullyRegistered(t *testing.T) {
	css := appCSS(t)
	layout := layoutHTML(t)
	for _, e := range catalog {
		t.Run(e.Key, func(t *testing.T) {
			// 1. The icon. awsIcon builds an <img src> by string concatenation,
			// so a missing file is a broken image and no error at all.
			if _, err := staticFS.ReadFile("static/aws/" + e.Key + ".svg"); err != nil {
				t.Errorf("static/aws/%s.svg is missing — the rail renders a broken image", e.Key)
			}

			// 2. The colour, in all four theme blocks.
			for _, v := range []string{"--svc-" + e.Key, "--svc-" + e.Key + "-ink"} {
				if n := strings.Count(css, v+":"); n != themeBlocks {
					t.Errorf("%s is defined in %d of %d theme blocks in app.css — "+
						"a service missing from the dark blocks renders light-tuned "+
						"hues in dark mode", v, n, themeBlocks)
				}
			}

			// 3. The rail. Hand-written per service in layout.html, not
			// generated from catalog, so it is the easiest thing to forget.
			if !strings.Contains(layout, `"Svc" "`+e.Key+`"`) {
				t.Errorf("layout.html has no rail entry for %q — the service is "+
					"unreachable from the nav", e.Key)
			}

			// 4. The empty-state voice.
			if _, ok := emptyCopyBySvc[e.Key]; !ok {
				t.Errorf("copy.go has no empty-state copy for %q; the page falls "+
					"back to the generic line", e.Key)
			}
		})
	}
}

// TestNoOrphanServiceRegistrations is the reverse direction: anything
// registered for a service key that `catalog` does not know about is either a
// leftover or a service someone forgot to list.
func TestNoOrphanServiceRegistrations(t *testing.T) {
	known := map[string]bool{}
	for _, e := range catalog {
		known[e.Key] = true
	}
	// sts has a colour and is resolvable from an ARN, but has no page and no
	// catalog entry on purpose: nothing in the console lists STS resources.
	known["sts"] = true

	t.Run("icons", func(t *testing.T) {
		entries, err := fs.ReadDir(staticFS, "static/aws")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range entries {
			key := strings.TrimSuffix(f.Name(), ".svg")
			if !known[key] {
				t.Errorf("static/aws/%s is an icon for %q, which is not in catalog",
					f.Name(), key)
			}
		}
	})

	t.Run("colours", func(t *testing.T) {
		re := regexp.MustCompile(`--svc-([a-z0-9]+):`)
		for _, m := range re.FindAllStringSubmatch(appCSS(t), -1) {
			if !known[m[1]] {
				t.Errorf("app.css defines --svc-%s, which is not in catalog", m[1])
			}
		}
	})

	t.Run("rail", func(t *testing.T) {
		re := regexp.MustCompile(`"Svc" "([a-z0-9]+)"`)
		for _, m := range re.FindAllStringSubmatch(layoutHTML(t), -1) {
			if !known[m[1]] {
				t.Errorf("layout.html has a rail entry for %q, which is not in catalog", m[1])
			}
		}
	})
}
