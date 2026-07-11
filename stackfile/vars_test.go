package stackfile

import (
	"strings"
	"testing"
)

func TestVariableReferences(t *testing.T) {
	t.Setenv("SF_TEST_HOST", "db.internal")

	doc := `
vars:
  stage: dev
  prefix: app-${env:SF_TEST_HOST, x}

queues:
  ${var:stage}-orders: {}

parameters:
  /cfg/host: ${env:SF_TEST_HOST}
  /cfg/stage: ${var:stage}
  /cfg/missing-ok: ${env:SF_TEST_UNSET, fallback-value}
  /cfg/literal: $${env:not-a-ref}
  /cfg/prefixed: ${var:prefix}
`
	s, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Queues["dev-orders"]; !ok {
		t.Errorf("var in a map key did not expand: %+v", s.Queues)
	}
	want := map[string]string{
		"/cfg/host":       "db.internal",
		"/cfg/stage":      "dev",
		"/cfg/missing-ok": "fallback-value",
		"/cfg/literal":    "${env:not-a-ref}",
		"/cfg/prefixed":   "app-db.internal", // vars values expand ${env:...} themselves
	}
	for name, v := range want {
		if got := s.Parameters[name].Value; got != v {
			t.Errorf("%s = %q, want %q", name, got, v)
		}
	}

	// --var overrides beat the file's vars block.
	s2, err := ParseWithVars([]byte(doc), map[string]string{"stage": "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Queues["ci-orders"]; !ok {
		t.Errorf("--var override did not win: %+v", s2.Queues)
	}

	// Unset without a default is a loud, collected error.
	_, err = Parse([]byte("parameters:\n  /a: ${env:SF_TEST_UNSET}\n  /b: ${var:nope}\n"))
	if err == nil {
		t.Fatal("want unresolved-reference error")
	}
	for _, want := range []string{"${env:SF_TEST_UNSET}", "${var:nope}", "--var"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}
