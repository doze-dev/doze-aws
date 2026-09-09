package secretsmanager

// The secret's resource policy (PutResourcePolicy), evaluated on
// secret-scoped requests under IAM soft or enforce.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// guardRequest runs the guard for a request that names a secret.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request, action string, p map[string]any) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	id := awsjson.Str(p, "SecretId")
	if id == "" {
		return s.guard.CheckIdentity(w, r, "secretsmanager:"+action, "")
	}
	sec, err := s.store.Get(id)
	if err != nil {
		// No secret, no policy: the identity verdict decides, and the
		// handler reports the missing secret.
		return s.guard.CheckIdentity(w, r, "secretsmanager:"+action, "")
	}
	var docs []*iampolicy.Document
	if sec.Policy != "" {
		if doc, err := iampolicy.Parse(sec.Policy); err == nil {
			docs = append(docs, doc)
		}
	}
	return s.guard.Check(w, r, docs, "secretsmanager:"+action, sec.ARN)
}
