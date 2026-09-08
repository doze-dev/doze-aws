// Package stepfunctions is doze-aws's local Step Functions: state machines
// written in the Amazon States Language, executed against the services this
// stack already runs.
//
// The language lives in internal/asl, which is pure — it parses a definition,
// validates it, and advances one execution frame at a time. This package is the
// service around it: the awsJson1.0 wire, bbolt persistence, and the layer that
// turns a Task's resource ARN into a call on a sibling.
//
// The control plane creates, describes, updates, lists, deletes and tags state
// machines and activities, and genuinely validates a definition — a broken one
// is refused here rather than on deploy, which is the whole point of validating
// locally. Standard executions run on a single driver goroutine (engine.go,
// scheduler.go) that owns every interpreter step and every bbolt write, with
// Task calls on transient workers that never touch the store. Frames are the
// schedule: a restart re-issues whatever each frame's status names. Express,
// activities, redrive and TestState answer an honest
// UnsupportedOperationException from notYet rather than accepting work they
// would silently drop.
//
// See docs/api-support/stepfunctions.md for the operation-by-operation table.
package stepfunctions

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
	"github.com/doze-dev/doze-aws/internal/schemaver"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (stepfunctions.bolt). Required.
	DataDir string
	// Peers is how a Task state reaches Lambda, SQS, SNS and the rest. nil
	// disables integrations, which are logged rather than failed.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the Step Functions service: an http.Handler speaking AWS JSON 1.0,
// and an io.Closer.
type Server struct {
	store  *Store
	peers  peers.Directory
	logf   func(format string, args ...any)
	api    awsjson.API
	sink   trace.Sink
	engine *engine
	logs   *machineLogs
}

// New opens the store under DataDir.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "stepfunctions.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "stepfunctions", schemaver.Current); err != nil {
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
		// Step Functions is awsJson 1.0, not 1.1 — the one place in this repo
		// where the target prefix and the JSON version disagree with the
		// service's own name: it signs as "states" and targets AWSStepFunctions.
		api: awsjson.API{TargetPrefix: "AWSStepFunctions", JSONVersion: "1.0"},
	}
	if s.peers == nil {
		s.peers = peers.None()
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
	}
	s.logs = newMachineLogs(s)
	s.engine = newEngine(s)
	return s, nil
}

// Close drains the engine — the driver goroutine and every task worker —
// before closing the store. The ordering is the point: only the driver
// writes execution state, so once it has exited, nothing can touch a closed
// bbolt.
func (s *Server) Close() error {
	s.engine.close()
	s.logs.close()
	return s.store.db.Close()
}

// SetTraceSink tells the engine where to report the steps a resumed execution
// causes. Request-driven work inherits its sink from the request context; the
// engine drives executions from a scheduler goroutine, which has no request,
// so it needs the sink handed to it the way lambda's pollers do.
func (s *Server) SetTraceSink(sink trace.Sink) { s.sink = sink }

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
		if reason, staged := notYet[action]; staged {
			s.logf("stepfunctions: %s -> not yet", action)
			s.api.WriteError(w, errNotYet(action, reason))
			return
		}
		if reason, stub := stubActions[action]; stub {
			s.api.WriteError(w, awshttp.Errf(400, "UnsupportedOperationException",
				"%s is not supported by doze-aws: %s", action, reason))
			return
		}
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown Step Functions action %q", action))
		return
	}
	// Model-derived input validation runs before the handler, for every
	// operation at once — coverage is then a property of the dispatch table
	// rather than something each handler has to remember.
	if aerr := modelcheck.ValidateMap(params, constraintTables[action]); aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	result, aerr := h(s, r.Context(), params)
	if aerr != nil {
		s.logf("stepfunctions: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("stepfunctions: %s ok", action)
	s.api.Write(w, result)
}

// notYet is where operations waited while the service was built in stages,
// each with the reason, so a caller could tell a staged gap from a typo. It
// is empty: every operation in the model is handled or refused for good.
// The mechanism stays, because the next model refresh may add one.
var notYet = map[string]string{}

// stubActions are operations doze-aws does not intend to implement, with the
// reason. Distinct from notYet: these are not coming. Empty — Distributed
// Map, the one family that lived here, runs locally now: items from S3 or
// the state's input, each as its own execution under a Map Run record.
var stubActions = map[string]string{}
