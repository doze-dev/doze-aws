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
	}
	for _, c := range cases {
		if got := policyIsPublic(wrap(c.stmt)); got != c.public {
			t.Errorf("%s: public=%v, want %v", c.name, got, c.public)
		}
	}
}
