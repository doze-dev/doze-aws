package dozeaws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// SQS compares the account in a queue URL with its own (SQS-23), so the
// helpers other services use to reach a queue must not name an account that is
// not this stack's. They did: peercall built every URL from the default account
// constant, and an EventBridge rule, an S3 notification or a Lambda trigger
// could not deliver to a queue under any other --account-id (SQS-30, LAM-36).
func TestPeerHelpersReachAQueueUnderAnotherAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	const account = "111122223333"
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t),
		Services: []string{"sqs"}, Identity: awsident.Identity{Region: "eu-west-1", AccountID: account}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)

	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	c := awssqs.NewFromConfig(aws.Config{Region: "eu-west-1", Credentials: creds},
		func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("jobs")})
	if err != nil {
		t.Fatal(err)
	}
	dir := peers.Static{"sqs": peers.Endpoint{Client: http.DefaultClient, BaseURL: ts.URL}}

	if err := peercall.SQSSend(ctx, dir, "jobs", "one", nil); err != nil {
		t.Errorf("SQSSend: %v", err)
	}
	if _, _, err := peercall.SQSSendDetailed(ctx, dir, "jobs", "two", nil); err != nil {
		t.Errorf("SQSSendDetailed: %v", err)
	}
	msgs, err := peercall.SQSReceive(ctx, dir, "jobs", 10, 0)
	if err != nil {
		t.Fatalf("SQSReceive: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("SQSReceive returned %d messages, want the two sent", len(msgs))
	}
	for _, m := range msgs {
		if err := peercall.SQSDelete(ctx, dir, "jobs", m.ReceiptHandle); err != nil {
			t.Errorf("SQSDelete: %v", err)
		}
	}
	// And the other side still holds: a URL that does name another account's
	// queue is not this one.
	other := *q.QueueUrl
	other = other[:len(other)-len(account)-len("/jobs")] + "999999999999/jobs"
	if _, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(other)}); err == nil {
		t.Errorf("a queue URL in another account was served")
	}
}
