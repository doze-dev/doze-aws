// Package kinesis is doze-aws's ground-up, pure-Go Kinesis Data Streams: no
// JVM, no Node sidecar, no third-party mock process. It speaks AWS JSON 1.1,
// persists streams, shards and records to a bbolt store, and implements the
// parts of Kinesis that mean something on a laptop:
//
//   - real partition-key routing — MD5 of the key read as a 128-bit integer,
//     matched against each shard's hash range, so a key lands on the same shard
//     locally as it does in the cloud;
//   - the full shard-iterator model (TRIM_HORIZON, LATEST, AT_TIMESTAMP,
//     AT_/AFTER_SEQUENCE_NUMBER) with AWS's five-minute iterator expiry;
//   - resharding with genuine parent/child lineage — SplitShard, MergeShards
//     and UpdateShardCount close the parent and open children, so per-key
//     ordering survives a reshard the way consumers expect;
//   - retention: records really expire, on a background sweep.
//
// Enhanced fan-out is registered and described honestly, but SubscribeToShard
// needs HTTP/2 event-stream framing and is refused rather than faked.
//
// See docs/api-support/kinesis.md for the operation-by-operation support table.
package kinesis

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
	// DataDir holds the bbolt store (kinesis.bolt). Required.
	DataDir string
	// Peers resolves KMS. Kinesis dispatches nothing — Lambda polls it rather
	// than the other way round — but a stream encrypted with a customer key
	// has to check that key is still usable, the way AWS does, instead of
	// accepting writes against one that has been disabled or deleted.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// SweepInterval is how often expired records are reclaimed. Zero uses one
	// minute.
	SweepInterval time.Duration
}

// Server is the Kinesis service: an http.Handler speaking AWS JSON 1.1, and an
// io.Closer that stops the retention sweeper and closes the store.
type Server struct {
	store *Store
	logf  func(format string, args ...any)
	api   awsjson.API
	now   func() time.Time
	stop  chan struct{}
	// peers resolves KMS, so a stream encrypted with a customer key can check
	// that the key is still usable rather than accepting writes against one
	// that has been disabled or deleted.
	peers peers.Directory
}

// New opens the store under DataDir and starts the retention sweeper.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "kinesis.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "kinesis", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store: newStore(db),
		logf:  logf,
		api:   awsjson.API{TargetPrefix: "Kinesis_20131202", JSONVersion: "1.1"},
		now:   time.Now,
		stop:  make(chan struct{}),
		peers: opts.Peers,
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
		s.now = opts.Clock
	}
	interval := opts.SweepInterval
	if interval <= 0 {
		interval = time.Minute
	}
	go s.sweeper(interval)
	return s, nil
}

// Close stops the sweeper and closes the bbolt DB.
func (s *Server) Close() error {
	close(s.stop)
	return s.store.db.Close()
}

// sweeper reclaims records past their stream's retention window.
func (s *Server) sweeper(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if n := s.store.Sweep(); n > 0 {
				s.logf("kinesis: retention reclaimed %d record(s)", n)
			}
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
	if h, ok := handlers[action]; ok {
		// Model-derived input validation runs before the handler, for every
		// operation at once — coverage is then a property of the dispatch
		// table rather than something each handler has to remember.
		if aerr := modelcheck.ValidateMap(params, constraintTables[action]); aerr != nil {
			s.logf("kinesis: %s -> %s", action, aerr.Code)
			s.api.WriteError(w, aerr)
			return
		}
		result, aerr := h(s, params)
		if aerr != nil {
			s.logf("kinesis: %s -> %s", action, aerr.Code)
			s.api.WriteError(w, aerr)
			return
		}
		s.logf("kinesis: %s ok", action)
		s.api.Write(w, result)
		return
	}
	// Documented operations doze-aws refuses on purpose answer with a reason
	// rather than a bare InvalidAction, so the boundary is legible.
	if why, ok := stubActions[action]; ok {
		s.logf("kinesis: %s -> unsupported", action)
		s.api.WriteError(w, awshttp.Errf(400, "InvalidArgumentException",
			"doze-aws does not implement %s: %s", action, why))
		return
	}
	s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown Kinesis action %q", action))
}

// stubActions are operations doze-aws deliberately refuses, with the reason.
// Emulating them would be a lie rather than a simplification.
var stubActions = map[string]string{
	"SubscribeToShard": "enhanced fan-out delivers over an HTTP/2 event stream; " +
		"use GetRecords, or register the consumer and poll",
	"UpdateStreamWarmThroughput": "warm throughput is a capacity hint with no local meaning",
}
