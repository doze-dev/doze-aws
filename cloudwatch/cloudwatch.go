// Package cloudwatch is doze-aws's local CloudWatch Metrics: metric data,
// statistics, and the alarms that watch them.
//
// # Three wires
//
// This is the only service here that speaks three protocols, and it is not a
// choice. CloudWatch is migrating off the Query protocol, and which
// replacement a caller uses is decided at SDK codegen time per language:
// Go v2 and Java send Smithy RPC v2 CBOR, the AWS CLI and boto3 send AWS JSON
// 1.0, and anything pinned to an older SDK still sends Query. There is no
// negotiation and no fallback, so serving one of the three would leave most
// callers — including `aws cloudwatch` itself — unable to talk to it at all.
//
// The three meet in codec.go, which normalises every one of them into the
// same map before a handler sees it. Handlers are wire-agnostic; the wire is
// remembered only long enough to render the response.
package cloudwatch

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/schemaver"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (cloudwatch.bolt). Required.
	DataDir string
	// Peers reaches the services an alarm action notifies.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// Identity is the region and account this service mints ARNs for. The zero
	// value means the conventional local identity.
	Identity awsident.Identity
	// Retention is how long samples are kept; zero uses the default (24h).
	Retention time.Duration
	// MaxSamples caps stored samples; zero uses the default (1e6).
	MaxSamples int
	// IAMMode is the enforcement mode the service's resource-policy guard
	// runs in when the middleware does not stamp one.
	IAMMode string
}

// Server is the CloudWatch service: an http.Handler over three protocols, and
// an io.Closer that closes the store.
type Server struct {
	db    *bolt.DB
	peers peers.Directory
	logf  func(format string, args ...any)
	now   func() time.Time
	stop  chan struct{}
	// bg waits for the sweeper and the evaluator, so Close does not close the
	// store while one of them is inside a bolt transaction.
	bg         sync.WaitGroup
	retention  time.Duration
	maxSamples int
	sink       trace.Sink
	id         awsident.Identity // the region and account this service mints ARNs for
}

// New opens the store under DataDir.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	// Samples are high-volume and disposable, so the store does not fsync per
	// write — the same trade logs makes, and for the same reason: a metric
	// published on every invocation must not make Invoke slow.
	db, err := bolt.Open(filepath.Join(opts.DataDir, "cloudwatch.bolt"), 0o600,
		&bolt.Options{NoSync: true, Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "cloudwatch", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	dir := opts.Peers
	if dir == nil {
		dir = peers.None()
	}
	s := &Server{db: db, peers: dir, logf: logf, now: time.Now, stop: make(chan struct{}),
		retention: opts.Retention, maxSamples: opts.MaxSamples, id: opts.Identity}
	if opts.Clock != nil {
		s.now = opts.Clock
	}
	if s.retention <= 0 {
		s.retention = defaultRetention
	}
	if s.maxSamples <= 0 {
		s.maxSamples = defaultMaxSamples
	}
	s.bg.Add(2)
	go s.sweeper()
	go s.evaluator()
	return s, nil
}

// sweeper drops samples past the retention window on a slow tick. A minute is
// far finer than the window it enforces, which is the point: the cost of a
// sweep is proportional to what it removes, and a long gap would make one
// sweep expensive rather than many cheap.
func (s *Server) sweeper() {
	defer s.bg.Done()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			bg.Tick(s.logf, "cloudwatch: sweeper", func() {
				if n, err := s.sweep(s.retention, s.maxSamples); err != nil {
					s.logf("cloudwatch: sweep: %v", err)
				} else if n > 0 {
					s.logf("cloudwatch: swept %d samples", n)
				}
			})
		}
	}
}

// SweepNow runs one retention pass and reports what it removed. Exported so a
// test can force a sweep rather than wait a minute for the ticker.
func (s *Server) SweepNow() (int, error) { return s.sweep(s.retention, s.maxSamples) }

// SetTraceSink gives the alarm evaluator somewhere to record the actions it
// fires. It runs on a ticker with no request to inherit a sink from, so
// without this an alarm notifying a topic would be a cascade the recorder
// never sees — the same reason lambda and stepfunctions take one. Called once
// during stack construction, before the service serves anything.
func (s *Server) SetTraceSink(sink trace.Sink) { s.sink = sink }

// Close stops the background work and closes the store.
func (s *Server) Close() error {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	// Waited on: a sweep or an evaluation inside a bolt transaction races the
	// close below, and bolt panics on a closed DB.
	s.bg.Wait()
	return s.db.Close()
}

// handler is one CloudWatch operation. It reads a normalised request and
// returns the value to render, with nil meaning "an empty result" — which is
// what an operation returning Unit answers.
type handler func(s *Server, req *request) (any, *awshttp.APIError)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, aerr := parseRequest(r)
	if aerr != nil {
		writeParseError(w, r, aerr)
		return
	}
	h, ok := handlers[req.action]
	if !ok {
		if why, refused := notHere[req.action]; refused {
			s.logf("cloudwatch: %s -> unsupported", req.action)
			req.writeError(w, awshttp.Errf(400, "UnsupportedOperationException",
				"doze-aws does not implement %s: %s", req.action, why))
			return
		}
		req.writeError(w, awshttp.Errf(400, "InvalidAction",
			"unknown CloudWatch action %q", req.action))
		return
	}
	// One constraint table, checked once, whichever wire this arrived on.
	if aerr := req.validate(); aerr != nil {
		req.writeError(w, aerr)
		return
	}
	result, aerr := h(s, req)
	if aerr != nil {
		s.logf("cloudwatch: %s -> %s", req.action, aerr.Code)
		req.writeError(w, aerr)
		return
	}
	s.logf("cloudwatch: %s ok (%s)", req.action, req.wire)
	req.writeResult(w, result)
}

// readAll is io.ReadAll with the MaxBytesReader error surfaced as itself, so
// an oversized body reports as too large rather than as a decode failure.
func readAll(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return buf.Bytes(), nil
}
