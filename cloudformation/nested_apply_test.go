package cloudformation_test

// A nested stack through the wire: the child template staged in the local
// S3 as CDK stages it, the parent created with TemplateBody, both stacks
// describable (the child with ParentId and RootId), the child's queue real,
// a direct delete of the child refused, and the parent's delete taking the
// child and its resources with it.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

const nestedParentWire = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Inbox:
    Type: AWS::SQS::Queue
  QueuesNestedStackQueuesNestedStackResourceAB12CD:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: https://s3.us-east-1.amazonaws.com/cdk-staging/child.yaml
      Parameters:
        InboxArn: !GetAtt Inbox.Arn
        Prefix: orders
Outputs:
  Work:
    Value: !GetAtt QueuesNestedStackQueuesNestedStackResourceAB12CD.Outputs.WorkArn
`

const nestedChildWire = `
AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  InboxArn: {Type: String}
  Prefix: {Type: String}
Resources:
  Work:
    Type: AWS::SQS::Queue
  Named:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub ${Prefix}-named
Outputs:
  WorkArn:
    Value: !GetAtt Work.Arn
`

func TestNestedStackDeploysAndDeletes(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewServer(st.Handler())
	defer ts.Close()
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	cfn := awscfn.NewFromConfig(cfg, func(o *awscfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// Stage the child as CDK does.
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("cdk-staging")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("cdk-staging"), Key: aws.String("child.yaml"), Body: strings.NewReader(nestedChildWire)}); err != nil {
		t.Fatal(err)
	}

	out, err := cfn.CreateStack(ctx, &awscfn.CreateStackInput{StackName: aws.String("app"), TemplateBody: aws.String(nestedParentWire)})
	if err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	parent, err := cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("app")})
	if err != nil || parent.Stacks[0].StackStatus != cfntypes.StackStatusCreateComplete {
		t.Fatalf("parent: %v %+v", err, parent)
	}
	if v := aws.ToString(parent.Stacks[0].Outputs[0].OutputValue); v != "arn:aws:sqs:us-east-1:000000000000:Queues-Work" {
		t.Errorf("parent output: %q", v)
	}
	child, err := cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("app-Queues")})
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	c := child.Stacks[0]
	if c.StackStatus != cfntypes.StackStatusCreateComplete || aws.ToString(c.ParentId) != aws.ToString(out.StackId) || aws.ToString(c.RootId) != aws.ToString(out.StackId) {
		t.Errorf("child record: status %s parent %q root %q (want %q)", c.StackStatus, aws.ToString(c.ParentId), aws.ToString(c.RootId), aws.ToString(out.StackId))
	}
	res, _ := cfn.DescribeStackResources(ctx, &awscfn.DescribeStackResourcesInput{StackName: aws.String("app")})
	childRow := false
	for _, r := range res.StackResources {
		if aws.ToString(r.ResourceType) == "AWS::CloudFormation::Stack" && strings.Contains(aws.ToString(r.PhysicalResourceId), "stack/app-Queues/") {
			childRow = true
		}
	}
	if !childRow {
		t.Errorf("the parent's resources should name the child stack: %+v", res.StackResources)
	}
	// The child's queues exist for real: the prefixed one and the named one.
	for _, q := range []string{"Queues-Work", "orders-named", "Inbox"} {
		if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(q)}); err != nil {
			t.Errorf("queue %s: %v", q, err)
		}
	}
	// The child cannot be deleted on its own.
	if _, err := cfn.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("app-Queues")}); err == nil || !strings.Contains(err.Error(), "nested stack") {
		t.Errorf("deleting the child directly should be refused, got %v", err)
	}
	// The staging object may be gone by delete time; the parent kept the body.
	s3c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("cdk-staging"), Key: aws.String("child.yaml")})
	if _, err := cfn.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("app")}); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}
	for _, q := range []string{"Queues-Work", "orders-named", "Inbox"} {
		if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(q)}); err == nil {
			t.Errorf("queue %s survived the parent's delete", q)
		}
	}
	gone, err := cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: c.StackId})
	if err != nil || gone.Stacks[0].StackStatus != cfntypes.StackStatusDeleteComplete {
		t.Errorf("child after the parent's delete: %v %+v", err, gone)
	}

	// A change set on a nested parent transpiles the child too.
	s3c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("cdk-staging"), Key: aws.String("child.yaml"), Body: strings.NewReader(nestedChildWire)})
	cs, err := cfn.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("app2"), ChangeSetName: aws.String("initial"), ChangeSetType: cfntypes.ChangeSetTypeCreate,
		TemplateBody: aws.String(nestedParentWire),
	})
	if err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}
	if _, err := cfn.ExecuteChangeSet(ctx, &awscfn.ExecuteChangeSetInput{ChangeSetName: cs.Id}); err != nil {
		t.Fatalf("ExecuteChangeSet: %v", err)
	}
	if _, err := cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("app2-Queues")}); err != nil {
		t.Errorf("child of the change-set stack: %v", err)
	}
}
