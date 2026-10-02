package ssm

// Refusals the boto3 conformance suite found missing (conformance/tests/test_ssm.py).
// None is in the service model, so the model-derived audit could not see them.

import (
	"net/http"
	"strings"
	"testing"
)

func TestParameterNamesAreHeldToWhatSSMAccepts(t *testing.T) {
	for _, name := range []string{"plain", "/app/db/host", "/a.b-c_d/e", "/awsome/x", "my-aws-thing"} {
		if aerr := validParameterName(name); aerr != nil {
			t.Errorf("%q refused: %v", name, aerr)
		}
	}
	deep := "/" + strings.Repeat("a/", 15) + "b" // sixteen levels
	for _, name := range []string{
		"has space", "/app/has space", "/app//x", "/app/x/", "semi;colon",
		"aws-mine", "SSMthing", "/aws/mine", "/SSM/mine", deep,
	} {
		if aerr := validParameterName(name); aerr == nil || aerr.Code != "ValidationException" {
			t.Errorf("%q accepted (%v)", name, aerr)
		}
	}
}

func TestPutParameterNeedsATypeOnlyTheFirstTime(t *testing.T) {
	ts := ssmServer(t)
	code, body := call(t, ts, "PutParameter", map[string]any{"Name": "/t/x", "Value": "v"})
	if code != http.StatusBadRequest || !strings.Contains(body, "A parameter type is required") {
		t.Fatalf("create without Type = %d: %s", code, body)
	}
	if code, body := call(t, ts, "GetParameter", map[string]any{"Name": "/t/x"}); code == http.StatusOK {
		t.Fatalf("the refused put created the parameter anyway: %s", body)
	}
	if code, body := call(t, ts, "PutParameter", map[string]any{"Name": "/t/x", "Value": "v", "Type": "SecureString"}); code != http.StatusOK {
		t.Fatalf("create = %d: %s", code, body)
	}
	// An overwrite inherits the type, and a SecureString stays one.
	if code, body := call(t, ts, "PutParameter", map[string]any{"Name": "/t/x", "Value": "w", "Overwrite": true}); code != http.StatusOK {
		t.Fatalf("overwrite without Type = %d: %s", code, body)
	}
	_, body = call(t, ts, "GetParameter", map[string]any{"Name": "/t/x"})
	if !strings.Contains(body, `"Type":"SecureString"`) || strings.Contains(body, `"Value":"w"`) {
		t.Fatalf("the overwrite changed the type or left the value in the clear: %s", body)
	}
}

func TestGetParametersByPathNeedsAPath(t *testing.T) {
	ts := ssmServer(t)
	code, body := call(t, ts, "GetParametersByPath", map[string]any{"Path": "app/db"})
	if code != http.StatusBadRequest || !strings.Contains(body, "must begin with a forward slash") {
		t.Fatalf("a path without its slash = %d: %s", code, body)
	}
	// Reading under /aws is allowed: that is where the public parameters are.
	if code, body := call(t, ts, "GetParametersByPath", map[string]any{"Path": "/aws/service"}); code != http.StatusOK {
		t.Fatalf("reading /aws = %d: %s", code, body)
	}
}
