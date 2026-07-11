package dozeaws_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
)

// These tests prove the "older code can get on board" promise at the WIRE level:
// requests signed the legacy way (SigV2 header form and the SigV2 presigned
// query form) must flow through the full Stack to the real handlers and back —
// not merely route. doze-aws parses signatures without verifying them, so the
// point is that an old-style request is ACCEPTED and served, while an expired
// presigned one is still rejected.

func stackServer(t *testing.T) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a Stack over HTTP")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestProtocolSigV2HeaderS3 drives a plain (non-chunked) S3 put/get signed with
// the legacy SigV2 `Authorization: AWS <AKID>:<sig>` header — the form old boto2
// / SDK-v1-with-SigV2 / signing-tool clients emit.
func TestProtocolSigV2HeaderS3(t *testing.T) {
	ts := stackServer(t)

	// Create the bucket (path-style), SigV2-signed.
	do(t, sigV2Header(req(t, "PUT", ts.URL+"/legacy-bucket", "")), 200)

	// Plain body upload with UNSIGNED-PAYLOAD (no aws-chunked framing) — the
	// simplest thing an old client sends.
	put := sigV2Header(req(t, "PUT", ts.URL+"/legacy-bucket/obj.txt", "old-client-payload"))
	put.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")
	do(t, put, 200)

	// Read it back, also SigV2-signed, and assert the real bytes.
	rec := do(t, sigV2Header(req(t, "GET", ts.URL+"/legacy-bucket/obj.txt", "")), 200)
	if rec != "old-client-payload" {
		t.Fatalf("SigV2 GET body = %q", rec)
	}
}

// TestProtocolSigV2PresignedS3 proves the legacy presigned query form
// (AWSAccessKeyId/Signature/Expires) is accepted when live and rejected when
// expired — end to end through the real S3 handler.
func TestProtocolSigV2PresignedS3(t *testing.T) {
	ts := stackServer(t)
	do(t, sigV2Header(req(t, "PUT", ts.URL+"/presign-bucket", "")), 200)
	put := sigV2Header(req(t, "PUT", ts.URL+"/presign-bucket/k", "data"))
	put.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")
	do(t, put, 200)

	// Live SigV2 presigned GET (Expires far in the future) — no auth header.
	live := ts.URL + "/presign-bucket/k?" + url.Values{
		"AWSAccessKeyId": {"AKID"}, "Signature": {"x"}, "Expires": {"4102444800"}, // 2100
	}.Encode()
	if b := do(t, req(t, "GET", live, ""), 200); b != "data" {
		t.Fatalf("live SigV2 presigned body = %q", b)
	}

	// Expired SigV2 presigned GET (Expires in 2006) must be refused with 403.
	expired := ts.URL + "/presign-bucket/k?" + url.Values{
		"AWSAccessKeyId": {"AKID"}, "Signature": {"x"}, "Expires": {"1141889120"},
	}.Encode()
	do(t, req(t, "GET", expired, ""), 403)
}

// TestProtocolLegacyQuerySQS exercises the full legacy path: SQS's Query
// (form-encoded) protocol AND a SigV2 header, together, as an old aws-sdk-go /
// boto client would send — through to real queue state and back.
func TestProtocolLegacyQuerySQS(t *testing.T) {
	ts := stackServer(t)

	form := func(v url.Values) string {
		r := req(t, "POST", ts.URL+"/", v.Encode())
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return do(t, sigV2Header(r), 200)
	}

	form(url.Values{"Action": {"CreateQueue"}, "QueueName": {"legacy-q"}})
	qurl := ts.URL + "/000000000000/legacy-q"
	form(url.Values{"Action": {"SendMessage"}, "QueueUrl": {qurl}, "MessageBody": {"legacy-body"}})
	body := form(url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {qurl}})
	if !strings.Contains(body, "<Body>legacy-body</Body>") {
		t.Fatalf("legacy Query ReceiveMessage XML missing body:\n%s", body)
	}
}

// ---- helpers ----

func req(t *testing.T, method, urlStr, body string) *http.Request {
	t.Helper()
	var r *http.Request
	var err error
	if body == "" {
		r, err = http.NewRequestWithContext(context.Background(), method, urlStr, nil)
	} else {
		r, err = http.NewRequestWithContext(context.Background(), method, urlStr, strings.NewReader(body))
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// sigV2Header stamps the legacy SigV2 authorization header. The signature is not
// verified (doze-aws parses, never verifies), so a placeholder is fine — what
// matters is that its PRESENCE is accepted and correctly routed.
func sigV2Header(r *http.Request) *http.Request {
	r.Header.Set("Authorization", "AWS AKID:bGVnYWN5c2ln")
	r.Header.Set("Date", "Tue, 10 Jul 2026 12:00:00 GMT")
	return r
}

func do(t *testing.T, r *http.Request, wantStatus int) string {
	t.Helper()
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s -> %d (want %d)\n%s", r.Method, r.URL.Path, resp.StatusCode, wantStatus, b)
	}
	return string(b)
}
