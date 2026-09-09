package eventbridge_test

// Two things EventBridge never asserted.
//
// First, that its stubs refuse by name. Seventeen operations answer
// UnsupportedOperationException with a reason, and the string
// "UnsupportedOperationException" appeared in no eventbridge test — so all
// seventeen could have been answering InvalidAction and nothing would have
// noticed. logs, dynamodb, ssm, sns and stepfunctions each have this test.
//
// Second, UpdateEventBus, which the accounting test found was neither handled
// nor refused: it fell through to InvalidAction, so a Terraform apply that
// changed a bus description got an error naming the action as unknown.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

// TestSDKRefusedOperationsAreNamed: a refusal says what it is and why.
func TestSDKRefusedOperationsAreNamed(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)

	for _, c := range []struct {
		name string
		call func() error
		says string
	}{
		{"PutPermission", func() error {
			_, err := eb.PutPermission(ctx, &awseb.PutPermissionInput{
				Action: aws.String("events:PutEvents"), Principal: aws.String("111122223333"),
				StatementId: aws.String("s1")})
			return err
		}, "cross-account"},
		{"RemovePermission", func() error {
			_, err := eb.RemovePermission(ctx, &awseb.RemovePermissionInput{StatementId: aws.String("s1")})
			return err
		}, "cross-account"},
		{"ListEventSources", func() error {
			_, err := eb.ListEventSources(ctx, &awseb.ListEventSourcesInput{})
			return err
		}, "partner event sources"},
		{"DescribeEndpoint", func() error {
			_, err := eb.DescribeEndpoint(ctx, &awseb.DescribeEndpointInput{Name: aws.String("ep")})
			return err
		}, "global endpoints"},
		{"ListPartnerEventSources", func() error {
			_, err := eb.ListPartnerEventSources(ctx, &awseb.ListPartnerEventSourcesInput{
				NamePrefix: aws.String("aws.partner/x")})
			return err
		}, "partner event sources"},
		{"ListEndpoints", func() error {
			_, err := eb.ListEndpoints(ctx, &awseb.ListEndpointsInput{})
			return err
		}, "global endpoints"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatalf("%s should be refused", c.name)
			}
			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("%s: not an API error: %v", c.name, err)
			}
			if apiErr.ErrorCode() != "UnsupportedOperationException" {
				t.Errorf("%s: code = %q, want UnsupportedOperationException — InvalidAction reads like the caller's typo",
					c.name, apiErr.ErrorCode())
			}
			if !strings.Contains(apiErr.ErrorMessage(), c.says) {
				t.Errorf("%s: the refusal should say why (%q), got %q", c.name, c.says, apiErr.ErrorMessage())
			}
			if !strings.Contains(apiErr.ErrorMessage(), c.name) {
				t.Errorf("%s: the refusal should name the operation, got %q", c.name, apiErr.ErrorMessage())
			}
		})
	}
}

// TestSDKUpdateEventBus: the operation the accounting test found missing.
func TestSDKUpdateEventBus(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)

	if _, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{
		Name: aws.String("orders"), Description: aws.String("first"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := eb.UpdateEventBus(ctx, &awseb.UpdateEventBusInput{
		Name: aws.String("orders"), Description: aws.String("second"),
		DeadLetterConfig: &ebtypes.DeadLetterConfig{Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:dlq")},
	}); err != nil {
		t.Fatalf("UpdateEventBus: %v", err)
	}
	got, err := eb.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(got.Description) != "second" {
		t.Errorf("Description = %q, want the updated one", aws.ToString(got.Description))
	}

	// A bus that does not exist is a 404, not a silently created one.
	_, err = eb.UpdateEventBus(ctx, &awseb.UpdateEventBusInput{Name: aws.String("nope")})
	if err == nil {
		t.Fatal("updating a bus that does not exist must fail")
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Errorf("code = %q, want ResourceNotFoundException", apiErr.ErrorCode())
	}
	if _, err := eb.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("nope")}); err == nil {
		t.Error("a failed update must not create the bus")
	}
}
