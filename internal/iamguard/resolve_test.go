package iamguard

// resolve.go decides what every request is authorized AS. It is 464 lines of
// hand-written mapping — X-Amz-Target for the JSON services, the Action
// parameter for the Query ones, method-and-path for S3 and Lambda — and it
// had no test of its own. Everything that exercised it did so end to end,
// through a stack, one path at a time, which is why a wrong mapping shipped
// three times: an S3 object read evaluated as a bucket operation, a batch
// send authorized under a name that is not an IAM action, a Lambda ARN
// resolved to a different ARN.
//
// A wrong mapping here is not a wrong error message. Authorizing a request as
// the wrong action means a Deny written for the real action never fires, and
// a policy written for the real resource never matches — it fails open,
// quietly, for exactly one shape of request. So this is a table, per protocol
// and per path family, that says what each request resolves to.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

func req(method, target, path, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if target != "" {
		r.Header.Set("X-Amz-Target", target)
	}
	return r
}

// TestResolveActionJSONProtocol: the services that carry their operation in
// X-Amz-Target, and the resource field each one names it by.
func TestResolveActionJSONProtocol(t *testing.T) {
	for _, c := range []struct {
		name             string
		service          string
		target           string
		body             string
		action, resource string
	}{
		{"sqs by queue url", "sqs", "AmazonSQS.SendMessage",
			`{"QueueUrl":"http://localhost:4566/000000000000/orders"}`,
			"sqs:SendMessage", awsident.Default().ARN("sqs", "orders")},
		{"sqs by queue name", "sqs", "AmazonSQS.CreateQueue", `{"QueueName":"orders"}`,
			"sqs:CreateQueue", awsident.Default().ARN("sqs", "orders")},
		{"sqs batch is its base action", "sqs", "AmazonSQS.SendMessageBatch",
			`{"QueueUrl":"http://h/000000000000/orders"}`,
			"sqs:SendMessage", awsident.Default().ARN("sqs", "orders")},
		{"sqs delete batch", "sqs", "AmazonSQS.DeleteMessageBatch", `{"QueueName":"q"}`,
			"sqs:DeleteMessage", awsident.Default().ARN("sqs", "q")},
		{"sqs visibility batch", "sqs", "AmazonSQS.ChangeMessageVisibilityBatch", `{"QueueName":"q"}`,
			"sqs:ChangeMessageVisibility", awsident.Default().ARN("sqs", "q")},
		{"dynamodb table", "dynamodb", "DynamoDB_20120810.PutItem", `{"TableName":"orders"}`,
			"dynamodb:PutItem", awsident.Default().ARN("dynamodb", "table/orders")},
		{"kms key id", "kms", "TrentService.Encrypt", `{"KeyId":"abcd-1234"}`,
			"kms:Encrypt", awsident.Default().ARN("kms", "key/abcd-1234")},
		{"kms alias stays an alias", "kms", "TrentService.Encrypt", `{"KeyId":"alias/app"}`,
			"kms:Encrypt", awsident.Default().ARN("kms", "alias/app")},
		{"kms full arn passes through", "kms", "TrentService.Decrypt",
			`{"KeyId":"arn:aws:kms:us-east-1:000000000000:key/k1"}`,
			"kms:Decrypt", "arn:aws:kms:us-east-1:000000000000:key/k1"},
		{"secret by name", "secretsmanager", "secretsmanager.GetSecretValue", `{"SecretId":"db"}`,
			"secretsmanager:GetSecretValue", awsident.Default().ARN("secretsmanager", "secret:db")},
		{"secret by arn", "secretsmanager", "secretsmanager.GetSecretValue",
			`{"SecretId":"arn:aws:secretsmanager:us-east-1:000000000000:secret:db-Ab12"}`,
			"secretsmanager:GetSecretValue", "arn:aws:secretsmanager:us-east-1:000000000000:secret:db-Ab12"},
		{"ssm parameter gets a leading slash", "ssm", "AmazonSSM.GetParameter", `{"Name":"app/key"}`,
			"ssm:GetParameter", awsident.Default().ARN("ssm", "parameter/app/key")},
		{"ssm parameter keeps its slash", "ssm", "AmazonSSM.GetParameter", `{"Name":"/app/key"}`,
			"ssm:GetParameter", awsident.Default().ARN("ssm", "parameter/app/key")},
		{"eventbridge signs as events", "eventbridge", "AWSEvents.PutRule", `{"Name":"nightly"}`,
			"events:PutRule", awsident.Default().ARN("events", "rule/nightly")},
		{"kinesis by name", "kinesis", "Kinesis_20131202.PutRecord", `{"StreamName":"telemetry"}`,
			"kinesis:PutRecord", awsident.Default().ARN("kinesis", "stream/telemetry")},
		{"kinesis by arn", "kinesis", "Kinesis_20131202.PutRecord",
			`{"StreamARN":"arn:aws:kinesis:us-east-1:000000000000:stream/t"}`,
			"kinesis:PutRecord", "arn:aws:kinesis:us-east-1:000000000000:stream/t"},
		{"step functions spells its members lowercase", "stepfunctions", "AWSStepFunctions.StartExecution",
			`{"stateMachineArn":"arn:aws:states:us-east-1:000000000000:stateMachine:flow"}`,
			"states:StartExecution", "arn:aws:states:us-east-1:000000000000:stateMachine:flow"},
		{"logs has no resource rule, action only", "logs", "Logs_20140328.PutLogEvents",
			`{"logGroupName":"/app"}`, "logs:PutLogEvents", ""},
		{"no body: action still resolves", "dynamodb", "DynamoDB_20120810.ListTables", "",
			"dynamodb:ListTables", ""},
		{"unparseable body: action still resolves", "dynamodb", "DynamoDB_20120810.PutItem", "not json",
			"dynamodb:PutItem", ""},
		{"field absent from the body", "dynamodb", "DynamoDB_20120810.PutItem", `{"Item":{}}`,
			"dynamodb:PutItem", ""},
		{"empty field value is not a resource", "dynamodb", "DynamoDB_20120810.PutItem", `{"TableName":""}`,
			"dynamodb:PutItem", ""},
		{"non-string field value", "dynamodb", "DynamoDB_20120810.PutItem", `{"TableName":7}`,
			"dynamodb:PutItem", ""},
		{"target with no operation", "sqs", "AmazonSQS.", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			action, resource := ResolveAction(awsident.Default(), req(http.MethodPost, c.target, "/", c.body), c.service)
			if action != c.action || resource != c.resource {
				t.Errorf("got (%q, %q), want (%q, %q)", action, resource, c.action, c.resource)
			}
		})
	}
}

// TestResolveActionUnknownService: an unclassifiable request is never guessed
// at. An empty action means "do not evaluate this", which is safer than a
// wrong one only because the middleware treats it that way.
func TestResolveActionUnknownService(t *testing.T) {
	// route53, not cloudwatch: cloudwatch was the example here until it
	// gained an action prefix of its own, at which point this test started
	// asserting the opposite of what it means. The service named has to be
	// one doze-aws genuinely does not implement.
	action, resource := ResolveAction(awsident.Default(), req(http.MethodPost, "Whatever.DoThing", "/", `{"Name":"x"}`), "route53")
	if action != "" || resource != "" {
		t.Errorf("an unknown service must not resolve: got (%q, %q)", action, resource)
	}
}

// TestResolveActionQueryProtocol: SNS and the legacy SQS wire put the
// operation in an Action parameter, in the query string or the form body.
func TestResolveActionQueryProtocol(t *testing.T) {
	topic := awsident.Default().ARN("sns", "alerts")

	t.Run("query string", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/?Action=Publish&TopicArn="+topic, nil)
		action, resource := ResolveAction(awsident.Default(), r, "sns")
		if action != "sns:Publish" || resource != topic {
			t.Errorf("got (%q, %q)", action, resource)
		}
	})

	t.Run("form body", func(t *testing.T) {
		form := "Action=Publish&TopicArn=" + topic
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		action, resource := ResolveAction(awsident.Default(), r, "sns")
		if action != "sns:Publish" || resource != topic {
			t.Errorf("got (%q, %q)", action, resource)
		}
		// The handler must still see its body. Peeking that consumed the
		// request would break every Query-protocol call in the emulator.
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != form {
			t.Errorf("the body was not restored: %q err=%v", body, err)
		}
	})

	t.Run("batch publish is its base action", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/?Action=PublishBatch&TopicArn="+topic, nil)
		action, _ := ResolveAction(awsident.Default(), r, "sns")
		if action != "sns:Publish" {
			t.Errorf("PublishBatch must authorize as sns:Publish, got %q", action)
		}
	})

	t.Run("subscription arn is a resource too", func(t *testing.T) {
		sub := topic + ":1234"
		r := httptest.NewRequest(http.MethodPost, "/?Action=Unsubscribe&SubscriptionArn="+sub, nil)
		_, resource := ResolveAction(awsident.Default(), r, "sns")
		if resource != sub {
			t.Errorf("got %q, want %q", resource, sub)
		}
	})

	t.Run("a GET form body is not read", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", strings.NewReader("Action=Publish"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if action, _ := ResolveAction(awsident.Default(), r, "sns"); action != "" {
			t.Errorf("peekForm is POST-only, got %q", action)
		}
	})

	t.Run("a JSON body is not parsed as a form", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Action":"Publish"}`))
		r.Header.Set("Content-Type", "application/json")
		if action, _ := ResolveAction(awsident.Default(), r, "sns"); action != "" {
			t.Errorf("got %q, want no action", action)
		}
	})

	t.Run("no action anywhere", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if action, resource := ResolveAction(awsident.Default(), r, "sns"); action != "" || resource != "" {
			t.Errorf("got (%q, %q)", action, resource)
		}
	})
}

// The REST services are not resolved here: S3 and Lambda name the permission
// from their own routers and AWS's own lists (s3/iam_test.go, lambda/iam_test.go),
// and ResolveAction hands them the request only through the resolver the stack
// installs. With none, they are not evaluated.
func TestRESTServicesAreResolvedByTheirOwnResolver(t *testing.T) {
	r := httptest.NewRequest("GET", "/docs/report.pdf", nil)
	if action, _ := ResolveAction(awsident.Default(), r, "s3"); action != "" {
		t.Errorf("s3 with no resolver = %q, want it left unevaluated", action)
	}
	r = WithResolver(r, func(*http.Request) (string, string) { return "s3:GetObject", "arn:aws:s3:::docs/report.pdf" })
	if action, resource := ResolveAction(awsident.Default(), r, "s3"); action != "s3:GetObject" || resource != "arn:aws:s3:::docs/report.pdf" {
		t.Errorf("s3 with a resolver = (%q, %q), want what it said", action, resource)
	}
	// A resolver is for the REST services; a JSON service names its own.
	jr := httptest.NewRequest("POST", "/", nil)
	jr.Header.Set("X-Amz-Target", "AmazonSQS.SendMessage")
	jr = WithResolver(jr, func(*http.Request) (string, string) { return "wrong:Action", "" })
	if action, _ := ResolveAction(awsident.Default(), jr, "sqs"); action != "sqs:SendMessage" {
		t.Errorf("sqs = %q, want its own operation", action)
	}
}

// TestLambdaFunctionReferences: a function policy written for an alias must
// be found by a request naming that alias, and a request naming the function
// by ARN must not resolve to a different ARN.
func TestLambdaFunctionReferences(t *testing.T) {
	const full = "arn:aws:lambda:us-east-1:000000000000:function:worker"
	for _, c := range []struct{ ref, arn, name string }{
		{"worker", full, "worker"},
		{"worker:live", full + ":live", "worker"},
		{"worker:3", full + ":3", "worker"},
		{full, full, "worker"},
		{full + ":live", full + ":live", "worker"},
		{"000000000000:function:worker", full, "worker"},
	} {
		if got := LambdaFunctionARN(awsident.Default(), c.ref); got != c.arn {
			t.Errorf("LambdaFunctionARN(awsident.Default(), %q) = %q, want %q", c.ref, got, c.arn)
		}
		if got := LambdaFunctionName(c.ref); got != c.name {
			t.Errorf("LambdaFunctionName(%q) = %q, want %q", c.ref, got, c.name)
		}
	}
}

// TestActionIsExportedForServices: the guards name their own actions through
// Action, which must agree with what the middleware resolved — that agreement
// is what the reauthorize path compares.
func TestActionIsExportedForServices(t *testing.T) {
	for _, c := range []struct{ prefix, op, want string }{
		{"sqs", "SendMessage", "sqs:SendMessage"},
		{"sqs", "SendMessageBatch", "sqs:SendMessage"},
		{"sns", "PublishBatch", "sns:Publish"},
		{"sns", "Publish", "sns:Publish"},
		{"kms", "Decrypt", "kms:Decrypt"},
		{"events", "PutEvents", "events:PutEvents"},
	} {
		if got := Action(c.prefix, c.op); got != c.want {
			t.Errorf("Action(%q, %q) = %q, want %q", c.prefix, c.op, got, c.want)
		}
	}
}

// TestPeekRestoresTheBody: resolution reads request bodies to find resource
// names. Every one of them has to be put back, or the handler behind the
// middleware receives an empty request.
func TestPeekRestoresTheBody(t *testing.T) {
	const body = `{"TableName":"orders","Item":{"pk":{"S":"1"}}}`
	r := req(http.MethodPost, "DynamoDB_20120810.PutItem", "/", body)
	if _, resource := ResolveAction(awsident.Default(), r, "dynamodb"); resource != awsident.Default().ARN("dynamodb", "table/orders") {
		t.Fatalf("resource = %q", resource)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil || string(got) != body {
		t.Fatalf("the body was not restored: %q err=%v", got, err)
	}
}

// TestPeekSkipsOversizeBodies: a large upload is a data-plane payload, and
// reading it into memory to look for a resource name would be a memory
// amplification on every request. It resolves the action and leaves the body.
func TestPeekSkipsOversizeBodies(t *testing.T) {
	big := strings.Repeat("x", 64)
	r := req(http.MethodPost, "DynamoDB_20120810.PutItem", "/", `{"TableName":"orders"}`)
	r.ContentLength = maxPeek + 1
	_ = big
	action, resource := ResolveAction(awsident.Default(), r, "dynamodb")
	if action != "dynamodb:PutItem" {
		t.Errorf("the action must still resolve: %q", action)
	}
	if resource != "" {
		t.Errorf("an oversize body must not be peeked, got resource %q", resource)
	}
}
