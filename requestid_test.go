package dozeaws_test

// One request, one id — on the wire, in the console's Traffic row, and in the
// log when it is a fault.
//
// Before this, awshttp.RequestID() was called at RESPONSE-WRITE time in ten
// places, each minting a fresh UUID. The id a client received had therefore
// never existed anywhere else: nothing logged it, nothing recorded it, and the
// only thing you could do with "my call failed with request id 7f3a…" was read
// it back to the person who told you.
//
// These assertions are cheap, which is rather the point — the old behaviour
// was not subtle, it was just never asserted.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/console"
)

func idTestStack(t *testing.T, logf func(string, ...any)) *httptest.Server {
	t.Helper()
	if logf == nil {
		logf = func(string, ...any) {}
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func idTestConfig() aws.Config {
	return aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
}

// The header id and the body id are the same value.
//
// S3's error document carries both, and they used to be minted independently —
// in fact the error path set no header at all, so a client reading
// x-amz-request-id off a failure, which is exactly when you want it, got
// nothing.
func TestAnS3ErrorAgreesWithItsOwnRequestID(t *testing.T) {
	ts := idTestStack(t, nil)

	resp, err := http.Get(ts.URL + "/no-such-bucket-here/key")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	header := resp.Header.Get("x-amz-request-id")
	if header == "" {
		t.Fatal("an S3 error carried no x-amz-request-id header")
	}
	if !strings.Contains(body, "<RequestId>"+header+"</RequestId>") {
		t.Errorf("header id %q is not the id in the body:\n%s", header, body)
	}
}

// Each protocol writer stamps an id. One request per wire format: S3's XML,
// DynamoDB's AWS JSON 1.0, SQS's Query.
func TestEveryProtocolStampsARequestID(t *testing.T) {
	ts := idTestStack(t, nil)
	cfg := idTestConfig()
	_ = cfg

	for _, tc := range []struct {
		name   string
		header string
		build  func() *http.Request
	}{
		{"s3 (XML)", "x-amz-request-id", func() *http.Request {
			r, _ := http.NewRequest("GET", ts.URL+"/no-such-bucket-here/key", nil)
			return r
		}},
		{"dynamodb (AWS JSON)", "x-amzn-RequestId", func() *http.Request {
			r, _ := http.NewRequest("POST", ts.URL+"/", strings.NewReader(`{}`))
			r.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
			r.Header.Set("Content-Type", "application/x-amz-json-1.0")
			return r
		}},
		{"sqs (Query)", "x-amzn-RequestId", func() *http.Request {
			r, _ := http.NewRequest("POST", ts.URL+"/",
				strings.NewReader("Action=ListQueues&Version=2012-11-05"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Authorization",
				"AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260913/us-east-1/sqs/aws4_request, "+
					"SignedHeaders=host, Signature=deadbeef")
			return r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.DefaultClient.Do(tc.build())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body) //nolint:errcheck // draining
			if got := resp.Header.Get(tc.header); got == "" {
				t.Errorf("no %s on a %d response", tc.header, resp.StatusCode)
			}
		})
	}
}

// The id on the wire is the id in the Traffic row — the join that makes an id
// a colleague quotes findable in the console.
func TestTheTrafficRowCarriesTheWireRequestID(t *testing.T) {
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	rec := console.NewRecorder(stack.Handler(), awsident.Default())
	ts := httptest.NewServer(rec)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/no-such-bucket-here/key")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wire := resp.Header.Get("x-amz-request-id")
	if wire == "" {
		t.Fatal("no request id on the wire")
	}

	entries := rec.Entries(0)
	if len(entries) == 0 {
		t.Fatal("the recorder captured nothing")
	}
	last := entries[len(entries)-1]
	if last.RequestID != wire {
		t.Errorf("Traffic row id = %q, wire id = %q — the console cannot find a call by the id its caller was given",
			last.RequestID, wire)
	}
}

// A REFUSAL must not log a fault line. Without this the line would appear on
// every mistyped bucket name and stop meaning anything.
//
// The positive case — a 5xx does log its id — is asserted in
// internal/awshttp, where a fault can be produced directly instead of
// contrived through a service.
func TestARefusalDoesNotLogAFaultLine(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	ts := idTestStack(t, func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	})

	resp, err := http.Get(ts.URL + "/no-such-bucket-here/key")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 4 {
		t.Fatalf("expected a 4xx for a missing bucket, got %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, l := range lines {
		if strings.Contains(l, "answered") && strings.Contains(l, "request id") {
			t.Errorf("a %d logged a fault line:\n  %s", resp.StatusCode, l)
		}
	}
}
