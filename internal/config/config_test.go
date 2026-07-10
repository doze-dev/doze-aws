package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	c := Default()
	c.ListenAddr = ""
	if err := c.Validate(); err == nil {
		t.Error("empty listen address accepted")
	}

	c = Default()
	c.Services = []string{"sts", "nope"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown service: err = %v", err)
	}
}

func TestLoadFileOverlay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze-aws.toml")
	os.WriteFile(path, []byte("listen = \"127.0.0.1:9999\"\nservices = [\"sts\", \"sqs\"]\n\n[s3]\nhost = \"s3.test\"\n"), 0o644)

	c := Default()
	if err := LoadFile(path, &c); err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	if c.DataDir != Default().DataDir {
		t.Errorf("DataDir overwritten to %q despite being absent from the file", c.DataDir)
	}
	if len(c.Services) != 2 || c.Services[1] != "sqs" {
		t.Errorf("Services = %v", c.Services)
	}
	if c.S3Host != "s3.test" {
		t.Errorf("S3Host = %q", c.S3Host)
	}
}

func TestLoadFileRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze-aws.toml")
	os.WriteFile(path, []byte("lisen = \"typo\"\n"), 0o644)

	c := Default()
	err := LoadFile(path, &c)
	if err == nil || !strings.Contains(err.Error(), "lisen") {
		t.Errorf("unknown key: err = %v", err)
	}
}

func TestWriteTOMLRoundTrips(t *testing.T) {
	orig := Default()
	orig.ListenAddr = "0.0.0.0:4566"
	orig.Services = []string{"sts"}

	var buf bytes.Buffer
	if err := WriteTOML(&buf, orig); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "rt.toml")
	os.WriteFile(path, buf.Bytes(), 0o644)
	got := Default()
	if err := LoadFile(path, &got); err != nil {
		t.Fatalf("re-reading WriteTOML output: %v\n%s", err, buf.String())
	}
	if got.ListenAddr != orig.ListenAddr || got.DataDir != orig.DataDir || got.S3Host != orig.S3Host || len(got.Services) != 1 {
		t.Errorf("round trip: got %+v, want %+v", got, orig)
	}
}
