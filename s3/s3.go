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
	peers peers.Directory
	logf  func(format string, args ...any)
	now   func() time.Time
	stop  chan struct{}
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
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/")
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
	bucket = awshost.Parse(r.Host, s.suffix).Bucket
	if bucket == "" {
		s.warnLostVHost(r)
	}
	if bucket != "" {
		key, _ = url.PathUnescape(path)
		return bucket, key
	}
	// Path style: /bucket/key...
	b, rest, _ := strings.Cut(path, "/")
	bucket, _ = url.PathUnescape(b)
	key, _ = url.PathUnescape(rest)
	return bucket, key
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
	bucket, key := s.resolvePath(r)
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

	// Model-derived input validation runs before the dispatch, for every routed
	// operation at once — coverage is then a property of the route table rather
	// than something each handler has to remember.
	if _, verr := validateControl(r); verr != nil {
		s.logf("s3: %s %s -> %s", r.Method, r.URL.Path, verr.Code)
		writeS3Error(w, verr)
		return
	}

	if aerr := s.guardRequest(w, r, bucket, key); aerr != nil {
		s.logf("s3: %s /%s/%s -> %s", r.Method, bucket, key, aerr.Code)
		writeS3Error(w, aerr)
		return
	}

	var aerr *awshttp.APIError
	switch {
	case bucket == "":
		aerr = s.serviceLevel(w, r)
	case key == "":
		aerr = s.bucketLevel(w, r, bucket, q)
	default:
		aerr = s.objectLevel(w, r, bucket, key, q)
	}
	if aerr != nil {
		s.logf("s3: %s /%s/%s -> %s", r.Method, bucket, key, aerr.Code)
		writeS3Error(w, aerr)
	}
}

// serviceLevel handles requests with no bucket in the path.
func (s *Server) serviceLevel(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	if r.Method != http.MethodGet {
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported service-level method %s", r.Method)
	}
	return s.listBuckets(w)
}

// bucketLevel dispatches bucket-scoped operations by method + query flag.
func (s *Server) bucketLevel(w http.ResponseWriter, r *http.Request, bucket string, q url.Values) *awshttp.APIError {
	// A sub-resource this build does not implement must be refused here: every
	// method below ends in a default arm that is a DIFFERENT operation.
	if aerr := checkBucketSubresource(q); aerr != nil {
		return aerr
	}
	switch r.Method {
	case http.MethodHead:
		return s.headBucket(w, bucket)
	case http.MethodGet:
		switch {
		case q.Has("location"):
			return s.getBucketLocation(w, bucket)
		case q.Has("versioning"):
			return s.getBucketVersioning(w, bucket)
		case q.Has("versions"):
			return s.listObjectVersions(w, bucket, q)
		case q.Has("uploads"):
			return s.listMultipartUploads(w, bucket, q)
		case q.Has("tagging"):
			return s.getBucketTagging(w, bucket)
		case q.Has("cors"):
			return s.getBucketDoc(w, bucket, "cors")
		case q.Has("lifecycle"):
			return s.getBucketDoc(w, bucket, "lifecycle")
		case q.Has("website"):
			return s.getBucketDoc(w, bucket, "website")
		case q.Has("object-lock"):
			return s.getBucketDoc(w, bucket, "object-lock")
		case q.Has("policy"):
			return s.getBucketPolicy(w, bucket)
		case q.Has("policyStatus"):
			return s.getBucketPolicyStatus(w, bucket)
		case q.Has("publicAccessBlock"):
			return s.getBucketDoc(w, bucket, "publicAccessBlock")
		case q.Has("ownershipControls"):
			return s.getBucketDoc(w, bucket, "ownershipControls")
		case q.Has("acl"):
			return s.getBucketACL(w, bucket)
		case q.Has("encryption"):
			return s.getBucketDoc(w, bucket, "encryption")
		case q.Has("notification"):
			return s.getBucketDoc(w, bucket, "notification")
		case q.Has("replication"):
			return s.getBucketDoc(w, bucket, "replication")
		case q.Has("logging"):
			return s.getBucketDoc(w, bucket, "logging")
		case q.Has("accelerate"):
			return s.getBucketDoc(w, bucket, "accelerate")
		case q.Has("requestPayment"):
			return s.getBucketDoc(w, bucket, "requestPayment")
		default:
			return s.listObjects(w, r, bucket, q)
		}
	case http.MethodPut:
		switch {
		case q.Has("versioning"):
			return s.putBucketVersioning(w, r, bucket)
		case q.Has("tagging"):
			return s.putBucketTagging(w, r, bucket)
		case q.Has("cors"):
			return s.putBucketDoc(w, r, bucket, "cors")
		case q.Has("lifecycle"):
			return s.putBucketDoc(w, r, bucket, "lifecycle")
		case q.Has("website"):
			return s.putBucketDoc(w, r, bucket, "website")
		case q.Has("object-lock"):
			return s.putBucketDoc(w, r, bucket, "object-lock")
		case q.Has("policy"):
			return s.putBucketPolicy(w, r, bucket)
		case q.Has("publicAccessBlock"):
			return s.putPublicAccessBlock(w, r, bucket)
		case q.Has("ownershipControls"):
			return s.putBucketOwnershipControls(w, r, bucket)
		case q.Has("acl"):
			return s.putBucketACL(w, r, bucket)
		case q.Has("encryption"):
			return s.putBucketDoc(w, r, bucket, "encryption")
		case q.Has("notification"):
			return s.putBucketDoc(w, r, bucket, "notification")
		case q.Has("replication"):
			return s.putBucketDoc(w, r, bucket, "replication")
		case q.Has("logging"):
			return s.putBucketDoc(w, r, bucket, "logging")
		case q.Has("accelerate"):
			return s.putBucketDoc(w, r, bucket, "accelerate")
		case q.Has("requestPayment"):
			return s.putBucketDoc(w, r, bucket, "requestPayment")
		default:
			return s.createBucket(w, r, bucket)
		}
	case http.MethodDelete:
		switch {
		case q.Has("tagging"):
			return s.deleteBucketDoc(w, bucket, "tagging")
		case q.Has("cors"):
			return s.deleteBucketDoc(w, bucket, "cors")
		case q.Has("lifecycle"):
			return s.deleteBucketDoc(w, bucket, "lifecycle")
		case q.Has("website"):
			return s.deleteBucketDoc(w, bucket, "website")
		case q.Has("policy"):
			return s.deleteBucketDoc(w, bucket, "policy")
		case q.Has("publicAccessBlock"):
			return s.deleteBucketDoc(w, bucket, "publicAccessBlock")
		case q.Has("ownershipControls"):
			return s.deleteBucketDoc(w, bucket, "ownershipControls")
		case q.Has("encryption"):
			return s.deleteBucketDoc(w, bucket, "encryption")
		case q.Has("replication"):
			return s.deleteBucketDoc(w, bucket, "replication")
		case q.Has("notification"):
			return s.deleteBucketDoc(w, bucket, "notification")
		case q.Has("logging"):
			return s.deleteBucketDoc(w, bucket, "logging")
		case q.Has("accelerate"):
			return s.deleteBucketDoc(w, bucket, "accelerate")
		case q.Has("requestPayment"):
			return s.deleteBucketDoc(w, bucket, "requestPayment")
		case len(q) == 0:
			return s.deleteBucket(w, bucket)
		default:
			// A subresource DELETE we don't model (publicAccessBlock,
			// ownershipControls, analytics, …). Never fall through to
			// deleting the whole bucket — real S3 removes only the config.
			w.WriteHeader(204)
			return nil
		}
	case http.MethodPost:
		if q.Has("delete") {
			return s.deleteObjects(w, r, bucket)
		}
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported bucket-level request")
}

// objectLevel dispatches object-scoped operations.
func (s *Server) objectLevel(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) *awshttp.APIError {
	// See bucketLevel: an unrecognised object sub-resource would otherwise
	// fall through to putObject or, worse, deleteObject.
	if aerr := checkObjectSubresource(q); aerr != nil {
		return aerr
	}
	switch r.Method {
	case http.MethodHead:
		return s.getObject(w, r, bucket, key, q, true)
	case http.MethodGet:
		switch {
		case q.Has("tagging"):
			return s.getObjectTagging(w, bucket, key, q.Get("versionId"))
		case q.Has("attributes"):
			return s.getObjectAttributes(w, r, bucket, key, q.Get("versionId"))
		case q.Has("retention"):
			return s.getObjectRetention(w, bucket, key, q.Get("versionId"))
		case q.Has("legal-hold"):
			return s.getObjectLegalHold(w, bucket, key, q.Get("versionId"))
		case q.Has("acl"):
			return s.getObjectACL(w, bucket, key)
		case q.Has("uploadId"):
			return s.listParts(w, bucket, key, q.Get("uploadId"))
		default:
			return s.getObject(w, r, bucket, key, q, false)
		}
	case http.MethodPut:
		switch {
		case q.Has("tagging"):
			return s.putObjectTagging(w, r, bucket, key, q.Get("versionId"))
		case q.Has("retention"):
			return s.putObjectRetention(w, r, bucket, key, q)
		case q.Has("legal-hold"):
			return s.putObjectLegalHold(w, r, bucket, key, q.Get("versionId"))
		case q.Has("acl"):
			return s.putObjectACL(w, r, bucket, key)
		case q.Has("partNumber") && q.Has("uploadId"):
			if r.Header.Get("x-amz-copy-source") != "" {
				return s.uploadPartCopy(w, r, bucket, key, q)
			}
			return s.uploadPart(w, r, bucket, key, q)
		case r.Header.Get("x-amz-copy-source") != "":
			return s.copyObject(w, r, bucket, key)
		default:
			return s.putObject(w, r, bucket, key)
		}
	case http.MethodPost:
		switch {
		case q.Has("uploads"):
			return s.createMultipartUpload(w, r, bucket, key)
		case q.Has("uploadId"):
			return s.completeMultipartUpload(w, r, bucket, key, q.Get("uploadId"))
		}
	case http.MethodDelete:
		switch {
		case q.Has("uploadId"):
			return s.abortMultipartUpload(w, bucket, key, q.Get("uploadId"))
		case q.Has("tagging"):
			return s.deleteObjectTagging(w, bucket, key, q.Get("versionId"))
		default:
			return s.deleteObject(w, r, bucket, key, q)
		}
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported object-level request")
}

// SweepLifecycleNow runs one lifecycle sweep immediately (tests drive this
// with an injected clock instead of waiting for the janitor tick).
func (s *Server) SweepLifecycleNow() { s.sweepLifecycle() }
