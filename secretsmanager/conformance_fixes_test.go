package secretsmanager

// Refusals the boto3 conformance suite found missing (conformance/tests/test_secretsmanager.py).
// None is in the service model, so the model-derived audit could not see them.

import (
	"net/http"
	"strings"
	"testing"
)

func TestSecretNamesAreHeldToWhatSecretsManagerAccepts(t *testing.T) {
	ts := smServer(t)
	for _, name := range []string{"prod/db/password", "a_b+c=d.e@f-g"} {
		if code, body := call(t, ts, "secretsmanager.CreateSecret", map[string]any{"Name": name, "SecretString": "x"}); code != http.StatusOK {
			t.Errorf("CreateSecret(%q) = %d: %s", name, code, body)
		}
	}
	for _, name := range []string{"has space", "semi;colon", "quo\"te"} {
		code, body := call(t, ts, "secretsmanager.CreateSecret", map[string]any{"Name": name, "SecretString": "x"})
		if code != http.StatusBadRequest || !strings.Contains(body, "Invalid name.") {
			t.Errorf("CreateSecret(%q) = %d: %s", name, code, body)
		}
	}
}

// A secret has one value. Given a string and a binary together, both were
// stored, and which one a reader got depended on which it asked for.
func TestASecretValueIsAStringOrABinaryNotBoth(t *testing.T) {
	ts := smServer(t)
	both := map[string]any{"Name": "both", "SecretString": "s", "SecretBinary": "Yg=="}
	code, body := call(t, ts, "secretsmanager.CreateSecret", both)
	if code != http.StatusBadRequest || !strings.Contains(body, "InvalidParameterException") {
		t.Fatalf("CreateSecret with both = %d: %s", code, body)
	}
	call(t, ts, "secretsmanager.CreateSecret", map[string]any{"Name": "one", "SecretString": "s"})
	code, body = call(t, ts, "secretsmanager.PutSecretValue", map[string]any{"SecretId": "one", "SecretString": "s", "SecretBinary": "Yg=="})
	if code != http.StatusBadRequest || !strings.Contains(body, "InvalidParameterException") {
		t.Fatalf("PutSecretValue with both = %d: %s", code, body)
	}
}
