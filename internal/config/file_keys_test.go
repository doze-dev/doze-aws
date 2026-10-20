package config

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doze-aws.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// LoadFile says which keys the file wrote down, by their flag spelling, so a
// flag that overrules one can say so. Each key is its own question: a key the
// file did not write is not reported, and one it did is.
func TestLoadFileReportsExactlyTheKeysItSet(t *testing.T) {
	for _, tc := range []struct {
		body string
		want string // the keys, sorted and joined
	}{
		{`listen = "127.0.0.1:1"`, "listen"},
		{`data-dir = "/d"`, "data-dir"},
		{`services = ["sts"]`, "services"},
		{`template = "t.yaml"`, "template"},
		{`region = "eu-west-1"`, "region"},
		{`account-id = "111122223333"`, "account-id"},
		{`name = "harbour"`, "name"},
		{"", ""},
		{"listen = \"x\"\nregion = \"r\"", "listen region"},
	} {
		c := Default()
		set, err := LoadFile(write(t, tc.body), &c)
		if err != nil {
			t.Fatalf("%q: %v", tc.body, err)
		}
		var got []string
		for k, v := range set {
			if v {
				got = append(got, k)
			}
		}
		sort.Strings(got)
		if strings.Join(got, " ") != tc.want {
			t.Errorf("file %q set %v, want %q", tc.body, got, tc.want)
		}
	}
}

// The Lambda table: quiet and runtimes are read, each on its own.
func TestLoadFileReadsTheLambdaTable(t *testing.T) {
	c := Default()
	if _, err := LoadFile(write(t, "[lambda]\nquiet = true\n"), &c); err != nil {
		t.Fatal(err)
	}
	if !c.LambdaQuiet || c.LambdaRuntimes != nil {
		t.Errorf("quiet alone: quiet %v, runtimes %v", c.LambdaQuiet, c.LambdaRuntimes)
	}
	c = Default()
	if _, err := LoadFile(write(t, "[lambda.runtimes]\npython3 = \"/usr/bin/python3\"\n"), &c); err != nil {
		t.Fatal(err)
	}
	if c.LambdaQuiet || c.LambdaRuntimes["python3"] != "/usr/bin/python3" {
		t.Errorf("runtimes alone: quiet %v, runtimes %v", c.LambdaQuiet, c.LambdaRuntimes)
	}
	// Neither written: both stay as they were.
	c = Default()
	c.LambdaQuiet = true
	if _, err := LoadFile(write(t, "[lambda]\n"), &c); err != nil {
		t.Fatal(err)
	}
	if !c.LambdaQuiet {
		t.Error("an empty [lambda] table switched quiet off")
	}
}

func TestTheDefaultLambdaIdleTimeoutIsTenMinutes(t *testing.T) {
	if got := Default().LambdaIdleTimeout; got != 10*time.Minute {
		t.Errorf("LambdaIdleTimeout = %v, want 10m", got)
	}
}

// config print writes no services key when none were chosen: an empty list in
// the file would read back as "run no services", not "run the default ones".
func TestWriteTOMLLeavesOutAnEmptyServiceList(t *testing.T) {
	c := Default()
	c.Services = nil
	var buf bytes.Buffer
	if err := WriteTOML(&buf, c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "services") {
		t.Errorf("the default list was written down:\n%s", buf.String())
	}
	c.Services = []string{}
	buf.Reset()
	if err := WriteTOML(&buf, c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "services") {
		t.Errorf("an empty list was written down:\n%s", buf.String())
	}
}
