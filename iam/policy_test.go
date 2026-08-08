package iam

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"s3:GetObject", "s3:GetObject", true},
		{"s3:GetObject", "s3:PutObject", false},
		{"s3:*", "s3:GetObject", true},
		{"s3:*", "sqs:SendMessage", false},
		{"s3:Get*", "s3:GetObject", true},
		{"s3:Get*", "s3:PutObject", false},
		{"*Object", "s3:GetObject", true},
		{"s3:?etObject", "s3:GetObject", true},
		{"s3:?etObject", "s3:GeetObject", false},
		{"a*b*c", "abc", true},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
		// Backtracking: the first '*' must give back characters so the tail can match.
		{"*.txt", "a.b.txt", true},
		{"*a*a*a", "aaa", true},
		{"*a*a*a", "aa", false},
		{"arn:aws:s3:::bucket/*", "arn:aws:s3:::bucket/key.txt", true},
		{"arn:aws:s3:::bucket/*", "arn:aws:s3:::other/key.txt", false},
		{"", "", true},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestGlobMatchFoldsActionCase(t *testing.T) {
	if !globMatchFold("s3:getobject", "s3:GetObject") {
		t.Error("action matching must be case-insensitive")
	}
	if globMatch("s3:getobject", "s3:GetObject") {
		t.Error("resource matching must stay case-sensitive")
	}
}

func mustParse(t *testing.T, raw string) *Document {
	t.Helper()
	d, err := ParsePolicy(raw)
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	return d
}

func TestParsePolicyAcceptsAWSShapes(t *testing.T) {
	// A single statement object rather than an array, and scalar Action/Resource.
	d := mustParse(t, `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:*","Resource":"*"}}`)
	if len(d.Statement) != 1 || d.Statement[0].Action[0] != "s3:*" {
		t.Fatalf("scalar shapes not absorbed: %+v", d.Statement)
	}
	// Arrays.
	d = mustParse(t, `{"Statement":[{"Effect":"Deny","Action":["s3:Delete*","s3:Put*"],"Resource":["arn:a","arn:b"]}]}`)
	if len(d.Statement[0].Action) != 2 || len(d.Statement[0].Resource) != 2 {
		t.Fatal("array shapes not absorbed")
	}
}

func TestParsePolicyRejectsGarbage(t *testing.T) {
	for _, raw := range []string{
		``,
		`not json`,
		`{"Statement":[]}`,
		`{"Statement":[{"Effect":"Maybe","Action":"s3:*"}]}`,
		`{"Statement":[{"Effect":"Allow","Resource":"*"}]}`, // no Action or NotAction
	} {
		if _, err := ParsePolicy(raw); err == nil {
			t.Errorf("ParsePolicy(%q) should have failed", raw)
		}
	}
}

func TestEvaluateOrder(t *testing.T) {
	allow := mustParse(t, `{"Statement":[{"Sid":"A","Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	deny := mustParse(t, `{"Statement":[{"Sid":"D","Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`)

	// Allow alone grants.
	if d, _ := Evaluate([]*Document{allow}, Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::b/k"}); d != Allowed {
		t.Fatalf("allow-only = %v, want Allowed", d)
	}
	// Nothing attached is an implicit deny.
	if d, _ := Evaluate(nil, Request{Action: "s3:GetObject"}); d != ImplicitDeny {
		t.Fatalf("no policies = %v, want ImplicitDeny", d)
	}
	// An explicit Deny in a *different* document overrides the Allow, and does
	// so regardless of document order.
	for _, docs := range [][]*Document{{allow, deny}, {deny, allow}} {
		d, by := Evaluate(docs, Request{Action: "s3:DeleteObject", Resource: "arn:aws:s3:::b/k"})
		if d != ExplicitDeny {
			t.Fatalf("deny should win, got %v", d)
		}
		if by != "D" {
			t.Fatalf("attributed to %q, want the Deny SID", by)
		}
	}
	// An action the Deny does not cover is still allowed.
	if d, _ := Evaluate([]*Document{allow, deny}, Request{Action: "s3:GetObject", Resource: "arn:x"}); d != Allowed {
		t.Fatal("unrelated action should remain allowed")
	}
}

func TestEvaluateResourceScoping(t *testing.T) {
	doc := mustParse(t, `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/*"}]}`)

	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::photos/a.jpg"}); d != Allowed {
		t.Fatal("in-scope resource should be allowed")
	}
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::docs/a.pdf"}); d != ImplicitDeny {
		t.Fatal("out-of-scope resource should be denied")
	}
	// An unresolved resource must not match a narrow statement — the engine
	// never guesses its way into an Allow.
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject"}); d != ImplicitDeny {
		t.Fatal("unresolved resource must not satisfy a scoped Resource")
	}
	// ...but it does match a "*" grant, which is the common local case.
	star := mustParse(t, `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`)
	if d, _ := Evaluate([]*Document{star}, Request{Action: "s3:GetObject"}); d != Allowed {
		t.Fatal("unresolved resource should satisfy a * grant")
	}
}

func TestEvaluateNotActionAndNotResource(t *testing.T) {
	// Allow everything except IAM.
	doc := mustParse(t, `{"Statement":[{"Effect":"Allow","NotAction":"iam:*","Resource":"*"}]}`)
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "x"}); d != Allowed {
		t.Fatal("NotAction should allow the non-excluded action")
	}
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "iam:CreateUser", Resource: "x"}); d != ImplicitDeny {
		t.Fatal("NotAction should exclude the named action")
	}

	doc = mustParse(t, `{"Statement":[{"Effect":"Deny","Action":"s3:*","NotResource":"arn:aws:s3:::public/*"}]}`)
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::private/x"}); d != ExplicitDeny {
		t.Fatal("NotResource should deny outside the exclusion")
	}
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::public/x"}); d != ImplicitDeny {
		t.Fatal("NotResource should spare the excluded resource")
	}
}

func TestConditionOperators(t *testing.T) {
	ctx := map[string][]string{
		"aws:username":        {"alice"},
		"aws:SourceIp":        {"10.0.0.7"},
		"aws:SecureTransport": {"true"},
		"s3:max-keys":         {"50"},
	}
	cases := []struct {
		name, policy string
		want         Decision
	}{
		{"StringEquals hit", `{"StringEquals":{"aws:username":"alice"}}`, Allowed},
		{"StringEquals miss", `{"StringEquals":{"aws:username":"bob"}}`, ImplicitDeny},
		{"StringEquals multi-value ORs", `{"StringEquals":{"aws:username":["bob","alice"]}}`, Allowed},
		{"StringLike", `{"StringLike":{"aws:username":"al*"}}`, Allowed},
		{"StringNotLike", `{"StringNotLike":{"aws:username":"bo*"}}`, Allowed},
		{"key lookup is case-insensitive", `{"StringEquals":{"aws:UserName":"alice"}}`, Allowed},
		{"Bool", `{"Bool":{"aws:SecureTransport":"true"}}`, Allowed},
		{"Bool miss", `{"Bool":{"aws:SecureTransport":"false"}}`, ImplicitDeny},
		{"IpAddress in CIDR", `{"IpAddress":{"aws:SourceIp":"10.0.0.0/24"}}`, Allowed},
		{"IpAddress outside CIDR", `{"IpAddress":{"aws:SourceIp":"192.168.0.0/24"}}`, ImplicitDeny},
		{"NotIpAddress", `{"NotIpAddress":{"aws:SourceIp":"192.168.0.0/24"}}`, Allowed},
		{"NumericLessThan", `{"NumericLessThan":{"s3:max-keys":"100"}}`, Allowed},
		{"NumericGreaterThan miss", `{"NumericGreaterThan":{"s3:max-keys":"100"}}`, ImplicitDeny},
		{"absent key fails", `{"StringEquals":{"aws:PrincipalTag/team":"shop"}}`, ImplicitDeny},
		{"absent key passes IfExists", `{"StringEqualsIfExists":{"aws:PrincipalTag/team":"shop"}}`, Allowed},
		{"Null true on absent key", `{"Null":{"aws:PrincipalTag/team":"true"}}`, Allowed},
		{"Null false on absent key", `{"Null":{"aws:PrincipalTag/team":"false"}}`, ImplicitDeny},
		{"Null false on present key", `{"Null":{"aws:username":"false"}}`, Allowed},
		{"all conditions must pass", `{"StringEquals":{"aws:username":"alice"},"Bool":{"aws:SecureTransport":"false"}}`, ImplicitDeny},
		{"unknown operator never matches", `{"MadeUpOperator":{"aws:username":"alice"}}`, ImplicitDeny},
	}
	for _, c := range cases {
		raw := `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*","Condition":` + c.policy + `}]}`
		doc := mustParse(t, raw)
		got, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "arn:x", Context: ctx})
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestConditionSetQuantifiers(t *testing.T) {
	ctx := map[string][]string{"aws:TagKeys": {"team", "env"}}

	// ForAllValues: every supplied value must be in the allowed set.
	doc := mustParse(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*",
		"Condition":{"ForAllValues:StringEquals":{"aws:TagKeys":["team","env","owner"]}}}]}`)
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "x", Context: ctx}); d != Allowed {
		t.Error("ForAllValues should pass when every value is permitted")
	}
	doc = mustParse(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*",
		"Condition":{"ForAllValues:StringEquals":{"aws:TagKeys":["team"]}}}]}`)
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "x", Context: ctx}); d != ImplicitDeny {
		t.Error("ForAllValues should fail when a value is not permitted")
	}
	// ForAnyValue: one overlap is enough.
	doc = mustParse(t, `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*",
		"Condition":{"ForAnyValue:StringEquals":{"aws:TagKeys":["team"]}}}]}`)
	if d, _ := Evaluate([]*Document{doc}, Request{Action: "s3:GetObject", Resource: "x", Context: ctx}); d != Allowed {
		t.Error("ForAnyValue should pass on a single overlap")
	}
}
