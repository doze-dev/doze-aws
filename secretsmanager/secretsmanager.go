// Package secretsmanager is doze-aws's local AWS Secrets Manager: secrets with
// version stages (AWSCURRENT/AWSPREVIOUS plus custom labels), deletion with a
// recovery window, tags, and resource-policy round-trips. Secret values are
// genuinely encrypted at rest with a per-data-dir AES-256-GCM key the service
// manages itself (a KMS KeyId is recorded and returned cosmetically).
//
// Rotation (RotateSecret invoking a rotation Lambda) arrives in Phase 8 once
// the lambda service exists; until then it answers a clean error. Cross-region
// replication is physically meaningless locally and answers honestly.
//
// See docs/api-support/secretsmanager.md for the support table.
package secretsmanager

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (secretsmanager.bolt) and the value
	// encryption key (secretsmanager.key). Required.
	DataDir string
	// Peers is accepted for constructor uniformity; rotation will use it in
	// Phase 8 to invoke the rotation lambda.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the Secrets Manager service: an http.Handler speaking AWS JSON
// 1.1, and an io.Closer that stops the janitor and closes the store.
type Server struct {
	store *Store
	logf  func(format string, args ...any)
	api   awsjson.API
	stop  chan struct{}
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
		logf:  logf,
		api:   awsjson.API{TargetPrefix: "secretsmanager", JSONVersion: "1.1"},
		stop:  make(chan struct{}),
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
	}
	go s.janitor()
	return s, nil
}

// Close stops the janitor and closes the bbolt DB.
func (s *Server) Close() error {
	close(s.stop)
	return s.store.db.Close()
}

// janitor purges secrets whose recovery window has passed.
func (s *Server) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.store.SweepDeleted()
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
	result, aerr := h(s, params)
	if aerr != nil {
		s.logf("secretsmanager: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("secretsmanager: %s ok", action)
	s.api.Write(w, result)
}
