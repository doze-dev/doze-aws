// Package stepfunctions is doze-aws's local Step Functions: state machines
// written in the Amazon States Language, executed against the services this
// stack already runs.
//
// The language lives in internal/asl, which is pure — it parses a definition,
// validates it, and advances one execution frame at a time. This package is the
// service around it: the awsJson1.0 wire, bbolt persistence, and the layer that
// turns a Task's resource ARN into a call on a sibling.
//
// Stage 1 implements the control plane. State machines and activities can be
// created, described, updated, listed, deleted and tagged, and a definition is
// genuinely validated — a broken one is refused here rather than on deploy,
// which is the whole point of validating locally. Execution arrives in stage 2;
// StartExecution answers an honest UnsupportedOperationException until then
// rather than accepting work it would silently drop.
//
// See docs/api-support/stepfunctions.md for the operation-by-operation table.
package stepfunctions

import (
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
	store *Store
	peers peers.Directory
	logf  func(format string, args ...any)
	api   awsjson.API
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
	return s, nil
}

// Close closes the store.
func (s *Server) Close() error { return s.store.db.Close() }

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
	result, aerr := h(s, params)
	if aerr != nil {
		s.logf("stepfunctions: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("stepfunctions: %s ok", action)
	s.api.Write(w, result)
}

// notYet are operations this build will implement in a later stage. They are
// listed rather than left to fall through to InvalidAction, so the message says
// "not yet" and names why — a caller can tell a staged gap from a typo, and an
// SDK sees UnsupportedOperationException rather than a mystery.
var notYet = map[string]string{
	"StartExecution":                   "executions arrive in the next stage; the control plane is complete",
	"StartSyncExecution":               "executions arrive in the next stage; the control plane is complete",
	"DescribeExecution":                "executions arrive in the next stage",
	"StopExecution":                    "executions arrive in the next stage",
	"ListExecutions":                   "executions arrive in the next stage",
	"GetExecutionHistory":              "executions arrive in the next stage",
	"DescribeStateMachineForExecution": "executions arrive in the next stage",
	"GetActivityTask":                  "activity polling arrives with executions",
	"SendTaskSuccess":                  "task tokens arrive with executions",
	"SendTaskFailure":                  "task tokens arrive with executions",
	"SendTaskHeartbeat":                "task tokens arrive with executions",
	"RedriveExecution":                 "executions arrive in the next stage",
	"TestState":                        "running a state in isolation needs the interpreter's eval half",
}

// stubActions are operations doze-aws does not intend to implement, with the
// reason. Distinct from notYet: these are not coming.
var stubActions = map[string]string{
	"DescribeMapRun": "Distributed Map is cloud-scale fan-out over an S3 item reader",
	"ListMapRuns":    "Distributed Map is cloud-scale fan-out over an S3 item reader",
	"UpdateMapRun":   "Distributed Map is cloud-scale fan-out over an S3 item reader",
}
