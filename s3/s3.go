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
// See docs/api-support/s3.md for the operation-by-operation support table.
package s3

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/s3store"
	"github.com/doze-dev/doze-aws/internal/sigparse"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the metadata database and blob files. Required.
	DataDir string
	// Host is the base host for virtual-hosted-style addressing: a request
	// whose Host is <bucket>.<Host> addresses that bucket. Path-style always
	// works. Empty disables vhost detection (path-style only).
	Host string
	// Peers is how S3 event notifications reach SNS/SQS/Lambda targets.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
}

// Server is the S3 service: an http.Handler + io.Closer.
type Server struct {
	store *s3store.Store
	host  string
	peers peers.Directory
	logf  func(format string, args ...any)
	now   func() time.Time
	stop  chan struct{}
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
		store: st,
		host:  strings.ToLower(opts.Host),
		peers: opts.Peers,
		logf:  logf,
		now:   opts.Clock,
		stop:  make(chan struct{}),
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
	close(s.stop)
	return s.store.Close()
}

// janitor applies lifecycle expiration rules.
func (s *Server) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.sweepLifecycle()
		}
	}
}

// resolvePath splits a request into (bucket, key) handling both addressing
// styles. bucket=="" means a service-level request (ListBuckets).
func (s *Server) resolvePath(r *http.Request) (bucket, key string) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	// Virtual-hosted style: Host = <bucket>.<base> (base configured), or the
	// conventional <bucket>.s3.<anything> shape.
	host := strings.ToLower(r.Host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	if s.host != "" && host != s.host && strings.HasSuffix(host, "."+s.host) {
		bucket = strings.TrimSuffix(host, "."+s.host)
	} else if i := strings.Index(host, ".s3."); i > 0 {
		bucket = host[:i]
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
