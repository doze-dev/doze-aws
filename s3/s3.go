// Package s3 is doze-aws's from-scratch S3 service. It speaks the REST-XML
// protocol both SDK generations produce, in both addressing styles
// (path-style /bucket/key and virtual-hosted bucket.host/key), and accepts the
// full upload-body matrix: plain, UNSIGNED-PAYLOAD, signed aws-chunked, and
// trailer-checksum streaming (aws-sdk-go-v2's default). Versioning, multipart,
// conditional reads AND writes, flexible checksums (CRC32/C, CRC64NVME,
// SHA1/256), object tagging, CORS with real preflight evaluation, lifecycle
// expiration enforced by a janitor, object lock (retention + legal hold), and
// bucket-website index/error serving are all functional.
//
// See docs/SUPPORT.md for the operation-by-operation support table.
package s3

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshost"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/restroute"
	"github.com/doze-dev/doze-aws/internal/s3store"
	"github.com/doze-dev/doze-aws/internal/sigparse"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the metadata database and blob files. Required.
	DataDir string
	// Peers is how S3 event notifications reach SNS/SQS/Lambda targets.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// Suffix is the instance's DNS suffix, standing in for amazonaws.com.
	Suffix string
	// Identity is the region and account this service mints ARNs for. The zero
	// value means the conventional local identity.
	Identity awsident.Identity
	// IAMMode is the IAM service's mode; under soft or enforce the bucket
	// policy is evaluated on every bucket and object request.
	IAMMode string
}

// maxVhostWarned bounds the set of base hosts warnLostVHost will remember.
const maxVhostWarned = 64

// Server is the S3 service: an http.Handler + io.Closer.
type Server struct {
	store *s3store.Store
	// router is the chi router, built on the first request (router.go).
	router func() *restroute.Router
	peers  peers.Directory
	logf   func(format string, args ...any)
	now    func() time.Time
	stop   chan struct{}
	// done closes when the janitor has returned, so Close waits for it before
	// closing bbolt — a sweep mid-transaction against a closed DB is a panic.
	done chan struct{}
	// stopOnce keeps a second Close from closing stop twice. These servers are
	// exported for direct embedding, so an embedder with a `defer svc.Close()`
	// plus an error path that also closes gets two calls.
	stopOnce sync.Once
	guard    iamguard.Guard    // the bucket policy, under IAM soft/enforce
	id       awsident.Identity // the region and account this service mints ARNs for
	suffix   string            // stands in for amazonaws.com in hostnames
	// vhostWarned remembers which base hosts warnLostVHost has already
	// mentioned, so a client sending every request that way gets one line.
	//
	// Bounded, because the key comes from the client's Host header. It was a
	// sync.Map with no cap, which is a map keyed by user input and never
	// evicted — the same shape as two other findings in this pass, and I wrote
	// it. A mutex rather than sync.Map so the size is knowable; this sits on
	// the path-style branch, which is a map read and nothing else.
	vhostMu     sync.Mutex
	vhostWarned map[string]bool
}

// New opens the store under DataDir and starts the lifecycle janitor.
func New(opts Options) (*Server, error) {
	st, err := s3store.Open(opts.DataDir)
	if err != nil {
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	st.Logf = logf
	s := &Server{
		store:       st,
		peers:       opts.Peers,
		logf:        logf,
		now:         opts.Clock,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		vhostWarned: map[string]bool{},
		guard:       iamguard.Guard{Mode: opts.IAMMode, Logf: logf, Identity: opts.Identity},
		id:          opts.Identity,
		suffix:      opts.Suffix,
	}
	s.router = sync.OnceValue(s.buildRouter)
	iamguard.RegisterResolver(s, s.resolveIAM)
	if s.peers == nil {
		s.peers = peers.None()
	}
	if s.now == nil {
		s.now = time.Now
	} else {
		st.SetClock(s.now)
	}
	go s.janitor()
	return s, nil
}

// Close stops the janitor and closes the store.
func (s *Server) Close() error {
	iamguard.UnregisterResolver(s)
	var err error
	s.stopOnce.Do(func() {
		close(s.stop)
		// Waited on, not just signalled: a sweep inside a bolt transaction races
		// the close below, and bolt panics on a closed DB.
		<-s.done
		err = s.store.Close()
	})
	return err
}

// janitor applies lifecycle expiration rules.
func (s *Server) janitor() {
	defer close(s.done)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			bg.Tick(s.logf, "s3: janitor", func() { s.sweepLifecycle() })
		}
	}
}

// resolvePath splits a request into (bucket, key) handling both addressing
// styles. bucket=="" means a service-level request (ListBuckets).
func (s *Server) resolvePath(r *http.Request) (bucket, key string) {
	// Virtual-hosted style has one shape now: <bucket>.s3.<region>.<suffix>,
	// AWS's own, read by internal/awshost.
	//
	// There used to be a second — <bucket>.<--s3-host>, a base host the
	// operator named, with its own hand-rolled parser here. It was the last of
	// the five Host parsers awshost was created to absorb, and a second way to
	// address a bucket by host is exactly the kind of choice this tree is
	// removing. The suffix covers it.
	//
	// The load-bearing part survives for free: a request to the BARE base host
	// is a service-level request (ListBuckets), not a bucket named "".
	// Parse("aws.harbour.doze", "aws.harbour.doze") strips to an empty host and
	// returns the zero Info, and Parse("s3.<region>.<suffix>", …) finds the
	// infix in leading position and refuses to read a resource name from it —
	// both leave bucket empty and fall through to path style.
	bucket, key, vhost := s.addressOf(r)
	if !vhost {
		s.warnLostVHost(r)
	}
	return bucket, key
}

// addressOf is resolvePath without the warning, and says which style the
// request was: the IAM resolver asks too, and a line per request is a line
// nobody reads.
func (s *Server) addressOf(r *http.Request) (bucket, key string, vhost bool) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	if bucket = awshost.Parse(r.Host, s.suffix).Bucket; bucket != "" {
		key, _ = url.PathUnescape(path)
		return bucket, key, true
	}
	// Path style: /bucket/key...
	b, rest, _ := strings.Cut(path, "/")
	bucket, _ = url.PathUnescape(b)
	key, _ = url.PathUnescape(rest)
	return bucket, key, false
}

// warnLostVHost says so when a request LOOKS like virtual-hosted addressing
// this service no longer understands.
//
// Removing --s3-host is the one change in this cleanup that can fail quietly.
// Its default was "localhost", and *.localhost really does resolve to loopback
// on macOS and on Linux under systemd-resolved — so an SDK pointed at
// http://localhost:4566 with the default UsePathStyle:false addressed buckets
// this way, and worked. Afterwards the same request is read path-style:
// GET photos.localhost/receipts/jan.pdf becomes bucket "receipts", key
// "jan.pdf". That is a NoSuchBucket for a bucket nobody named, or worse a
// successful read from the wrong one.
//
// So it is said out loud. Once per distinct shape — a client does this on
// every request, and a line per request is a line nobody reads.
func (s *Server) warnLostVHost(r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	// An IP literal is not a hostname with a bucket in front of it. 127.0.0.1
	// otherwise reads as head "127", rest "0.0.1", and "127" is a legal bucket
	// name — so this has to come first.
	if net.ParseIP(host) != nil {
		return
	}
	head, rest, ok := strings.Cut(host, ".")
	// Two labels minimum, no AWS infix (awshost would have read it), not the
	// instance's own suffix, and a leading label that could be a bucket.
	if !ok || rest == "" || !s3store.ValidBucketName(head) {
		return
	}
	// The instance's own suffix, and the suffix ITSELF — a request to the bare
	// suffix is a service-level call, not a bucket that failed to resolve.
	if suf := strings.ToLower(s.suffix); suf != "" &&
		(host == suf || strings.HasSuffix(host, "."+suf)) {
		return
	}
	if strings.Contains(rest, ".s3.") || strings.HasPrefix(rest, "s3.") {
		return
	}
	s.vhostMu.Lock()
	if s.vhostWarned[rest] {
		s.vhostMu.Unlock()
		return
	}
	if len(s.vhostWarned) >= maxVhostWarned {
		// Said it enough. A client reaching this many distinct base hosts is
		// not someone who needs the advice repeated; it is someone sending
		// varied Host headers, and the map is keyed by what they send.
		s.vhostMu.Unlock()
		return
	}
	s.vhostWarned[rest] = true
	s.vhostMu.Unlock()
	s.logf("s3: read %s path-style — <bucket>.%s addressing was removed with --s3-host. "+
		"Use UsePathStyle, or --suffix %s and <bucket>.s3.%s.%s",
		r.Host, rest, rest, s.id.RegionName(), rest)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, _ := s.resolvePath(r)
	q := r.URL.Query()

	// Presigned-URL expiry is enforced here as well as at the gateway, so a
	// standalone-mounted s3 service keeps the same behavior.
	if present, expired := sigparse.PresignedExpiry(q, s.now()); present && expired {
		writeS3Error(w, awshttp.Errf(403, "AccessDenied", "Request has expired"))
		return
	}

	// CORS: answer preflights from the bucket's rules; decorate everything
	// else that carries an Origin.
	if r.Method == http.MethodOptions {
		s.handlePreflight(w, r, bucket)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && bucket != "" {
		s.applyCORS(w, r, bucket, origin)
	}

	// Everything else is the router's: it matches the request once and runs
	// validation, the IAM guard and the sub-resource refusal with its operation
	// known — see router.go. A virtual-hosted request's bucket joins the path
	// first, so there is one shape to match.
	s.router().ServeHTTP(w, routed(r, awshost.Parse(r.Host, s.suffix).Bucket))
}

// SweepLifecycleNow runs one lifecycle sweep immediately (tests drive this
// with an injected clock instead of waiting for the janitor tick).
func (s *Server) SweepLifecycleNow() { s.sweepLifecycle() }
