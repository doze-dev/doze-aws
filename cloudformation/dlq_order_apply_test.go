package cloudformation_test

// The usual way to name a dead-letter queue is the queue's name with -dlq on
// the end, which sorts after it. A template with the two used to fail: the
// queue was applied first, with a redrive policy naming a queue that did not
// exist yet. (SQS-1, the same bug as in internal/provision, through the
// CloudFormation path.)

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/cfn"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/provision"
)

func TestAQueueAndItsDeadLetterQueueInOneTemplate(t *testing.T) {
	const tmpl = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: orders
      RedrivePolicy:
        deadLetterTargetArn: !GetAtt OrdersDlq.Arn
        maxReceiveCount: 4
  OrdersDlq:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: orders-dlq
`
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	parsed, err := cfn.Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	sf, _, err := cfn.Transpile(parsed, cfn.TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provision.Apply(ctx, stack.Handler(), sf, awsident.Default()); err != nil {
		t.Fatalf("a queue and its dead-letter queue in one template: %v", err)
	}

	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	q, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameRedrivePolicy}})
	if err != nil {
		t.Fatal(err)
	}
	if rp := got.Attributes["RedrivePolicy"]; !strings.Contains(rp, "orders-dlq") || !strings.Contains(rp, `"4"`) && !strings.Contains(rp, ":4") {
		t.Errorf("RedrivePolicy = %q, want it to name orders-dlq with a maxReceiveCount of 4", rp)
	}
}
