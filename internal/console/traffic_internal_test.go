package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

// JSON-protocol SQS clients may address by QueueUrl or bare QueueName; the
// wire's resource column must survive both.
func TestJSONResourceQueueName(t *testing.T) {
	if got := jsonResource("sqs", `{"QueueUrl":"http://host/sqs/emails"}`); got != "emails" {
		t.Fatalf("QueueUrl: got %q", got)
	}
	if got := jsonResource("sqs", `{"QueueName":"emails","WaitTimeSeconds":2}`); got != "emails" {
		t.Fatalf("QueueName: got %q", got)
	}
}

// TestClassifyUsesGatewayRouting is the regression for a class of bug, not a
// single case: the traffic tail used to classify requests with its own
// heuristics, which drifted from the gateway's actual routing.
//
// Two of those drifts were live. IAM was reported as STS, because the sts rule
// matched "Role" in CreateRole/AttachRolePolicy. API Gateway was reported as
// S3 with action GetObject, because /restapis fell through to the path-style
// catch-all. A debugging tool that misattributes requests is worse than one
// that shows nothing.
func TestClassifyUsesGatewayRouting(t *testing.T) {
	sign := func(r *http.Request, service string) *http.Request {
		r.Header.Set("Authorization",
			"AWS4-HMAC-SHA256 Credential=test/20240101/us-east-1/"+service+"/aws4_request, "+
				"SignedHeaders=host, Signature=x")
		return r
	}
	form := func(path, body, service string) *http.Request {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return sign(r, service)
	}
	target := func(t, body, service string) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("X-Amz-Target", t)
		return sign(r, service)
	}

	cases := []struct {
		name    string
		req     *http.Request
		body    string
		wantSvc string
		wantAct string
		wantRes string
	}{
		{
			// The bug: "Role" matched the sts rule.
			name:    "IAM is not STS",
			req:     form("/", "Action=CreateRole&RoleName=lambda-exec", "iam"),
			body:    "Action=CreateRole&RoleName=lambda-exec",
			wantSvc: "iam", wantAct: "CreateRole", wantRes: "lambda-exec",
		},
		{
			name:    "STS is still STS",
			req:     form("/", "Action=GetCallerIdentity", "sts"),
			body:    "Action=GetCallerIdentity",
			wantSvc: "sts", wantAct: "GetCallerIdentity",
		},
		{
			// The bug: /restapis hit the S3 path-style fallback.
			name:    "API Gateway control plane is not S3",
			req:     sign(httptest.NewRequest("GET", "/restapis/abc123", nil), "apigateway"),
			wantSvc: "apigw", wantRes: "abc123",
		},
		{
			name:    "a deployed API invocation is named as one",
			req:     httptest.NewRequest("GET", "/_aws/execute-api/abc123/prod/orders/42", nil),
			wantSvc: "apigw", wantAct: "Invoke GET", wantRes: "abc123/prod/orders/42",
		},
		{
			name:    "CloudFormation is not generic aws",
			req:     form("/", "Action=CreateStack&StackName=shop", "cloudformation"),
			body:    "Action=CreateStack&StackName=shop",
			wantSvc: "cfn", wantAct: "CreateStack", wantRes: "shop",
		},
		{
			name:    "Kinesis gets a label, not a raw target prefix",
			req:     target("Kinesis_20131202.PutRecord", `{"StreamName":"telemetry"}`, "kinesis"),
			body:    `{"StreamName":"telemetry"}`,
			wantSvc: "kinesis", wantAct: "PutRecord", wantRes: "telemetry",
		},
		{
			name:    "S3 still classifies as S3",
			req:     sign(httptest.NewRequest("GET", "/uploads/photo.jpg", nil), "s3"),
			wantSvc: "s3", wantAct: "GetObject", wantRes: "uploads/photo.jpg",
		},
		{
			name:    "SQS keeps its short label",
			req:     target("AmazonSQS.SendMessage", `{"QueueUrl":"http://h/000/jobs"}`, "sqs"),
			body:    `{"QueueUrl":"http://h/000/jobs"}`,
			wantSvc: "sqs", wantAct: "SendMessage", wantRes: "jobs",
		},
		{
			// Lambda dates its paths per API family; concurrency is 2017-10-31,
			// and the S3 fallback used to name it PutObject.
			name:    "Lambda concurrency is Lambda",
			req:     sign(httptest.NewRequest("PUT", "/2017-10-31/functions/fn/concurrency", strings.NewReader("{}")), "lambda"),
			wantSvc: "lambda", wantAct: "PutFunctionConcurrency", wantRes: "fn",
		},
		{
			name: "Secrets Manager keeps its short label",
			req:  target("secretsmanager.GetSecretValue", `{"SecretId":"app/config"}`, "secretsmanager"),
			body: `{"SecretId":"app/config"}`,
			// The whole name: "config" alone links to a secret that is not there.
			wantSvc: "sm", wantAct: "GetSecretValue", wantRes: "app/config",
		},
	}
	for _, c := range cases {
		svc, act, res := classify(awsident.Default(), c.req, c.body, nil)
		if svc != c.wantSvc {
			t.Errorf("%s: service = %q, want %q", c.name, svc, c.wantSvc)
		}
		if c.wantAct != "" && act != c.wantAct {
			t.Errorf("%s: action = %q, want %q", c.name, act, c.wantAct)
		}
		if c.wantRes != "" && res != c.wantRes {
			t.Errorf("%s: resource = %q, want %q", c.name, res, c.wantRes)
		}
	}
}

// TestTrafficLinksLandOnRealPages: what the wire names a resource is what its
// link opens. The crawl found five that did not — a dashboard and a metric
// namespace linked as alarms, an alias cut down to a key id, a secret with a
// slash cut to its last segment.
func TestTrafficLinksLandOnRealPages(t *testing.T) {
	cases := []struct {
		svc, body, wantPath string
	}{
		{"cw", `{"AlarmName":"slow"}`, "/cw/alarm/slow"},
		{"cw", `{"DashboardName":"trading"}`, ""},
		{"cw", `{"Namespace":"Harbour/Orders","MetricName":"Placed"}`, ""},
		{"kms", `{"KeyId":"alias/payments"}`, ""},
		{"kms", `{"KeyId":"arn:aws:kms:us-east-1:000000000000:key/1234abcd-12ab-34cd-56ef-1234567890ab"}`, "/kms/1234abcd-12ab-34cd-56ef-1234567890ab"},
		{"sm", `{"SecretId":"prod/courier-token"}`, "/sm/secret?name=prod%2Fcourier-token"},
		{"sm", `{"SecretId":"arn:aws:secretsmanager:us-east-1:000000000000:secret:prod/db-AbCdEf"}`, "/sm/secret?name=prod%2Fdb"},
		{"sm", `{"SecretId":"stripe-secret-key"}`, "/sm/secret?name=stripe-secret-key"},
	}
	for _, c := range cases {
		res := jsonResource(c.svc, c.body)
		if got := resourceURL(c.svc, res).Path; got != c.wantPath {
			t.Errorf("%s %s: resource %q links to %q, want %q", c.svc, c.body, res, got, c.wantPath)
		}
	}
}

// The wire's small print, as the screenshots read it.
func TestTheWireSpeaksPlainly(t *testing.T) {
	for in, want := range map[float64]string{0.215416: "0.22", 3.137: "3.14", 28.261: "28.3", 391.6: "392"} {
		if got := fmtMillis(in); got != want {
			t.Errorf("fmtMillis(%v) = %q, want %q", in, got, want)
		}
	}
	if got := plural(2, "bus"); got != "2 buses" {
		t.Errorf("plural(2, bus) = %q", got)
	}
	if got := plural(2, "rule"); got != "2 rules" {
		t.Errorf("plural(2, rule) = %q", got)
	}
	// A cascade row says "cw" like the calls beside it, and Step Functions is
	// "sfn" — the key its links and the service filter use.
	for in, want := range map[string]string{"cloudwatch": "cw", "stepfunctions": "sfn", "sns": "sns"} {
		if got := shortLabel(in); got != want {
			t.Errorf("shortLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
