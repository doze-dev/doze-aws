package dozeaws_test

// The number that actually matters: one whole request, in at the gateway and
// out with a response — routing, signature parsing, IAM evaluation in soft
// mode, model-derived validation, the handler, the store, and the encode.
//
// The per-package benchmarks say what each layer costs. This says what a
// developer's SDK call costs, which is the only figure anyone experiences.
//
// Not a network benchmark: it drives the handler directly, so there is no
// loopback TCP, no TLS and no SDK client in the number. That is deliberate —
// those are the parts doze-aws does not control.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
)

func benchStack(b *testing.B, services ...string) http.Handler {
	b.Helper()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  b.TempDir(),
		Services: services,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { stack.Close() })
	return stack.Handler()
}

// post drives one signed request through the gateway.
func post(b *testing.B, h http.Handler, target, contentType, body, scope string) {
	b.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", contentType)
	if target != "" {
		r.Header.Set("X-Amz-Target", target)
	}
	r.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/"+scope+
			"/aws4_request, SignedHeaders=host, Signature=x")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		b.Fatalf("%s: %d %s", target, w.Code, w.Body.String())
	}
}

// A DynamoDB PutItem: the shape a hot loop in a test suite actually sends,
// and the one carrying a 277-constraint validation table.
func BenchmarkRequestPutItem(b *testing.B) {
	h := benchStack(b, "dynamodb")
	post(b, h, "DynamoDB_20120810.CreateTable", "application/x-amz-json-1.0",
		`{"TableName":"bench","AttributeDefinitions":[{"AttributeName":"pk","AttributeType":"S"}],`+
			`"KeySchema":[{"AttributeName":"pk","KeyType":"HASH"}],"BillingMode":"PAY_PER_REQUEST"}`,
		"dynamodb")

	body := `{"TableName":"bench","Item":{"pk":{"S":"customer#77"},` +
		`"total":{"N":"149.5"},"currency":{"S":"GBP"},"tier":{"S":"gold"}}}`
	b.ReportAllocs()
	for b.Loop() {
		post(b, h, "DynamoDB_20120810.PutItem", "application/x-amz-json-1.0", body, "dynamodb")
	}
}

// An SQS SendMessage over JSON, the other very common hot call.
func BenchmarkRequestSendMessage(b *testing.B) {
	h := benchStack(b, "sqs")
	post(b, h, "AmazonSQS.CreateQueue", "application/x-amz-json-1.0",
		`{"QueueName":"bench"}`, "sqs")

	body := `{"QueueUrl":"http://127.0.0.1/000000000000/bench","MessageBody":"hello"}`
	b.ReportAllocs()
	for b.Loop() {
		post(b, h, "AmazonSQS.SendMessage", "application/x-amz-json-1.0", body, "sqs")
	}
}

// CloudWatch PutMetricData over the AWS CLI's wire. Every producer in the
// tree publishes through this path, batched.
func BenchmarkRequestPutMetricData(b *testing.B) {
	h := benchStack(b, "cloudwatch")
	body := `{"Namespace":"Bench","MetricData":[{"MetricName":"Probe","Value":1,` +
		`"Unit":"Count","Dimensions":[{"Name":"Stage","Value":"prod"}]}]}`
	b.ReportAllocs()
	for b.Loop() {
		post(b, h, "GraniteServiceVersion20100801.PutMetricData",
			"application/x-amz-json-1.0", body, "monitoring")
	}
}

// Does batching actually save fsyncs? The advice "batch your writes" is only
// true if the batch is one transaction, and in doze-aws today it is not:
// SendMessageBatch loops calling Store.Send, each its own bolt.Update. This
// benchmark exists so that claim is measured rather than assumed.
func BenchmarkRequestSendMessageBatch(b *testing.B) {
	h := benchStack(b, "sqs")
	post(b, h, "AmazonSQS.CreateQueue", "application/x-amz-json-1.0",
		`{"QueueName":"bench"}`, "sqs")

	entries := ""
	for i := range 10 {
		if i > 0 {
			entries += ","
		}
		entries += `{"Id":"m` + string(rune('0'+i)) + `","MessageBody":"hello"}`
	}
	body := `{"QueueUrl":"http://127.0.0.1/000000000000/bench","Entries":[` + entries + `]}`
	b.ReportAllocs()
	for b.Loop() {
		post(b, h, "AmazonSQS.SendMessageBatch", "application/x-amz-json-1.0", body, "sqs")
	}
}
