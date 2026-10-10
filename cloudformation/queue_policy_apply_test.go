package cloudformation_test

// AWS::SQS::QueuePolicy was recognised and dropped, so a template's grant never
// reached the queue (CFN-4, the queue part). A stack that gives an SNS topic or
// an S3 bucket the right to send into a queue did nothing of the kind, and
// under IAM enforce the delivery was then refused for want of the statement the
// template had written.

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

func TestAQueuePolicyReachesTheQueue(t *testing.T) {
	// The policy is declared BEFORE the queue it is for, as templates do.
	const tmpl = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  InboxPolicy:
    Type: AWS::SQS::QueuePolicy
    Properties:
      Queues: [!Ref Inbox]
      PolicyDocument:
        Version: "2012-10-17"
        Statement:
          - Sid: FromTopic
            Effect: Allow
            Principal: {Service: sns.amazonaws.com}
            Action: sqs:SendMessage
            Resource: !GetAtt Inbox.Arn
  Inbox:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: inbox
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
		t.Fatal(err)
	}

	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	q, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("inbox")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNamePolicy}})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Attributes["Policy"]; !strings.Contains(p, "FromTopic") || !strings.Contains(p, "sns.amazonaws.com") {
		t.Errorf("the queue has no policy from the template: %q", p)
	}
}

// A policy may name a queue the template does not declare, by URL; it has to
// exist when the stack is applied, and the failure names it.
func TestAQueuePolicyForAQueueThatIsNotThereFailsTheStack(t *testing.T) {
	const tmpl = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orphan:
    Type: AWS::SQS::QueuePolicy
    Properties:
      Queues: ["http://localhost/000000000000/nobody"]
      PolicyDocument:
        Version: "2012-10-17"
        Statement:
          - {Effect: Allow, Principal: "*", Action: "sqs:SendMessage", Resource: "*"}
`
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()

	parsed, err := cfn.Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	sf, _, err := cfn.Transpile(parsed, cfn.TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	_, err = provision.Apply(ctx, stack.Handler(), sf, awsident.Default())
	if err == nil {
		t.Fatal("a policy for a queue that does not exist was applied")
	}
	if !strings.Contains(err.Error(), "nobody") {
		t.Errorf("the failure does not name the queue: %v", err)
	}
}
