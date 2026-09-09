package iam_test

// The handoff between the middleware and the services is a set of X-Doze-*
// request headers: the mode, the principal, the identity verdict, and the
// pair it was reached for. Headers are the one part of a request a client
// controls completely, so the middleware strips every inbound X-Doze-* before
// it stamps its own.
//
// That strip was unit-tested on the function and never end to end. Nothing
// would have caught a refactor that moved iamguard.Strip out of the request
// path — the unit test would still pass, and the emulator would accept
// "X-Doze-Identity: allowed" from anyone who sent it. This is the test that
// asks the running stack.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

// headerInjector adds headers AFTER the SDK has signed the request, which is
// exactly what an attacker can do and a legitimate client cannot: the
// signature does not cover them.
type headerInjector struct {
	inner   *http.Client
	headers map[string]string
}

func (h headerInjector) Do(r *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.inner.Do(r)
}

// TestClientSuppliedSourceArnIsIgnored is the test with teeth.
//
// Five of the six handoff headers are self-healing: Stamp overwrites mode,
// principal, identity, action and resource on every request, so a test that
// spoofs those passes whether or not the strip runs — I wrote that test first
// and it passed with iamguard.Strip commented out, which is the definition of
// proving nothing.
//
// X-Doze-Source-Arn is the exception. Stamp never writes it; it is set only
// by a genuine peer call (peers.WithSourceARN), and it supplies the
// aws:SourceArn a resource policy conditions on. Strip is the only thing
// standing between a client and any policy written "allow, but only when the
// call comes from this bucket".
func TestClientSuppliedSourceArnIsIgnored(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)

	rootSQS := sqsClient(rootCfg(), endpoint)
	q, err := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("hooks")})
	if err != nil {
		t.Fatal(err)
	}
	// The shape every S3-notification and EventBridge-target policy has: an
	// allow for anyone, narrowed to calls originating from one resource.
	const trusted = "arn:aws:s3:::trusted-bucket"
	if _, err := rootSQS.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl: q.QueueUrl,
		Attributes: map[string]string{"Policy": `{"Version":"2012-10-17","Statement":[{"Sid":"OnlyFromBucket",` +
			`"Effect":"Allow","Principal":"*","Action":"sqs:SendMessage","Resource":"*",` +
			`"Condition":{"ArnEquals":{"aws:SourceArn":"` + trusted + `"}}}]}`},
	}); err != nil {
		t.Fatal(err)
	}

	// guest has no identity policy: only that conditioned allow could admit
	// it, and only if it could name the source.
	cfg := userConfig(t, root, "guest", noPolicy)
	cfg.HTTPClient = headerInjector{inner: http.DefaultClient, headers: map[string]string{
		"X-Doze-Source-Arn": trusted,
	}}
	spoofer := sqsClient(cfg, endpoint)

	_, err = spoofer.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: q.QueueUrl, MessageBody: aws.String("hi")})
	if err == nil {
		t.Fatal("a client that supplies its own X-Doze-Source-Arn satisfied a policy condition it must not reach")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("want AccessDenied, got %v", err)
	}
}

// TestClientSuppliedDozeHeadersAreIgnored: a request that claims to be root,
// already allowed, for a resource it is not asking about, is still evaluated
// as the identity that signed it. Stamp makes this true by overwriting, so
// this one holds even without the strip — it is here to pin the overwrite,
// not the strip.
func TestClientSuppliedDozeHeadersAreIgnored(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)

	rootSQS := sqsClient(rootCfg(), endpoint)
	q, err := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}

	// guest has no identity policy and no resource policy admits it.
	cfg := userConfig(t, root, "guest", noPolicy)
	cfg.HTTPClient = headerInjector{inner: http.DefaultClient, headers: map[string]string{
		"X-Doze-Iam-Mode":  "off",
		"X-Doze-Principal": awsident.GlobalARN("iam", "root"),
		"X-Doze-Identity":  "allowed",
		"X-Doze-Action":    "sqs:SendMessage",
		"X-Doze-Resource":  "*",
	}}
	spoofer := sqsClient(cfg, endpoint)

	_, err = spoofer.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: q.QueueUrl, MessageBody: aws.String("hi")})
	if err == nil {
		t.Fatal("a client that sends its own X-Doze-Identity must still be denied")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("want AccessDenied, got %v", err)
	}
	// The denial must name the signing identity, not the claimed one. If it
	// says "root", the stamp was not overwritten.
	if !strings.Contains(err.Error(), "user/guest") {
		t.Errorf("the denial must name the signer, not the claimed principal: %v", err)
	}
	if strings.Contains(err.Error(), "user/root") {
		t.Errorf("the claimed principal reached the evaluator: %v", err)
	}
}

// TestClientCannotDowngradeTheMode: "X-Doze-Iam-Mode: off" would turn every
// guard into a no-op if it survived. Covered by the test above through the
// denial, and separately here on a service whose guard reads the mode first.
func TestClientCannotDowngradeTheMode(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)
	rootSQS := sqsClient(rootCfg(), endpoint)
	q, err := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("guarded")})
	if err != nil {
		t.Fatal(err)
	}
	writer := awsident.GlobalARN("iam", "user/writer")
	if _, err := rootSQS.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl: q.QueueUrl,
		Attributes: map[string]string{"Policy": `{"Version":"2012-10-17","Statement":[{"Sid":"NoWriter",` +
			`"Effect":"Deny","Principal":{"AWS":"` + writer + `"},"Action":"sqs:SendMessage","Resource":"*"}]}`},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := userConfig(t, root, "writer", allowAll)
	cfg.HTTPClient = headerInjector{inner: http.DefaultClient, headers: map[string]string{
		"X-Doze-Iam-Mode": "off",
	}}
	writerSQS := sqsClient(cfg, endpoint)

	_, err = writerSQS.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl: q.QueueUrl, MessageBody: aws.String("hi")})
	wantDenied(t, err, "a client asking for mode off", "NoWriter")
}
