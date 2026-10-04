//go:build e2e

package console

// The e2e suite's route gate: which console routes did a browser drive?
//
// Built only with -tags e2e, which is how playwright.config.ts builds the
// binary it boots. When DOZE_E2E_ROUTES names a file, every route a request
// lands on is appended to it once, and the routes the console registers are
// written beside it (<file>.registered), from the router itself — so the gate
// compares what is served with what is reached, and cannot be fooled by how
// the routes happen to be spelled in source. e2e/route-gate.ts reads both.

import (
	"net/http"
	"os"
	"sync"

	"github.com/go-chi/chi/v5"
)

var routeLog struct {
	sync.Mutex
	seen map[string]bool
	file *os.File
	once sync.Once
}

func (c *Console) recordRoute(next http.Handler) http.Handler {
	path := os.Getenv("DOZE_E2E_ROUTES")
	if path == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeLog.once.Do(func() { os.WriteFile(path+".registered", []byte(c.registeredRoutes()), 0o644) })
		next.ServeHTTP(w, r)
		rctx := chi.RouteContext(r.Context())
		if rctx == nil || rctx.RoutePattern() == "" {
			return
		}
		line := routeKey(r.Method, rctx.RoutePattern(), c.prefix)
		routeLog.Lock()
		defer routeLog.Unlock()
		if routeLog.seen[line] {
			return
		}
		if routeLog.file == nil {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			routeLog.file, routeLog.seen = f, map[string]bool{}
		}
		routeLog.seen[line] = true
		routeLog.file.WriteString(line + "\n")
	})
}
