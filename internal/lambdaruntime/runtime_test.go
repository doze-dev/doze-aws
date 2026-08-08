package lambdaruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeCommand(t *testing.T) {
	cases := []struct {
		runtime string
		handler string
		want    []string
		wantErr bool
	}{
		{"", "", []string{"./bootstrap"}, false},
		{"go", "main", []string{"./main"}, false},
		{"provided.al2", "bootstrap", []string{"./bootstrap"}, false},
		{"python3.12", "app.handler", []string{"python3", "-m", "awslambdaric", "app.handler"}, false},
		{"nodejs20.x", "index.handler", []string{"npx", "--yes", "aws-lambda-ric", "index.handler"}, false},
		{"java21", "example.Handler::run", []string{"java", "-cp", "./*:.", "com.amazonaws.services.lambda.runtime.api.client.AWSLambda", "example.Handler::run"}, false},
		{"ruby3.3", "app.LambdaFunction::Handler.process", []string{"aws_lambda_ric", "app.LambdaFunction::Handler.process"}, false},
		{"dotnet8", "Assembly::Type::Method", []string{"dotnet", "exec", "/opt/aws-lambda-ric.dll", "Assembly::Type::Method"}, false},
		{"cobol", "x", nil, true},
	}
	for _, c := range cases {
		got, err := runtimeCommand(c.runtime, c.handler)
		if c.wantErr {
			if err == nil {
				t.Errorf("runtime %q: expected error, got %v", c.runtime, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("runtime %q: unexpected error: %v", c.runtime, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("runtime %q: got %v, want %v", c.runtime, got, c.want)
		}
	}
}

// TestRelativeCodeDirResolvesAbsolutely is a regression from the API Gateway
// work. cmd.Dir is set to the code directory, and Go resolves a relative
// argv[0] against cmd.Dir — so a relative code dir made the child look for
// <dir>/<dir>/bootstrap and fail with "no such file or directory".
//
// It only bit code unpacked from a zip (every `sam deploy` / `cdk deploy`
// function) under a relative --data-dir, which is the default.
func TestRelativeCodeDirResolvesAbsolutely(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "bootstrap")
	if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Address the code dir RELATIVELY, as a default ./data deployment would.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, dir)
	if err != nil {
		t.Skipf("cannot express %s relative to %s", dir, wd)
	}

	r := &Runner{spec: Spec{
		Name: "fn", Handler: "bootstrap", Runtime: "provided.al2", Dir: rel,
	}, logf: func(string, ...any) {}}
	cmd, err := r.buildCommand("127.0.0.1:1")
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if !filepath.IsAbs(cmd.Path) {
		t.Fatalf("argv[0] = %q; it must be absolute, because cmd.Dir=%q would "+
			"otherwise resolve it a second time against itself", cmd.Path, cmd.Dir)
	}
	if _, err := os.Stat(cmd.Path); err != nil {
		t.Fatalf("resolved argv[0] %q does not exist: %v", cmd.Path, err)
	}
}
