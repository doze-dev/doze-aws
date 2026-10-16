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

// The response to CreateFunction is built from the record just made, which was
// never given the instance's identity, so its FunctionArn and Role carried the
// default account and us-east-1 while Get and List (which read the stored
// record back through the store) were right.
func TestTheCreateResponseCarriesTheConfiguredIdentity(t *testing.T) {
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
		"FunctionName": "fresh", "Runtime": "provided.al2", "Handler": "bootstrap",
		"Role": "arn:aws:iam::111122223333:role/r",
		"Code": map[string]string{"S3Bucket": "_local_", "S3Key": code},
	})
	resp, err := http.Post(base+"/2015-03-31/functions", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("CreateFunction: %d %s", resp.StatusCode, out)
	}
	var cfg struct{ FunctionArn string }
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if want := "arn:aws:lambda:eu-west-1:111122223333:function:fresh"; cfg.FunctionArn != want {
		t.Errorf("the create response's FunctionArn = %q, want %q", cfg.FunctionArn, want)
	}
}

// Every response that shows a function names the configured account and Region,
// not only Get and the create: publishing a version and changing the
// configuration build their answer from a record too.
func TestEveryFunctionResponseCarriesTheConfiguredIdentity(t *testing.T) {
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
	do := func(method, path, body string) string {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, out)
		}
		return string(out)
	}
	create, _ := json.Marshal(map[string]any{
		"FunctionName": "every", "Runtime": "provided.al2", "Handler": "bootstrap",
		"Role": "arn:aws:iam::111122223333:role/r",
		"Code": map[string]string{"S3Bucket": "_local_", "S3Key": code},
	})
	do("POST", "/2015-03-31/functions", string(create))
	for name, out := range map[string]string{
		"UpdateFunctionConfiguration": do("PUT", "/2015-03-31/functions/every/configuration", `{"Description":"d"}`),
		"PublishVersion":              do("POST", "/2015-03-31/functions/every/versions", `{}`),
		"GetFunction":                 do("GET", "/2015-03-31/functions/every", ""),
		"ListFunctions":               do("GET", "/2015-03-31/functions", ""),
	} {
		if strings.Contains(out, "000000000000") || strings.Contains(out, "us-east-1") {
			t.Errorf("%s names the default account or Region: %.300s", name, out)
		}
	}
}

// A published version is read back from the versions bucket, and a record read
// back is given the instance's identity by the store, as a function is. The
// first PublishVersion was right (it copies the live record); a second one that
// found nothing changed, ListVersionsByFunction, and a Qualifier read all
// answered the default account and us-east-1.
func TestEveryReadOfAPublishedVersionCarriesTheConfiguredIdentity(t *testing.T) {
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
	do := func(method, path, body string) string {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, out)
		}
		return string(out)
	}
	create, _ := json.Marshal(map[string]any{
		"FunctionName": "ver", "Runtime": "provided.al2", "Handler": "bootstrap",
		"Role": "arn:aws:iam::111122223333:role/r",
		"Code": map[string]string{"S3Bucket": "_local_", "S3Key": code},
	})
	do("POST", "/2015-03-31/functions", string(create))
	do("POST", "/2015-03-31/functions/ver/versions", `{}`)
	for name, out := range map[string]string{
		"PublishVersion again":       do("POST", "/2015-03-31/functions/ver/versions", `{}`),
		"ListVersionsByFunction":     do("GET", "/2015-03-31/functions/ver/versions", ""),
		"GetFunction?Qualifier=1":    do("GET", "/2015-03-31/functions/ver?Qualifier=1", ""),
		"GetFunctionConfiguration:1": do("GET", "/2015-03-31/functions/ver/configuration?Qualifier=1", ""),
	} {
		if strings.Contains(out, "000000000000") || strings.Contains(out, "us-east-1") {
			t.Errorf("%s names the default account or Region: %.300s", name, out)
		}
	}
}
