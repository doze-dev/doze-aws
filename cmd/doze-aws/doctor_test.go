package main

import (
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/config"
)

// doctor says where this instance answers and whether something answers there:
// the two things to know when an SDK says it cannot connect.
func TestDoctorSaysWhereItAnswersAndWhetherAnythingDoes(t *testing.T) {
	var b strings.Builder
	code := doctorTo(&b, config.Default(), func(string) bool { return false })
	out := b.String()
	if code != 0 {
		t.Errorf("exit = %d; being told what is missing is the command WORKING", code)
	}
	for _, want := range []string{"answers on  127.0.0.1:4566", "nothing answers at 127.0.0.1:4566", "suffix      (none"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ".doze") || strings.Contains(out, "dns-setup") {
		t.Errorf("the standalone binary has no names to report:\n%s", out)
	}

	b.Reset()
	cfg := config.Default()
	cfg.Suffix = "aws.example.test"
	doctorTo(&b, cfg, func(string) bool { return true })
	out = b.String()
	if !strings.Contains(out, "✓ something answers at 127.0.0.1:4566") || !strings.Contains(out, "aws.example.test (--suffix)") {
		t.Errorf("a running instance with a suffix:\n%s", out)
	}
}
