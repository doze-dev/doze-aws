package lambda_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/lambda"
)

// A function stored without a role is shown with a role of its own account.
//
// CreateFunction requires Role today, so the record this is about is one made
// before that was enforced, and the way to make one here is to take the model's
// constraint tables away for the create. What is read back is the ordinary
// GetFunctionConfiguration.
func TestAFunctionWithoutARoleIsShownOneInItsOwnAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the service")
	}
	id := awsident.Identity{Region: "eu-west-1", AccountID: "111122223333"}
	s, err := lambda.New(lambda.Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	base := newTestServer(t, s)

	code := t.TempDir()
	if err := os.WriteFile(filepath.Join(code, "bootstrap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"FunctionName": "old", "Runtime": "provided.al2", "Handler": "bootstrap",
		"Code": map[string]string{"S3Bucket": "_local_", "S3Key": code},
	})
	restore := lambda.WithoutConstraintTables()
	resp, err := http.Post(base+"/2015-03-31/functions", "application/json", strings.NewReader(string(body)))
	restore()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("CreateFunction without a Role: %d %s", resp.StatusCode, out)
	}

	resp, err = http.Get(base + "/2015-03-31/functions/old/configuration")
	if err != nil {
		t.Fatal(err)
	}
	out, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var cfg struct{ Role, FunctionArn string }
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if want := "arn:aws:iam::111122223333:role/lambda-role"; cfg.Role != want {
		t.Errorf("Role = %q, want %q", cfg.Role, want)
	}
	if want := "arn:aws:lambda:eu-west-1:111122223333:function:old"; cfg.FunctionArn != want {
		t.Errorf("FunctionArn = %q, want %q", cfg.FunctionArn, want)
	}
}
