package console

// The console's routes, on chi.
//
// This file is the shell: the wire, the connect page, the command palette's
// feeds, the tag editor, the fidelity ledger and the embedded assets. Each
// service's pages and actions are a group in routes_<area>.go, mounted here
// under the service's own prefix.
//
// A group is a sub-router, and chi does not fall out of one: a request that
// reaches /eb is answered by the EventBridge group or not at all. That is why a
// literal segment and a parameter that share a position (/eb/destinations and
// /eb/{bus}, /iam/user/{name}/keys and /iam/{kind}/{name}/attach) are both
// registered flat in the same group — inside one router chi prefers the literal
// and backtracks to the parameter, and a nested Route would not.

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (c *Console) routes() {
	r := chi.NewRouter()
	// Before routing: refuse a cross-origin write, let a HEAD be answered by
	// its GET (ServeMux did; chi does not), and note the route for the e2e
	// suite's route gate.
	r.Use(c.sameOrigin, middleware.GetHead, c.recordRoute)

	r.Route(c.prefix, func(r chi.Router) {
		// An unmatched GET is the console's not-found page, not a blank 404. Set
		// before any group is mounted — a group inherits it when it is.
		r.NotFound(c.fallback)
		r.MethodNotAllowed(c.methodNotAllowed)

		// Static assets (htmx, css) — embedded, served locally (no CDN). Embedded
		// files have a zero modtime, so plain FileServerFS gives the browser no
		// validator at all and every hard reload re-downloads ~700KB (font,
		// CodeMirror, htmx, Alpine). cacheStatic adds content ETags + max-age.
		r.Get("/static/*", http.StripPrefix(c.prefix+"/", cacheStatic(http.FileServerFS(staticFS))).ServeHTTP)

		// The wire is the home surface: the question people open this to answer is
		// "what did my app just do", not "what resources exist".
		r.Get("/", c.traffic)
		r.Get("/traffic", c.traffic)
		r.Get("/traffic/feed", c.trafficFeed)    // polled live tail
		r.Get("/traffic/entry", c.trafficEntry)  // inspector drawer
		r.Post("/traffic/clear", c.trafficClear) // empty the ring
		r.Get("/connect", c.connect)
		r.Post("/connect/verify", c.connectVerify)
		r.Get("/deck", c.deck) // the stack at a glance

		// Resource index for the command palette.
		r.Get("/api/resources", c.apiResources)
		r.Get("/api/palette", c.apiPalette)
		r.Get("/api/resolve", c.apiResolve)
		r.Get("/api/counts", c.apiCounts)
		r.Get("/api/glance", c.apiGlance) // one-call feed for the doze dash page
		r.Get("/tags/view", c.tagsView)
		r.Post("/tags/save", c.tagsSave) // the whole set, explicitly
		r.Get("/info/{svc}", c.svcInfo)  // HTMX partial (the fidelity ledger)

		// Storage and data.
		r.Route("/s3", c.s3Routes)
		r.Route("/ddb", c.ddbRoutes)
		// Messaging.
		r.Route("/sqs", c.sqsRoutes)
		r.Route("/sns", c.snsRoutes)
		r.Route("/eb", c.ebRoutes)
		r.Route("/kinesis", c.kinesisRoutes)
		// Compute and APIs.
		r.Route("/lambda", c.lambdaRoutes)
		r.Route("/apigw", c.apigwRoutes)
		r.Route("/apigw-http", c.apigwHttpRoutes)
		r.Route("/apigw-keys", c.apigwKeysRoutes)
		r.Route("/sfn", c.sfnRoutes)
		r.Route("/logs", c.logsRoutes)
		r.Route("/cw", c.cwRoutes)
		// Config and secrets.
		r.Route("/kms", c.kmsRoutes)
		r.Route("/sm", c.smRoutes)
		r.Route("/ssm", c.ssmRoutes)
		// Stack-wide.
		r.Route("/iam", c.iamRoutes)
		r.Route("/cfn", c.cfnRoutes)
	})
	c.mux = r
}

// sameOrigin is the CSRF / DNS-rebinding defense: a state-changing request must
// originate from the console itself. Browsers always send Origin on
// cross-origin (and most same-origin) POSTs; when present it must match the
// Host we're serving on. This blocks a malicious page from driving destructive
// actions against a developer's localhost console.
func (c *Console) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" && !originMatchesHost(origin, r.Host) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// fallback is the router's answer to a path it has no route for: the console's
// own not-found page, which guesses the section a stale bookmark meant, to a
// GET — and a 405 to anything that would have changed something.
func (c *Console) fallback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		c.notFound(w, r)
		return
	}
	c.methodNotAllowed(w, r)
}

func (c *Console) methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}
