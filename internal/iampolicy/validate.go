package iampolicy

// Is this text a resource policy that could mean something?
//
// A resource policy is stored by SQS, SNS, KMS, Secrets Manager, Kinesis, S3
// and API Gateway, and evaluated on every request to the resource. Five of
// them used to store whatever text they were given. The guard then parsed it,
// failed, and carried on with no policy, so a Deny that was mistyped was
// silently inert and a grant that was mistyped silently granted nothing:
// "not json" was stored, read back, and ignored.
//
// Parse is deliberately shallow and cannot be tightened for everyone: it also
// reads identity policies and trust policies, which have different required
// elements. A resource policy has one more rule than either, every statement
// names who it is about, so this is the check a service runs before it stores
// one. It reports a Problem, not an error, so the service says it with its own
// code and its own words (SQS answers InvalidAttributeValue, KMS
// MalformedPolicyDocumentException, API Gateway BadRequestException), because
// the code an SDK branches on is the service's, not this package's.

import "fmt"

// ProblemKind names what is wrong with a stored resource policy.
type ProblemKind string

const (
	NotAPolicy  ProblemKind = "NotAPolicy"  // empty, or not JSON, or not an object
	NoStatement ProblemKind = "NoStatement" // no Statement
	BadVersion  ProblemKind = "BadVersion"  // a Version other than the two IAM accepts
	BadEffect   ProblemKind = "BadEffect"   // Effect is not Allow or Deny
	NoAction    ProblemKind = "NoAction"    // neither Action nor NotAction
	NoPrincipal ProblemKind = "NoPrincipal" // neither Principal nor NotPrincipal
	NoResource  ProblemKind = "NoResource"  // neither Resource nor NotResource
	BothOfAPair ProblemKind = "BothOfAPair" // Action with NotAction, Principal with NotPrincipal, Resource with NotResource
)

// Problem is the first thing wrong with a resource policy.
type Problem struct {
	Kind ProblemKind
	// Statement is the zero-based index of the statement at fault, or -1 when
	// the problem is with the document as a whole.
	Statement int
	// Detail is a sentence a caller can be shown, in AWS's wording where AWS
	// words it and plainly where it does not.
	Detail string
}

func (p *Problem) Error() string { return p.Detail }

// ValidResourcePolicyVersions are the only Version values IAM accepts.
// Source: the IAM User Guide, "IAM JSON policy elements: Version".
var validVersions = map[string]bool{"2012-10-17": true, "2008-10-17": true}

// ValidateResourcePolicy checks raw as a resource policy and returns the first
// problem, or nil. It is what a service calls before it stores a policy.
//
// It does not know the service's own rules (a key policy's Resource must be
// "*", a bucket policy must name its own bucket); those are the service's, and
// run after this.
func ValidateResourcePolicy(raw string) *Problem {
	d, p := parse(raw)
	if p != nil {
		return p
	}
	if d.Version != "" && !validVersions[d.Version] {
		return &Problem{Kind: BadVersion, Statement: -1,
			Detail: fmt.Sprintf("The policy must contain a valid version string: %q is not one of 2012-10-17, 2008-10-17", d.Version)}
	}
	for i, st := range d.Statement {
		if len(st.Action) > 0 && len(st.NotAction) > 0 ||
			len(st.Resource) > 0 && len(st.NotResource) > 0 ||
			st.Principal.Present && st.NotPrincipal.Present {
			return &Problem{Kind: BothOfAPair, Statement: i,
				Detail: fmt.Sprintf("statement %d: Action, Resource and Principal may not be given together with their Not counterparts", i)}
		}
		if !st.Principal.Present && !st.NotPrincipal.Present {
			return &Problem{Kind: NoPrincipal, Statement: i,
				Detail: fmt.Sprintf("statement %d: a resource policy statement must name a Principal", i)}
		}
		if len(st.Resource) == 0 && len(st.NotResource) == 0 {
			return &Problem{Kind: NoResource, Statement: i,
				Detail: fmt.Sprintf("statement %d: a resource policy statement must name a Resource", i)}
		}
	}
	return nil
}
