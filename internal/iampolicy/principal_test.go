package iampolicy

import (
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

// TestPrincipalMatching walks the principal forms a resource policy can name
// against the callers doze-aws stamps: identities, the root, services.
func TestPrincipalMatching(t *testing.T) {
	root := awsident.GlobalARN("iam", "root")
	alice := awsident.GlobalARN("iam", "user/alice")
	role := awsident.GlobalARN("iam", "role/worker")
	other := "arn:aws:iam::111111111111:user/alice"

	cases := []struct {
		name      string
		principal string // the Principal block, as JSON
		caller    string
		want      bool
	}{
		{"star admits anyone", `"*"`, alice, true},
		{"star admits root", `"*"`, root, true},
		{"star admits a service", `"*"`, "s3.amazonaws.com", true},
		{"AWS star admits anyone", `{"AWS":"*"}`, alice, true},
		{"account root admits root", `{"AWS":"` + root + `"}`, root, true},
		{"account root delegates: a user is not admitted by it", `{"AWS":"` + root + `"}`, alice, false},
		{"account root delegates: a role is not admitted by it", `{"AWS":"` + root + `"}`, role, false},
		{"account root does not admit another account", `{"AWS":"` + root + `"}`, other, false},
		{"account id admits root", `{"AWS":"` + awsident.AccountID + `"}`, root, true},
		{"account id delegates: a user is not admitted by it", `{"AWS":"` + awsident.AccountID + `"}`, alice, false},
		{"exact user", `{"AWS":"` + alice + `"}`, alice, true},
		{"exact user does not admit the role", `{"AWS":"` + alice + `"}`, role, false},
		{"exact user does not admit root", `{"AWS":"` + alice + `"}`, root, false},
		{"list admits any member", `{"AWS":["` + role + `","` + alice + `"]}`, alice, true},
		{"glob on the user path", `{"AWS":"arn:aws:iam::` + awsident.AccountID + `:user/*"}`, alice, true},
		{"glob does not admit the role", `{"AWS":"arn:aws:iam::` + awsident.AccountID + `:user/*"}`, role, false},
		{"service matches its principal", `{"Service":"s3.amazonaws.com"}`, "s3.amazonaws.com", true},
		{"service matches case-insensitively", `{"Service":"S3.AmazonAWS.com"}`, "s3.amazonaws.com", true},
		{"service does not admit another service", `{"Service":"s3.amazonaws.com"}`, "sns.amazonaws.com", false},
		{"service does not admit a user", `{"Service":"s3.amazonaws.com"}`, alice, false},
		{"AWS star admits a service", `{"AWS":"*"}`, "s3.amazonaws.com", true},
		{"account root does not admit a service", `{"AWS":"` + root + `"}`, "s3.amazonaws.com", false},
		{"anonymous caller only matches star", `{"AWS":"` + root + `"}`, "", false},
		{"anonymous caller matches star", `"*"`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":` + tc.principal +
				`,"Action":"sqs:SendMessage","Resource":"*"}]}`)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			dec, _ := Evaluate([]*Document{doc}, Request{Action: "sqs:SendMessage", Resource: "arn:aws:sqs:us-east-1:000000000000:q", Principal: tc.caller})
			if got := dec == Allowed; got != tc.want {
				t.Fatalf("principal %s for caller %q: allowed=%v, want %v", tc.principal, tc.caller, got, tc.want)
			}
		})
	}
}

// TestNotPrincipalInverts: a NotPrincipal statement matches everyone but the
// named ones, and a statement with no Principal block (an identity policy)
// matches every caller.
func TestNotPrincipalInverts(t *testing.T) {
	alice := awsident.GlobalARN("iam", "user/alice")
	bob := awsident.GlobalARN("iam", "user/bob")
	doc, err := Parse(`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","NotPrincipal":{"AWS":"` + alice +
		`"},"Action":"sqs:*","Resource":"*"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Action: "sqs:SendMessage", Resource: "arn:aws:sqs:us-east-1:000000000000:q"}
	req.Principal = alice
	if dec, _ := Evaluate([]*Document{doc}, req); dec == ExplicitDeny {
		t.Fatal("NotPrincipal must exempt the named principal from the deny")
	}
	req.Principal = bob
	if dec, _ := Evaluate([]*Document{doc}, req); dec != ExplicitDeny {
		t.Fatal("NotPrincipal must deny everyone else")
	}

	identity, _ := Parse(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"}]}`)
	for _, caller := range []string{alice, "s3.amazonaws.com", ""} {
		if dec, _ := Evaluate([]*Document{identity}, Request{Action: "sqs:SendMessage", Resource: "arn:aws:sqs:us-east-1:000000000000:q", Principal: caller}); dec != Allowed {
			t.Fatalf("a statement with no Principal must match caller %q", caller)
		}
	}
}

// TestServicePrincipalConditions: a Lambda permission for S3 carries an
// ArnLike on the source; the guard supplies aws:SourceArn.
func TestServicePrincipalConditions(t *testing.T) {
	doc, err := Parse(`{"Version":"2012-10-17","Statement":[{"Sid":"s3","Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},
		"Action":"lambda:InvokeFunction","Resource":"arn:aws:lambda:us-east-1:000000000000:function:f",
		"Condition":{"ArnLike":{"AWS:SourceArn":"arn:aws:s3:::uploads"}}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Action: "lambda:InvokeFunction", Resource: "arn:aws:lambda:us-east-1:000000000000:function:f", Principal: "s3.amazonaws.com"}
	req.Context = map[string][]string{"aws:SourceArn": {"arn:aws:s3:::uploads"}, "AWS:SourceArn": {"arn:aws:s3:::uploads"}}
	if dec, by := Evaluate([]*Document{doc}, req); dec != Allowed || by != "s3" {
		t.Fatalf("matching source: %v by %q", dec, by)
	}
	req.Context = map[string][]string{"aws:SourceArn": {"arn:aws:s3:::other"}, "AWS:SourceArn": {"arn:aws:s3:::other"}}
	if dec, _ := Evaluate([]*Document{doc}, req); dec != ImplicitDeny {
		t.Fatalf("another bucket: %v, want implicit deny", dec)
	}
	req.Context = nil
	if dec, _ := Evaluate([]*Document{doc}, req); dec != ImplicitDeny {
		t.Fatalf("no source at all: %v, want implicit deny", dec)
	}
}
