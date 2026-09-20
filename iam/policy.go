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

// evaluate applies the AWS evaluation order across every supplied document.
func evaluate(docs []*document, req request) (Decision, string) {
	return iampolicy.Evaluate(docs, req)
}
