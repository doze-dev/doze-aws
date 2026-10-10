package dozeaws_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws/awsident"
)

// TestEnforceQueuePolicyDenyRefusesTheRoot: a Deny in a queue's policy refuses
// even the account's root, for the action it names and no other, and the
// refusal is stamped as IAM's so the console reads it as a denial.
// The service-principal half is TestEnforceSNSToSQSNeedsQueuePolicy.
func TestEnforceQueuePolicyDenyRefusesTheRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	ctx := context.Background()
	cfg, url := enforceStack(t, "iam", "sqs")
	c := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })

	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("guarded")})
	if err != nil {
		t.Fatal(err)
	}
	send := func() error {
		_, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("x")})
		return err
	}
	if err := send(); err != nil {
		t.Fatalf("root could not send before any policy: %v", err)
	}
	if _, err := c.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl, Attributes: map[string]string{
		"Policy": `{"Version":"2012-10-17","Statement":[{"Sid":"NoSend","Effect":"Deny","Principal":"*","Action":"sqs:SendMessage","Resource":"` +
			awsident.Default().ARN("sqs", "guarded") + `"}]}`,
	}}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	err = send()
	if err == nil {
		t.Fatal("a queue policy's Deny did not refuse the root")
	}
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "AccessDenied" && api.ErrorCode() != "AccessDeniedException" {
		t.Errorf("refused with %v, want an access-denied error", err)
	}
	var resp *awshttp.ResponseError
	if errors.As(err, &resp) && resp.Response.Header.Get("X-Doze-Denied") == "" {
		t.Errorf("the refusal carries no X-Doze-Denied stamp")
	}
	// Only the action the Deny names.
	if _, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl}); err != nil {
		t.Errorf("a Deny on SendMessage refused GetQueueAttributes: %v", err)
	}
}
