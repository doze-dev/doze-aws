// Package apigateway is doze-aws's local API Gateway: the REST (v1) control
// plane, plus an execute-api data plane that actually serves requests into
// Lambda.
//
// # Two planes
//
// The control plane is ordinary CRUD over a path tree — create an API, add
// resources, put methods and integrations, deploy to a stage. It is REST-JSON,
// routed by method and path like Lambda's.
//
// The execute-api plane is the part that matters. A deployed API has to be
// callable, and `/restapis` is already taken by the control plane, so a
// deployed API is served at:
//
//	/_aws/execute-api/{apiId}/{stage}/{path...}
//	{apiId}.execute-api.<host>/{stage}/{path...}     (virtual-host style)
//
// Both shapes match what LocalStack uses, so habits and existing test helpers
// transfer.
//
// # What it serves
//
// AWS_PROXY (Lambda proxy) is the integration that matters and is implemented
// fully: the request becomes a proxy event, the function's response shape
// drives status, headers and body. MOCK, HTTP and HTTP_PROXY also work.
// Non-proxy AWS integrations with Velocity mapping templates are refused —
// emulating VTL badly is worse than saying so.
//
// See docs/api-support/apigateway.md for the operation-by-operation table.
package apigateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/schemaver"
	"github.com/doze-dev/doze-aws/peers"
)

// ExecutePrefix is the path a deployed API is served under.
const ExecutePrefix = "/_aws/execute-api/"

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (apigateway.bolt). Required.
	DataDir string
	// Peers resolves sibling services; Lambda integrations invoke through it.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the API Gateway service.
type Server struct {
	store *Store
	peers peers.Directory
	logf  func(format string, args ...any)
	now   func() time.Time
}

// New opens the store under DataDir.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "apigateway.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "apigateway", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{store: newStore(db), peers: opts.Peers, logf: logf, now: time.Now}
	if s.peers == nil {
		s.peers = peers.None()
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
		s.now = opts.Clock
	}
	return s, nil
}

// Close closes the bbolt DB.
func (s *Server) Close() error { return s.store.db.Close() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The data plane is checked first: a deployed API's own paths must never
	// be mistaken for control-plane routes.
	if strings.HasPrefix(r.URL.Path, ExecutePrefix) {
		s.serveExecute(w, r, strings.TrimPrefix(r.URL.Path, ExecutePrefix))
		return
	}
	if apiID, rest, ok := virtualHostExecute(r); ok {
		s.serveExecute(w, r, apiID+"/"+rest)
		return
	}
	if aerr := s.routeControl(w, r); aerr != nil {
		s.logf("apigateway: %s %s -> %s", r.Method, r.URL.Path, aerr.Code)
		writeError(w, aerr)
	}
}

// virtualHostExecute detects the {apiId}.execute-api.<host> addressing form.
func virtualHostExecute(r *http.Request) (apiID, rest string, ok bool) {
	host := r.Host
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	label, remainder, found := strings.Cut(host, ".execute-api.")
	if !found || label == "" || remainder == "" {
		return "", "", false
	}
	return label, strings.TrimPrefix(r.URL.Path, "/"), true
}

// routeControl dispatches the control plane by path family.
func (s *Server) routeControl(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		return errNotFound("unknown resource")
	}
	switch segs[0] {
	case "restapis":
		return s.routeRestAPIs(w, r, segs)
	case "tags":
		return s.routeTags(w, r, segs)
	case "account":
		return s.getAccount(w)
	case "apikeys", "usageplans", "clientcertificates", "domainnames", "vpclinks", "sdktypes":
		// Recognised families doze-aws does not model. Refusing by name beats
		// a bare 404 that looks like a routing bug.
		return awshttp.Errf(501, "NotImplemented",
			"doze-aws does not implement API Gateway %s", segs[0])
	}
	return errNotFound("unknown resource %s", segs[0])
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}
