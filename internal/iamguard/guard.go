// Package iamguard is the resource-policy half of IAM: what a service runs
// on its own requests so a bucket policy, queue policy, topic policy, function
// permission, key policy or resource policy is evaluated where the resource
// lives.
//
// The gateway middleware (iam.Server.Authorize) evaluates identity policies
// and cannot evaluate resource policies — it never sees a peer call, it
// resolves resources loosely, and it can only render one error shape. So the
// two halves meet through request headers, a doze extension:
//
//	X-Doze-Iam-Mode      soft | enforce; absent means off
//	X-Doze-Principal     the caller: a user or role ARN, the account root, or
//	                     a service principal such as s3.amazonaws.com
//	X-Doze-Identity      allowed | implicitDeny | root | service
//	X-Doze-Source-Arn    the resource a service call is made on behalf of
//
// The middleware strips whatever a client sent under X-Doze-* and stamps its
// own; a peer call (peers.WithPrincipal) stamps the service principal. The
// service combines the identity verdict with its resource policy by AWS's
// same-account rule: an explicit Deny anywhere denies; an Allow in either
// grants; otherwise the request is denied. A service principal has no
// identity policy, so only the resource policy can admit it — which is what
// makes an S3 notification need a Lambda permission under enforce, exactly
// as on AWS. The verdict goes back on X-Doze-Resource-Decision so the
// recorder can keep it.

package iamguard

import (
	"context"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// Header names of the handoff.
const (
	HeaderMode      = "X-Doze-Iam-Mode"
	HeaderPrincipal = "X-Doze-Principal"
	HeaderIdentity  = "X-Doze-Identity"
	HeaderSourceARN = "X-Doze-Source-Arn"
	HeaderDecision  = "X-Doze-Resource-Decision"
	HeaderMatchedBy = "X-Doze-Resource-Matched-By"
	// HeaderAction and HeaderResource say what the identity verdict was
	// evaluated for. A service whose own resolution differs — a key named
	// by alias, an S3 bucket in the host, a copy's source object — asks
	// for the verdict again on the pair it means.
	HeaderAction   = "X-Doze-Action"
	HeaderResource = "X-Doze-Resource"
)

// Identity verdicts the middleware stamps.
const (
	IdentityAllowed      = "allowed"
	IdentityImplicitDeny = "implicitDeny"
	IdentityExplicitDeny = "explicitDeny" // stamped under soft, where the middleware does not refuse
	IdentityRoot         = "root"         // no IAM identity behind the call: the account root
	IdentityService      = "service"      // a peer call by a service principal
)

// Reauthorize answers the identity verdict for the request's principal on
// an (action, resource) the middleware did not evaluate. The middleware
// attaches one to the request context (WithReauthorize); a guard whose
// resolution differs from the stamped pair calls it through Check.
type Reauthorize func(r *http.Request, action, resource string) string

type reauthKey struct{}

// WithReauthorize returns the request with a Reauthorize on its context.
func WithReauthorize(r *http.Request, fn Reauthorize) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), reauthKey{}, fn))
}

func reauthorizer(r *http.Request) Reauthorize {
	fn, _ := r.Context().Value(reauthKey{}).(Reauthorize)
	return fn
}

// Strip removes every X-Doze-* header a client sent, so a caller cannot
// claim a principal or a verdict. The middleware calls it before stamping.
func Strip(r *http.Request) {
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-doze-") {
			r.Header.Del(name)
		}
	}
}

// Stamp writes the middleware's handoff onto the request: the mode, the
// principal, the identity verdict, and the pair it was evaluated for.
func Stamp(r *http.Request, mode, principal, identity, action, resource string) {
	r.Header.Set(HeaderMode, mode)
	r.Header.Set(HeaderPrincipal, principal)
	r.Header.Set(HeaderIdentity, identity)
	r.Header.Set(HeaderAction, action)
	r.Header.Set(HeaderResource, resource)
}

// Guard is one service's resource-policy check.
type Guard struct {
	// Mode is the service's own IAM mode, for peer calls that arrive without
	// the middleware's headers. "" or "off" disables the guard.
	Mode string
	// Logf receives the soft-mode lines.
	Logf func(format string, args ...any)
	// KeyPolicyGates is KMS's rule: an identity policy counts only when the
	// key policy lets the account in (an Allow for the account root), so a
	// key policy that names nobody locks everyone out, as on AWS.
	KeyPolicyGates bool
}

// CheckIdentity settles a request that has no resource policy to consult: it
// names no resource, or one that does not exist yet (Create*) or at all. The
// middleware left the identity verdict for the service to apply, so an
// identity that nothing grants is refused here, and every other identity
// passes — KMS's gate does not apply, there being no key policy to open it.
func (g Guard) CheckIdentity(w http.ResponseWriter, r *http.Request, action, resource string) *awshttp.APIError {
	g.KeyPolicyGates = false
	return g.Check(w, r, nil, action, resource)
}

// Check evaluates a resource policy for the request. docs are the resource's
// policy documents (nil when it has none); action and resource are what the
// request exercises. It returns the error to answer with under enforce, and
// nil when the request may proceed; under soft a denial is logged and nil is
// returned. The verdict is written to the response header when w is set.
func (g Guard) Check(w http.ResponseWriter, r *http.Request, docs []*iampolicy.Document, action, resource string) *awshttp.APIError {
	mode := r.Header.Get(HeaderMode)
	if mode == "" {
		mode = g.Mode
	}
	if mode == "" || mode == "off" {
		return nil
	}
	principal := r.Header.Get(HeaderPrincipal)
	identity := r.Header.Get(HeaderIdentity)
	if principal == "" {
		principal = awsident.GlobalARN("iam", "root")
	}
	if identity == "" {
		if strings.HasSuffix(principal, ".amazonaws.com") {
			identity = IdentityService
		} else {
			identity = IdentityRoot
		}
	}
	// The middleware's verdict is for the pair it resolved from the wire;
	// when this service means a different one, the verdict is asked for
	// again on that pair rather than trusted for the wrong resource.
	if identity != IdentityService && identity != IdentityRoot {
		if r.Header.Get(HeaderAction) != action || r.Header.Get(HeaderResource) != resource {
			if ask := reauthorizer(r); ask != nil {
				identity = ask(r, action, resource)
			}
		}
	}
	ctx := map[string][]string{
		"aws:PrincipalArn":     {principal},
		"aws:PrincipalAccount": {awsident.AccountID},
		"aws:SourceAccount":    {awsident.AccountID},
	}
	if src := r.Header.Get(HeaderSourceARN); src != "" {
		ctx["aws:SourceArn"] = []string{src} // condition keys fold case
	}
	rdec, by := iampolicy.Evaluate(docs, iampolicy.Request{Action: action, Resource: resource, Context: ctx, Principal: principal})

	decision := combine(rdec, identity, g.KeyPolicyGates && !accountAdmitted(docs, action, resource, ctx))
	if w != nil {
		w.Header().Set(HeaderDecision, decision.String())
		if by != "" && rdec == decision {
			w.Header().Set(HeaderMatchedBy, by)
		}
	}
	if decision == iampolicy.Allowed {
		return nil
	}
	reason := "the resource policy does not allow it"
	if rdec == iampolicy.ExplicitDeny {
		reason = "the resource policy denies it (" + by + ")"
	} else if identity == IdentityExplicitDeny {
		reason = "the identity policies deny it"
	} else if identity == IdentityImplicitDeny {
		reason = "neither the identity policies nor the resource policy allow it"
	} else if identity == IdentityService {
		reason = "the resource policy does not allow " + principal
	}
	if mode == "soft" {
		if g.Logf != nil {
			g.Logf("iam[soft]: would deny %s on %s for %s: %s", action, orDash(resource), principal, reason)
		}
		return nil
	}
	return awshttp.Errf(403, "AccessDeniedException",
		"User: %s is not authorized to perform: %s on resource: %s because %s", principal, action, orDash(resource), reason)
}

// combine applies the same-account rule to a resource verdict and the
// identity verdict the middleware stamped.
func combine(resource iampolicy.Decision, identity string, keyGateClosed bool) iampolicy.Decision {
	if resource == iampolicy.ExplicitDeny || identity == IdentityExplicitDeny {
		return iampolicy.ExplicitDeny
	}
	if resource == iampolicy.Allowed {
		return iampolicy.Allowed
	}
	switch identity {
	case IdentityRoot:
		if keyGateClosed {
			return iampolicy.ImplicitDeny
		}
		return iampolicy.Allowed
	case IdentityAllowed:
		if keyGateClosed {
			return iampolicy.ImplicitDeny
		}
		return iampolicy.Allowed
	}
	return iampolicy.ImplicitDeny
}

// accountAdmitted reports whether the key policy allows the account root
// the action on the key, which is what the default KMS key policy's one
// clause does ("Enable IAM policies" — kms:* for the root principal on
// "*") and what a policy naming the key's own ARN, or conditioning on the
// request, does as well. Evaluated as the root would be: the same resource
// and context the request carries.
func accountAdmitted(docs []*iampolicy.Document, action, resource string, ctx map[string][]string) bool {
	root := awsident.GlobalARN("iam", "root")
	rootCtx := map[string][]string{}
	for k, v := range ctx {
		rootCtx[k] = v
	}
	rootCtx["aws:PrincipalArn"] = []string{root}
	dec, _ := iampolicy.Evaluate(docs, iampolicy.Request{Action: action, Resource: resource, Principal: root, Context: rootCtx})
	return dec == iampolicy.Allowed
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
