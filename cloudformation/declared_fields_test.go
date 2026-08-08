package cloudformation_test

// Settings a deploy declares that nothing local acts on still have to read
// back. sam and cdk pass --capabilities on every deploy, and Terraform tracks
// capabilities, notification ARNs, the rollback configuration and
// DisableRollback on aws_cloudformation_stack. A stack that accepts them and
// then describes itself without them is a stack that never stops looking
// changed.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

const bucketOnly = `
Resources:
  B:
    Type: AWS::S3::Bucket
    Properties: { BucketName: declared-fields-bucket }
`

func TestDeclaredStackFieldsReadBack(t *testing.T) {
	ctx := context.Background()
	cfn, _ := cfnStack(t)

	mins := int32(5)
	_, err := cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName:    aws.String("declared"),
		TemplateBody: aws.String(bucketOnly),
		Capabilities: []cfntypes.Capability{
			cfntypes.CapabilityCapabilityIam, cfntypes.CapabilityCapabilityNamedIam,
		},
		NotificationARNs: []string{"arn:aws:sns:us-east-1:000000000000:cfn-events"},
		DisableRollback:  aws.Bool(true),
		RollbackConfiguration: &cfntypes.RollbackConfiguration{
			MonitoringTimeInMinutes: &mins,
			RollbackTriggers: []cfntypes.RollbackTrigger{
				{Arn: aws.String("arn:aws:cloudwatch:us-east-1:000000000000:alarm:a"), Type: aws.String("AWS::CloudWatch::Alarm")},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	out, err := cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("declared")})
	if err != nil || len(out.Stacks) == 0 {
		t.Fatalf("DescribeStacks: %v", err)
	}
	st := out.Stacks[0]

	if len(st.Capabilities) != 2 {
		t.Errorf("capabilities = %v, want both that were set", st.Capabilities)
	}
	if len(st.NotificationARNs) != 1 {
		t.Errorf("notification ARNs = %v, want the one that was set", st.NotificationARNs)
	}
	if !aws.ToBool(st.DisableRollback) {
		t.Error("DisableRollback did not read back")
	}
	rc := st.RollbackConfiguration
	if rc == nil {
		t.Fatal("RollbackConfiguration missing")
	}
	if aws.ToInt32(rc.MonitoringTimeInMinutes) != 5 {
		t.Errorf("monitoring window = %v, want 5", rc.MonitoringTimeInMinutes)
	}
	if len(rc.RollbackTriggers) != 1 {
		t.Errorf("rollback triggers = %v, want the one that was set", rc.RollbackTriggers)
	}
}

// A stack that declared none of them must not invent any.
func TestUndeclaredStackFieldsStayEmpty(t *testing.T) {
	ctx := context.Background()
	cfn, _ := cfnStack(t)
	if _, err := cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("bare"), TemplateBody: aws.String(bucketOnly),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	out, err := cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("bare")})
	if err != nil {
		t.Fatal(err)
	}
	st := out.Stacks[0]
	if len(st.Capabilities) != 0 || len(st.NotificationARNs) != 0 || st.RollbackConfiguration != nil {
		t.Errorf("bare stack reports declared fields: caps=%v arns=%v rollback=%+v",
			st.Capabilities, st.NotificationARNs, st.RollbackConfiguration)
	}
}
