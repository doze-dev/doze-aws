package console

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// routeKey spells a route the way the route gate and the route tests do:
// "METHOD /path", the path relative to the console's prefix, with no trailing
// slash, and the static tree as /static/.
func routeKey(method, pattern, prefix string) string {
	p := strings.TrimPrefix(pattern, prefix)
	switch {
	case p == "/static/*":
		return method + " /static/"
	case p == "":
		p = "/"
	case len(p) > 1:
		p = strings.TrimSuffix(p, "/")
	}
	return method + " " + p
}

// registeredRoutes lists every route the console serves, one "METHOD /path" a
// line, sorted.
func (c *Console) registeredRoutes() string {
	var out []string
	chi.Walk(c.mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, routeKey(method, route, c.prefix))
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, "\n") + "\n"
}
