package lambda

// The function's resource policy — what AddPermission wrote — evaluated on
// the request, under IAM soft or enforce. This is the gate that makes an S3
// notification or an SNS delivery need a permission before it can invoke,
// exactly as on AWS: those arrive as peer calls stamped with a service
// principal, and a service principal has no identity policy to fall back on.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// guardRequest runs the guard for a function-scoped request. Requests that
// name no function (ListFunctions, layers, mappings), or one that does not
// exist, carry no resource policy: the identity verdict alone decides.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	action, resource := iamguard.ResolveAction(r, "lambda")
	if action == "" {
		return nil
	}
	if resource == "" {
		return s.guard.CheckIdentity(w, r, action, "")
	}
	name := resource[strings.LastIndex(resource, ":")+1:]
	f, err := s.store.GetFunction(name)
	if err != nil {
		return s.guard.CheckIdentity(w, r, action, resource) // the handler reports the missing function
	}
	return s.guard.Check(w, r, functionPolicyDocs(f), action, resource)
}

// functionPolicyDocs parses a function's permission statements into the
// engine's shape. GetPolicy renders the same document to callers.
func functionPolicyDocs(f *Function) []*iampolicy.Document {
	if len(f.Policy) == 0 {
		return nil
	}
	raw, err := json.Marshal(policyDoc{Version: "2012-10-17", Id: "default", Statement: f.Policy})
	if err != nil {
		return nil
	}
	doc, err := iampolicy.Parse(string(raw))
	if err != nil {
		return nil
	}
	return []*iampolicy.Document{doc}
}
