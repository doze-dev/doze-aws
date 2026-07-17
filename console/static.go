package console

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

// cacheStatic layers HTTP caching over the embedded static file server.
// Embedded files carry no modtime, so without this every asset is fully
// re-downloaded on every hard reload. ETags are content hashes computed once
// per path; vendored assets (font, CodeMirror, htmx, Alpine — they only change
// with a release) get a long max-age, first-party files (app.css, shell.js —
// they change while developing the console) revalidate on every load and ride
// the 304.
func cacheStatic(next http.Handler) http.Handler {
	var (
		mu    sync.Mutex
		etags = map[string]string{} // path → strong ETag; bounded by the embed set
	)
	etagFor := func(path string) string {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := etags[path]; ok {
			return t
		}
		body, err := staticFS.ReadFile(strings.TrimPrefix(path, "/"))
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(body)
		t := `"` + hex.EncodeToString(sum[:8]) + `"`
		etags[path] = t
		return t
	}
	vendored := func(path string) bool {
		return strings.Contains(path, "/cm/") || strings.Contains(path, "/aws/") ||
			strings.HasSuffix(path, ".min.js") || strings.HasSuffix(path, ".woff2")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := etagFor(r.URL.Path)
		if tag != "" {
			w.Header().Set("ETag", tag)
			if vendored(r.URL.Path) {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			} else {
				w.Header().Set("Cache-Control", "no-cache") // always revalidate → 304
			}
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
