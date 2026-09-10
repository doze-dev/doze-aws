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
		{"a blanket deny cancels the allow", `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*"},{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"*"}`, false},
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

// An explicit Deny cancels a public Allow, but only when it actually reaches
// the caller the Allow reached: unconditional, aimed at everyone, and at
// least as broad. Each case below is a Deny that must NOT cancel, except the
// first — get any of these backwards and BlockPublicPolicy waves through a
// bucket policy that does expose the bucket.
func TestPolicyIsPublicWeighsDenyStatements(t *testing.T) {
	const allowAll = `{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}`
	wrap := func(stmts string) string { return `{"Version":"2012-10-17","Statement":[` + stmts + `]}` }
	cases := []struct {
		name   string
		deny   string
		public bool
	}{
		{"deny of the same action and resource", `{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}`, false},
		{"broader deny by wildcard action", `{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::b/*"}`, false},
		{"broader deny by star", `{"Effect":"Deny","Principal":"*","Action":"*","Resource":"*"}`, false},
		// A deny narrower than the allow leaves the rest of the grant public.
		{"narrower deny by resource", `{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/private/*"}`, true},
		{"deny of a different action", `{"Effect":"Deny","Principal":"*","Action":"s3:PutObject","Resource":"arn:aws:s3:::b/*"}`, true},
		// A deny that may not fire, or that does not name everyone, cannot
		// be relied on to close the grant.
		{"conditional deny", `{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*","Condition":{"Bool":{"aws:SecureTransport":"false"}}}`, true},
		{"deny aimed at one principal", `{"Effect":"Deny","Principal":{"AWS":"arn:aws:iam::000000000000:user/x"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}`, true},
		{"deny by NotPrincipal", `{"Effect":"Deny","NotPrincipal":{"AWS":"arn:aws:iam::000000000000:user/x"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}`, true},
	}
	for _, c := range cases {
		if got := policyIsPublic(wrap(allowAll + "," + c.deny)); got != c.public {
			t.Errorf("%s: public=%v, want %v", c.name, got, c.public)
		}
		// Order must not matter: a deny before the allow reads the same.
		if got := policyIsPublic(wrap(c.deny + "," + allowAll)); got != c.public {
			t.Errorf("%s (deny first): public=%v, want %v", c.name, got, c.public)
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
