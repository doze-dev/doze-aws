package console

import (
	"strings"
	"testing"
)

// TestRedactSecrets: masked values must not leak, and — regression — redaction
// must terminate. It once looped forever because the search restarted from 0
// after every replacement while the key pattern survived the replacement,
// hanging every KMS/SSM/SecretsManager write that came through the recorder.
func TestRedactSecrets(t *testing.T) {
	cases := []struct{ in, wantGone, wantKept string }{
		{`{"Name":"app/config","SecretString":"hunter2"}`, "hunter2", "app/config"},
		{`{"KeyId":"k1","Plaintext":"aGVsbG8="}`, "aGVsbG8=", "k1"},
		{`{"Name":"/db/pass","Value":"pg-secret","Type":"SecureString"}`, "pg-secret", "SecureString"},
		{`Action=Publish&Message=hi&password=letmein&Version=2010`, "letmein", "Action=Publish"},
		// two secret fields in one body — the resume-after-mask path
		{`{"SecretString":"a1b2c3","ClientRequestToken":"tok","Password":"z9y8"}`, "a1b2c3", "ClientRequestToken"},
	}
	for _, c := range cases {
		got := redact(c.in) // terminating at all is half the assertion
		if strings.Contains(got, c.wantGone) {
			t.Errorf("redact(%q) leaked %q: %q", c.in, c.wantGone, got)
		}
		if !strings.Contains(got, c.wantKept) {
			t.Errorf("redact(%q) destroyed non-secret %q: %q", c.in, c.wantKept, got)
		}
	}
}
