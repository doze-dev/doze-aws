// End-to-end: a CloudFormation template applied against a real doze-aws stack,
// then verified with AWS SDK calls. This is the test that matters — everything
// else proves the transpiler produces the right IR, and this proves the IR
// actually provisions.
package cloudformation_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/internal/provision"
)

const template = `
AWSTemplateFormatVersion: "2010-09-09"
Description: end-to-end template
Parameters:
  Stage:
    Type: String
    Default: dev
Conditions:
  IsProd: !Equals [!Ref Stage, prod]
Resources:
  DeadLetters:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${Stage}-dlq"
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${Stage}-orders"
      VisibilityTimeout: 45
      RedrivePolicy:
        deadLetterTargetArn: !GetAtt DeadLetters.Arn
        maxReceiveCount: 3
  OrderEvents:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: !Sub "${Stage}-order-events"
      Subscription:
        - Protocol: sqs
          Endpoint: !GetAtt Orders.Arn
          RawMessageDelivery: true
  Uploads:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub "${Stage}-uploads"
      VersioningConfiguration:
        Status: Enabled
  Sessions:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-sessions"
      AttributeDefinitions:
        - {AttributeName: pk, AttributeType: S}
      KeySchema:
        - {AttributeName: pk, KeyType: HASH}
  ProdOnly:
    Type: AWS::SQS::Queue
    Condition: IsProd
  ExecutionRole:
    Type: AWS::IAM::Role
    Properties:
      AssumeRolePolicyDocument: {}
Outputs:
  OrdersUrl:
    Value: !Ref Orders
  TopicArn:
    Value: !GetAtt OrderEvents.TopicArn
`

func TestApplyTemplateToLiveStack(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	// Transpile, then converge with the existing stack-file apply.
	tmpl, err := cloudformation.Parse([]byte(template))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	applyRep, err := provision.Apply(ctx, stack.Handler(), sf)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if created, _, _ := applyRep.Counts(); created == 0 {
		t.Fatalf("apply created nothing: %+v", applyRep.Actions)
	}

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })
	ddb := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// The queues exist, and the redrive policy really wired the DLQ.
	q, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("dev-orders")})
	if err != nil {
		t.Fatalf("GetQueueUrl(dev-orders): %v", err)
	}
	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}
	if attrs.Attributes["VisibilityTimeout"] != "45" {
		t.Errorf("VisibilityTimeout = %q, want 45", attrs.Attributes["VisibilityTimeout"])
	}
	if rp := attrs.Attributes["RedrivePolicy"]; !strings.Contains(rp, "dev-dlq") {
		t.Errorf("RedrivePolicy = %q, want it to name dev-dlq", rp)
	}

	// The bucket exists with versioning on.
	ver, err := s3c.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: aws.String("dev-uploads")})
	if err != nil {
		t.Fatalf("GetBucketVersioning: %v", err)
	}
	if string(ver.Status) != "Enabled" {
		t.Errorf("versioning = %q", ver.Status)
	}

	// The table exists with the right key.
	desc, err := ddb.DescribeTable(ctx, &awsddb.DescribeTableInput{TableName: aws.String("dev-sessions")})
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if len(desc.Table.KeySchema) != 1 || aws.ToString(desc.Table.KeySchema[0].AttributeName) != "pk" {
		t.Errorf("key schema = %+v", desc.Table.KeySchema)
	}

	// The condition was false, so the prod-only queue must NOT exist.
	if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("ProdOnly")}); err == nil {
		t.Error("a resource gated by a false condition was created anyway")
	}

	// The end-to-end proof that the SNS→SQS subscription really wired: publish
	// to the topic and receive on the queue.
	topicArn := rep.Outputs["TopicArn"]
	if topicArn == "" {
		t.Fatal("no TopicArn output")
	}
	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: aws.String(topicArn), Message: aws.String("hello-from-cfn"),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	recv, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, WaitTimeSeconds: 3, MaxNumberOfMessages: 1,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if len(recv.Messages) != 1 {
		t.Fatal("the template's SNS subscription did not deliver to the queue")
	}
	// RawMessageDelivery: true means the body is the message, not an envelope.
	if body := aws.ToString(recv.Messages[0].Body); body != "hello-from-cfn" {
		t.Errorf("raw delivery body = %q", body)
	}
}

// TestApplyIsConvergent proves re-applying the same template is a no-op, which
// is what makes the transpiler safe to run repeatedly.
func TestApplyIsConvergent(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()

	apply := func() (created, updated, skipped int) {
		tmpl, err := cloudformation.Parse([]byte(template))
		if err != nil {
			t.Fatal(err)
		}
		sf, _, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "shop"})
		if err != nil {
			t.Fatal(err)
		}
		rep, err := provision.Apply(ctx, stack.Handler(), sf)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return rep.Counts()
	}

	created, _, _ := apply()
	if created == 0 {
		t.Fatal("first apply should create resources")
	}
	created2, _, skipped2 := apply()
	if created2 != 0 {
		t.Errorf("second apply created %d resources; it should be convergent", created2)
	}
	if skipped2 == 0 {
		t.Error("second apply should report resources already in place")
	}
}

func TestLooksLikeTemplate(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"cfn yaml", template, true},
		{"cfn json", `{"Resources":{"Q":{"Type":"AWS::SQS::Queue"}}}`, true},
		{"sam", "Transform: AWS::Serverless-2016-10-31\nResources: {}", true},
		{"stack file", "queues:\n  orders:\n    fifo: true", false},
		{"stack file with buckets", "buckets:\n  uploads:\n    versioning: true", false},
		{"garbage", "not a document at all: [", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := cloudformation.LooksLikeTemplate([]byte(c.raw)); got != c.want {
			t.Errorf("%s: LooksLikeTemplate = %v, want %v", c.name, got, c.want)
		}
	}
}
