package eventbridge_test

// Rule and bus settings that have no local behaviour still have to read back.
// Terraform tracks role_arn on aws_cloudwatch_event_rule, and the description,
// dead-letter config and KMS key on aws_cloudwatch_event_bus.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

func TestRuleRoleArnReadsBack(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	role := "arn:aws:iam::000000000000:role/eb-invoke"

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name:               aws.String("with-role"),
		ScheduleExpression: aws.String("rate(5 minutes)"),
		RoleArn:            aws.String(role),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}
	out, err := eb.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String("with-role")})
	if err != nil {
		t.Fatalf("DescribeRule: %v", err)
	}
	if aws.ToString(out.RoleArn) != role {
		t.Errorf("RoleArn = %q, want %q", aws.ToString(out.RoleArn), role)
	}

	// A rule without one must not invent it.
	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("no-role"), ScheduleExpression: aws.String("rate(5 minutes)"),
	}); err != nil {
		t.Fatal(err)
	}
	bare, err := eb.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String("no-role")})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(bare.RoleArn) != "" {
		t.Errorf("rule without a role reports %q", aws.ToString(bare.RoleArn))
	}
}

func TestEventBusDeclaredFieldsReadBack(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	dlq := "arn:aws:sqs:us-east-1:000000000000:eb-dlq"

	if _, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{
		Name:             aws.String("declared-bus"),
		Description:      aws.String("orders domain"),
		KmsKeyIdentifier: aws.String("alias/aws/events"),
		DeadLetterConfig: &ebtypes.DeadLetterConfig{Arn: aws.String(dlq)},
	}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}
	out, err := eb.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("declared-bus")})
	if err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}
	if aws.ToString(out.Description) != "orders domain" {
		t.Errorf("Description = %q", aws.ToString(out.Description))
	}
	if aws.ToString(out.KmsKeyIdentifier) != "alias/aws/events" {
		t.Errorf("KmsKeyIdentifier = %q", aws.ToString(out.KmsKeyIdentifier))
	}
	if out.DeadLetterConfig == nil || aws.ToString(out.DeadLetterConfig.Arn) != dlq {
		t.Errorf("DeadLetterConfig = %+v, want %s", out.DeadLetterConfig, dlq)
	}
}
