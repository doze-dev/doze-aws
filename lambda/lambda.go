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
// Runtime = a per-function concurrency pool of supervised processes that grows
// with demand (capped) and scales back to zero after an idle window, so an
// unused function holds no processes. See docs/api-support/lambda.md.
package lambda

import (
	"net/http"

	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/trace"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/schemaver"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/gateway"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/logship"
	"github.com/doze-dev/doze-aws/internal/metricship"
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
	// IAMMode is the IAM service's mode ("off", "soft", "enforce"); under soft
	// or enforce a function's resource policy is evaluated on every request
	// that names the function, peer calls included.
	IAMMode string
	// Identity is the region and account this service mints ARNs for, and tells
	// function processes they run in. The zero value means the conventional
	// local identity.
	Identity awsident.Identity
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// IdleTimeout is how long a warm function keeps its process(es) before
	// scaling to zero. Zero uses lambdaruntime.DefaultIdleTimeout.
	IdleTimeout time.Duration
	// QuietFunctions stops function output from being echoed to Logf. The
	// lines still reach the logs service when it is enabled.
	QuietFunctions bool
	// Runtimes overrides the interpreter per runtime family ("python",
	// "nodejs", "ruby", "java", "dotnet"); the PATH is searched otherwise.
	Runtimes map[string]string
}

// Server is the Lambda service.
type Server struct {
	// sink receives cascade events from the event-source pollers. It arrives
	// after construction because the recorder wraps the assembled stack, so it
	// cannot exist when the services are built.
	sink trace.Sink

	store       *Store
	dataDir     string
	peers       peers.Directory
	endpoint    string
	logf        func(format string, args ...any)
	now         func() time.Time
	idleTimeout time.Duration
	echo        bool                       // function output to Logf
	shimDir     string                     // the embedded runtime clients, materialised
	interps     lambdaruntime.Interpreters // configured interpreter overrides
	guard       iamguard.Guard             // the function resource policy, under IAM soft/enforce
	id          awsident.Identity          // the region and account this service mints ARNs for

	mu       sync.Mutex
	runners  map[string]*lambdaruntime.Pool // function name -> concurrency pool
	sinks    map[string]*logSink            // function name -> its log sink, closed with the pool
	mappings map[string]*esm                // mapping UUID -> poller
	pollers  sync.WaitGroup                 // tracks live ESM poller goroutines
	logs     *logship.Shipper               // carries every function's lines to the logs service
	metrics  *metricship.Shipper            // carries AWS/Lambda metrics to the cloudwatch service
}

// New opens the store under DataDir.
func New(opts Options) (*Server, error) {
	for _, dir := range []string{opts.DataDir, filepath.Join(opts.DataDir, "code"), filepath.Join(opts.DataDir, "logs")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	// The embedded runtime clients, written where a function process can
	// read them (LAMBDA_RUNTIME_DIR).
	shimDir, err := lambdaruntime.Materialize(filepath.Join(opts.DataDir, "shims"))
	if err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "lambda.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "lambda", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store:       newStore(db),
		dataDir:     opts.DataDir,
		peers:       opts.Peers,
		guard:       iamguard.Guard{Mode: opts.IAMMode, Logf: logf, Identity: opts.Identity},
		endpoint:    opts.Endpoint,
		logf:        logf,
		now:         opts.Clock,
		idleTimeout: opts.IdleTimeout,
		echo:        !opts.QuietFunctions,
		interps:     lambdaruntime.Interpreters(opts.Runtimes),
		shimDir:     shimDir,
		runners:     map[string]*lambdaruntime.Pool{},
		sinks:       map[string]*logSink{},
		mappings:    map[string]*esm{},
		id:          opts.Identity,
	}
	s.store.id = opts.Identity
	if s.peers == nil {
		s.peers = peers.None()
	}
	s.logs = logship.New("lambda", s.peers, s.logf)
	s.metrics = metricship.New("lambda", s.peers, s.logf)
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

// Close stops every runner and poller and closes the store. It waits for the
// ESM poller goroutines to actually exit so none can call into sibling services
// (or log) after Close returns.
func (s *Server) Close() error {
	s.mu.Lock()
	for _, r := range s.runners {
		r.Stop()
	}
	for _, k := range s.sinks {
		k.Close()
	}
	for _, m := range s.mappings {
		m.stop()
	}
	s.mu.Unlock()
	s.pollers.Wait()
	s.logs.Close()
	s.metrics.Close()
	return s.store.db.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A function URL is the data plane: an unsigned HTTP request the gateway
	// routed here by its host or path, not a control-plane operation.
	if gateway.IsFunctionURL(r) {
		s.serveFunctionURL(w, r)
		return
	}
	// Model-derived input validation runs before the router, for every routed
	// operation at once — coverage is then a property of the route table rather
	// than something each handler has to remember.
	if _, aerr := validateControl(r); aerr != nil {
		s.logf("lambda: %s %s -> %s", r.Method, r.URL.Path, aerr.Code)
		writeError(w, aerr)
		return
	}
	if aerr := s.guardRequest(w, r); aerr != nil {
		s.logf("lambda: %s %s -> %s", r.Method, r.URL.Path, aerr.Code)
		writeError(w, aerr)
		return
	}
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
	case "layers":
		return s.routeLayers(w, r, segs)
	case "account-settings":
		return s.accountSettings(w, r)
	}
	return awshttp.Errf(404, "ResourceNotFoundException", "unknown resource %q", segs[1])
}

// SetTraceSink tells the pollers where to report the work a queued message
// caused. Safe to leave unset: tracing is then a no-op.
func (s *Server) SetTraceSink(sink trace.Sink) { s.sink = sink }
