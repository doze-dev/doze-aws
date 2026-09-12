package awsident

import "testing"

func TestARN(t *testing.T) {
	got := ARN("sqs", "jobs")
	want := "arn:aws:sqs:" + Region + ":" + AccountID + ":jobs"
	if got != want {
		t.Fatalf("ARN = %q, want %q", got, want)
	}
}

func TestGlobalARN(t *testing.T) {
	// GlobalARN omits the region segment (IAM-style) but keeps the account.
	got := GlobalARN("iam", "role/app")
	want := "arn:aws:iam::" + AccountID + ":role/app"
	if got != want {
		t.Fatalf("GlobalARN = %q, want %q", got, want)
	}
	if r := ARN("iam", "role/app"); r == got {
		t.Fatal("ARN and GlobalARN should differ (region segment)")
	}
}

func TestIdentityConstants(t *testing.T) {
	if Region == "" || AccountID == "" || AccessKeyID == "" || SecretAccessKey == "" {
		t.Fatal("identity constants must be non-empty")
	}
}

// An instance's own identity is what the whole migration is for: two stacks in
// one process must mint ARNs for their own account, not for the default.
func TestAnIdentityMintsItsOwnARNs(t *testing.T) {
	id := Identity{Region: "ap-south-1", AccountID: "811690671382"}

	for _, tc := range []struct{ what, got, want string }{
		{"ARN", id.ARN("sqs", "orders"),
			"arn:aws:sqs:ap-south-1:811690671382:orders"},
		{"GlobalARN", id.GlobalARN("iam", "role/app"),
			"arn:aws:iam::811690671382:role/app"},
		{"FunctionURL, no endpoint", id.FunctionURL("abc", ""),
			"https://abc.lambda-url.ap-south-1.on.aws/"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.what, tc.got, tc.want)
		}
	}

	// The package-level form is the default, and must not follow the instance.
	if ARN("sqs", "orders") == id.ARN("sqs", "orders") {
		t.Error("package-level ARN followed a non-default identity; it must stay the default")
	}
}

// The zero value resolving to the defaults is what lets the tree migrate a
// package at a time — see the note on Identity. If that ever stops being true,
// every un-plumbed service starts minting arn:aws:sqs:::name.
func TestTheZeroIdentityIsTheDefault(t *testing.T) {
	var zero Identity
	if got, want := zero.ARN("sqs", "jobs"), ARN("sqs", "jobs"); got != want {
		t.Errorf("zero Identity ARN = %q, want %q", got, want)
	}
	// A half-set identity fills only what is missing, so configuring an account
	// without a region does not silently blank the region.
	half := Identity{AccountID: "811690671382"}
	if got, want := half.ARN("sqs", "jobs"), "arn:aws:sqs:"+Region+":811690671382:jobs"; got != want {
		t.Errorf("half-set Identity ARN = %q, want %q", got, want)
	}
}

// FunctionURLID is a hash of the name and must not move with the identity — a
// function URL has to address the same id after a redeploy, and a template
// computes it before the function exists.
func TestFunctionURLIDIgnoresIdentity(t *testing.T) {
	if a, b := FunctionURLID("orders"), FunctionURLID("orders"); a != b {
		t.Fatalf("FunctionURLID is not stable: %q vs %q", a, b)
	}
	if FunctionURLID("orders") == FunctionURLID("payments") {
		t.Fatal("FunctionURLID collided across names")
	}
}
