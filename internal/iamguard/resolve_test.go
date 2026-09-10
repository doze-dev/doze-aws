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
			"sqs:SendMessage", awsident.ARN("sqs", "orders")},
		{"sqs by queue name", "sqs", "AmazonSQS.CreateQueue", `{"QueueName":"orders"}`,
			"sqs:CreateQueue", awsident.ARN("sqs", "orders")},
		{"sqs batch is its base action", "sqs", "AmazonSQS.SendMessageBatch",
			`{"QueueUrl":"http://h/000000000000/orders"}`,
			"sqs:SendMessage", awsident.ARN("sqs", "orders")},
		{"sqs delete batch", "sqs", "AmazonSQS.DeleteMessageBatch", `{"QueueName":"q"}`,
			"sqs:DeleteMessage", awsident.ARN("sqs", "q")},
		{"sqs visibility batch", "sqs", "AmazonSQS.ChangeMessageVisibilityBatch", `{"QueueName":"q"}`,
			"sqs:ChangeMessageVisibility", awsident.ARN("sqs", "q")},
		{"dynamodb table", "dynamodb", "DynamoDB_20120810.PutItem", `{"TableName":"orders"}`,
			"dynamodb:PutItem", awsident.ARN("dynamodb", "table/orders")},
		{"kms key id", "kms", "TrentService.Encrypt", `{"KeyId":"abcd-1234"}`,
			"kms:Encrypt", awsident.ARN("kms", "key/abcd-1234")},
		{"kms alias stays an alias", "kms", "TrentService.Encrypt", `{"KeyId":"alias/app"}`,
			"kms:Encrypt", awsident.ARN("kms", "alias/app")},
		{"kms full arn passes through", "kms", "TrentService.Decrypt",
			`{"KeyId":"arn:aws:kms:us-east-1:000000000000:key/k1"}`,
			"kms:Decrypt", "arn:aws:kms:us-east-1:000000000000:key/k1"},
		{"secret by name", "secretsmanager", "secretsmanager.GetSecretValue", `{"SecretId":"db"}`,
			"secretsmanager:GetSecretValue", awsident.ARN("secretsmanager", "secret:db")},
		{"secret by arn", "secretsmanager", "secretsmanager.GetSecretValue",
			`{"SecretId":"arn:aws:secretsmanager:us-east-1:000000000000:secret:db-Ab12"}`,
			"secretsmanager:GetSecretValue", "arn:aws:secretsmanager:us-east-1:000000000000:secret:db-Ab12"},
		{"ssm parameter gets a leading slash", "ssm", "AmazonSSM.GetParameter", `{"Name":"app/key"}`,
			"ssm:GetParameter", awsident.ARN("ssm", "parameter/app/key")},
		{"ssm parameter keeps its slash", "ssm", "AmazonSSM.GetParameter", `{"Name":"/app/key"}`,
			"ssm:GetParameter", awsident.ARN("ssm", "parameter/app/key")},
		{"eventbridge signs as events", "eventbridge", "AWSEvents.PutRule", `{"Name":"nightly"}`,
			"events:PutRule", awsident.ARN("events", "rule/nightly")},
		{"kinesis by name", "kinesis", "Kinesis_20131202.PutRecord", `{"StreamName":"telemetry"}`,
			"kinesis:PutRecord", awsident.ARN("kinesis", "stream/telemetry")},
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
			action, resource := ResolveAction(req(http.MethodPost, c.target, "/", c.body), c.service)
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
	action, resource := ResolveAction(req(http.MethodPost, "Whatever.DoThing", "/", `{"Name":"x"}`), "route53")
	if action != "" || resource != "" {
		t.Errorf("an unknown service must not resolve: got (%q, %q)", action, resource)
	}
}

// TestResolveActionQueryProtocol: SNS and the legacy SQS wire put the
// operation in an Action parameter, in the query string or the form body.
func TestResolveActionQueryProtocol(t *testing.T) {
	topic := awsident.ARN("sns", "alerts")

	t.Run("query string", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/?Action=Publish&TopicArn="+topic, nil)
		action, resource := ResolveAction(r, "sns")
		if action != "sns:Publish" || resource != topic {
			t.Errorf("got (%q, %q)", action, resource)
		}
	})

	t.Run("form body", func(t *testing.T) {
		form := "Action=Publish&TopicArn=" + topic
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		action, resource := ResolveAction(r, "sns")
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
		action, _ := ResolveAction(r, "sns")
		if action != "sns:Publish" {
			t.Errorf("PublishBatch must authorize as sns:Publish, got %q", action)
		}
	})

	t.Run("subscription arn is a resource too", func(t *testing.T) {
		sub := topic + ":1234"
		r := httptest.NewRequest(http.MethodPost, "/?Action=Unsubscribe&SubscriptionArn="+sub, nil)
		_, resource := ResolveAction(r, "sns")
		if resource != sub {
			t.Errorf("got %q, want %q", resource, sub)
		}
	})

	t.Run("a GET form body is not read", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", strings.NewReader("Action=Publish"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if action, _ := ResolveAction(r, "sns"); action != "" {
			t.Errorf("peekForm is POST-only, got %q", action)
		}
	})

	t.Run("a JSON body is not parsed as a form", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Action":"Publish"}`))
		r.Header.Set("Content-Type", "application/json")
		if action, _ := ResolveAction(r, "sns"); action != "" {
			t.Errorf("got %q, want no action", action)
		}
	})

	t.Run("no action anywhere", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if action, resource := ResolveAction(r, "sns"); action != "" || resource != "" {
			t.Errorf("got (%q, %q)", action, resource)
		}
	})
}

// TestResolveS3 is the mapping that shipped wrong: an object addressed in the
// host was read as a bucket operation on a bucket named after the key. The
// path-style and the service-resolved forms are both here.
func TestResolveS3(t *testing.T) {
	const b = "arn:aws:s3:::docs"
	const o = "arn:aws:s3:::docs/report.pdf"

	for _, c := range []struct {
		name             string
		method, path     string
		action, resource string
	}{
		{"list buckets", "GET", "/", "s3:ListAllMyBuckets", ""},
		{"list a bucket", "GET", "/docs", "s3:ListBucket", b},
		{"head a bucket", "HEAD", "/docs", "s3:ListBucket", b},
		{"create a bucket", "PUT", "/docs", "s3:CreateBucket", b},
		{"delete a bucket", "DELETE", "/docs", "s3:DeleteBucket", b},
		{"batch delete", "POST", "/docs?delete=", "s3:DeleteObject", b},
		{"get an object", "GET", "/docs/report.pdf", "s3:GetObject", o},
		{"head an object", "HEAD", "/docs/report.pdf", "s3:GetObject", o},
		{"put an object", "PUT", "/docs/report.pdf", "s3:PutObject", o},
		{"post an object", "POST", "/docs/report.pdf", "s3:PutObject", o},
		{"delete an object", "DELETE", "/docs/report.pdf", "s3:DeleteObject", o},

		// Sub-resources are named by the query parameter, and the verb by the
		// method.
		{"get bucket policy", "GET", "/docs?policy=", "s3:GetBucketPolicy", b},
		{"put bucket policy", "PUT", "/docs?policy=", "s3:PutBucketPolicy", b},
		{"delete bucket policy", "DELETE", "/docs?policy=", "s3:DeleteBucketPolicy", b},
		{"get bucket acl", "GET", "/docs?acl=", "s3:GetAcl", b},
		{"put versioning", "PUT", "/docs?versioning=", "s3:PutBucketVersioning", b},
		{"get cors", "GET", "/docs?cors=", "s3:GetBucketCORS", b},
		{"put notification", "PUT", "/docs?notification=", "s3:PutBucketNotification", b},
		{"put encryption", "PUT", "/docs?encryption=", "s3:PutEncryptionConfiguration", b},
		{"put lifecycle", "PUT", "/docs?lifecycle=", "s3:PutLifecycleConfiguration", b},
		{"put website", "PUT", "/docs?website=", "s3:PutBucketWebsite", b},
		{"put replication", "PUT", "/docs?replication=", "s3:PutReplicationConfiguration", b},
		// Tagging is the one sub-resource whose action depends on whether a
		// key is present.
		{"bucket tagging", "PUT", "/docs?tagging=", "s3:PutBucketTagging", b},
		{"object tagging", "PUT", "/docs/report.pdf?tagging=", "s3:PutObjectTagging", o},
		{"object tagging read", "GET", "/docs/report.pdf?tagging=", "s3:GetObjectTagging", o},

		// Multipart, authorized as AWS names it.
		{"list multipart uploads", "GET", "/docs?uploads=", "s3:ListBucketMultipartUploads", b},
		{"initiate multipart", "POST", "/docs/report.pdf?uploads=", "s3:PutObject", o},
		{"upload a part", "PUT", "/docs/report.pdf?uploadId=x&partNumber=1", "s3:PutObject", o},
		{"complete multipart", "POST", "/docs/report.pdf?uploadId=x", "s3:PutObject", o},
		{"abort multipart", "DELETE", "/docs/report.pdf?uploadId=x", "s3:AbortMultipartUpload", o},
		{"list parts", "GET", "/docs/report.pdf?uploadId=x", "s3:ListMultipartUploadParts", o},

		{"an unmapped method on a bucket", "OPTIONS", "/docs", "", b},
		{"an unmapped method on an object", "OPTIONS", "/docs/report.pdf", "", o},
	} {
		t.Run(c.name, func(t *testing.T) {
			action, resource := ResolveAction(httptest.NewRequest(c.method, c.path, nil), "s3")
			if action != c.action || resource != c.resource {
				t.Errorf("got (%q, %q), want (%q, %q)", action, resource, c.action, c.resource)
			}
		})
	}
}

// TestResolveS3WithServiceResolvedTarget: the virtual-host form. The path
// alone says "/report.pdf", so the middleware resolves a bucket called
// "report.pdf" and no key — which is how an object read came to be evaluated
// as a bucket operation. The service knows better and passes the real pair.
func TestResolveS3WithServiceResolvedTarget(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/report.pdf", nil)
	r.Host = "docs.s3.localhost"

	action, resource := ResolveAction(r, "s3")
	if action != "s3:ListBucket" || resource != "arn:aws:s3:::report.pdf" {
		t.Fatalf("the path-only reading is the one the guard must correct: got (%q, %q)", action, resource)
	}

	action, resource = ResolveS3(r, "docs", "report.pdf")
	if action != "s3:GetObject" || resource != "arn:aws:s3:::docs/report.pdf" {
		t.Errorf("with the service's own (bucket, key): got (%q, %q), want (s3:GetObject, arn:aws:s3:::docs/report.pdf)", action, resource)
	}
}

// TestResolveLambda walks every path family of the Lambda REST API.
func TestResolveLambda(t *testing.T) {
	fn := awsident.ARN("lambda", "function:worker")

	for _, c := range []struct {
		name             string
		method, path     string
		action, resource string
	}{
		{"invoke", "POST", "/2015-03-31/functions/worker/invocations", "lambda:InvokeFunction", fn},
		{"get a function", "GET", "/2015-03-31/functions/worker", "lambda:GetFunction", fn},
		{"list functions", "GET", "/2015-03-31/functions", "lambda:ListFunctions", ""},
		{"create", "POST", "/2015-03-31/functions", "lambda:CreateFunction", ""},
		{"delete", "DELETE", "/2015-03-31/functions/worker", "lambda:DeleteFunction", fn},
		{"get configuration", "GET", "/2015-03-31/functions/worker/configuration", "lambda:GetFunctionConfiguration", fn},
		{"update configuration", "PUT", "/2015-03-31/functions/worker/configuration", "lambda:UpdateFunctionConfiguration", fn},
		{"update code", "PUT", "/2015-03-31/functions/worker/code", "lambda:UpdateFunctionCode", fn},
		{"publish a version", "POST", "/2015-03-31/functions/worker/versions", "lambda:PublishVersion", fn},
		{"create an alias", "POST", "/2015-03-31/functions/worker/aliases", "lambda:CreateAlias", fn},
		{"get an alias", "GET", "/2015-03-31/functions/worker/aliases/live", "lambda:GetAlias", fn},
		{"delete an alias", "DELETE", "/2015-03-31/functions/worker/aliases/live", "lambda:DeleteAlias", fn},
		{"concurrency", "PUT", "/2015-03-31/functions/worker/concurrency", "lambda:PutFunctionConcurrency", fn},
		{"add permission", "POST", "/2015-03-31/functions/worker/policy", "lambda:CreatePermission", fn},
		{"remove permission", "DELETE", "/2015-03-31/functions/worker/policy/sid", "lambda:DeletePermission", fn},
		{"get policy", "GET", "/2015-03-31/functions/worker/policy", "lambda:GetPermission", fn},
		{"create a function url", "POST", "/2021-10-31/functions/worker/url", "lambda:CreateFunctionUrlConfig", fn},
		{"event invoke config", "PUT", "/2019-09-25/functions/worker/event-invoke-config", "lambda:CreateFunctionEventInvokeConfig", fn},
		{"list event source mappings", "GET", "/2015-03-31/event-source-mappings", "lambda:ListEventSourceMappings", ""},
		{"create event source mapping", "POST", "/2015-03-31/event-source-mappings", "lambda:CreateEventSourceMapping", ""},
		{"update event source mapping", "PUT", "/2015-03-31/event-source-mappings/id", "lambda:UpdateEventSourceMapping", ""},
		{"delete event source mapping", "DELETE", "/2015-03-31/event-source-mappings/id", "lambda:DeleteEventSourceMapping", ""},
		{"tag", "POST", "/2017-03-31/tags/arn", "lambda:TagResource", ""},
		{"untag", "DELETE", "/2017-03-31/tags/arn", "lambda:UntagResource", ""},
		{"list tags", "GET", "/2017-03-31/tags/arn", "lambda:ListTags", ""},
		{"publish a layer version", "POST", "/2018-10-31/layers/util/versions", "lambda:CreateLayerVersion", ""},
		{"get a layer version", "GET", "/2018-10-31/layers/util/versions/1", "lambda:GetLayerVersion", ""},
		{"an unknown family", "GET", "/2015-03-31/account-settings", "", ""},
		{"too few segments", "GET", "/2015-03-31", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			action, resource := ResolveAction(httptest.NewRequest(c.method, c.path, nil), "lambda")
			if action != c.action || resource != c.resource {
				t.Errorf("got (%q, %q), want (%q, %q)", action, resource, c.action, c.resource)
			}
		})
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
		if got := LambdaFunctionARN(c.ref); got != c.arn {
			t.Errorf("LambdaFunctionARN(%q) = %q, want %q", c.ref, got, c.arn)
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
	if _, resource := ResolveAction(r, "dynamodb"); resource != awsident.ARN("dynamodb", "table/orders") {
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
	action, resource := ResolveAction(r, "dynamodb")
	if action != "dynamodb:PutItem" {
		t.Errorf("the action must still resolve: %q", action)
	}
	if resource != "" {
		t.Errorf("an oversize body must not be peeked, got resource %q", resource)
	}
}
