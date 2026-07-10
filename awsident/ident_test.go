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
