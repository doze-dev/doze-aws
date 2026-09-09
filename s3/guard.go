package s3

// The bucket policy, evaluated on bucket and object requests under IAM soft
// or enforce. The public access block already refuses a public policy from
// being written; this is where a policy that was written decides who may
// read and write.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// guardRequest runs the guard for a request that names a bucket.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request, bucket, key string) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	action, resource := iamguard.ResolveS3(r, bucket, key)
	if action == "" {
		return nil
	}
	var docs []*iampolicy.Document
	b, err := s.store.GetBucket(bucket)
	if bucket == "" || err != nil {
		// No bucket, no policy: the identity verdict decides, and the
		// handler reports a missing bucket.
		if aerr := s.guard.CheckIdentity(w, r, action, resource); aerr != nil {
			return awshttp.Errf(403, "AccessDenied", "%s", aerr.Message)
		}
		return nil
	}
	if b.Policy != "" {
		if doc, err := iampolicy.Parse(b.Policy); err == nil {
			docs = append(docs, doc)
		}
	}
	if aerr := s.guard.Check(w, r, docs, action, resource); aerr != nil {
		// S3 spells the refusal its own way.
		return awshttp.Errf(403, "AccessDenied", "%s", aerr.Message)
	}
	return nil
}
