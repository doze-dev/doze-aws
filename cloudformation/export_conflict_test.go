package cloudformation_test

// An export name belongs to exactly one stack.
//
// That is what makes Fn::ImportValue unambiguous, and it was not enforced: a
// second stack declaring an export another stack already owned simply took the
// name. Store.Exports() builds a map keyed by export name, so after the
// collision an importer resolved to whichever of the two stacks the iteration
// happened to reach last — a third stack getting the wrong queue, with nothing
// anywhere saying why.
//
// AWS refuses this. It surfaces asynchronously, as a stack event and a
// rollback; doze-aws is synchronous by design, so the refusal lands on the
// call itself, carrying the message AWS uses.

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
)

// Two templates exporting the SAME fixed name, so the conflict does not depend
// on the stack name. (svcTemplate exports ${AWS::StackName}-queue, which can
// never collide between two differently-named stacks.)
const fixedExportTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${AWS::StackName}-q"
Outputs:
  QueueUrl:
    Value: !Ref Q
    Export:
      Name: shared-export-name
`

func TestASecondStackCannotClaimAnExistingExport(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("first"), TemplateBody: aws.String(fixedExportTemplate),
	}); err != nil {
		t.Fatalf("the first stack should be fine: %v", err)
	}

	_, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("second"), TemplateBody: aws.String(fixedExportTemplate),
	})
	if err == nil {
		t.Fatal("a second stack claimed an export the first already owned — " +
			"Fn::ImportValue now resolves to whichever one the map iteration reaches last")
	}
	if !strings.Contains(err.Error(), "already exported by stack first") {
		t.Errorf("error should name the owning stack, got: %v", err)
	}

	// And nothing was created: the refusal happens before provision.Apply, so
	// the second stack must not exist even in a failed state.
	if _, derr := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{
		StackName: aws.String("second"),
	}); derr == nil {
		t.Error("the refused stack was recorded anyway — a refusal before apply should leave nothing behind")
	}
}

// A stack updating itself already owns its own export, and must not be refused
// by the check that protects it from everyone else.
func TestAStackCanUpdateWithoutLosingItsOwnExport(t *testing.T) {
	ctx := context.Background()
	c, _ := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("solo"), TemplateBody: aws.String(fixedExportTemplate),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	updated := strings.Replace(fixedExportTemplate,
		`      QueueName: !Sub "${AWS::StackName}-q"`,
		"      QueueName: !Sub \"${AWS::StackName}-q\"\n      VisibilityTimeout: 45", 1)
	if _, err := c.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String("solo"), TemplateBody: aws.String(updated),
	}); err != nil {
		t.Fatalf("a stack must be able to update while keeping its own export: %v", err)
	}
}
