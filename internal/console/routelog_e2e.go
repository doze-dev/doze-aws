//go:build e2e

package console

// The e2e suite's route gate: which console routes did a browser drive?
//
// Built only with -tags e2e, which is how playwright.config.ts builds the
// binary it boots. Every route a request lands on is appended, once, to the
// file DOZE_E2E_ROUTES names; e2e/route-gate.ts reads it after the run and
// fails when a route the console registers was never reached.

import (
	"net/http"
	"os"
	"strings"
	"sync"
)

var routeLog struct {
	sync.Mutex
	seen map[string]bool
	file *os.File
}

func (c *Console) noteRoute(r *http.Request) {
	path := os.Getenv("DOZE_E2E_ROUTES")
	if path == "" {
		return
	}
	_, pattern := c.mux.Handler(r)
	if pattern == "" {
		return
	}
	// Patterns are registered under the console's prefix; the gate compares
	// them as console.go spells them, prefix-relative.
	// Some registrations pad the method with two spaces; the gate wants one.
	if f := strings.Fields(pattern); len(f) == 2 {
		pattern = f[0] + " " + strings.TrimPrefix(f[1], c.prefix)
	}
	routeLog.Lock()
	defer routeLog.Unlock()
	if routeLog.seen[pattern] {
		return
	}
	if routeLog.file == nil {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		routeLog.file, routeLog.seen = f, map[string]bool{}
	}
	routeLog.seen[pattern] = true
	routeLog.file.WriteString(pattern + "\n")
}
