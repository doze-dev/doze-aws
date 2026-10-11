package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQSStrictIsOffByDefaultAndReadFromTheFile(t *testing.T) {
	c := Default()
	if c.SQSStrict {
		t.Fatal("strict SQS is on by default")
	}
	path := filepath.Join(t.TempDir(), "doze-aws.toml")
	os.WriteFile(path, []byte("[sqs]\nstrict = true\n"), 0o644)
	if _, err := LoadFile(path, &c); err != nil {
		t.Fatal(err)
	}
	if !c.SQSStrict {
		t.Error("[sqs] strict = true was not read")
	}
}
