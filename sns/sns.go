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

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/schemaver"

	"github.com/doze-dev/doze-aws/peers"
)

// snsXMLNS is the SNS Query API namespace, fixed since 2010.
const snsXMLNS = "http://sns.amazonaws.com/doc/2010-03-31/"

// qapi renders the SNS Query/XML envelopes. EmptyResult: real SNS always
// emits the {Action}Result element, and some SDK deserializers (e.g.
// TagResource in aws-sdk-go-v2) require the node to be present.
var qapi = awsquery.API{XMLNS: snsXMLNS, EmptyResult: true}

// Attr is an SNS message attribute (String/Number use StringValue; Binary
// uses BinaryValue) — the Query codec's decoded shape, used directly.
type Attr = awsquery.MessageAttr

// writeError renders err in the Query error envelope, classifying the fault
// from its status (4xx Sender, 5xx Receiver) the way real AWS does.
func writeError(w http.ResponseWriter, err *apiError) {
	e := *err
	e.SenderFault = err.Status < 500
	qapi.WriteError(w, &e)
}

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
	form, err := awsquery.Params(r)
	if err != nil {
		writeError(w, awshttp.AsAPIError(err))
		return
	}
	action := form.Get("Action")
	if action == "" {
		writeError(w, &apiError{Code: "MissingAction", Status: 400, Message: "no Action specified", SenderFault: true})
		return
	}
	h, ok := dispatch[action]
	if !ok {
		writeError(w, &apiError{Code: "InvalidAction", Status: 400, Message: "unsupported action: " + action, SenderFault: true})
		return
	}
	result, aerr := h(s, form, r.Host)
	if aerr != nil {
		s.logf("sns: %s -> %s", action, aerr.Code)
		writeError(w, aerr)
		return
	}
	qapi.WriteResult(w, action, result)
}
