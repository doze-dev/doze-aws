package s3

// S3, routed by the table generated from AWS's model.
//
// S3 has three path shapes — the service (/), a bucket (/{Bucket}) and an
// object (/{Bucket}/{Key+}) — and tells most of its operations apart by a
// query flag (?tagging, ?versioning) or a header (x-amz-copy-source) on the
// same method and path. chi routes the path; the model's Marks, NeedHeaders and
// NeedQuery are each operation's Pick, tried in the table's order — more
// required markers first, as LocalStack's op_router scores them.
//
// A request is matched once. validation, the IAM guard, the unimplemented
// sub-resource refusal and the handler read its operation from the context;
// before this, the handler's own `q.Has` chains and the validator's matcher
// each worked it out separately and could disagree.

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/restroute"
)

const (
	servicePattern = "/"
	bucketPattern  = "/{Bucket}"
	objectPattern  = "/{Bucket}/*"
)

// pick is the part of a route's identity a path cannot carry: the markers it
// requires, no marker it does not declare, and the headers and query
// parameters whose presence makes it this operation and not a neighbour.
func (rt route) pick() func(*http.Request) bool {
	return func(r *http.Request) bool {
		q := r.URL.Query()
		for k, want := range rt.Marks {
			v, ok := q[k]
			if !ok || (want != "" && (len(v) == 0 || v[0] != want)) {
				return false
			}
		}
		// A marker the route does not declare belongs to a different operation.
		// Without this, ListObjects — which declares none — claims
		// GET /bucket?legal-hold and answers with a bucket listing.
		for k := range q {
			if markerKeys[k] {
				if _, declared := rt.Marks[k]; !declared {
					return false
				}
			}
		}
		for _, h := range rt.NeedHeaders {
			if _, ok := r.Header[http.CanonicalHeaderKey(h)]; !ok {
				return false
			}
		}
		for _, name := range rt.NeedQuery {
			if _, ok := q[name]; !ok {
				return false
			}
		}
		return true
	}
}

// noContent is the answer to a DELETE of a bucket sub-resource doze-aws does
// not model: real S3 removes only the config, so it is a no-op here and never
// the bucket itself.
func noContent(w http.ResponseWriter, _ *http.Request) *awshttp.APIError {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// routeSpecs is every route S3 serves, without handlers, in the order the
// candidates for a method and path are tried.
var routeSpecs = sync.OnceValue(func() []restroute.Route {
	var service, bucket, object, emptyKey []restroute.Route
	for _, rt := range routes {
		spec := restroute.Route{Op: rt.Op, Method: rt.Method, Pick: rt.pick()}
		switch {
		case len(rt.Segs) == 0:
			spec.Pattern = servicePattern
			// ListBuckets is the last resort at the service level: GET / is a
			// listing whatever else it carries.
			if rt.Op == "ListBuckets" {
				spec.Pick = nil
			}
			service = append(service, spec)
		case rt.Greedy:
			// The object-level route, and the same route on the bucket's path
			// with the key left out — the second pass, tried only when no
			// bucket-level operation claims the request. GET /bucket?retention
			// is GetObjectRetention with no key, which validation refuses,
			// rather than a bucket listing.
			spec.Pattern = objectPattern
			object = append(object, spec)
			spec.Pattern = bucketPattern
			// These exist so validation can refuse the missing key; if it does
			// not, the answer is still a 405, never the object handler with a
			// blank key.
			spec.Handler = func(_ http.ResponseWriter, r *http.Request) *awshttp.APIError { return unsupported(r) }
			emptyKey = append(emptyKey, spec)
		default:
			spec.Pattern = bucketPattern
			if rt.Op == "DeleteBucket" {
				// Only a DELETE with nothing else on it removes the bucket.
				pick := spec.Pick
				spec.Pick = func(r *http.Request) bool { return len(r.URL.Query()) == 0 && pick(r) }
			}
			bucket = append(bucket, spec)
		}
	}
	// A DELETE of a bucket sub-resource that is not modelled is a no-op, tried
	// after every operation that names one and before the empty-key objects.
	bucket = append(bucket, restroute.Route{Method: "DELETE", Pattern: bucketPattern, Handler: noContent})
	out := append(service, bucket...)
	out = append(out, emptyKey...)
	return append(out, object...)
})

// invocation is one request, as the handlers read it.
type invocation struct {
	w      http.ResponseWriter
	r      *http.Request
	bucket string
	key    string
	q      url.Values
}

// handlers says which handler serves each operation.
func (s *Server) handlers() map[string]restroute.Handler {
	h := func(f func(c invocation) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return f(invocation{w, r, restroute.Param(r, "Bucket"), restroute.Wildcard(r), r.URL.Query()})
		}
	}
	// Bucket sub-resources that are one stored document, served by the generic
	// handlers: the operation names the document.
	docs := map[string]string{
		"BucketCors": "cors", "BucketLifecycleConfiguration": "lifecycle", "BucketWebsite": "website",
		"ObjectLockConfiguration": "object-lock", "BucketEncryption": "encryption",
		"BucketNotificationConfiguration": "notification", "BucketReplication": "replication",
		"BucketLogging": "logging", "BucketAccelerateConfiguration": "accelerate",
		"BucketRequestPayment": "requestPayment",
	}
	out := map[string]restroute.Handler{
		// service
		"ListBuckets":          h(func(c invocation) *awshttp.APIError { return s.listBuckets(c.w) }),
		"ListDirectoryBuckets": h(func(c invocation) *awshttp.APIError { return s.listBuckets(c.w) }),

		// bucket: sub-resources with a handler of their own
		"HeadBucket":                 h(func(c invocation) *awshttp.APIError { return s.headBucket(c.w, c.bucket) }),
		"CreateBucket":               h(func(c invocation) *awshttp.APIError { return s.createBucket(c.w, c.r, c.bucket) }),
		"DeleteBucket":               h(func(c invocation) *awshttp.APIError { return s.deleteBucket(c.w, c.bucket) }),
		"ListObjects":                h(func(c invocation) *awshttp.APIError { return s.listObjects(c.w, c.r, c.bucket, c.q) }),
		"ListObjectsV2":              h(func(c invocation) *awshttp.APIError { return s.listObjects(c.w, c.r, c.bucket, c.q) }),
		"ListObjectVersions":         h(func(c invocation) *awshttp.APIError { return s.listObjectVersions(c.w, c.bucket, c.q) }),
		"ListMultipartUploads":       h(func(c invocation) *awshttp.APIError { return s.listMultipartUploads(c.w, c.bucket, c.q) }),
		"GetBucketLocation":          h(func(c invocation) *awshttp.APIError { return s.getBucketLocation(c.w, c.bucket) }),
		"GetBucketVersioning":        h(func(c invocation) *awshttp.APIError { return s.getBucketVersioning(c.w, c.bucket) }),
		"PutBucketVersioning":        h(func(c invocation) *awshttp.APIError { return s.putBucketVersioning(c.w, c.r, c.bucket) }),
		"GetBucketTagging":           h(func(c invocation) *awshttp.APIError { return s.getBucketTagging(c.w, c.bucket) }),
		"PutBucketTagging":           h(func(c invocation) *awshttp.APIError { return s.putBucketTagging(c.w, c.r, c.bucket) }),
		"DeleteBucketTagging":        h(func(c invocation) *awshttp.APIError { return s.deleteBucketDoc(c.w, c.bucket, "tagging") }),
		"GetBucketPolicy":            h(func(c invocation) *awshttp.APIError { return s.getBucketPolicy(c.w, c.bucket) }),
		"PutBucketPolicy":            h(func(c invocation) *awshttp.APIError { return s.putBucketPolicy(c.w, c.r, c.bucket) }),
		"DeleteBucketPolicy":         h(func(c invocation) *awshttp.APIError { return s.deleteBucketDoc(c.w, c.bucket, "policy") }),
		"GetBucketPolicyStatus":      h(func(c invocation) *awshttp.APIError { return s.getBucketPolicyStatus(c.w, c.bucket) }),
		"GetBucketAcl":               h(func(c invocation) *awshttp.APIError { return s.getBucketACL(c.w, c.bucket) }),
		"PutBucketAcl":               h(func(c invocation) *awshttp.APIError { return s.putBucketACL(c.w, c.r, c.bucket) }),
		"GetPublicAccessBlock":       h(func(c invocation) *awshttp.APIError { return s.getBucketDoc(c.w, c.bucket, "publicAccessBlock") }),
		"PutPublicAccessBlock":       h(func(c invocation) *awshttp.APIError { return s.putPublicAccessBlock(c.w, c.r, c.bucket) }),
		"DeletePublicAccessBlock":    h(func(c invocation) *awshttp.APIError { return s.deleteBucketDoc(c.w, c.bucket, "publicAccessBlock") }),
		"GetBucketOwnershipControls": h(func(c invocation) *awshttp.APIError { return s.getBucketDoc(c.w, c.bucket, "ownershipControls") }),
		"PutBucketOwnershipControls": h(func(c invocation) *awshttp.APIError { return s.putBucketOwnershipControls(c.w, c.r, c.bucket) }),
		"DeleteBucketOwnershipControls": h(func(c invocation) *awshttp.APIError {
			return s.deleteBucketDoc(c.w, c.bucket, "ownershipControls")
		}),
		"DeleteObjects": h(func(c invocation) *awshttp.APIError { return s.deleteObjects(c.w, c.r, c.bucket) }),

		// object
		"GetObject":    h(func(c invocation) *awshttp.APIError { return s.getObject(c.w, c.r, c.bucket, c.key, c.q, false) }),
		"HeadObject":   h(func(c invocation) *awshttp.APIError { return s.getObject(c.w, c.r, c.bucket, c.key, c.q, true) }),
		"PutObject":    h(func(c invocation) *awshttp.APIError { return s.putObject(c.w, c.r, c.bucket, c.key) }),
		"CopyObject":   h(func(c invocation) *awshttp.APIError { return s.copyObject(c.w, c.r, c.bucket, c.key) }),
		"DeleteObject": h(func(c invocation) *awshttp.APIError { return s.deleteObject(c.w, c.r, c.bucket, c.key, c.q) }),

		"GetObjectTagging": h(func(c invocation) *awshttp.APIError {
			return s.getObjectTagging(c.w, c.bucket, c.key, c.q.Get("versionId"))
		}),
		"PutObjectTagging": h(func(c invocation) *awshttp.APIError {
			return s.putObjectTagging(c.w, c.r, c.bucket, c.key, c.q.Get("versionId"))
		}),
		"DeleteObjectTagging": h(func(c invocation) *awshttp.APIError {
			return s.deleteObjectTagging(c.w, c.bucket, c.key, c.q.Get("versionId"))
		}),
		"GetObjectAttributes": h(func(c invocation) *awshttp.APIError {
			return s.getObjectAttributes(c.w, c.r, c.bucket, c.key, c.q.Get("versionId"))
		}),
		"GetObjectRetention": h(func(c invocation) *awshttp.APIError {
			return s.getObjectRetention(c.w, c.bucket, c.key, c.q.Get("versionId"))
		}),
		"PutObjectRetention": h(func(c invocation) *awshttp.APIError {
			return s.putObjectRetention(c.w, c.r, c.bucket, c.key, c.q)
		}),
		"GetObjectLegalHold": h(func(c invocation) *awshttp.APIError {
			return s.getObjectLegalHold(c.w, c.bucket, c.key, c.q.Get("versionId"))
		}),
		"PutObjectLegalHold": h(func(c invocation) *awshttp.APIError {
			return s.putObjectLegalHold(c.w, c.r, c.bucket, c.key, c.q.Get("versionId"))
		}),
		"GetObjectAcl": h(func(c invocation) *awshttp.APIError { return s.getObjectACL(c.w, c.bucket, c.key) }),
		"PutObjectAcl": h(func(c invocation) *awshttp.APIError { return s.putObjectACL(c.w, c.r, c.bucket, c.key) }),

		"CreateMultipartUpload": h(func(c invocation) *awshttp.APIError { return s.createMultipartUpload(c.w, c.r, c.bucket, c.key) }),
		"UploadPart":            h(func(c invocation) *awshttp.APIError { return s.uploadPart(c.w, c.r, c.bucket, c.key, c.q) }),
		"UploadPartCopy":        h(func(c invocation) *awshttp.APIError { return s.uploadPartCopy(c.w, c.r, c.bucket, c.key, c.q) }),
		"CompleteMultipartUpload": h(func(c invocation) *awshttp.APIError {
			return s.completeMultipartUpload(c.w, c.r, c.bucket, c.key, c.q.Get("uploadId"))
		}),
		"AbortMultipartUpload": h(func(c invocation) *awshttp.APIError {
			return s.abortMultipartUpload(c.w, c.bucket, c.key, c.q.Get("uploadId"))
		}),
		"ListParts": h(func(c invocation) *awshttp.APIError { return s.listParts(c.w, c.bucket, c.key, c.q.Get("uploadId")) }),
	}
	for suffix, doc := range docs {
		doc := doc
		out["Get"+suffix] = h(func(c invocation) *awshttp.APIError { return s.getBucketDoc(c.w, c.bucket, doc) })
		out["Put"+suffix] = h(func(c invocation) *awshttp.APIError { return s.putBucketDoc(c.w, c.r, c.bucket, doc) })
	}
	// The deletes are named differently from their gets: DeleteBucketLifecycle,
	// not DeleteBucketLifecycleConfiguration.
	for op, doc := range map[string]string{
		"DeleteBucketCors": "cors", "DeleteBucketLifecycle": "lifecycle", "DeleteBucketWebsite": "website",
		"DeleteBucketEncryption": "encryption", "DeleteBucketReplication": "replication",
	} {
		doc := doc
		out[op] = h(func(c invocation) *awshttp.APIError { return s.deleteBucketDoc(c.w, c.bucket, doc) })
	}
	return out
}

// routed is the request as the router sees it. Virtual-hosted addressing puts
// the bucket in the Host, so the bucket joins the path and the router, the
// validator and the guard all read one shape; and "/bucket/" is how a bucket
// listing is spelled, not an object whose key is blank, so its slash goes.
func routed(r *http.Request, vhostBucket string) *http.Request {
	esc := r.URL.EscapedPath()
	if vhostBucket != "" {
		esc = "/" + vhostBucket + esc
	}
	if strings.Count(esc, "/") == 2 && strings.HasSuffix(esc, "/") && len(esc) > 2 {
		esc = strings.TrimSuffix(esc, "/")
	}
	if esc == r.URL.EscapedPath() {
		return r
	}
	r2 := new(http.Request)
	*r2 = *r
	u := *r.URL
	u.Path, _ = url.PathUnescape(esc)
	u.RawPath = esc
	r2.URL = &u
	return r2
}

// refuse logs and writes an error the way every S3 refusal is.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, aerr *awshttp.APIError) {
	bucket, key := target(r)
	s.logf("s3: %s /%s/%s -> %s", r.Method, bucket, key, aerr.Code)
	writeS3Error(w, aerr)
}

// target is the (bucket, key) a request addresses, read from its path: the
// router's labels exist only once a route has matched, and the checks that
// run on a request nothing matched need them too.
func target(r *http.Request) (bucket, key string) {
	b, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	bucket, _ = url.PathUnescape(b)
	key, _ = url.PathUnescape(rest)
	return bucket, key
}

// refuseUnmodelled refuses a sub-resource this build does not implement.
// Every method's default arm is a DIFFERENT operation — a PUT of ?analytics
// would otherwise create a bucket, a DELETE of one an object — so it is
// refused before a handler can be chosen by default.
func refuseUnmodelled(r *http.Request) *awshttp.APIError {
	bucket, key := target(r)
	switch {
	case bucket == "":
		return nil
	case key == "":
		return checkBucketSubresource(r.URL.Query())
	}
	return checkObjectSubresource(r.URL.Query())
}

// unmatched is what happens to a request no route serves: the guard and the
// sub-resource refusal still run first, as they always have, so a denied
// principal is told so and an unimplemented sub-resource is named — and only
// then does it get the router's 405.
func (s *Server) unmatched(w http.ResponseWriter, r *http.Request, e *awshttp.APIError) {
	bucket, key := target(r)
	if aerr := s.guardRequest(w, r, bucket, key); aerr != nil {
		s.refuse(w, r, aerr)
		return
	}
	if aerr := refuseUnmodelled(r); aerr != nil {
		s.refuse(w, r, aerr)
		return
	}
	s.refuse(w, r, e)
}

// unsupported is what the router answers when nothing it has serves the
// request: a 405 naming the level, as the hand dispatch did.
func unsupported(r *http.Request) *awshttp.APIError {
	level := "object-level request"
	switch strings.Count(strings.Trim(r.URL.EscapedPath(), "/"), "/") {
	case 0:
		level = "bucket-level request"
		if strings.Trim(r.URL.EscapedPath(), "/") == "" {
			return awshttp.Errf(405, "MethodNotAllowed", "unsupported service-level method %s", r.Method)
		}
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported %s", level)
}

func (s *Server) buildRouter() *restroute.Router {
	specs := append([]restroute.Route(nil), routeSpecs()...)
	h := s.handlers()
	for i := range specs {
		if specs[i].Handler == nil {
			specs[i].Handler = h[specs[i].Op]
		}
	}
	return restroute.Build(specs, restroute.Options{
		OnError:          s.refuse,
		Unmatched:        s.unmatched,
		NotFound:         unsupported,
		MethodNotAllowed: unsupported,
		// After matching, so the operation is known: validation, then the IAM
		// guard, then the refusal of sub-resources doze-aws does not implement.
		Use: []func(http.Handler) http.Handler{s.validateMiddleware, s.guardMiddleware, s.subresourceMiddleware},
	})
}

func (s *Server) validateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if aerr := validateRequest(r); aerr != nil {
			s.refuse(w, r, aerr)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) guardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket, key := target(r)
		if aerr := s.guardRequest(w, r, bucket, key); aerr != nil {
			s.refuse(w, r, aerr)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// subresourceMiddleware runs the sub-resource refusal on a matched request.
func (s *Server) subresourceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if aerr := refuseUnmodelled(r); aerr != nil {
			s.refuse(w, r, aerr)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// opsOnly names operations for callers with no Server — the console's wire
// page: the same routes with no handlers behind them.
var opsOnly = sync.OnceValue(func() *restroute.Router {
	return restroute.Build(routeSpecs(), restroute.Options{
		OnError:          func(http.ResponseWriter, *http.Request, *awshttp.APIError) {},
		NotFound:         unsupported,
		MethodNotAllowed: unsupported,
	})
})

// OperationFor reports the S3 operation a request addresses, or "" when no
// route matches. Exported for the console's traffic classifier. It reads the
// path-style address; a virtual-hosted request's bucket is in a Host this has
// no suffix to read.
func OperationFor(r *http.Request) string { return opsOnly().Op(routed(r, "")) }
