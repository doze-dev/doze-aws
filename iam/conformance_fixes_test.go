package iam

// Refusals the boto3 conformance suite found missing (conformance/tests/test_iam.py).
// None is in the service model, so the model-derived audit could not see them.

import (
	"strings"
	"testing"
)

const (
	trustShape    = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	identityShape = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`
)

// A trust policy names who; an identity policy names what. IAM refuses each
// in the other's place, and this used to accept both in both.
func TestPolicyDocumentsAreHeldToTheirShape(t *testing.T) {
	if _, aerr := parseTrustPolicy("AssumeRolePolicyDocument", trustShape); aerr != nil {
		t.Errorf("a trust policy as a trust policy: %v", aerr)
	}
	if _, aerr := parseIdentityPolicy(identityShape); aerr != nil {
		t.Errorf("an identity policy as an identity policy: %v", aerr)
	}
	for _, tc := range []struct {
		what, want string
		aerr       *apiError
	}{
		{"identity as trust", "Has prohibited field Resource",
			second(parseTrustPolicy("AssumeRolePolicyDocument", identityShape))},
		{"trust without a principal", "Missing required field Principal",
			second(parseTrustPolicy("AssumeRolePolicyDocument", `{"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole"}]}`))},
		{"trust as identity", "Policy document should not specify a principal.",
			second(parseIdentityPolicy(trustShape))},
		{"identity without a resource", "Policy statement must contain resources.",
			second(parseIdentityPolicy(`{"Statement":[{"Effect":"Allow","Action":"s3:GetObject"}]}`))},
	} {
		if tc.aerr == nil || tc.aerr.Code != "MalformedPolicyDocument" || tc.aerr.Message != tc.want {
			t.Errorf("%s: got %v, want MalformedPolicyDocument %q", tc.what, tc.aerr, tc.want)
		}
	}
	// NotResource counts as a resource; NotPrincipal as a principal.
	if _, aerr := parseIdentityPolicy(`{"Statement":[{"Effect":"Deny","Action":"*","NotResource":"arn:aws:s3:::keep"}]}`); aerr != nil {
		t.Errorf("NotResource: %v", aerr)
	}
}

func second(_ *document, aerr *apiError) *apiError { return aerr }

// The role a service-linked role creates has to pass the check its callers
// are now held to: its trust policy used to be a permissions statement.
func TestServiceLinkedRoleTrustsItsService(t *testing.T) {
	ts := iamServer(t)
	code, body := call(t, ts, "CreateServiceLinkedRole", map[string]any{"AWSServiceName": "elasticbeanstalk.amazonaws.com"})
	if code != 200 {
		t.Fatalf("CreateServiceLinkedRole = %d: %s", code, body)
	}
	_, body = call(t, ts, "GetRole", map[string]any{"RoleName": "AWSServiceRoleForElasticbeanstalk"})
	if !strings.Contains(body, "elasticbeanstalk.amazonaws.com") || strings.Contains(body, "Resource") {
		t.Fatalf("the role's trust policy does not name its service: %s", body)
	}
}
