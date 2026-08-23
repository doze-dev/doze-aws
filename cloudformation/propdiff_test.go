package cloudformation_test

// A change set has to see a property-only edit.
//
// It did not, for as long as change sets have existed here: diffChanges
// compared resource identity — logical id, type, physical name — and a queue
// whose VisibilityTimeout went from 30 to 45 matched on all three. The set
// landed in FAILED carrying "the submitted information didn't contain changes",
// which is the phrase the AWS CLI special-cases, so a local `cdk deploy` was
// told there was nothing to do.

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

const propBefore = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: prop-orders
      VisibilityTimeout: 30
`

const propAfter = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: prop-orders
      VisibilityTimeout: 45
`

func TestChangeSetSeesAPropertyOnlyEdit(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("propdiff"), TemplateBody: aws.String(propBefore),
	}); err != nil {
		t.Fatal(err)
	}

	// Same resource, same name, one property different.
	if _, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("propdiff"), ChangeSetName: aws.String("bump"),
		TemplateBody: aws.String(propAfter),
	}); err != nil {
		t.Fatal(err)
	}
	desc, err := c.DescribeChangeSet(ctx, &awscfn.DescribeChangeSetInput{
		StackName: aws.String("propdiff"), ChangeSetName: aws.String("bump"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if desc.Status == cfntypes.ChangeSetStatusFailed {
		t.Fatalf("change set FAILED: %s\n"+
			"A property edit is a change; reporting none tells a deploy tool to stop.",
			aws.ToString(desc.StatusReason))
	}
	if len(desc.Changes) != 1 {
		t.Fatalf("changes = %d, want 1: %+v", len(desc.Changes), desc.Changes)
	}
	if got := aws.ToString(desc.Changes[0].ResourceChange.LogicalResourceId); got != "Orders" {
		t.Fatalf("changed resource = %q, want Orders", got)
	}

	// And an identical template still reports nothing, or every no-op deploy
	// would look like work.
	if _, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("propdiff"), ChangeSetName: aws.String("same"),
		TemplateBody: aws.String(propBefore),
	}); err != nil {
		t.Fatal(err)
	}
	same, err := c.DescribeChangeSet(ctx, &awscfn.DescribeChangeSetInput{
		StackName: aws.String("propdiff"), ChangeSetName: aws.String("same"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if same.Status != cfntypes.ChangeSetStatusFailed ||
		!strings.Contains(aws.ToString(same.StatusReason), "didn't contain changes") {
		t.Fatalf("an unchanged template should still report no changes, got %s %q",
			same.Status, aws.ToString(same.StatusReason))
	}
}
