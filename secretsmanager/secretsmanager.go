// Package secretsmanager is doze-aws's local AWS Secrets Manager: secrets with
// version stages (AWSCURRENT/AWSPREVIOUS plus custom labels), deletion with a
// recovery window, tags, and resource-policy round-trips. Secret values are
// genuinely encrypted at rest with a per-data-dir AES-256-GCM key the service
// manages itself (a KMS KeyId is recorded and returned cosmetically).
//
// Rotation (RotateSecret) drives the four-step protocol against a configured
// rotation Lambda via peers. Cross-region replication is physically meaningless
// locally and answers honestly.
//
// See docs/api-support/secretsmanager.md for the support table.
package secretsmanager

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/schemaver"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (secretsmanager.bolt) and the value
	// encryption key (secretsmanager.key). Required.
	DataDir string
	// Peers lets RotateSecret invoke the configured rotation lambda.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// Identity is the region and account this service mints ARNs for. The zero
	// value means the conventional local identity.
	Identity awsident.Identity
	// IAMMode is the IAM service's mode; under soft or enforce a secret's
	// resource policy is evaluated on every request that names it.
	IAMMode string
}

// Server is the Secrets Manager service: an http.Handler speaking AWS JSON
// 1.1, and an io.Closer that stops the janitor and closes the store.
type Server struct {
	store *Store
	peers peers.Directory
	logf  func(format string, args ...any)
	api   awsjson.API
	stop  chan struct{}
	// done closes when the janitor has returned, so Close waits for it before
	// closing bbolt — a sweep mid-transaction against a closed DB is a panic.
	done  chan struct{}
	guard iamguard.Guard // the secret's resource policy, under IAM soft/enforce
}

// New opens the store under DataDir and starts the deletion janitor.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "secretsmanager.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "secretsmanager", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	st, err := newStore(db, filepath.Join(opts.DataDir, "secretsmanager.key"))
	if err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store: st,
		peers: opts.Peers,
		logf:  logf,
		api:   awsjson.API{TargetPrefix: "secretsmanager", JSONVersion: "1.1"},
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		guard: iamguard.Guard{Mode: opts.IAMMode, Logf: logf, Identity: opts.Identity},
	}
	if s.peers == nil {
		s.peers = peers.None()
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
	}
	s.store.id = opts.Identity
	go s.janitor()
	return s, nil
}

// Close stops the janitor and closes the bbolt DB.
func (s *Server) Close() error {
	close(s.stop)
	// Waited on, not just signalled: a sweep inside a bolt transaction races
	// the close below, and bolt panics on a closed DB.
	<-s.done
	return s.store.db.Close()
}

// janitor purges secrets whose recovery window has passed.
func (s *Server) janitor() {
	defer close(s.done)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			bg.Tick(s.logf, "secretsmanager: janitor", func() { s.store.SweepDeleted() })
		}
	}
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
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown Secrets Manager action %q", action))
		return
	}
	// Model-derived input validation runs before the handler, for every
	// operation at once — coverage is then a property of the dispatch table
	// rather than something each handler has to remember.
	if aerr := modelcheck.ValidateMap(params, constraintTables[action]); aerr != nil {
		s.logf("secretsmanager: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}

	if aerr := s.guardRequest(w, r, action, params); aerr != nil {
		s.logf("secretsmanager: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	result, aerr := h(s, params)
	if aerr != nil {
		s.logf("secretsmanager: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("secretsmanager: %s ok", action)
	s.api.Write(w, result)
}
