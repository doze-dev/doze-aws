package console

import "testing"

// The ARN shapes that used to be parsed wrong, plus the ones that were only
// ever right by luck. The old resolver took everything after the last colon,
// which works for sqs and sns and quietly fails for every service whose
// resource part carries a slash.
func TestResourceFromARN(t *testing.T) {
	for _, c := range []struct{ arn, svc, name, path string }{
		{"arn:aws:sqs:us-east-1:000000000000:emails-dlq", "sqs", "emails-dlq", "/sqs/emails-dlq"},
		{"arn:aws:sns:us-east-1:000000000000:order-events", "sns", "order-events", "/sns/order-events"},
		// a subscription ARN is the topic plus a uuid; the topic owns the page
		{"arn:aws:sns:us-east-1:000000000000:order-events:8f1c-42", "sns", "order-events", "/sns/order-events"},
		{"arn:aws:lambda:us-east-1:000000000000:function:resize", "lambda", "resize", "/lambda/resize"},
		{"arn:aws:lambda:us-east-1:000000000000:function:resize:3", "lambda", "resize", "/lambda/resize"},
		// these three carry a slash and were the silent failures
		{"arn:aws:kinesis:us-east-1:000000000000:stream/events", "kinesis", "events", "/kinesis/events"},
		{"arn:aws:dynamodb:us-east-1:000000000000:table/orders", "ddb", "orders", "/ddb/orders"},
		{"arn:aws:dynamodb:us-east-1:000000000000:table/orders/stream/2026-01-01T00:00:00", "ddb", "orders", "/ddb/orders"},
		// the bus is part of a rule's identity — two buses may hold the same name
		{"arn:aws:events:us-east-1:000000000000:rule/billing/nightly", "eb", "nightly", "/eb/billing/rule/nightly"},
		{"arn:aws:events:us-east-1:000000000000:event-bus/billing", "eb", "billing", "/eb/billing"},
		{"arn:aws:cloudformation:us-east-1:000000000000:stack/api/7f-2c", "cfn", "api", "/cfn/api"},
		{"arn:aws:iam::000000000000:role/worker", "iam", "worker", "/iam/role/worker"},
		{"arn:aws:s3:::uploads", "s3", "uploads", "/s3/uploads"},
		// a queue URL is not an ARN but arrives in the same fields
		{"http://127.0.0.1:4566/000000000000/ingest", "sqs", "ingest", "/sqs/ingest"},
	} {
		got := resourceFromARN(c.arn)
		if got.Svc != c.svc || got.Name != c.name || got.Path != c.path {
			t.Errorf("%s\n  got  svc=%q name=%q path=%q\n  want svc=%q name=%q path=%q",
				c.arn, got.Svc, got.Name, got.Path, c.svc, c.name, c.path)
		}
	}
}

func TestResourceURLEdges(t *testing.T) {
	// An S3 key must land you in the folder the object is in, not at the root:
	// classify hands the wire "bucket/key" for every S3 call.
	if r := resourceURL("s3", "drop/2026/01/order.json"); r.Path != "/s3/drop?prefix=2026%2F01%2F" {
		t.Errorf("s3 key: path=%q", r.Path)
	}
	// Secrets Manager appends six random characters to the name in the ARN.
	if r := resourceURL("sm", "secret:prod/db-AbCdEf"); r.Name != "prod/db" {
		t.Errorf("sm suffix: name=%q", r.Name)
	}
	// A KMS alias needs a lookup to become a key id. Name it, do not link it —
	// a wrong link is worse than plain text.
	r := resourceURL("kms", "alias/app")
	if r.Name != "alias/app" || r.Path != "" {
		t.Errorf("kms alias: name=%q path=%q (want a name and no path)", r.Name, r.Path)
	}
	// Unresolvable input must never produce a link.
	for _, bad := range []string{"", "not-an-arn", "arn:aws:route53:::hostedzone/Z1"} {
		if got := resourceFromARN(bad); got.OK() {
			t.Errorf("%q resolved to %q — should not link", bad, got.Path)
		}
	}
}

// Key is what the wiring graph uses for node identity. It differs from Name
// only where the name alone is not unique — which today is EventBridge rules,
// and is exactly the collision the graph used to have.
func TestRefKeyDisambiguatesEventBridgeRules(t *testing.T) {
	a := resourceURL("eb", "billing/nightly")
	b := resourceURL("eb", "orders/nightly")
	if a.Name != "nightly" || b.Name != "nightly" {
		t.Fatalf("display names should both be nightly: %q %q", a.Name, b.Name)
	}
	if a.Key == b.Key {
		t.Errorf("two buses, same rule name, same key %q — they would collapse into one node", a.Key)
	}
	if a.Path == b.Path {
		t.Errorf("both link to %q — one of them is wrong", a.Path)
	}
	if bus := resourceURL("eb", "billing"); bus.Key == a.Key {
		t.Errorf("bus and rule share key %q", bus.Key)
	}
}
