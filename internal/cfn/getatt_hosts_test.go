package cfn

import (
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

// Four GetAtt values are hostnames or URLs rather than ARNs, and all four were
// literals pointing where doze-aws does not answer: <bucket>.s3.localhost,
// http://127.0.0.1:4566/_aws/execute-api/<id>, http://127.0.0.1/<acct>/<queue>
// (port 80 — never right, even before the addressing change), and
// <acct>.dkr.ecr.<region>.localhost.
//
// A user reads these out of a template and then tries to use them, so they are
// worth a test that names them all.
func TestHostShapedAttributesFollowTheSuffix(t *testing.T) {
	m := minting{
		id:       awsident.Identity{Region: "ap-south-1", AccountID: "811690671382"},
		suffix:   "aws.harbour.doze",
		endpoint: "http://aws.harbour.doze",
	}

	bucket := attributes(m, "AWS::S3::Bucket", "receipts")
	for att, want := range map[string]string{
		"DomainName":         "receipts.s3.aws.harbour.doze",
		"RegionalDomainName": "receipts.s3.ap-south-1.aws.harbour.doze",
		"WebsiteURL":         "http://receipts.s3-website.ap-south-1.aws.harbour.doze",
	} {
		if got := bucket[att]; got != want {
			t.Errorf("S3 %s = %q, want %q", att, got, want)
		}
	}

	api := attributes(m, "AWS::ApiGatewayV2::Api", "x70an6eshc")
	if got, want := api["ApiEndpoint"], "http://x70an6eshc.execute-api.ap-south-1.aws.harbour.doze"; got != want {
		t.Errorf("ApiEndpoint = %q, want %q", got, want)
	}

	q := attributes(m, "AWS::SQS::Queue", "orders")
	if got, want := q["QueueUrl"], "http://sqs.ap-south-1.aws.harbour.doze/811690671382/orders"; got != want {
		t.Errorf("QueueUrl = %q, want %q", got, want)
	}

	_, ghost := ghostIdentity(m, "AWS::ECR::Repository", "app")
	if got, want := ghost["RepositoryUri"], "811690671382.dkr.ecr.ap-south-1.aws.harbour.doze/app"; got != want {
		t.Errorf("RepositoryUri = %q, want %q", got, want)
	}

	// Nothing may still claim an address doze-aws does not serve.
	for _, atts := range []map[string]string{bucket, api, q, ghost} {
		for name, v := range atts {
			if strings.Contains(v, "localhost") || strings.Contains(v, "127.0.0.1") {
				t.Errorf("%s = %q still names a local literal", name, v)
			}
		}
	}
}

// With no suffix — --listen, where doze-aws has no hostname space of its own —
// every one of these must still RESOLVE. Fn::GetAtt on an absent attribute is
// an error, and for the ignored tier the whole point is that a reference
// resolves rather than exploding.
//
// Two different fallbacks, because the attributes differ in kind. ApiEndpoint
// and QueueUrl have a working local form (a path under the endpoint), so they
// use it. The pure hostnames do not, so they fall back to AWS's real domain —
// what the attribute MEANS, and not an address doze-aws pretends to serve.
func TestHostShapedAttributesStillResolveWithNoSuffix(t *testing.T) {
	m := minting{
		id:       awsident.Identity{Region: "us-east-1"},
		endpoint: "http://127.0.0.1:4566",
	}

	for _, tc := range []struct{ typ, name, att, want string }{
		{"AWS::S3::Bucket", "receipts", "DomainName", "receipts.s3.amazonaws.com"},
		{"AWS::ApiGatewayV2::Api", "abc", "ApiEndpoint", "http://127.0.0.1:4566/_aws/execute-api/abc"},
		{"AWS::SQS::Queue", "orders", "QueueUrl", "http://127.0.0.1:4566/000000000000/orders"},
	} {
		got := attributes(m, tc.typ, tc.name)[tc.att]
		if got != tc.want {
			t.Errorf("%s %s = %q, want %q", tc.typ, tc.att, got, tc.want)
		}
	}

	if _, ghost := ghostIdentity(m, "AWS::ECR::Repository", "app"); ghost["RepositoryUri"] == "" {
		t.Error("RepositoryUri must resolve — ${Repo.RepositoryUri} has to transpile")
	}
}
