package main

import (
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/config"
	names "github.com/doze-dev/doze-names"
)

type fakeRegistry map[string]names.Entry

func (f fakeRegistry) Snapshot() map[string]names.Entry { return f }

// The case doctor exists for: something is missing, and the operator needs to
// know WHICH thing and what to run. A report that says only "not working" is
// the error they already had.
func TestDoctorNamesWhatIsMissingAndHowToFixIt(t *testing.T) {
	var b strings.Builder
	status := names.Status{Platform: "darwin", Steps: []names.Step{
		{Name: "loopback pool", Done: true, Detail: "127.0.0.2-65 aliased on lo0"},
		{Name: "resolver route", Done: false, Detail: "/etc/resolver/doze missing"},
	}}
	code := doctorTo(&b, config.Default(), status, fakeRegistry{})
	out := b.String()

	if code != 0 {
		t.Errorf("exit = %d; being told what is missing is the command WORKING", code)
	}
	if !strings.Contains(out, "✗ resolver route") {
		t.Errorf("the missing step is not marked:\n%s", out)
	}
	if !strings.Contains(out, "✓ loopback pool") {
		t.Errorf("the present step is not marked:\n%s", out)
	}
	for _, want := range []string{"dns-setup", "--print", "--listen"} {
		if !strings.Contains(out, want) {
			t.Errorf("no remedy mentions %q:\n%s", want, out)
		}
	}
}

// "My name stopped working" is almost always another process holding it, and
// that is invisible from the SDK's error. The registry knows, so doctor says.
func TestDoctorNamesTheHolder(t *testing.T) {
	var b strings.Builder
	reg := fakeRegistry{
		"aws.doze": {IP: "127.0.0.2", PID: 4242, Owner: "doze-kafka", Target: "127.0.0.2:4566"},
	}
	doctorTo(&b, config.Default(), names.Status{Platform: "darwin"}, reg)
	out := b.String()
	for _, want := range []string{"aws.doze", "doze-kafka", "4242", "127.0.0.2:4566"} {
		if !strings.Contains(out, want) {
			t.Errorf("holder report is missing %q:\n%s", want, out)
		}
	}
}

// With no suffix the AWS-shaped hostnames are off, and saying so beats leaving
// a blank line someone has to interpret.
func TestDoctorSaysWhenThereIsNoSuffix(t *testing.T) {
	var b strings.Builder
	doctorTo(&b, config.Default(), names.Status{Platform: "darwin"}, fakeRegistry{})
	if !strings.Contains(b.String(), "--suffix") {
		t.Errorf("an absent suffix is not explained:\n%s", b.String())
	}
}
