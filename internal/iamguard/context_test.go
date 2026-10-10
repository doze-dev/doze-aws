package iamguard

import (
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"testing"
)

// The keys the ledger says are supplied are supplied to a resource policy as
// well as to an identity policy. Each case is a Deny that applies only when its
// key arrives with the value the request carries; a key that never arrives
// makes the condition false, the Deny does not apply, and the request goes
// through, which is how aws:SecureTransport, aws:SourceIp and aws:UserAgent
// were silently ignored.
func TestResourcePolicySeesEveryGlobalKey(t *testing.T) {
	const alice = "arn:aws:iam::000000000000:user/team/alice"

	deny := func(cond string) string {
		return `{"Effect":"Deny","Principal":"*","Action":"sqs:SendMessage","Resource":"*","Condition":` + cond + `}`
	}
	g := Guard{}
	send := func(t *testing.T, cond, principal, ua, remote string) *awshttp.APIError {
		t.Helper()
		r := request("enforce", principal, IdentityRoot, "")
		r.RemoteAddr = remote
		if ua != "" {
			r.Header.Set("User-Agent", ua)
		}
		return g.Check(nil, r, docs(t, deny(cond)), "sqs:SendMessage", resource)
	}

	t.Run("aws:UserAgent", func(t *testing.T) {
		cond := `{"StringLike":{"aws:UserAgent":"evil*"}}`
		if send(t, cond, alice, "evil-client/1.0", "127.0.0.1:5000") == nil {
			t.Error("a matching user agent should be denied")
		}
		if err := send(t, cond, alice, "boto3/1.34", "127.0.0.1:5000"); err != nil {
			t.Errorf("another user agent was denied: %v", err)
		}
	})
	t.Run("aws:SourceIp", func(t *testing.T) {
		cond := `{"IpAddress":{"aws:SourceIp":"10.0.0.0/8"}}`
		if send(t, cond, alice, "", "10.1.2.3:4000") == nil {
			t.Error("a caller inside the range should be denied")
		}
		if err := send(t, cond, alice, "", "127.0.0.1:4000"); err != nil {
			t.Errorf("a caller outside the range was denied: %v", err)
		}
	})
	t.Run("aws:username", func(t *testing.T) {
		cond := `{"StringEquals":{"aws:username":"alice"}}`
		if send(t, cond, alice, "", "127.0.0.1:1") == nil {
			t.Error("a user named alice should be denied, including one with a path")
		}
		if err := send(t, cond, "arn:aws:iam::000000000000:role/alice", "", "127.0.0.1:1"); err != nil {
			t.Errorf("a role has no aws:username and must not match: %v", err)
		}
	})
	t.Run("aws:RequestedRegion", func(t *testing.T) {
		cond := `{"StringEquals":{"aws:RequestedRegion":"us-east-1"}}`
		if send(t, cond, alice, "", "127.0.0.1:1") == nil {
			t.Error("the instance's own region should be seen")
		}
		cond = `{"StringNotEquals":{"aws:RequestedRegion":"us-east-1"}}`
		if err := send(t, cond, alice, "", "127.0.0.1:1"); err != nil {
			t.Errorf("denied for a region that is not the instance's: %v", err)
		}
	})
	t.Run("aws:SecureTransport, decision D7", func(t *testing.T) {
		// The canonical hardening policy must not refuse every local request.
		cond := `{"Bool":{"aws:SecureTransport":"false"}}`
		if err := send(t, cond, alice, "", "127.0.0.1:1"); err != nil {
			t.Errorf("a deny-when-not-TLS policy locked out the local endpoint: %v", err)
		}
		// And the key is PRESENT, so a policy that requires it admits. The
		// caller's identity policies grant nothing here (an implicit deny), so
		// the conditional Allow is the only thing that can let the request
		// through: with the key absent the condition is false and this is
		// refused. (With a root identity it passed whether the key was there or
		// not, and proved nothing.)
		allow := func(want string) string {
			return `{"Effect":"Allow","Principal":"*","Action":"sqs:SendMessage","Resource":"*","Condition":{"Bool":{"aws:SecureTransport":"` + want + `"}}}`
		}
		if err := g.Check(nil, request("enforce", alice, IdentityImplicitDeny, ""), docs(t, allow("true")), "sqs:SendMessage", resource); err != nil {
			t.Errorf("an allow that requires TLS did not admit: the key is not supplied as true: %v", err)
		}
		if err := g.Check(nil, request("enforce", alice, IdentityImplicitDeny, ""), docs(t, allow("false")), "sqs:SendMessage", resource); err == nil {
			t.Error("an allow that requires NO TLS admitted: the key is not true")
		}
	})
}

func TestUserNameOf(t *testing.T) {
	for arn, want := range map[string]string{
		"arn:aws:iam::123456789012:user/alice":             "alice",
		"arn:aws:iam::123456789012:user/division/team/bob": "bob",
		"arn:aws:iam::123456789012:role/alice":             "",
		"arn:aws:iam::123456789012:root":                   "",
		"arn:aws:sts::123456789012:assumed-role/r/session": "",
		"": "",
	} {
		if got := userNameOf(arn); got != want {
			t.Errorf("userNameOf(%q) = %q, want %q", arn, got, want)
		}
	}
}
