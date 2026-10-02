package iam

// The policy engine lives in internal/iampolicy, shared with every service
// that evaluates a resource policy of its own. These aliases keep the IAM
// service's own vocabulary while the engine itself stays internal.
//
// Decision is the one that stays exported, because it is genuinely part of
// the contract: the middleware in dozeaws.go re-authorizes through
// ParseDecision and RecordResource, and anything holding a Decision needs
// the three constants below to compare it against.

import "github.com/doze-dev/doze-aws/internal/iampolicy"

// document is a parsed IAM policy document.
type document = iampolicy.Document

// request is one authorization question.
type request = iampolicy.Request

// Decision is the outcome of evaluating a request against a policy set.
type Decision = iampolicy.Decision

const (
	// ImplicitDeny is the default: nothing granted the action.
	ImplicitDeny = iampolicy.ImplicitDeny
	// Allowed means at least one statement allowed it and none denied it.
	Allowed = iampolicy.Allowed
	// ExplicitDeny means a Deny statement matched. It can never be overridden.
	ExplicitDeny = iampolicy.ExplicitDeny
)

// parsePolicy parses and lightly validates a policy document.
func parsePolicy(raw string) (*document, error) { return iampolicy.Parse(raw) }

// A policy document is one of two shapes, and IAM refuses each in the other's
// place. A trust policy says WHO may assume a role: every statement names a
// principal and none names a resource. An identity policy says WHAT its holder
// may do: every statement names a resource and none names a principal.
//
// parsePolicy alone checks neither, so a permissions document was accepted as
// a role's trust policy — a role nobody can assume, created without complaint
// — and a trust document was accepted as an inline policy that grants nothing.
// Both are mistakes a deploy would have caught at once.

// parseTrustPolicy parses an AssumeRolePolicyDocument. field is the request
// member, for the error a document that does not parse at all gets.
func parseTrustPolicy(field, raw string) (*document, *apiError) {
	doc, err := parsePolicy(raw)
	if err != nil {
		return nil, errMalformedPolicy("%s: %v", field, err)
	}
	for _, st := range doc.Statement {
		if len(st.Resource) > 0 || len(st.NotResource) > 0 {
			return nil, errMalformedPolicy("Has prohibited field Resource")
		}
		if !st.Principal.Present && !st.NotPrincipal.Present {
			return nil, errMalformedPolicy("Missing required field Principal")
		}
	}
	return doc, nil
}

// parseIdentityPolicy parses a managed or inline policy document.
func parseIdentityPolicy(raw string) (*document, *apiError) {
	doc, err := parsePolicy(raw)
	if err != nil {
		return nil, errMalformedPolicy("PolicyDocument: %v", err)
	}
	for _, st := range doc.Statement {
		if st.Principal.Present || st.NotPrincipal.Present {
			return nil, errMalformedPolicy("Policy document should not specify a principal.")
		}
		if len(st.Resource) == 0 && len(st.NotResource) == 0 {
			return nil, errMalformedPolicy("Policy statement must contain resources.")
		}
	}
	return doc, nil
}

// evaluate applies the AWS evaluation order across every supplied document.
func evaluate(docs []*document, req request) (Decision, string) {
	return iampolicy.Evaluate(docs, req)
}
