package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

// An empty listen address is the DEFAULT now, not an error: doze-aws answers
// on its .doze name and --listen adds an address alongside it. This used to
// assert the opposite, which was right when 127.0.0.1:4566 was the contract.
func TestAnEmptyListenAddressIsTheDefault(t *testing.T) {
	if got := Default().ListenAddr; got != "" {
		t.Errorf("Default().ListenAddr = %q, want empty — the name is the address", got)
	}
	c := Default()
	c.ListenAddr = ""
	if err := c.Validate(); err != nil {
		t.Errorf("an empty listen address must validate: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	c := Default()
	c.Services = []string{"sts", "nope"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown service: err = %v", err)
	}
}

func TestLoadFileOverlay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze-aws.toml")
	os.WriteFile(path, []byte("listen = \"127.0.0.1:9999\"\nservices = [\"sts\", \"sqs\"]\n"), 0o644)

	c := Default()
	if _, err := LoadFile(path, &c); err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	// An ABSENT data-dir means "beside the config file", not "leave the
	// default alone". This assertion used to be the opposite, which was right
	// while the working directory was the anchor for everything.
	if want := filepath.Join(filepath.Dir(path), "data"); c.DataDir != want {
		t.Errorf("DataDir = %q, want %q — an absent key means beside the file", c.DataDir, want)
	}
	if len(c.Services) != 2 || c.Services[1] != "sqs" {
		t.Errorf("Services = %v", c.Services)
	}
}

// A path written in a config file has to mean the same thing wherever the file
// is read from. Before this, `doze-aws --config /srv/harbour/doze-aws.toml`
// run from your home directory put the data in ~/data.
func TestDataDirIsAnchoredToTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doze-aws.toml")

	for _, tc := range []struct{ what, key, want string }{
		{"relative resolves against the file", "data-dir = \"store\"\n", filepath.Join(dir, "store")},
		{"absent means beside the file", "", filepath.Join(dir, "data")},
		{"absolute is left exactly as written", "data-dir = \"/mnt/aws-data\"\n", "/mnt/aws-data"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			os.WriteFile(path, []byte(tc.key), 0o644)
			c := Default()
			if _, err := LoadFile(path, &c); err != nil {
				t.Fatal(err)
			}
			if c.DataDir != tc.want {
				t.Errorf("DataDir = %q, want %q", c.DataDir, tc.want)
			}
		})
	}
}

// A key that USED to work is not a typo, and "unknown key" is not an answer.
// Somebody wrote [s3] host when it was real; the error has to say where the
// behaviour went, or they are left guessing whether the feature or the
// spelling changed.
func TestARemovedKeyNamesItsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze-aws.toml")
	os.WriteFile(path, []byte("[s3]\nhost = \"localhost\"\n"), 0o644)

	c := Default()
	_, err := LoadFile(path, &c)
	if err == nil {
		t.Fatal("[s3] host must not be silently ignored")
	}
	for _, want := range []string{"was removed", "suffix", "UsePathStyle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}

func TestLoadFileRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze-aws.toml")
	os.WriteFile(path, []byte("lisen = \"typo\"\n"), 0o644)

	c := Default()
	_, err := LoadFile(path, &c)
	if err == nil || !strings.Contains(err.Error(), "lisen") {
		t.Errorf("unknown key: err = %v", err)
	}
}

func TestWriteTOMLRoundTrips(t *testing.T) {
	orig := Default()
	orig.ListenAddr = "0.0.0.0:4566"
	orig.Services = []string{"sts"}
	orig.LambdaIdleTimeout = 90 * time.Second
	// An ABSOLUTE data dir, so the round trip is an identity.
	//
	// A relative one deliberately is not: "./data" means "beside this file",
	// so writing it here and reading it back from a temp directory resolves
	// somewhere else — correctly. That property has its own test
	// (TestDataDirIsAnchoredToTheConfigFile); this one is about the encoder.
	orig.DataDir = "/mnt/aws-data"

	var buf bytes.Buffer
	if err := WriteTOML(&buf, orig); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "rt.toml")
	os.WriteFile(path, buf.Bytes(), 0o644)
	got := Default()
	if _, err := LoadFile(path, &got); err != nil {
		t.Fatalf("re-reading WriteTOML output: %v\n%s", err, buf.String())
	}
	if got.ListenAddr != orig.ListenAddr || got.DataDir != orig.DataDir || len(got.Services) != 1 || got.LambdaIdleTimeout != orig.LambdaIdleTimeout {
		t.Errorf("round trip: got %+v, want %+v", got, orig)
	}
}
