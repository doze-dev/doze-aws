package iamguard

// The reauthorize path is the mechanism the last audit added to close three
// enforce-mode bypasses at once: the middleware resolves the wire loosely, so
// a service that knows the real (action, resource) asks IAM again rather than
// trusting a verdict reached for the wrong pair. Every test of it went
// through a whole stack, one service at a time; the mechanism itself was 0%
// covered here, including the guards on when it must NOT re-ask.
//
// Those guards are the security-relevant half. Re-asking for a service
// principal would hand a peer call an identity verdict it must never get —
// a peer carries no identity policy, and the resource policy is the only
// thing that may admit it.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// asked records what the reauthorizer was called with.
type asked struct {
	calls    int
	action   string
	resource string
}

func withAsk(r *http.Request, verdict string, rec *asked) *http.Request {
	return WithReauthorize(r, func(_ *http.Request, action, resource string) string {
		rec.calls++
		rec.action, rec.resource = action, resource
		return verdict
	})
}

const (
	aliasARN = "arn:aws:kms:us-east-1:000000000000:alias/app"
	keyARN   = "arn:aws:kms:us-east-1:000000000000:key/k-1"
)

// TestReauthorizeAsksOnADifferentPair: the middleware stamped the alias (all
// it could see on the wire); the service resolved the key the alias points
// at. The identity verdict must be re-taken for the key.
func TestReauthorizeAsksOnADifferentPair(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	r := request("enforce", alice, IdentityImplicitDeny, "")
	r.Header.Set(HeaderAction, "kms:Encrypt")
	r.Header.Set(HeaderResource, aliasARN)

	var rec asked
	r = withAsk(r, IdentityAllowed, &rec)

	w := httptest.NewRecorder()
	if err := (Guard{Mode: "enforce"}).Check(w, r, nil, "kms:Encrypt", keyARN); err != nil {
		t.Fatalf("the re-asked verdict allows: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("the reauthorizer was called %d times, want 1", rec.calls)
	}
	if rec.action != "kms:Encrypt" || rec.resource != keyARN {
		t.Errorf("asked for (%q, %q), want the pair the service resolved", rec.action, rec.resource)
	}
}

// TestReauthorizeNotAskedWhenThePairAgrees: the common case must not pay for
// a second evaluation, and a stamped verdict for the right pair is already
// correct.
func TestReauthorizeNotAskedWhenThePairAgrees(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	r := request("enforce", alice, IdentityAllowed, "")
	r.Header.Set(HeaderAction, "kms:Encrypt")
	r.Header.Set(HeaderResource, keyARN)

	var rec asked
	r = withAsk(r, IdentityImplicitDeny, &rec)

	if err := (Guard{Mode: "enforce"}).Check(httptest.NewRecorder(), r, nil, "kms:Encrypt", keyARN); err != nil {
		t.Fatalf("the stamped allow stands: %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("the reauthorizer must not be called when the pair matches, got %d calls", rec.calls)
	}
}

// TestReauthorizeIsNeverAskedForAServicePrincipal is the one that matters. A
// peer call carries a service principal and no identity policy; asking IAM
// for an identity verdict on its behalf could only ever widen what it may do,
// and the resource policy is the whole of its authorization.
func TestReauthorizeIsNeverAskedForAServicePrincipal(t *testing.T) {
	for _, identity := range []string{IdentityService, IdentityRoot} {
		t.Run(identity, func(t *testing.T) {
			r := request("enforce", "sns.amazonaws.com", identity, "")
			r.Header.Set(HeaderAction, "sqs:SendMessage")
			r.Header.Set(HeaderResource, "arn:aws:sqs:us-east-1:000000000000:other")

			var rec asked
			r = withAsk(r, IdentityAllowed, &rec)

			// No resource policy: a service principal has nothing admitting it.
			err := (Guard{Mode: "enforce"}).Check(httptest.NewRecorder(), r, nil, "sqs:SendMessage", resource)
			if rec.calls != 0 {
				t.Errorf("%s must never be re-asked, got %d calls", identity, rec.calls)
			}
			if identity == IdentityService && err == nil {
				t.Errorf("a service principal with no resource policy must be denied")
			}
			if identity == IdentityRoot && err != nil {
				t.Errorf("root is not gated by a policy that does not name it: %v", err)
			}
		})
	}
}

// TestReauthorizeAbsentLeavesTheStampedVerdict: a service that resolves its
// own pair but runs without the middleware (a direct peer call) still gets a
// decision rather than a panic.
func TestReauthorizeAbsentLeavesTheStampedVerdict(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	r := request("enforce", alice, IdentityImplicitDeny, "")
	r.Header.Set(HeaderAction, "kms:Encrypt")
	r.Header.Set(HeaderResource, aliasARN)

	// No WithReauthorize on the context.
	if err := (Guard{Mode: "enforce"}).Check(httptest.NewRecorder(), r, nil, "kms:Encrypt", keyARN); err == nil {
		t.Fatal("with nothing to re-ask, the stamped implicit deny stands and denies")
	}
}

// TestReauthorizeExplicitDenyStillDenies: re-asking may not launder an
// explicit Deny into an allow. The re-asked verdict is the identity half, and
// an explicit deny on either half denies.
func TestReauthorizeExplicitDenyStillDenies(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	r := request("enforce", alice, IdentityAllowed, "")
	r.Header.Set(HeaderAction, "kms:Encrypt")
	r.Header.Set(HeaderResource, aliasARN)

	var rec asked
	r = withAsk(r, IdentityExplicitDeny, &rec)

	// A resource policy that allows: the identity's explicit deny must win.
	allow := docs(t, `{"Effect":"Allow","Principal":{"AWS":"`+alice+`"},"Action":"kms:*","Resource":"*"}`)
	err := (Guard{Mode: "enforce"}).Check(httptest.NewRecorder(), r, allow, "kms:Encrypt", keyARN)
	if rec.calls != 1 {
		t.Fatalf("reauthorizer calls = %d", rec.calls)
	}
	if err == nil {
		t.Error("an explicit deny from the re-asked identity verdict must deny even against a resource allow")
	}
}

// TestStampAndStripRoundTrip: what the middleware writes is what Check reads,
// and a client cannot pre-set any of it.
func TestStampAndStripRoundTrip(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	// A client claiming to be root, already allowed, for a different pair.
	r.Header.Set(HeaderMode, "off")
	r.Header.Set(HeaderPrincipal, awsident.GlobalARN("iam", "root"))
	r.Header.Set(HeaderIdentity, IdentityAllowed)
	r.Header.Set(HeaderAction, "sqs:*")
	r.Header.Set(HeaderResource, "*")
	r.Header.Set("x-doze-source-arn", "arn:aws:s3:::anything")

	Strip(r)
	for name := range r.Header {
		if len(name) >= 7 && (name[:7] == "X-Doze-" || name[:7] == "x-doze-") {
			t.Fatalf("Strip left %q behind — a client could name its own principal", name)
		}
	}

	Stamp(r, "enforce", alice, IdentityImplicitDeny, "sqs:SendMessage", resource)
	if r.Header.Get(HeaderMode) != "enforce" || r.Header.Get(HeaderPrincipal) != alice ||
		r.Header.Get(HeaderIdentity) != IdentityImplicitDeny ||
		r.Header.Get(HeaderAction) != "sqs:SendMessage" || r.Header.Get(HeaderResource) != resource {
		t.Fatalf("Stamp did not write the handoff: %+v", r.Header)
	}

	// And the stamped implicit deny is what decides, not the client's claim.
	w := httptest.NewRecorder()
	if err := (Guard{}).Check(w, r, nil, "sqs:SendMessage", resource); err == nil {
		t.Error("the stamped implicit deny must deny, whatever the client asked for")
	}
	if got := w.Header().Get(HeaderDecision); got != iampolicy.ImplicitDeny.String() {
		t.Errorf("decision header = %q", got)
	}
}
