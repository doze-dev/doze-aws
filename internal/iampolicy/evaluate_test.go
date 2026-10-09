package iampolicy

import "testing"

func decide(t *testing.T, doc string, req Request) Decision {
	t.Helper()
	got, _ := Evaluate([]*Document{mustParse(t, doc)}, req)
	return got
}

// "Deny unless the caller is one of these" is the commonest guard policy there
// is, and it only works if a negated operator over a list means "none of them".
func TestDenyUnlessInTheListDeniesOnlyOutsiders(t *testing.T) {
	const doc = `{"Statement":[
		{"Effect":"Allow","Action":"s3:*","Resource":"*"},
		{"Effect":"Deny","Action":"s3:*","Resource":"*",
		 "Condition":{"StringNotEquals":{"aws:username":["mina","omar"]}}}]}`
	for user, want := range map[string]Decision{"mina": Allowed, "omar": Allowed, "eve": ExplicitDeny} {
		got := decide(t, doc, Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::b/k", Context: ctxOf("aws:username", user)})
		if got != want {
			t.Errorf("user %s: %v, want %v", user, got, want)
		}
	}
	const byAddress = `{"Statement":[
		{"Effect":"Allow","Action":"*","Resource":"*"},
		{"Effect":"Deny","Action":"*","Resource":"*",
		 "Condition":{"NotIpAddress":{"aws:SourceIp":["10.0.0.0/8","192.168.0.0/16"]}}}]}`
	for ip, want := range map[string]Decision{"10.1.1.1": Allowed, "192.168.5.5": Allowed, "8.8.8.8": ExplicitDeny} {
		if got := decide(t, byAddress, Request{Action: "sqs:SendMessage", Context: ctxOf("aws:SourceIp", ip)}); got != want {
			t.Errorf("from %s: %v, want %v", ip, got, want)
		}
	}
}

func TestResourceMatching(t *testing.T) {
	for _, c := range []struct {
		name     string
		st       Statement
		resource string
		want     bool
	}{
		{"no Resource block matches anything", Statement{}, "arn:aws:s3:::b", true},
		{"exact", Statement{Resource: stringList{"arn:aws:s3:::b"}}, "arn:aws:s3:::b", true},
		{"glob", Statement{Resource: stringList{"arn:aws:s3:::b/*"}}, "arn:aws:s3:::b/k", true},
		{"outside the glob", Statement{Resource: stringList{"arn:aws:s3:::b/*"}}, "arn:aws:s3:::c/k", false},
		{"case matters in an ARN", Statement{Resource: stringList{"arn:aws:s3:::B"}}, "arn:aws:s3:::b", false},
		{"unknown resource matches only *", Statement{Resource: stringList{"arn:aws:s3:::b"}}, "", false},
		{"unknown resource under *", Statement{Resource: stringList{"*"}}, "", true},
		{"one of several", Statement{Resource: stringList{"arn:a", "arn:b"}}, "arn:b", true},
		{"NotResource excludes", Statement{NotResource: stringList{"arn:aws:s3:::b/secret/*"}}, "arn:aws:s3:::b/secret/x", false},
		{"NotResource admits the rest", Statement{NotResource: stringList{"arn:aws:s3:::b/secret/*"}}, "arn:aws:s3:::b/open/x", true},
		{"NotResource cannot prove exclusion without a resource", Statement{NotResource: stringList{"arn:aws:s3:::b"}}, "", false},
	} {
		st := c.st
		if got := resourceMatches(&st, c.resource); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrincipalBlocksAcrossStatementShapes(t *testing.T) {
	const acct = "123456789012"
	const mina = "arn:aws:iam::123456789012:user/mina"
	for _, c := range []struct {
		name   string
		doc    string
		caller string
		want   Decision
	}{
		{"star admits anyone", `{"Effect":"Allow","Action":"*","Resource":"*","Principal":"*"}`, "arn:aws:iam::999999999999:user/x", Allowed},
		{"named user", `{"Effect":"Allow","Action":"*","Resource":"*","Principal":{"AWS":"` + mina + `"}}`, mina, Allowed},
		{"another user is not named", `{"Effect":"Allow","Action":"*","Resource":"*","Principal":{"AWS":"` + mina + `"}}`, "arn:aws:iam::123456789012:user/omar", ImplicitDeny},
		{"an unknown caller is named by nothing but *", `{"Effect":"Allow","Action":"*","Resource":"*","Principal":{"AWS":"` + mina + `"}}`, "", ImplicitDeny},
		{"service principal", `{"Effect":"Allow","Action":"*","Resource":"*","Principal":{"Service":"sns.amazonaws.com"}}`, "sns.amazonaws.com", Allowed},
		{"another service", `{"Effect":"Allow","Action":"*","Resource":"*","Principal":{"Service":"sns.amazonaws.com"}}`, "s3.amazonaws.com", ImplicitDeny},
		{"a user is not a service", `{"Effect":"Allow","Action":"*","Resource":"*","Principal":{"Service":"sns.amazonaws.com"}}`, mina, ImplicitDeny},
		{"a Deny naming the account covers its users", `{"Effect":"Deny","Action":"*","Resource":"*","Principal":{"AWS":"` + acct + `"}}`, mina, ExplicitDeny},
		{"NotPrincipal Deny spares the named", `{"Effect":"Deny","Action":"*","Resource":"*","NotPrincipal":{"AWS":"` + mina + `"}}`, mina, ImplicitDeny},
		{"NotPrincipal Deny catches the rest", `{"Effect":"Deny","Action":"*","Resource":"*","NotPrincipal":{"AWS":"` + mina + `"}}`, "arn:aws:iam::123456789012:user/omar", ExplicitDeny},
		{"NotPrincipal with Allow matches nobody", `{"Effect":"Allow","Action":"*","Resource":"*","NotPrincipal":{"AWS":"` + mina + `"}}`, "arn:aws:iam::123456789012:user/omar", ImplicitDeny},
	} {
		got := decide(t, `{"Statement":[`+c.doc+`]}`, Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::b", Principal: c.caller, Account: acct})
		if got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsPublic(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  string
		want bool
	}{
		{"everyone may read", `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`, true},
		{"AWS star is everyone too", `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`, true},
		{"one named account is not public", `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::1:root"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`, false},
		{"a condition narrows it", `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*","Condition":{"StringEquals":{"aws:SourceVpce":"vpce-1"}}}]}`, false},
		{"an unconditional Deny to everyone cancels the Allow", `{"Statement":[
			{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"},
			{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::b/*"}]}`, false},
		{"a narrower Deny does not", `{"Statement":[
			{"Effect":"Allow","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::b/*"},
			{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`, true},
		{"a Deny to one principal does not stop the world", `{"Statement":[
			{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"},
			{"Effect":"Deny","Principal":{"AWS":"arn:aws:iam::1:root"},"Action":"s3:*","Resource":"arn:aws:s3:::b/*"}]}`, true},
		{"a conditional Deny may not fire", `{"Statement":[
			{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"},
			{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::b/*","Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`, true},
		{"NotAction grants everything but", `{"Statement":[{"Effect":"Allow","Principal":"*","NotAction":"s3:Delete*","Resource":"arn:aws:s3:::b/*"}]}`, true},
		{"no Resource means the resource it hangs on", `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"sqs:SendMessage"}]}`, true},
		{"Deny only", `{"Statement":[{"Effect":"Deny","Principal":"*","Action":"*","Resource":"*"}]}`, false},
	} {
		if got := IsPublic(mustParse(t, c.doc)); got != c.want {
			t.Errorf("%s: IsPublic = %v, want %v", c.name, got, c.want)
		}
	}
	if IsPublic(nil) {
		t.Error("a nil document is public")
	}
}
