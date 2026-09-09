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

// guardRequest runs the guard for a request that names a bucket. A copy
// (PutObject or UploadPart with x-amz-copy-source) reads its source too, so
// the source is authorized for s3:GetObject against its own bucket's policy
// before the destination is, as on AWS.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request, bucket, key string) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	action, resource := iamguard.ResolveS3(r, bucket, key)
	if action == "" {
		return nil
	}
	if src := r.Header.Get("x-amz-copy-source"); src != "" && (action == "s3:PutObject" || action == "s3:UploadPart") {
		srcBucket, srcKey, _, aerr := parseCopySource(src)
		if aerr == nil && srcBucket != "" && srcKey != "" {
			if aerr := s.guardOne(w, r, srcBucket, "s3:GetObject", "arn:aws:s3:::"+srcBucket+"/"+srcKey); aerr != nil {
				return aerr
			}
		}
	}
	return s.guardOne(w, r, bucket, action, resource)
}

// guardOne checks one action on one bucket's policy.
func (s *Server) guardOne(w http.ResponseWriter, r *http.Request, bucket, action, resource string) *awshttp.APIError {
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
