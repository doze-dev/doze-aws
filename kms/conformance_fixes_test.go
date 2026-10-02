package kms

// Refusals the boto3 conformance suite found missing (conformance/tests/test_kms.py).
// None is in the service model, so the model-derived audit could not see them.

import "testing"

// alias/aws/ belongs to the keys AWS manages. A caller reads through those
// names and may not create, repoint or delete one.
func TestTheManagedAliasNamespaceIsNotWritable(t *testing.T) {
	if name, aerr := ownAliasName("alias/mine"); aerr != nil || name != "mine" {
		t.Errorf("alias/mine: %q, %v", name, aerr)
	}
	for _, alias := range []string{"alias/aws/mine", "alias/aws/s3"} {
		if _, aerr := ownAliasName(alias); aerr == nil || aerr.Code != "NotAuthorizedException" {
			t.Errorf("%s: got %v, want NotAuthorizedException", alias, aerr)
		}
		// Reading through one stays allowed.
		if _, aerr := aliasName(alias); aerr != nil {
			t.Errorf("reading %s: %v", alias, aerr)
		}
	}
	if _, aerr := ownAliasName("mine"); aerr == nil || aerr.Code != "ValidationException" {
		t.Errorf("no alias/ prefix: %v", aerr)
	}
}
