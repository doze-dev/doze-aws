package console

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRecorderPreservesFormBody: the recorder must never consume the request
// body it wraps. classify once called r.ParseForm() on form-encoded Query
// requests (SigV2-era SNS/STS/legacy-SQS put Action in the body), draining
// r.Body so the gateway's own body-Action routing found nothing and every such
// request fell through to the S3 404/405 fallback.
func TestRecorderPreservesFormBody(t *testing.T) {
	var seen string
	rec := NewRecorder(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
	}))
	form := "Action=ListTopics&Version=2010-03-31"
	r := httptest.NewRequest("POST", "/", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec.ServeHTTP(httptest.NewRecorder(), r)
	if seen != form {
		t.Fatalf("recorder consumed the form body: handler saw %q, want %q", seen, form)
	}
	// And the classification still names the action from the captured copy.
	entries := rec.Entries(0)
	if len(entries) != 1 || entries[0].Action != "ListTopics" || entries[0].Service != "sns" {
		t.Fatalf("classification lost the body action: %+v", entries)
	}
}

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
