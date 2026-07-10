package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestGateway registers a recording handler for every canonical service and
// returns the gateway plus a pointer to the last-routed service name.
func newTestGateway(t *testing.T) (*Gateway, *string) {
	t.Helper()
	var hit string
	g := New(Options{Now: func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) }})
	for _, svc := range Services {
		g.Register(svc, func(name string) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit = name
				// Echo the body so tests can assert it survived any peeking.
				io.Copy(w, r.Body)
			})
		}(svc))
	}
	return g, &hit
}

func route(t *testing.T, g *Gateway, r *http.Request) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, r)
	return rec, ""
}

func TestRouteByTarget(t *testing.T) {
	g, hit := newTestGateway(t)
	cases := map[string]string{
		"AmazonSQS.SendMessage":       "sqs",
		"DynamoDB_20120810.GetItem":   "dynamodb",
		"TrentService.Encrypt":        "kms",
		"AmazonSSM.GetParameter":      "ssm",
		"secretsmanager.CreateSecret": "secretsmanager",
		"AWSEvents.PutEvents":         "eventbridge",
	}
	for target, want := range cases {
		r := httptest.NewRequest("POST", "/", strings.NewReader("{}"))
		r.Header.Set("X-Amz-Target", target)
		route(t, g, r)
		if *hit != want {
			t.Errorf("target %s routed to %q, want %q", target, *hit, want)
		}
	}
}

func TestRouteBySigV4Scope(t *testing.T) {
	g, hit := newTestGateway(t)
	cases := map[string]string{
		"sns":            "sns",
		"sts":            "sts",
		"events":         "eventbridge",
		"lambda":         "lambda",
		"s3":             "s3",
		"secretsmanager": "secretsmanager",
	}
	for scope, want := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Authorization",
			"AWS4-HMAC-SHA256 Credential=AKID/20260710/us-east-1/"+scope+"/aws4_request, SignedHeaders=host, Signature=x")
		route(t, g, r)
		if *hit != want {
			t.Errorf("scope %s routed to %q, want %q", scope, *hit, want)
		}
	}
}

func TestRouteByLambdaPath(t *testing.T) {
	g, hit := newTestGateway(t)
	r := httptest.NewRequest("GET", "/2015-03-31/functions/my-fn", nil)
	route(t, g, r)
	if *hit != "lambda" {
		t.Errorf("routed to %q, want lambda", *hit)
	}
}

func TestRouteByQueryActionPreservesBody(t *testing.T) {
	g, hit := newTestGateway(t)

	// SigV2-signed Query POST: no scope, no target — the Action decides, and
	// the body must reach the handler intact after the peek.
	body := "Action=GetCallerIdentity&Version=2011-06-15"
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "AWS AKID:sig")
	rec, _ := route(t, g, r)
	if *hit != "sts" {
		t.Errorf("routed to %q, want sts", *hit)
	}
	if rec.Body.String() != body {
		t.Errorf("handler saw body %q, want %q", rec.Body.String(), body)
	}

	// SNS action, in the URL query of a GET.
	r = httptest.NewRequest("GET", "/?Action=ListTopics", nil)
	route(t, g, r)
	if *hit != "sns" {
		t.Errorf("routed to %q, want sns", *hit)
	}

	// Legacy SQS action.
	r = httptest.NewRequest("POST", "/", strings.NewReader("Action=SendMessage&QueueUrl=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	route(t, g, r)
	if *hit != "sqs" {
		t.Errorf("routed to %q, want sqs", *hit)
	}
}

func TestRouteFallbackS3(t *testing.T) {
	g, hit := newTestGateway(t)
	r := httptest.NewRequest("PUT", "/my-bucket/some/key", strings.NewReader("payload"))
	rec, _ := route(t, g, r)
	if *hit != "s3" {
		t.Errorf("routed to %q, want s3", *hit)
	}
	if rec.Body.String() != "payload" {
		t.Errorf("body not preserved: %q", rec.Body.String())
	}
}

func TestUnregisteredService(t *testing.T) {
	g := New(Options{})
	g.Register("sts", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	r := httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	r.Header.Set("X-Amz-Target", "DynamoDB_20120810.GetItem")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, r)
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "dynamodb") {
		t.Errorf("error body should name the service: %s", rec.Body.String())
	}
}

func TestExpiredPresignedRejected(t *testing.T) {
	g, hit := newTestGateway(t)
	// Expired SigV2 presigned URL (Expires in 2006), would otherwise route to s3.
	r := httptest.NewRequest("GET", "/bucket/key?AWSAccessKeyId=AKID&Signature=x&Expires=1141889120", nil)
	rec, _ := route(t, g, r)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if *hit != "" {
		t.Errorf("expired request still reached %q", *hit)
	}

	// A live presigned URL passes through.
	r = httptest.NewRequest("GET", "/bucket/key?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKID%2F20260710%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20260710T115500Z&X-Amz-Expires=3600", nil)
	rec, _ = route(t, g, r)
	if rec.Code != 200 || *hit != "s3" {
		t.Errorf("live presigned: status = %d, hit = %q", rec.Code, *hit)
	}
}

func TestKnownService(t *testing.T) {
	for _, s := range Services {
		if !KnownService(s) {
			t.Errorf("KnownService(%q) = false", s)
		}
	}
	if KnownService("nope") {
		t.Error("KnownService(nope) = true")
	}
}

// TestQueryActionTableConsistency guards the routing table against typos: every
// value must be a canonical service.
func TestQueryActionTableConsistency(t *testing.T) {
	for action, svc := range queryActions {
		if !KnownService(svc) {
			t.Errorf("queryActions[%q] = %q is not a known service", action, svc)
		}
	}
	for prefix, svc := range targetPrefixes {
		if !KnownService(svc) {
			t.Errorf("targetPrefixes[%q] = %q is not a known service", prefix, svc)
		}
	}
	for scope, svc := range scopeServices {
		if !KnownService(svc) {
			t.Errorf("scopeServices[%q] = %q is not a known service", scope, svc)
		}
	}
}
