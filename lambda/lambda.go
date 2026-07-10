// Package lambda is doze-aws's local AWS Lambda: functions run as supervised
// local processes speaking the Lambda Runtime API (no Docker). It implements
// the control plane (create/update/get/list/delete, versions, aliases,
// concurrency, DLQ/destinations, tags), synchronous and async Invoke, function
// URLs, and SQS event source mappings that poll a queue and deliver batches.
//
// Code packaging: ZipFile (unpacked to the data dir) and a doze extension
// where Code.S3Bucket == "_local_" and Code.S3Key is an absolute path to a
// directory or binary used in place (edit-and-reinvoke, no copy).
//
// Runtime = one supervised process per function, serial invocations in this
// phase (a scale-out pool arrives in Phase 8). See docs/api-support/lambda.md.
package lambda

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the store, unpacked code, and logs. Required.
	DataDir string
	// Peers resolves sibling services: injected as AWS_ENDPOINT_URL_* into
	// function processes, and used by event source mappings to poll SQS.
	Peers peers.Directory
	// Endpoint is the shared gateway URL handlers reach siblings through
	// (AWS_ENDPOINT_URL). Empty derives per-service from Peers where possible.
	Endpoint string
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the Lambda service.
type Server struct {
	store    *Store
	dataDir  string
	peers    peers.Directory
	endpoint string
	logf     func(format string, args ...any)
	now      func() time.Time

	mu       sync.Mutex
	runners  map[string]*lambdaruntime.Runner // function name -> runner
	mappings map[string]*esm                  // mapping UUID -> poller
}

// New opens the store under DataDir.
func New(opts Options) (*Server, error) {
	for _, dir := range []string{opts.DataDir, filepath.Join(opts.DataDir, "code"), filepath.Join(opts.DataDir, "logs")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "lambda.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store:    newStore(db),
		dataDir:  opts.DataDir,
		peers:    opts.Peers,
		endpoint: opts.Endpoint,
		logf:     logf,
		now:      opts.Clock,
		runners:  map[string]*lambdaruntime.Runner{},
		mappings: map[string]*esm{},
	}
	if s.peers == nil {
		s.peers = peers.None()
	}
	if s.now == nil {
		s.now = time.Now
	}
	// Resume enabled event source mappings.
	if maps, err := s.store.ListMappings(); err == nil {
		for i := range maps {
			if maps[i].State == "Enabled" {
				s.startPoller(&maps[i])
			}
		}
	}
	return s, nil
}

// Close stops every runner and poller and closes the store.
func (s *Server) Close() error {
	s.mu.Lock()
	for _, r := range s.runners {
		r.Stop()
	}
	for _, m := range s.mappings {
		m.stop()
	}
	s.mu.Unlock()
	return s.store.db.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Lambda's REST API routes by method + path. Dispatch on the path shape.
	if aerr := s.route(w, r); aerr != nil {
		s.logf("lambda: %s %s -> %s", r.Method, r.URL.Path, aerr.Code)
		writeError(w, aerr)
	}
}

// route dispatches one request.
func (s *Server) route(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	p := strings.Trim(r.URL.Path, "/")
	segs := strings.Split(p, "/")
	// segs[0] is the API version date; segs[1] is the resource collection.
	if len(segs) < 2 {
		return awshttp.Errf(404, "ResourceNotFoundException", "unknown path %q", r.URL.Path)
	}
	switch segs[1] {
	case "functions":
		return s.routeFunctions(w, r, segs)
	case "event-source-mappings":
		return s.routeMappings(w, r, segs)
	case "tags":
		return s.routeTags(w, r, segs)
	}
	return awshttp.Errf(404, "ResourceNotFoundException", "unknown resource %q", segs[1])
}
