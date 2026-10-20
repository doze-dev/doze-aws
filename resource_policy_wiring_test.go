package dozeaws_test

// B9's ratchet. iampolicy.ValidateResourcePolicy is the one validator for a
// policy stored on a resource, and each service wires it in its own stage with
// its own code and wording (and, as KMS's review found, its own decision about
// which of the shared problems it refuses). Until a service does, a policy that
// is not a policy ("not json", {"hello":1}) is stored and the guard silently
// ignores it, so a mistyped Deny is inert.
//
// This holds, in both directions, which services call it: a service that wires
// it fails until it is taken off the pending list, and one that is not on the
// list must call it. The list only shrinks.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var resourcePolicyWiring = map[string]string{
	"sqs": "wired",
	// S3 and IAM validate what they store themselves, before and apart from the
	// shared function (a bucket policy names its own bucket; IAM's own create
	// is where the shared rules came from).
	"s3":  "own",
	"iam": "own",
	// Pending: each stage wires it. The finding ids are in the service documents.
	"sns":            "pending", // SNS-16
	"kinesis":        "pending", // KIN-3
	"kms":            "pending", // KMS-35: not every shared problem is refused (a key policy may omit Action and Resource)
	"secretsmanager": "pending", // SM-5
	"apigateway":     "pending", // AGW-7
}

func callsTheSharedValidator(t *testing.T, dir string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s: %v", dir, err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "iampolicy.ValidateResourcePolicy(") {
			return true
		}
	}
	return false
}

func TestEveryServiceThatStoresAResourcePolicyValidatesItOrIsOnTheList(t *testing.T) {
	for svc, state := range resourcePolicyWiring {
		calls := callsTheSharedValidator(t, svc)
		switch {
		case state == "wired" && !calls:
			t.Errorf("%s is marked wired and does not call iampolicy.ValidateResourcePolicy", svc)
		case state == "pending" && calls:
			t.Errorf("%s now calls iampolicy.ValidateResourcePolicy: mark it wired in resourcePolicyWiring (this is the progress being recorded)", svc)
		case state == "own" && calls:
			t.Errorf("%s now calls the shared validator: mark it wired", svc)
		}
	}
}
