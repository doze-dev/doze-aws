// Package dynamodb is doze-aws's from-scratch DynamoDB: the full item model
// (arbitrary-precision numbers, sets, documents), real expression parsing
// (condition/filter/key-condition/update/projection), GSIs and LSIs maintained
// transactionally, Query/Scan with DynamoDB's paging semantics, conditional
// writes, batch operations, single-node TransactWriteItems/TransactGetItems
// with ClientRequestToken idempotency, and TTL enforced by a janitor.
//
// Deferred (post-1.0): DynamoDB Streams. PartiQL arrives in Phase 8. Global
// tables, DAX, and backup/export are cloud infrastructure and answer honestly.
//
// See docs/api-support/dynamodb.md for the operation-by-operation table.
package dynamodb

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/ddb/store"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (dynamodb.bolt). Required.
	DataDir string
	// Peers is accepted for constructor uniformity (streams would use it).
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the DynamoDB service: an http.Handler speaking AWS JSON 1.0, and
// an io.Closer that stops the janitor and closes the store.
type Server struct {
	store *store.Store
	logf  func(format string, args ...any)
	api   awsjson.API
	stop  chan struct{}
}

// New opens the store under DataDir and starts the TTL janitor.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "dynamodb.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store: store.New(db),
		logf:  logf,
		api:   awsjson.API{TargetPrefix: "DynamoDB_20120810", JSONVersion: "1.0"},
		stop:  make(chan struct{}),
	}
	if opts.Clock != nil {
		s.store.SetClock(opts.Clock)
	}
	go s.janitor()
	return s, nil
}

// Close stops the janitor and closes the bbolt DB.
func (s *Server) Close() error {
	close(s.stop)
	return s.store.DB().Close()
}

// janitor enforces TTL.
func (s *Server) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.store.SweepTTL()
		}
	}
}

// SweepTTLNow runs one TTL sweep immediately (tests).
func (s *Server) SweepTTLNow() { s.store.SweepTTL() }

type handler func(s *Server, body []byte) (any, *awshttp.APIError)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action, aerr := s.api.Action(r)
	if aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	var body rawBody
	if aerr := awsjson.DecodeBody(r, &body); aerr != nil {
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
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown DynamoDB action %q", action))
		return
	}
	result, aerr := h(s, body.raw)
	if aerr != nil {
		s.logf("dynamodb: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("dynamodb: %s ok", action)
	s.api.Write(w, result)
}

// rawBody captures the request body verbatim (handlers decode their own shapes,
// and transactions hash the body for idempotency).
type rawBody struct {
	raw []byte
}

func (b *rawBody) UnmarshalJSON(data []byte) error {
	b.raw = append([]byte(nil), data...)
	return nil
}

// stubActions answer honestly instead of pretending.
var stubActions = map[string]string{
	"ExecuteStatement":                    "PartiQL arrives in Phase 8",
	"BatchExecuteStatement":               "PartiQL arrives in Phase 8",
	"ExecuteTransaction":                  "PartiQL arrives in Phase 8",
	"CreateGlobalTable":                   "there is exactly one region locally",
	"DescribeGlobalTable":                 "there is exactly one region locally",
	"DescribeGlobalTableSettings":         "there is exactly one region locally",
	"ListGlobalTables":                    "there is exactly one region locally",
	"UpdateGlobalTable":                   "there is exactly one region locally",
	"UpdateGlobalTableSettings":           "there is exactly one region locally",
	"CreateBackup":                        "backups are cloud infrastructure; copy the data directory instead",
	"DeleteBackup":                        "backups are cloud infrastructure; copy the data directory instead",
	"DescribeBackup":                      "backups are cloud infrastructure; copy the data directory instead",
	"ListBackups":                         "backups are cloud infrastructure; copy the data directory instead",
	"RestoreTableFromBackup":              "backups are cloud infrastructure; copy the data directory instead",
	"RestoreTableToPointInTime":           "backups are cloud infrastructure; copy the data directory instead",
	"ExportTableToPointInTime":            "exports are cloud infrastructure",
	"ImportTable":                         "imports are cloud infrastructure",
	"DescribeExport":                      "exports are cloud infrastructure",
	"DescribeImport":                      "imports are cloud infrastructure",
	"ListExports":                         "exports are cloud infrastructure",
	"ListImports":                         "imports are cloud infrastructure",
	"DescribeStream":                      "DynamoDB Streams are deferred post-1.0",
	"GetRecords":                          "DynamoDB Streams are deferred post-1.0",
	"GetShardIterator":                    "DynamoDB Streams are deferred post-1.0",
	"ListStreams":                         "DynamoDB Streams are deferred post-1.0",
	"DescribeKinesisStreamingDestination": "Kinesis streaming needs Kinesis",
	"EnableKinesisStreamingDestination":   "Kinesis streaming needs Kinesis",
	"DisableKinesisStreamingDestination":  "Kinesis streaming needs Kinesis",
	"UpdateKinesisStreamingDestination":   "Kinesis streaming needs Kinesis",
}
