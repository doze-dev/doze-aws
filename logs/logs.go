// Package logs is doze-aws's local CloudWatch Logs: the slice of it that a
// developer reads — log groups, streams and events, PutLogEvents in and
// FilterLogEvents/GetLogEvents out — so `aws logs tail --follow`, `sam logs`
// and the SDKs see a function's output the way they would in the account.
//
// Lambda writes here through the same wire an SDK uses (PutLogEvents), so
// the two services can run in one process or two. What is not here is the
// rest of the service's 118 operations: Logs Insights queries, deliveries,
// anomaly detection, metric filters, export and import, account and
// data-protection policies. Each answers UnsupportedOperationException
// naming what it would need. Subscription filters are here: a group forwards
// its matching lines to a Lambda function or a Kinesis stream in the gzip
// envelope AWS sends (fanout.go).
//
// See docs/api-support/logs.md for the operation-by-operation table.
package logs

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
	"github.com/doze-dev/doze-aws/internal/schemaver"
	"github.com/doze-dev/doze-aws/peers"
)

// ErrNoGroup is PutEvents on a group nobody created.
var ErrNoGroup = errors.New("log group does not exist")

// DefaultRetention is how long events are kept when the group sets no
// retention policy: a working day's worth of local runs, not forever.
const DefaultRetention = 24 * time.Hour

// DefaultMaxEvents caps a group's events whatever its retention says.
const DefaultMaxEvents = 100000

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (logs.bolt). Required.
	DataDir string
	// Peers resolves the Lambda functions and Kinesis streams subscription
	// filters deliver to. Nil disables delivery (logged).
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// Retention is the default for groups without a policy; zero means
	// DefaultRetention.
	Retention time.Duration
	// MaxEvents caps each group; zero means DefaultMaxEvents.
	MaxEvents int
}

// Server is the logs service: an http.Handler speaking AWS JSON 1.1, and an
// io.Closer that stops the sweeper and closes the store.
type Server struct {
	store     *Store
	logf      func(format string, args ...any)
	api       awsjson.API
	retention time.Duration
	maxEvents int
	stop      chan struct{}
	peers     peers.Directory
	fan       *fanout        // subscription filters → Lambda / Kinesis
	met       *metricEmitter // metric filters → CloudWatch Metrics
}

// New opens the store under DataDir and starts the retention sweeper.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	// Logs are disposable and written on every invocation; a per-batch fsync
	// is the one thing that would make this slow, so the store does not.
	db, err := bolt.Open(filepath.Join(opts.DataDir, "logs.bolt"), 0o600, &bolt.Options{NoSync: true, Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "logs", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	st, err := newStore(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store:     st,
		logf:      logf,
		api:       awsjson.API{TargetPrefix: "Logs_20140328", JSONVersion: "1.1"},
		retention: opts.Retention,
		maxEvents: opts.MaxEvents,
		stop:      make(chan struct{}),
	}
	if s.retention <= 0 {
		s.retention = DefaultRetention
	}
	if s.maxEvents <= 0 {
		s.maxEvents = DefaultMaxEvents
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
	}
	s.peers = opts.Peers
	if s.peers == nil {
		s.peers = peers.None()
	}
	s.fan = newFanout(s.store, s.peers, logf)
	s.met = newMetricEmitter(s.store, s.peers, logf)
	go s.sweeper()
	return s, nil
}

// Close stops the sweeper, drains the subscription fan-out, and closes the
// store.
func (s *Server) Close() error {
	close(s.stop)
	s.fan.close()
	s.met.close()
	return s.store.db.Close()
}

// SweepNow runs one retention sweep and reports how many events it dropped.
func (s *Server) SweepNow() int {
	n, err := s.store.Sweep(s.retention, s.maxEvents)
	if err != nil {
		s.logf("logs: sweep: %v", err)
	}
	return n
}

func (s *Server) sweeper() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if n, err := s.store.Sweep(s.retention, s.maxEvents); err != nil {
				s.logf("logs: sweep: %v", err)
			} else if n > 0 {
				s.logf("logs: swept %d events past retention", n)
			}
		}
	}
}

type handler func(s *Server, ctx context.Context, p map[string]any) (any, *awshttp.APIError)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action, aerr := s.api.Action(r)
	if aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	var params map[string]any
	if aerr := awsjson.DecodeBody(r, &params); aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	h, ok := handlers[action]
	if !ok {
		if why, staged := notHere[action]; staged {
			s.api.WriteError(w, awshttp.Errf(400, "UnsupportedOperationException",
				"%s is not supported by doze-aws: %s", action, why))
			return
		}
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown CloudWatch Logs action %q", action))
		return
	}
	if aerr := modelcheck.ValidateMap(params, constraintTables[action]); aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	result, aerr := h(s, r.Context(), params)
	if aerr != nil {
		s.logf("logs: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	// PutLogEvents is the chatty one; every invocation lands one. Logging it
	// would double the noise the lines themselves make.
	if action != "PutLogEvents" {
		s.logf("logs: %s ok", action)
	}
	s.api.Write(w, result)
}

func errNotFound(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", format, args...)
}

func errExists(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceAlreadyExistsException", format, args...)
}

func errParam(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "InvalidParameterException", format, args...)
}
