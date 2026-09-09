package iam

// The policy engine lives in internal/iampolicy, shared with every service
// that evaluates a resource policy of its own. These aliases keep the IAM
// service's own vocabulary — and its public API — unchanged.

import "github.com/doze-dev/doze-aws/internal/iampolicy"

// Document is a parsed IAM policy document.
type Document = iampolicy.Document

// Statement is one policy statement.
type Statement = iampolicy.Statement

// Request is one authorization question.
type Request = iampolicy.Request

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

// ParsePolicy parses and lightly validates a policy document.
func ParsePolicy(raw string) (*Document, error) { return iampolicy.Parse(raw) }

// Evaluate applies the AWS evaluation order across every supplied document.
func Evaluate(docs []*Document, req Request) (Decision, string) { return iampolicy.Evaluate(docs, req) }
