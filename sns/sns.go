// Package sns is doze-aws's ground-up, pure-Go SNS-compatible service: no
// LocalStack, no JVM. It speaks the SNS Query/XML protocol, persists topics
// and subscriptions to a bbolt store, supports message-attribute filter
// policies, raw message delivery, and topic tags, and fans published messages
// out to SQS queues (via the peers directory) and to http(s) webhooks with the
// subscription-confirmation handshake.
//
// See docs/api-support/sns.md for the operation-by-operation support table.
package sns

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/schemaver"

	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (sns.bolt). Required.
	DataDir string
	// Peers resolves sibling services; SQS-protocol subscriptions deliver
	// through it. Nil disables SQS delivery (with a log line per attempt).
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the SNS service: an http.Handler speaking the Query/XML protocol,
// and an io.Closer that closes the store.
type Server struct {
	store *Store
	peers peers.Directory
	logf  func(format string, args ...any)
	now   func() time.Time
}

// New opens the bbolt store under DataDir.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "sns.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "sns", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	s := &Server{store: newStore(db), peers: opts.Peers, logf: opts.Logf, now: opts.Clock}
	if s.peers == nil {
		s.peers = peers.None()
	}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Close closes the bbolt DB.
func (s *Server) Close() error { return s.store.db.Close() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, errInvalid(err.Error()))
		return
	}
	action := r.Form.Get("Action")
	if action == "" {
		writeError(w, &apiError{Code: "MissingAction", Status: 400, Msg: "no Action specified"})
		return
	}
	h, ok := dispatch[action]
	if !ok {
		writeError(w, &apiError{Code: "InvalidAction", Status: 400, Msg: "unsupported action: " + action})
		return
	}
	result, err := h(s, r.Form, r.Host)
	if err != nil {
		s.logf("sns: %s -> %s", action, err.Code)
		writeError(w, err)
		return
	}
	writeResult(w, action, result)
}
