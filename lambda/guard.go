package lambda

// The function's resource policy — what AddPermission wrote — evaluated on
// the request, under IAM soft or enforce. This is the gate that makes an S3
// notification or an SNS delivery need a permission before it can invoke,
// exactly as on AWS: those arrive as peer calls stamped with a service
// principal, and a service principal has no identity policy to fall back on.

import (
	"encoding/json"
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// resolveIAM is the permission a request is authorized as and the function it names,
// as the stack's authorization middleware and this service's own guard both
// ask it. The operation is the one the router matches, and the action is the one
// AWS's model gives it — Invoke is lambda:InvokeFunction, AddPermission is
// lambda:AddPermission — never a guess from the path. A request that names a
// function is evaluated against its ARN; one that does not (ListFunctions,
// layers, mappings) against none, so only an identity policy can grant it.
// An operation with no action (doze's own runtime probe) is not evaluated.
func (s *Server) resolveIAM(r *http.Request) (action, resource string) {
	op, labels := opsOnly().Match(r)
	action = iamActions()[op]
	if action == "" {
		return "", ""
	}
	if name := labels["FunctionName"]; name != "" {
		resource = iamguard.LambdaFunctionARN(s.id, name)
	}
	return action, resource
}

// guardRequest runs the guard for a function-scoped request. Requests that
// name no function (ListFunctions, layers, mappings), or one that does not
// exist, carry no resource policy: the identity verdict alone decides.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	action, resource := s.resolveIAM(r)
	if action == "" {
		return nil
	}
	if resource == "" {
		return s.guard.CheckIdentity(w, r, action, "")
	}
	// The reference may carry a qualifier (fn:live) or be an ARN; the
	// function is looked up by its bare name, the policy evaluated for the
	// ARN as referenced.
	f, err := s.store.GetFunction(iamguard.LambdaFunctionName(resource))
	if err != nil {
		return s.guard.CheckIdentity(w, r, action, resource) // the handler reports the missing function
	}
	return s.guard.Check(w, r, functionPolicyDocs(f), action, resource)
}

// functionPolicyDocs parses a function's permission statements into the
// engine's shape. GetPolicy renders the same document to callers.
func functionPolicyDocs(f *function) []*iampolicy.Document {
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
