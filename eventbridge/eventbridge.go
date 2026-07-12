// Package eventbridge is doze-aws's local EventBridge: event buses, rules
// with the full content-based pattern language (internal/eventpattern), and
// synchronous delivery to SQS and Lambda targets with Input / InputPath /
// InputTransformer shaping.
//
// rate(...) scheduled rules are driven by a local ticker; cron(...) and
// destinations, partner event sources, and the schemas registry are cloud
// infrastructure and answer honestly.
//
// See docs/api-support/eventbridge.md for the operation table.
package eventbridge

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/schemaver"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (eventbridge.bolt). Required.
	DataDir string
	// Peers resolves SQS/Lambda targets. Nil disables delivery (logged).
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the EventBridge service: an http.Handler speaking AWS JSON 1.1,
// and an io.Closer.
type Server struct {
	store    *Store
	peers    peers.Directory
	logf     func(format string, args ...any)
	now      func() time.Time
	api      awsjson.API
	stop     chan struct{} // closed once (via stopOnce) to end the scheduler
	done     chan struct{} // closed by the scheduler goroutine when it exits
	stopOnce sync.Once
}

// New opens the store under DataDir (the default bus exists implicitly).
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "eventbridge.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "eventbridge", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store: newStore(db),
		peers: opts.Peers,
		logf:  logf,
		now:   opts.Clock,
		api:   awsjson.API{TargetPrefix: "AWSEvents", JSONVersion: "1.1"},
	}
	if s.peers == nil {
		s.peers = peers.None()
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		s.runScheduler(s.stop) // the goroutine only touches its arguments, not s.stop
	}()
	return s, nil
}

// Close stops the scheduler, waits for it to exit (so nothing touches the store
// after this), and closes the bbolt DB. Safe to call more than once.
func (s *Server) Close() error {
	var err error
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.done
		err = s.store.db.Close()
	})
	return err
}

type handler func(s *Server, p map[string]any) (any, *awshttp.APIError)

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
		if reason, stub := stubActions[action]; stub {
			s.api.WriteError(w, awshttp.Errf(400, "UnsupportedOperationException",
				"%s is not supported by doze-aws: %s", action, reason))
			return
		}
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown EventBridge action %q", action))
		return
	}
	result, aerr := h(s, params)
	if aerr != nil {
		s.logf("eventbridge: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("eventbridge: %s ok", action)
	s.api.Write(w, result)
}

var stubActions = map[string]string{
	"CreateApiDestination":           "API destinations call external endpoints via cloud infrastructure",
	"DeleteApiDestination":           "API destinations call external endpoints via cloud infrastructure",
	"DescribeApiDestination":         "API destinations call external endpoints via cloud infrastructure",
	"ListApiDestinations":            "API destinations call external endpoints via cloud infrastructure",
	"UpdateApiDestination":           "API destinations call external endpoints via cloud infrastructure",
	"CreateConnection":               "API destinations call external endpoints via cloud infrastructure",
	"DeleteConnection":               "API destinations call external endpoints via cloud infrastructure",
	"DescribeConnection":             "API destinations call external endpoints via cloud infrastructure",
	"ListConnections":                "API destinations call external endpoints via cloud infrastructure",
	"UpdateConnection":               "API destinations call external endpoints via cloud infrastructure",
	"ActivateEventSource":            "partner event sources are cloud infrastructure",
	"DeactivateEventSource":          "partner event sources are cloud infrastructure",
	"DescribeEventSource":            "partner event sources are cloud infrastructure",
	"ListEventSources":               "partner event sources are cloud infrastructure",
	"CreatePartnerEventSource":       "partner event sources are cloud infrastructure",
	"DeletePartnerEventSource":       "partner event sources are cloud infrastructure",
	"DescribePartnerEventSource":     "partner event sources are cloud infrastructure",
	"ListPartnerEventSources":        "partner event sources are cloud infrastructure",
	"ListPartnerEventSourceAccounts": "partner event sources are cloud infrastructure",
	"PutPartnerEvents":               "partner event sources are cloud infrastructure",
	"PutPermission":                  "cross-account permissions have no meaning locally",
	"RemovePermission":               "cross-account permissions have no meaning locally",
	"CreateEndpoint":                 "global endpoints are multi-region infrastructure",
	"DeleteEndpoint":                 "global endpoints are multi-region infrastructure",
	"DescribeEndpoint":               "global endpoints are multi-region infrastructure",
	"ListEndpoints":                  "global endpoints are multi-region infrastructure",
	"UpdateEndpoint":                 "global endpoints are multi-region infrastructure",
}
