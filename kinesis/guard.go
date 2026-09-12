package kinesis

// The stream's resource policy (PutResourcePolicy), evaluated on
// stream-scoped requests under IAM soft or enforce. A Logs subscription
// filter writes as logs.amazonaws.com, so a stream a filter targets needs a
// policy admitting it under enforce, as on AWS.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// guardRequest runs the guard for a request that names a stream.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request, action string, p map[string]any) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	name := awsjson.Str(p, "StreamName")
	if arn := awsjson.Str(p, "StreamARN"); name == "" && arn != "" {
		name, _ = streamFromARN(arn)
	}
	if arn := awsjson.Str(p, "ResourceARN"); name == "" && arn != "" {
		name, _ = streamFromARN(arn)
	}
	if name == "" {
		return s.guard.CheckIdentity(w, r, "kinesis:"+action, "")
	}
	st, err := s.store.Get(name)
	if err != nil || action == "CreateStream" {
		// No stream, no policy: the identity verdict decides, and the
		// handler reports a missing stream.
		return s.guard.CheckIdentity(w, r, "kinesis:"+action, streamARN(s.id, name))
	}
	var docs []*iampolicy.Document
	if st.ResourcePolicy != "" {
		if doc, err := iampolicy.Parse(st.ResourcePolicy); err == nil {
			docs = append(docs, doc)
		}
	}
	return s.guard.Check(w, r, docs, "kinesis:"+action, streamARN(s.id, name))
}
