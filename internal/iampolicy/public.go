package iampolicy

// Is a resource policy public?
//
// This is the question behind GetBucketPolicyStatus and behind
// BlockPublicPolicy's refusal, and it is not the same question Evaluate
// answers. Evaluate decides one caller's one request. This decides whether
// ANY caller outside the account could be granted anything at all — a
// property of the document, with no request to evaluate against.
//
// It lives here rather than in the S3 package because it needs the engine's
// own principal, action and resource matching. The first version had its own
// statement walker in s3/handlers_access.go, which is how it came to miss
// two things the engine already handles: a Statement that is one object
// rather than a list, and an explicit Deny that cancels the Allow above it.
//
// Two deliberate asymmetries, both erring toward "public":
//
//   - A Deny cancels only when it is unconditional and names everyone. A
//     conditional Deny may not fire for the caller who matters, and a Deny
//     aimed at one principal does not stop the rest of the world.
//   - A Deny using NotAction, NotResource or NotPrincipal is not weighed.
//     Deciding whether one negated pattern covers another is a different and
//     much harder problem, and getting it wrong in the permissive direction
//     would let a public policy through BlockPublicPolicy.
//
// Erring toward "public" means doze-aws may refuse a bucket policy AWS would
// have accepted. That is the safe direction for a setting whose whole job is
// catching the mistake of exposing a bucket.

import "strings"

// IsPublic reports whether the document grants a principal outside the
// account any access at all.
func IsPublic(d *Document) bool {
	if d == nil {
		return false
	}
	for i := range d.Statement {
		st := &d.Statement[i]
		if !strings.EqualFold(st.Effect, "Allow") || !grantsEveryone(st) {
			continue
		}
		if conditionNarrows(st.Condition) {
			continue
		}
		for _, action := range grantActions(st) {
			for _, resource := range grantResources(st) {
				if !cancelledByDeny(d, action, resource) {
					return true
				}
			}
		}
	}
	return false
}

// grantsEveryone: Principal "*" or {"AWS":"*"}, or a NotPrincipal Allow,
// which grants everyone it does not name.
func grantsEveryone(st *Statement) bool {
	return st.Principal.Any || st.NotPrincipal.Present
}

// grantActions and grantResources are the patterns a statement grants over.
// A negated list means "everything except", which is broader than anything
// it lists, so it probes as "*".
func grantActions(st *Statement) []string {
	if len(st.Action) > 0 {
		return st.Action
	}
	if len(st.NotAction) > 0 {
		return []string{"*"}
	}
	return nil
}

func grantResources(st *Statement) []string {
	if len(st.Resource) > 0 {
		return st.Resource
	}
	if len(st.NotResource) > 0 {
		return []string{"*"}
	}
	// A resource policy with no Resource applies to the resource it hangs
	// on — the queue or topic shape, where the member is optional.
	return []string{"*"}
}

// cancelledByDeny reports whether an unconditional Deny to everyone covers
// the granted (action, resource). The Allow's own patterns are the values
// probed, so a Deny is only cancelling when it is at least as broad: Deny
// "s3:*" cancels Allow "s3:GetObject", and Deny "s3:GetObject" does not
// cancel Allow "s3:*".
func cancelledByDeny(d *Document, action, resource string) bool {
	for i := range d.Statement {
		st := &d.Statement[i]
		switch {
		case !strings.EqualFold(st.Effect, "Deny"),
			len(st.Condition) > 0,
			st.NotPrincipal.Present, len(st.NotAction) > 0, len(st.NotResource) > 0,
			!st.Principal.Any,
			len(st.Action) == 0, len(st.Resource) == 0:
			continue
		}
		if coversAny(st.Action, action) && coversAny(st.Resource, resource) {
			return true
		}
	}
	return false
}

func coversAny(patterns []string, value string) bool {
	for _, p := range patterns {
		if globMatch(p, value) {
			return true
		}
	}
	return false
}

// narrowingKeys are the condition keys AWS treats as making a "*" grant
// non-public: the caller's network, the calling resource, or the account or
// organisation it belongs to. A condition on anything else — a date,
// aws:SecureTransport, s3:prefix — restricts WHEN the grant applies without
// restricting WHO it applies to, so the statement stays public.
var narrowingKeys = map[string]bool{
	"aws:sourceip": true, "aws:sourcevpc": true, "aws:sourcevpce": true,
	"aws:sourcearn": true, "aws:sourceaccount": true, "aws:sourceowner": true,
	"aws:principalarn": true, "aws:principalaccount": true,
	"aws:principalorgid": true, "aws:principalorgpaths": true, "aws:userid": true,
	"s3:dataaccesspointarn": true, "s3:dataaccesspointaccount": true,
	"s3:accesspointnetworkorigin": true, "aws:vpcsourceip": true,
}

// conditionNarrows reports whether a condition limits who the grant reaches.
func conditionNarrows(cond conditionBlock) bool {
	for op, keys := range cond {
		// A negated operator widens rather than narrows: "not from this IP"
		// still reaches everyone else.
		if isNegated(op) {
			continue
		}
		for key := range keys {
			if narrowingKeys[strings.ToLower(key)] {
				return true
			}
		}
	}
	return false
}
