// Service tests: the stack lifecycle deploy tools drive, exercised with a real
// aws-sdk-go-v2 CloudFormation client.
//
// The flows here mirror what `aws cloudformation deploy`, `sam deploy` and
// `cdk deploy` actually do — including the two behaviours that only turned up
// when those CLIs were run for real: a CREATE change set must materialise the
// stack in REVIEW_IN_PROGRESS before it is executed, and an empty change set
// must fail with the exact message the CLI special-cases.
package cloudformation_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

const svcTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Stage: {Type: String, Default: dev}
Resources:
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${Stage}-orders"
      VisibilityTimeout: 45
  Role:
    Type: AWS::IAM::Role
    Properties: {AssumeRolePolicyDocument: {}}
Outputs:
  QueueUrl:
    Value: !Ref Orders
    Export:
      Name: !Sub "${AWS::StackName}-queue"
`

func cfnStack(t *testing.T) (*awscfn.Client, *awssqs.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(st.Handler())
	t.Cleanup(ts.Close)

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	return awscfn.NewFromConfig(cfg, func(o *awscfn.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func TestServiceCreateStackProvisionsForReal(t *testing.T) {
	ctx := context.Background()
	c, sqs := cfnStack(t)

	out, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName:    aws.String("shop"),
		TemplateBody: aws.String(svcTemplate),
	})
	if err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	if !strings.Contains(aws.ToString(out.StackId), ":stack/shop/") {
		t.Fatalf("StackId = %q", aws.ToString(out.StackId))
	}

	desc, err := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("shop")})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}
	st := desc.Stacks[0]
	if st.StackStatus != cfntypes.StackStatusCreateComplete {
		t.Fatalf("status = %s", st.StackStatus)
	}
	if len(st.Outputs) != 1 || aws.ToString(st.Outputs[0].ExportName) != "shop-queue" {
		t.Fatalf("outputs = %+v", st.Outputs)
	}

	// The resource is genuinely provisioned, not merely recorded.
	if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("dev-orders")}); err != nil {
		t.Fatalf("the queue was recorded but not created: %v", err)
	}

	// A duplicate create conflicts.
	_, err = c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	})
	if err == nil || !strings.Contains(err.Error(), "AlreadyExists") {
		t.Fatalf("duplicate CreateStack should conflict, got %v", err)
	}
}

// TestServiceChangeSetFlow is the exact sequence `aws cloudformation deploy`
// and `sam deploy` follow.
func TestServiceChangeSetFlow(t *testing.T) {
	ctx := context.Background()
	c, sqs := cfnStack(t)

	cs, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName:     aws.String("shop"),
		ChangeSetName: aws.String("deploy-1"),
		TemplateBody:  aws.String(svcTemplate),
		ChangeSetType: cfntypes.ChangeSetTypeCreate,
	})
	if err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}

	// The stack must exist in REVIEW_IN_PROGRESS the moment the change set is
	// created — sam deploy polls events here, before executing.
	desc, err := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("shop")})
	if err != nil {
		t.Fatalf("a CREATE change set must materialise the stack: %v", err)
	}
	if desc.Stacks[0].StackStatus != cfntypes.StackStatusReviewInProgress {
		t.Fatalf("status before execute = %s, want REVIEW_IN_PROGRESS", desc.Stacks[0].StackStatus)
	}
	if _, err := c.DescribeStackEvents(ctx, &awscfn.DescribeStackEventsInput{
		StackName: aws.String("shop"),
	}); err != nil {
		t.Fatalf("DescribeStackEvents before execute: %v", err)
	}

	described, err := c.DescribeChangeSet(ctx, &awscfn.DescribeChangeSetInput{
		StackName: aws.String("shop"), ChangeSetName: aws.String("deploy-1"),
	})
	if err != nil {
		t.Fatalf("DescribeChangeSet: %v", err)
	}
	if described.Status != cfntypes.ChangeSetStatusCreateComplete {
		t.Fatalf("change set status = %s (%s)", described.Status, aws.ToString(described.StatusReason))
	}
	if described.ExecutionStatus != cfntypes.ExecutionStatusAvailable {
		t.Fatalf("execution status = %s", described.ExecutionStatus)
	}
	// Only the mappable resource shows up as a change; the IAM role does not.
	if len(described.Changes) != 1 {
		t.Fatalf("changes = %+v", described.Changes)
	}
	if described.Changes[0].ResourceChange.Action != cfntypes.ChangeActionAdd {
		t.Fatalf("action = %s", described.Changes[0].ResourceChange.Action)
	}

	if _, err := c.ExecuteChangeSet(ctx, &awscfn.ExecuteChangeSetInput{
		StackName: aws.String("shop"), ChangeSetName: cs.Id,
	}); err != nil {
		t.Fatalf("ExecuteChangeSet: %v", err)
	}

	desc, _ = c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("shop")})
	if desc.Stacks[0].StackStatus != cfntypes.StackStatusCreateComplete {
		t.Fatalf("status after execute = %s", desc.Stacks[0].StackStatus)
	}
	if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("dev-orders")}); err != nil {
		t.Fatalf("execute did not provision: %v", err)
	}
}

// TestServiceEmptyChangeSetMessage covers the case the CLI matches on by text:
// a no-op redeploy must FAIL with "didn't contain changes", which the CLI then
// reports as "No changes to deploy".
func TestServiceEmptyChangeSetMessage(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("shop"), ChangeSetName: aws.String("noop"),
		TemplateBody: aws.String(svcTemplate), ChangeSetType: cfntypes.ChangeSetTypeUpdate,
	}); err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}
	got, err := c.DescribeChangeSet(ctx, &awscfn.DescribeChangeSetInput{
		StackName: aws.String("shop"), ChangeSetName: aws.String("noop"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != cfntypes.ChangeSetStatusFailed {
		t.Fatalf("an unchanged template should produce a FAILED change set, got %s", got.Status)
	}
	if !strings.Contains(aws.ToString(got.StatusReason), "didn't contain changes") {
		t.Fatalf("StatusReason %q must contain the phrase the CLI matches on",
			aws.ToString(got.StatusReason))
	}
}

func TestServiceUpdateStack(t *testing.T) {
	ctx := context.Background()
	c, sqs := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	}); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(svcTemplate, "  Role:", `  Extra:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: extra
  Role:`, 1)

	if _, err := c.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(updated),
	}); err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}
	desc, _ := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("shop")})
	if desc.Stacks[0].StackStatus != cfntypes.StackStatusUpdateComplete {
		t.Fatalf("status = %s", desc.Stacks[0].StackStatus)
	}
	if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("extra")}); err != nil {
		t.Fatalf("the update did not create the added queue: %v", err)
	}
	res, err := c.ListStackResources(ctx, &awscfn.ListStackResourcesInput{StackName: aws.String("shop")})
	if err != nil || len(res.StackResourceSummaries) != 2 {
		t.Fatalf("resources = %+v, %v", res.StackResourceSummaries, err)
	}
}

// TestServiceDeleteStackReclaimsResources covers the capability the transpiler
// alone could not have.
func TestServiceDeleteStackReclaimsResources(t *testing.T) {
	ctx := context.Background()
	c, sqs := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("dev-orders")}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("shop")}); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}
	// The resource is genuinely gone, not just forgotten.
	if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("dev-orders")}); err == nil {
		t.Fatal("DeleteStack left the queue behind")
	}
	if _, err := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("shop")}); err == nil {
		t.Fatal("the stack record survived deletion")
	}
	// Deleting again is a no-op, as in AWS.
	if _, err := c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("shop")}); err != nil {
		t.Fatalf("DeleteStack should be idempotent, got %v", err)
	}
}

func TestServiceExportsAndImports(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("base"), TemplateBody: aws.String(svcTemplate),
	}); err != nil {
		t.Fatal(err)
	}
	exports, err := c.ListExports(ctx, &awscfn.ListExportsInput{})
	if err != nil || len(exports.Exports) != 1 {
		t.Fatalf("ListExports = %+v, %v", exports.Exports, err)
	}
	if aws.ToString(exports.Exports[0].Name) != "base-queue" {
		t.Fatalf("export name = %q", aws.ToString(exports.Exports[0].Name))
	}

	// A second stack importing that export resolves against the registry.
	importer := `
Resources:
  Mirror:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: mirror
Outputs:
  Borrowed:
    Value: !ImportValue base-queue
`
	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("consumer"), TemplateBody: aws.String(importer),
	}); err != nil {
		t.Fatalf("cross-stack ImportValue failed: %v", err)
	}
	desc, _ := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("consumer")})
	if !strings.Contains(aws.ToString(desc.Stacks[0].Outputs[0].OutputValue), "dev-orders") {
		t.Fatalf("imported value = %q", aws.ToString(desc.Stacks[0].Outputs[0].OutputValue))
	}

	// The exporting stack cannot be deleted while another imports from it.
	if _, err := c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("base")}); err == nil {
		t.Fatal("deleting a stack whose export is in use should be refused")
	}
}

func TestServiceStackEventsReachTerminalStatus(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	}); err != nil {
		t.Fatal(err)
	}
	ev, err := c.DescribeStackEvents(ctx, &awscfn.DescribeStackEventsInput{StackName: aws.String("shop")})
	if err != nil {
		t.Fatalf("DescribeStackEvents: %v", err)
	}
	if len(ev.StackEvents) == 0 {
		t.Fatal("no events")
	}
	// Newest first, and the newest must be terminal — that is what stops a
	// deploy tool's poll loop.
	first := ev.StackEvents[0]
	if string(first.ResourceStatus) != "CREATE_COMPLETE" ||
		aws.ToString(first.ResourceType) != "AWS::CloudFormation::Stack" {
		t.Fatalf("newest event = %s %s, want a terminal stack event",
			aws.ToString(first.ResourceType), first.ResourceStatus)
	}
}

func TestServiceBadTemplateRecordsFailure(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	_, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("broken"),
		TemplateBody: aws.String(`
Resources:
  Cluster:
    Type: AWS::ECS::Cluster
`),
	})
	if err == nil {
		t.Fatal("an unsupported resource type should fail the deploy")
	}
	// The failure is also visible through the normal describe path, so a
	// polling client is not left guessing.
	desc, derr := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("broken")})
	if derr != nil {
		t.Fatalf("a failed stack should still be describable: %v", derr)
	}
	if desc.Stacks[0].StackStatus != cfntypes.StackStatusCreateFailed {
		t.Fatalf("status = %s, want CREATE_FAILED", desc.Stacks[0].StackStatus)
	}
	if !strings.Contains(aws.ToString(desc.Stacks[0].StackStatusReason), "ECS") {
		t.Fatalf("reason should name the service: %q", aws.ToString(desc.Stacks[0].StackStatusReason))
	}
}

func TestServiceValidateAndGetTemplate(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	v, err := c.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{TemplateBody: aws.String(svcTemplate)})
	if err != nil {
		t.Fatalf("ValidateTemplate: %v", err)
	}
	if len(v.Parameters) != 1 || aws.ToString(v.Parameters[0].ParameterKey) != "Stage" {
		t.Fatalf("parameters = %+v", v.Parameters)
	}
	if _, err := c.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{
		TemplateBody: aws.String("not a template"),
	}); err == nil {
		t.Fatal("a malformed template should not validate")
	}

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetTemplate(ctx, &awscfn.GetTemplateInput{StackName: aws.String("shop")})
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if !strings.Contains(aws.ToString(got.TemplateBody), "AWS::SQS::Queue") {
		t.Fatal("GetTemplate did not return the deployed template")
	}
}

// TestServiceDeletedStackQueryableById is the fourth regression the real CLIs
// found: `cdk destroy` polls DescribeStacks by StackId and only declares
// success when it sees DELETE_COMPLETE. Removing the record outright made it
// report a failed destroy even though the resources were gone.
func TestServiceDeletedStackQueryableById(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	created, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	})
	if err != nil {
		t.Fatal(err)
	}
	stackID := aws.ToString(created.StackId)

	if _, err := c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("shop")}); err != nil {
		t.Fatal(err)
	}
	// By ID: still there, DELETE_COMPLETE.
	byID, err := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String(stackID)})
	if err != nil {
		t.Fatalf("a deleted stack must stay queryable by id: %v", err)
	}
	if byID.Stacks[0].StackStatus != cfntypes.StackStatusDeleteComplete {
		t.Fatalf("status by id = %s, want DELETE_COMPLETE", byID.Stacks[0].StackStatus)
	}
	// By name: gone.
	if _, err := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("shop")}); err == nil {
		t.Fatal("a deleted stack must not resolve by name")
	}
	// And the name is free to reuse.
	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("shop"), TemplateBody: aws.String(svcTemplate),
	}); err != nil {
		t.Fatalf("re-creating a deleted stack should be allowed: %v", err)
	}
}
