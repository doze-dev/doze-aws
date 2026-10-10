package awshttp_test

// A not-built refusal is HTTP 501, and the claim that carries the whole design
// is that no SDK retries a 501.
//
// If one did, a caller would wait through its backoff before seeing an answer
// that was never going to change, and the "refuse honestly" rule would turn
// into a slow failure. So the claim is proved here, against the SDKs this
// repository already tests with, before the rule is applied to any service.
//
// What is counted is requests that reach the server. One is the correct number.
// The same refusal as a 500 is driven alongside as a control: it must be
// retried, or the test could pass for the wrong reason (a client that never
// retries anything).
//
// boto3 and the JavaScript SDK are covered by the conformance harness
// (conformance/tests/test_wire.py), where those SDKs live.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	ddbv1 "github.com/aws/aws-sdk-go/service/dynamodb"
	snsv1 "github.com/aws/aws-sdk-go/service/sns"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/awsquery"
)

// answerer serves every request with err in the envelope write chooses, and
// counts them.
type answerer struct {
	hits  atomic.Int32
	write func(w http.ResponseWriter, e *awshttp.APIError)
	err   *awshttp.APIError
}

func (a *answerer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.hits.Add(1)
	a.write(w, a.err)
}

func (a *answerer) server(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(a)
	t.Cleanup(ts.Close)
	return ts
}

var (
	jsonAPI  = awsjson.API{TargetPrefix: "DynamoDB_20120810", JSONVersion: "1.0"}
	queryAPI = awsquery.API{XMLNS: "http://sns.amazonaws.com/doc/2010-03-31/"}
)

func notBuilt() *awshttp.APIError {
	return awshttp.NotBuilt(awshttp.Declined, "ListThings", "there is nothing for it to act on")
}

func serverFault() *awshttp.APIError {
	return &awshttp.APIError{Code: "InternalFailure", Status: 500, Message: "control"}
}

func v2cfg(url string) (aws.Config, func(*aws.Config)) {
	cfg := aws.Config{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		BaseEndpoint: aws.String(url),
	}
	return cfg, nil
}

func TestNoSDKRetriesANotBuiltRefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("drives the SDKs with their default backoff; skipped in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// One case per (SDK, envelope). The call is whatever the SDK makes of one
	// operation; its name does not matter, only that the reply is the refusal.
	type call func(url string) error
	cases := []struct {
		name  string
		write func(w http.ResponseWriter, e *awshttp.APIError)
		do    call
	}{
		{"go-v2 JSON 1.0 (dynamodb)", jsonAPI.WriteError, func(u string) error {
			cfg, _ := v2cfg(u)
			_, err := dynamodb.NewFromConfig(cfg).ListTables(ctx, &dynamodb.ListTablesInput{})
			return err
		}},
		{"go-v2 Query (sns)", queryAPI.WriteError, func(u string) error {
			cfg, _ := v2cfg(u)
			_, err := sns.NewFromConfig(cfg).ListTopics(ctx, &sns.ListTopicsInput{})
			return err
		}},
		{"go-v2 REST XML (s3)", func(w http.ResponseWriter, e *awshttp.APIError) {
			w.Header().Set("Content-Type", "application/xml")
			awshttp.NoteFault(w, "r1", e)
			w.WriteHeader(e.Status)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>` + e.Code + `</Code><Message>` + e.Message + `</Message></Error>`))
		}, func(u string) error {
			cfg, _ := v2cfg(u)
			_, err := s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true }).ListBuckets(ctx, &s3.ListBucketsInput{})
			return err
		}},
		{"go-v2 REST JSON (lambda)", func(w http.ResponseWriter, e *awshttp.APIError) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-amzn-errortype", e.Code)
			awshttp.NoteFault(w, "r1", e)
			w.WriteHeader(e.Status)
			_, _ = w.Write([]byte(`{"Type":"User","message":"` + e.Message + `"}`))
		}, func(u string) error {
			cfg, _ := v2cfg(u)
			_, err := lambda.NewFromConfig(cfg).ListFunctions(ctx, &lambda.ListFunctionsInput{})
			return err
		}},
		{"go-v1 JSON 1.0 (dynamodb)", jsonAPI.WriteError, func(u string) error {
			_, err := ddbv1.New(v1sess(t, u)).ListTablesWithContext(ctx, &ddbv1.ListTablesInput{})
			return err
		}},
		{"go-v1 Query (sns)", queryAPI.WriteError, func(u string) error {
			_, err := snsv1.New(v1sess(t, u)).ListTopicsWithContext(ctx, &snsv1.ListTopicsInput{})
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refusal := &answerer{write: c.write, err: notBuilt()}
			ts := refusal.server(t)
			if err := c.do(ts.URL); err == nil {
				t.Fatal("the call succeeded against a refusal")
			}
			if n := refusal.hits.Load(); n != 1 {
				t.Errorf("a 501 NotImplemented reached the server %d times; the SDK retried it. "+
					"A caller would wait through backoff for an answer that cannot change, so the "+
					"status has to be reconsidered before the rule spreads", n)
			}

			// The control: the same call answered with a 500 IS retried. If it
			// were not, the assertion above would prove nothing.
			fault := &answerer{write: c.write, err: serverFault()}
			ts2 := fault.server(t)
			_ = c.do(ts2.URL)
			if n := fault.hits.Load(); n < 2 {
				t.Errorf("control: a 500 reached the server %d time(s); this client does not retry "+
					"at all, so the 501 result above says nothing", n)
			}
		})
	}
}

func v1sess(t *testing.T, url string) *session.Session {
	t.Helper()
	s, err := session.NewSession(awsv1.NewConfig().
		WithRegion("us-east-1").
		WithEndpoint(url).
		WithMaxRetries(3).
		WithCredentials(credsv1.NewStaticCredentials("test", "test", "")))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The header is how the console and the model sweep recognise a refusal
// without reading the status, so it has to be there in every envelope.
func TestNotBuiltRefusalCarriesTheHeaderAndIsNotAFault(t *testing.T) {
	var faults atomic.Int32
	awshttp.SetFaultResponseHandler(func(string, *awshttp.APIError) { faults.Add(1) })
	t.Cleanup(func() { awshttp.SetFaultResponseHandler(nil) })

	for name, write := range map[string]func(http.ResponseWriter, *awshttp.APIError){
		"json":  jsonAPI.WriteError,
		"query": queryAPI.WriteError,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			write(rec, awshttp.NotBuilt(awshttp.Deferred, "StartThing", "nobody has built it"))
			if rec.Code != http.StatusNotImplemented {
				t.Errorf("status = %d, want 501", rec.Code)
			}
			if got := rec.Header().Get(awshttp.NotBuiltHeader); got != awshttp.Deferred {
				t.Errorf("%s = %q, want %q", awshttp.NotBuiltHeader, got, awshttp.Deferred)
			}
			body := rec.Body.String()
			if !contains(body, "NotImplemented") || !contains(body, "StartThing is not built in doze-aws: nobody has built it") {
				t.Errorf("body does not name the code and the operation: %s", body)
			}
		})
	}
	if n := faults.Load(); n != 0 {
		t.Errorf("a not-built refusal was reported as a server fault %d time(s); it is not one", n)
	}

	// And the opposite: a real 500 still is one, and carries no header.
	rec := httptest.NewRecorder()
	jsonAPI.WriteError(rec, serverFault())
	if faults.Load() != 1 || rec.Header().Get(awshttp.NotBuiltHeader) != "" {
		t.Errorf("a 500 must still be a fault with no header: faults=%d header=%q",
			faults.Load(), rec.Header().Get(awshttp.NotBuiltHeader))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
