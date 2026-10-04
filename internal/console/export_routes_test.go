package console

import (
	"net/http"
	"reflect"
	"runtime"
	"strings"

	"github.com/go-chi/chi/v5"
)

// RouteHandlers lists the console's routes with the handler each dispatches
// to: "METHOD /path" → "sqsSend". The mutation sweep reads the router with it
// instead of parsing console.go, so the property it checks holds for the
// routes that are served, however their registration is spelled.
func RouteHandlers(h http.Handler) map[string]string {
	c := h.(*Console)
	out := map[string]string{}
	chi.Walk(c.mux, func(method, route string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		name := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
		name = name[strings.LastIndex(name, ".")+1:]
		out[routeKey(method, route, c.prefix)] = strings.TrimSuffix(name, "-fm")
		return nil
	})
	return out
}
