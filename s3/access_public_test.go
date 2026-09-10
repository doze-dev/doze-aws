package s3

import "testing"

// policyIsPublic follows AWS's rule: a "*" Allow is public unless a
// condition on a narrowing key (source, principal, organization) limits
// it; any other condition leaves it public, and NotPrincipal is public.
func TestPolicyIsPublicByAWSRule(t *testing.T) {
	wrap := func(stmt string) string { return `{"Version":"2012-10-17","Statement":[` + stmt + `]}` }
	cases := []struct {
		name   string
		stmt   string
		public bool
	}{
		{"star", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}`, true},
		{"AWS star list", `{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":"s3:GetObject","Resource":"*"}`, true},
		{"named principal", `{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:user/x"},"Action":"s3:GetObject","Resource":"*"}`, false},
		{"deny star", `{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"*"}`, false},
		{"source ip narrows", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*","Condition":{"IpAddress":{"aws:SourceIp":"10.0.0.0/8"}}}`, false},
		{"vpce narrows", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*","Condition":{"StringEquals":{"aws:SourceVpce":"vpce-1"}}}`, false},
		{"principal org narrows", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*","Condition":{"StringEquals":{"aws:PrincipalOrgID":"o-1"}}}`, false},
		{"secure transport does not narrow", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*","Condition":{"Bool":{"aws:SecureTransport":"true"}}}`, true},
		{"prefix does not narrow", `{"Effect":"Allow","Principal":"*","Action":"s3:ListBucket","Resource":"*","Condition":{"StringLike":{"s3:prefix":"public/*"}}}`, true},
		{"negated source ip does not narrow", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*","Condition":{"NotIpAddress":{"aws:SourceIp":"10.0.0.0/8"}}}`, true},
		{"NotPrincipal allow", `{"Effect":"Allow","NotPrincipal":{"AWS":"arn:aws:iam::000000000000:user/x"},"Action":"s3:GetObject","Resource":"*"}`, true},
		{"AWS star scalar", `{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"s3:GetObject","Resource":"*"}`, true},
		{"star in a list beside a named principal", `{"Effect":"Allow","Principal":{"AWS":["arn:aws:iam::000000000000:user/x","*"]},"Action":"s3:GetObject","Resource":"*"}`, true},
		// A known simplification, pinned so a change to it is deliberate.
		// policyIsPublic returns on the first unconditioned "*" Allow, so a
		// later blanket Deny that would cancel it is not considered. AWS
		// evaluates the whole document and would very likely call this one
		// not public. It is a strictly conservative disagreement — doze-aws
		// says "public" where AWS might not, so BlockPublicPolicy refuses a
		// policy AWS would have taken, and nothing is let through.
		{"a later blanket deny is not weighed (conservative)", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*"},{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"*"}`, true},
	}
	for _, c := range cases {
		if got := policyIsPublic(wrap(c.stmt)); got != c.public {
			t.Errorf("%s: public=%v, want %v", c.name, got, c.public)
		}
	}
}

// Statement may be one object rather than a list — the shape a hand-written
// policy usually takes. It reaches a different branch of the parse, so a
// table that only ever wraps in a list leaves that branch untested.
func TestPolicyIsPublicWithASingleStatementObject(t *testing.T) {
	one := func(stmt string) string { return `{"Version":"2012-10-17","Statement":` + stmt + `}` }
	cases := []struct {
		name   string
		stmt   string
		public bool
	}{
		{"star", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}`, true},
		{"named principal", `{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:user/x"},"Action":"s3:GetObject","Resource":"*"}`, false},
		{"narrowed by source ip", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*","Condition":{"IpAddress":{"aws:SourceIp":"10.0.0.0/8"}}}`, false},
	}
	for _, c := range cases {
		if got := policyIsPublic(one(c.stmt)); got != c.public {
			t.Errorf("%s: public=%v, want %v", c.name, got, c.public)
		}
	}
}

// A policy that does not parse is not public — the caller stores it or
// refuses it on its own grounds, and this must not guess.
func TestPolicyIsPublicOnUnparseableInput(t *testing.T) {
	for _, doc := range []string{"", "{", `{"Statement":"not a statement"}`, `{"Version":"2012-10-17"}`, "null"} {
		if policyIsPublic(doc) {
			t.Errorf("%q was reported public", doc)
		}
	}
}
