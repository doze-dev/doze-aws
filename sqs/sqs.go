// Package sqs is doze-aws's ground-up, pure-Go SQS-compatible service: no
// LocalStack, no JVM. It speaks both wire protocols (AWS JSON 1.0 used by
// modern SDKs and the legacy Query/XML protocol used by aws-sdk-go v1-era
// clients), persists to a bbolt store under the data directory, and supports
// visibility timeout, delay, retention, message attributes, long polling, FIFO
// queues (group ordering + deduplication), dead-letter redrive, queue tags,
// and message move tasks.
//
// See docs/SUPPORT.md for the operation-by-operation support table.
package sqs

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/modelcheck"

	"github.com/doze-dev/doze-aws/internal/lazybolt"
	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/schemaver"
	"github.com/doze-dev/doze-aws/peers"
)

// sweepInterval is how often the background janitor reclaims expired messages
// and stale dedup entries from write-only queues.
const sweepInterval = time.Minute

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (sqs.bolt). Required.
	DataDir string
	// Peers is accepted for constructor uniformity; SQS initiates no
	// cross-service calls today (Lambda event source mappings poll SQS from
	// the Lambda side).
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// IAMMode is the IAM service's mode; under soft or enforce the queue policy
	// is evaluated on every queue-scoped request, peer calls included.
	IAMMode string
	// Identity is the region and account this service mints ARNs for. The zero
	// value means the conventional local identity.
	Identity awsident.Identity
	// Strict refuses what AWS refuses for a minute after a delete or a purge:
	// creating a queue under a name deleted less than 60 seconds ago, and a
	// second PurgeQueue inside 60 seconds. Off by default (strict.go says why).
	Strict bool
}

// Server is the SQS service: an http.Handler speaking both SQS wire protocols,
// and an io.Closer that stops the janitor and closes the store.
type Server struct {
	store *store
	logf  func(format string, args ...any)
	stop  chan struct{}
	// done closes when the janitor has returned, so Close waits for it before
	// closing bbolt — a sweep mid-transaction against a closed DB is a panic.
	done chan struct{}
	// stopOnce keeps a second Close from closing stop twice. These servers are
	// exported for direct embedding, so an embedder with a `defer svc.Close()`
	// plus an error path that also closes gets two calls.
	stopOnce sync.Once
	guard    iamguard.Guard    // the queue policy, under IAM soft/enforce
	id       awsident.Identity // the region and account this service mints ARNs for
}

// New opens the bbolt store under DataDir and starts the retention janitor.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := lazybolt.Open(filepath.Join(opts.DataDir, "sqs.bolt"), nil,
		func(db *bolt.DB) error { return schemaver.Ensure(db, "sqs", schemaver.Current) })
	if err != nil {
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{store: newStore(db), logf: logf, stop: make(chan struct{}), done: make(chan struct{}), guard: iamguard.Guard{Mode: opts.IAMMode, Logf: logf, Identity: opts.Identity}, id: opts.Identity}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
	}
	// The store mints ARNs for queues it decodes out of bbolt, and those records
	// carry no owner pointer. Stamped after construction, the same way Clock is.
	s.store.id = opts.Identity
	s.store.strict = opts.Strict
	go s.janitor()
	return s, nil
}

// Close stops the janitor goroutine, then closes the bbolt DB.
func (s *Server) Close() error {
	var err error
	s.stopOnce.Do(func() {
		close(s.stop)
		// Waited on, not just signalled: a sweep inside a bolt transaction races
		// the close below, and bolt panics on a closed DB.
		<-s.done
		// A move task running in the background is stopped before the store
		// closes under it.
		s.store.moves.stop()
		err = s.store.db.Close()
	})
	return err
}

func (s *Server) janitor() {
	defer close(s.done)
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			bg.Tick(s.logf, "sqs: janitor", func() { s.store.Sweep() })
		}
	}
}

// checkOwner refuses a queue that belongs to another account. A QueueUrl is
// https://host/<account>/<name>, and GetQueueUrl takes the owner as a
// parameter: the queue in another account is not the one here with the same
// name. Resolving by name alone let a hard-coded account id work locally.
func (s *Server) checkOwner(req *request) *apiError {
	own := s.store.id.Account()
	if owner := req.p.str("QueueOwnerAWSAccountId"); owner != "" && owner != own {
		return errQueueMissing(req.p.str("QueueName"))
	}
	if u := req.p.str("QueueUrl"); u != "" {
		// The path is /<account>/<name>, or /<name> alone, which is how a
		// sibling service in this process spells it (peercall names no
		// account). Anything with more to it, or another account, is not a
		// queue here; reading only the last two segments served
		// /999999999999/extra/orders as orders.
		path := u
		if i := strings.Index(path, "://"); i >= 0 {
			path = path[i+3:]
			if j := strings.Index(path, "/"); j >= 0 {
				path = path[j:]
			} else {
				path = ""
			}
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		switch {
		case len(parts) == 1:
		case len(parts) == 2 && parts[0] == own:
		default:
			return errQueueMissing(parts[len(parts)-1])
		}
	}
	return nil
}

// batchEntryPrefix is the Query-protocol name of each batch operation's entries.
var batchEntryPrefix = map[string]string{
	"SendMessageBatch":             "SendMessageBatchRequestEntry",
	"DeleteMessageBatch":           "DeleteMessageBatchRequestEntry",
	"ChangeMessageVisibilityBatch": "ChangeMessageVisibilityBatchRequestEntry",
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, aerr := parseRequest(r)
	if aerr != nil {
		writeError(w, r.Header.Get("X-Amz-Target") != "", aerr)
		return
	}
	h, ok := handlers[req.action]
	if !ok {
		writeError(w, req.json, &apiError{Code: "InvalidAction", Status: 400, Message: "unsupported action: " + req.action, SenderFault: true})
		return
	}

	// Model-derived input validation, before the handler and for both
	// protocols. The code differs by protocol family, so it follows the
	// request rather than being fixed per service.
	code := modelcheck.CodeQuery
	if req.json {
		code = modelcheck.CodeJSON
	}
	// A batch with no entries is EmptyBatchRequest (model: the batch
	// operations' errors). The Query protocol cannot spell an empty list, so
	// there it is a batch with no entry keys at all, which the required-member
	// check below would otherwise name a ValidationError.
	if prefix := batchEntryPrefix[req.action]; prefix != "" && !req.json && !req.p.hasFormPrefix(prefix+".") {
		writeError(w, false, errBatch("EmptyBatchRequest", "There should be at least one "+prefix+" in the request."))
		return
	}
	if aerr := modelcheck.ValidateMapAs(req.p.asMap(), constraintTables[req.action], code); aerr != nil {
		writeError(w, req.json, &apiError{
			Code: aerr.Code, Status: aerr.Status, Message: aerr.Message, SenderFault: true,
		})
		return
	}
	if aerr := s.checkOwner(req); aerr != nil {
		s.logf("sqs: %s -> %s", req.action, aerr.Code)
		writeError(w, req.json, aerr)
		return
	}
	if err := s.guardRequest(w, r, req); err != nil {
		s.logf("sqs: %s -> %s", req.action, err.Code)
		writeError(w, req.json, err)
		return
	}
	result, err := h(s.store, req)
	if err != nil {
		s.logf("sqs: %s -> %s", req.action, err.Code)
		writeError(w, req.json, err)
		return
	}
	writeResult(w, req, req.action, result)
}
