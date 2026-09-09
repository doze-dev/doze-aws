package iamguard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

const resource = "arn:aws:sqs:us-east-1:000000000000:q"

func docs(t *testing.T, statements string) []*iampolicy.Document {
	t.Helper()
	if statements == "" {
		return nil
	}
	d, err := iampolicy.Parse(`{"Version":"2012-10-17","Statement":[` + statements + `]}`)
	if err != nil {
		t.Fatal(err)
	}
	return []*iampolicy.Document{d}
}

func request(mode, principal, identity, source string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if mode != "" {
		r.Header.Set(HeaderMode, mode)
	}
	if principal != "" {
		r.Header.Set(HeaderPrincipal, principal)
	}
	if identity != "" {
		r.Header.Set(HeaderIdentity, identity)
	}
	if source != "" {
		r.Header.Set(HeaderSourceARN, source)
	}
	return r
}

// TestSameAccountRule is the combination table: the resource verdict and the
// identity verdict, with and without KMS's gate.
func TestSameAccountRule(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	allowAlice := `{"Effect":"Allow","Principal":{"AWS":"` + alice + `"},"Action":"sqs:*","Resource":"*"}`
	denyAlice := `{"Effect":"Deny","Principal":{"AWS":"` + alice + `"},"Action":"sqs:SendMessage","Resource":"*"}`
	denyAll := `{"Effect":"Deny","Principal":"*","Action":"sqs:SendMessage","Resource":"*"}`
	allowRoot := `{"Effect":"Allow","Principal":{"AWS":"` + awsident.GlobalARN("iam", "root") + `"},"Action":"sqs:*","Resource":"*"}`
	allowSNS := `{"Effect":"Allow","Principal":{"Service":"sns.amazonaws.com"},"Action":"sqs:SendMessage","Resource":"*"}`

	cases := []struct {
		name      string
		policy    string
		principal string
		identity  string
		gate      bool
		want      iampolicy.Decision
		reason    string
	}{
		{"no policy, identity allowed", "", alice, IdentityAllowed, false, iampolicy.Allowed, ""},
		{"no policy, identity denied", "", alice, IdentityImplicitDeny, false, iampolicy.ImplicitDeny, "neither the identity policies nor the resource policy"},
		{"no policy, root", "", "", "", false, iampolicy.Allowed, ""},
		{"resource allow grants an ungranted identity", allowAlice, alice, IdentityImplicitDeny, false, iampolicy.Allowed, ""},
		{"resource deny beats an identity allow", denyAlice, alice, IdentityAllowed, false, iampolicy.ExplicitDeny, "the resource policy denies it"},
		{"resource deny on everyone beats root", denyAll, "", "", false, iampolicy.ExplicitDeny, "the resource policy denies it"},
		{"resource deny on alice does not touch root", denyAlice, "", "", false, iampolicy.Allowed, ""},
		{"resource allow for someone else does not help", allowRoot, "arn:aws:iam::111111111111:user/x", IdentityImplicitDeny, false, iampolicy.ImplicitDeny, "neither"},
		{"service principal with no policy", "", "sns.amazonaws.com", IdentityService, false, iampolicy.ImplicitDeny, "does not allow sns.amazonaws.com"},
		{"service principal admitted by the policy", allowSNS, "sns.amazonaws.com", IdentityService, false, iampolicy.Allowed, ""},
		{"service principal inferred from the name", allowSNS, "sns.amazonaws.com", "", false, iampolicy.Allowed, ""},
		{"gate closed: identity allow does not count", allowAlice[:0], alice, IdentityAllowed, true, iampolicy.ImplicitDeny, "does not allow it"},
		{"gate closed: root does not count", "", "", "", true, iampolicy.ImplicitDeny, "does not allow it"},
		{"gate open by the root clause: identity counts", allowRoot, alice, IdentityAllowed, true, iampolicy.Allowed, ""},
		{"gate open: ungranted identity still denied", allowRoot, "arn:aws:iam::111111111111:user/x", IdentityImplicitDeny, true, iampolicy.ImplicitDeny, "neither"},
		{"gate closed but the policy names the caller", allowAlice, alice, IdentityImplicitDeny, true, iampolicy.Allowed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := Guard{KeyPolicyGates: tc.gate}
			w := httptest.NewRecorder()
			err := g.Check(w, request("enforce", tc.principal, tc.identity, ""), docs(t, tc.policy), "sqs:SendMessage", resource)
			if got := w.Header().Get(HeaderDecision); got != tc.want.String() {
				t.Fatalf("decision header %q, want %q (err %v)", got, tc.want, err)
			}
			if tc.want == iampolicy.Allowed {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Status != 403 || err.Code != "AccessDeniedException" {
				t.Fatalf("want a 403 AccessDeniedException, got %v", err)
			}
			if !strings.Contains(err.Message, "sqs:SendMessage") || !strings.Contains(err.Message, resource) || !strings.Contains(err.Message, tc.reason) {
				t.Fatalf("message %q lacks the action, resource or reason %q", err.Message, tc.reason)
			}
		})
	}
}

// TestModesAndHeaders: off is inert, soft logs and passes, the service's own
// mode applies when no header arrived, and CheckIdentity ignores the gate.
func TestModesAndHeaders(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	deny := `{"Effect":"Deny","Principal":"*","Action":"sqs:SendMessage","Resource":"*"}`

	if err := (Guard{Mode: "enforce"}).Check(nil, request("off", alice, IdentityAllowed, ""), docs(t, deny), "sqs:SendMessage", resource); err != nil {
		t.Fatalf("header off must win over the service mode: %v", err)
	}
	if err := (Guard{Mode: "off"}).Check(nil, request("", alice, IdentityAllowed, ""), docs(t, deny), "sqs:SendMessage", resource); err != nil {
		t.Fatalf("mode off must be inert: %v", err)
	}
	if err := (Guard{}).Check(nil, request("", alice, IdentityAllowed, ""), docs(t, deny), "sqs:SendMessage", resource); err != nil {
		t.Fatalf("no mode at all must be inert: %v", err)
	}
	if err := (Guard{Mode: "enforce"}).Check(nil, request("", "", "", ""), docs(t, deny), "sqs:SendMessage", resource); err == nil {
		t.Fatal("a peer call without headers must be judged by the service's own mode")
	}

	var logged []string
	soft := Guard{Logf: func(f string, a ...any) {
		logged = append(logged, strings.TrimSpace(strings.ReplaceAll(f, "%s", "%v"))+" "+join(a))
	}}
	w := httptest.NewRecorder()
	if err := soft.Check(w, request("soft", alice, IdentityAllowed, ""), docs(t, deny), "sqs:SendMessage", resource); err != nil {
		t.Fatalf("soft must not block: %v", err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "would deny") || !strings.Contains(logged[0], alice) {
		t.Fatalf("soft must log the denial once, got %q", logged)
	}
	if got := w.Header().Get(HeaderDecision); got != iampolicy.ExplicitDeny.String() {
		t.Fatalf("soft must still report the verdict, got %q", got)
	}

	gated := Guard{KeyPolicyGates: true}
	if err := gated.CheckIdentity(nil, request("enforce", alice, IdentityAllowed, ""), "kms:CreateKey", ""); err != nil {
		t.Fatalf("CheckIdentity must not apply the gate: %v", err)
	}
	if err := gated.CheckIdentity(nil, request("enforce", alice, IdentityImplicitDeny, ""), "kms:CreateKey", ""); err == nil {
		t.Fatal("CheckIdentity must refuse an ungranted identity")
	}
}

// TestSourceArnCondition: the source ARN a peer call carries reaches the
// policy's condition context under both spellings.
func TestSourceArnCondition(t *testing.T) {
	allow := `{"Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},"Action":"lambda:InvokeFunction","Resource":"*",
		"Condition":{"ArnLike":{"AWS:SourceArn":"arn:aws:s3:::uploads"}}}`
	fn := "arn:aws:lambda:us-east-1:000000000000:function:f"
	g := Guard{}
	if err := g.Check(nil, request("enforce", "s3.amazonaws.com", IdentityService, "arn:aws:s3:::uploads"), docs(t, allow), "lambda:InvokeFunction", fn); err != nil {
		t.Fatalf("the named bucket must be admitted: %v", err)
	}
	if err := g.Check(nil, request("enforce", "s3.amazonaws.com", IdentityService, "arn:aws:s3:::other"), docs(t, allow), "lambda:InvokeFunction", fn); err == nil {
		t.Fatal("another bucket must be refused")
	}
	if err := g.Check(nil, request("enforce", "s3.amazonaws.com", IdentityService, ""), docs(t, allow), "lambda:InvokeFunction", fn); err == nil {
		t.Fatal("no source at all must be refused")
	}
}

// TestStripAndStamp: whatever a client sent under X-Doze-* is gone before
// the middleware writes its own.
func TestStripAndStamp(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("X-Doze-Principal", "arn:aws:iam::000000000000:root")
	r.Header.Set("x-doze-identity", "allowed")
	r.Header.Set("X-Doze-Anything", "yes")
	r.Header.Set("X-Amz-Target", "keep")
	Strip(r)
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-doze-") {
			t.Fatalf("header %s survived Strip", name)
		}
	}
	if r.Header.Get("X-Amz-Target") != "keep" {
		t.Fatal("Strip must leave other headers alone")
	}
	Stamp(r, "enforce", "arn:aws:iam::000000000000:user/alice", IdentityImplicitDeny, "sqs:SendMessage", resource)
	if r.Header.Get(HeaderMode) != "enforce" || r.Header.Get(HeaderIdentity) != IdentityImplicitDeny || !strings.HasSuffix(r.Header.Get(HeaderPrincipal), "user/alice") {
		t.Fatalf("stamp incomplete: %v", r.Header)
	}
}

func join(a []any) string {
	var parts []string
	for _, v := range a {
		if s, ok := v.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}
