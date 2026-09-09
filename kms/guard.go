package kms

// The key policy, evaluated on key-scoped requests under IAM soft or enforce.
// KMS is the one service where the resource policy gates the identity
// policies: an identity policy grants only when the key policy lets the
// account in ("Enable IAM policies", the default policy's one statement),
// so a key policy that names nobody locks everyone out, as on AWS.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// guardRequest runs the guard for a request that names a key.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request, action string, p map[string]any) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	ident := awsjson.Str(p, "KeyId")
	if ident == "" || action == "CreateKey" {
		// No key yet, no key policy to open the gate: the identity verdict
		// alone decides, as it does for CreateKey on AWS.
		return s.guard.CheckIdentity(w, r, "kms:"+action, "")
	}
	k, err := s.store.Resolve(ident)
	if err != nil {
		return s.guard.CheckIdentity(w, r, "kms:"+action, "") // the handler reports the missing key
	}
	policy := k.Policy
	if policy == "" {
		policy = defaultKeyPolicy
	}
	var docs []*iampolicy.Document
	if doc, err := iampolicy.Parse(policy); err == nil {
		docs = append(docs, doc)
	}
	return s.guard.Check(w, r, docs, "kms:"+action, k.ARN())
}
