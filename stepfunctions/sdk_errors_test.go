package stepfunctions_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/smithy-go"
)

// Every typed error the service can answer, matched through the SDK's own
// exception types — the test that catches a near-miss spelling, which the
// SDK decodes as a generic error no program can branch on.
func TestSDKTypedErrors(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)
	const role = "arn:aws:iam::000000000000:role/sfn"
	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("conflicts"), Definition: aws.String(helloWorld), RoleArn: aws.String(role),
	})
	if err != nil {
		t.Fatal(err)
	}
	arn := aws.ToString(created.StateMachineArn)

	t.Run("StateMachineAlreadyExists on a different definition", func(t *testing.T) {
		_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
			Name: aws.String("conflicts"), RoleArn: aws.String(role),
			Definition: aws.String(`{"StartAt":"X","States":{"X":{"Type":"Succeed"}}}`),
		})
		var e *sfntypes.StateMachineAlreadyExists
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("identical create is idempotent", func(t *testing.T) {
		again, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
			Name: aws.String("conflicts"), Definition: aws.String(helloWorld), RoleArn: aws.String(role),
		})
		if err != nil || aws.ToString(again.StateMachineArn) != arn {
			t.Fatalf("got %v / %v", again, err)
		}
	})
	t.Run("InvalidName", func(t *testing.T) {
		_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
			Name: aws.String("has space"), Definition: aws.String(helloWorld), RoleArn: aws.String(role),
		})
		var e *sfntypes.InvalidName
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("InvalidArn", func(t *testing.T) {
		_, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String("not-an-arn")})
		var e *sfntypes.InvalidArn
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("StateMachineDoesNotExist", func(t *testing.T) {
		_, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
			StateMachineArn: aws.String(strings.Replace(arn, "conflicts", "ghost", 1)),
		})
		var e *sfntypes.StateMachineDoesNotExist
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
		_, err = c.StartExecution(ctx, &awssfn.StartExecutionInput{
			StateMachineArn: aws.String(strings.Replace(arn, "conflicts", "ghost", 1)),
		})
		if !errors.As(err, &e) {
			t.Fatalf("StartExecution on a missing machine: got %v", err)
		}
	})
	t.Run("InvalidExecutionInput", func(t *testing.T) {
		_, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
			StateMachineArn: aws.String(arn), Input: aws.String("{not json"),
		})
		var e *sfntypes.InvalidExecutionInput
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ExecutionDoesNotExist", func(t *testing.T) {
		ghost := strings.Replace(arn, ":stateMachine:conflicts", ":execution:conflicts:ghost", 1)
		var e *sfntypes.ExecutionDoesNotExist
		if _, err := c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: aws.String(ghost)}); !errors.As(err, &e) {
			t.Fatalf("DescribeExecution: got %v", err)
		}
		if _, err := c.StopExecution(ctx, &awssfn.StopExecutionInput{ExecutionArn: aws.String(ghost)}); !errors.As(err, &e) {
			t.Fatalf("StopExecution: got %v", err)
		}
		if _, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{ExecutionArn: aws.String(ghost)}); !errors.As(err, &e) {
			t.Fatalf("GetExecutionHistory: got %v", err)
		}
	})
	t.Run("TaskDoesNotExist", func(t *testing.T) {
		_, err := c.SendTaskSuccess(ctx, &awssfn.SendTaskSuccessInput{
			TaskToken: aws.String("never-minted"), Output: aws.String("{}"),
		})
		var e *sfntypes.TaskDoesNotExist
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ActivityDoesNotExist", func(t *testing.T) {
		_, err := c.DescribeActivity(ctx, &awssfn.DescribeActivityInput{
			ActivityArn: aws.String(strings.Replace(arn, ":stateMachine:conflicts", ":activity:ghost", 1)),
		})
		var e *sfntypes.ActivityDoesNotExist
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ResourceNotFound for tags on a missing resource", func(t *testing.T) {
		_, err := c.ListTagsForResource(ctx, &awssfn.ListTagsForResourceInput{
			ResourceArn: aws.String(strings.Replace(arn, "conflicts", "ghost", 1)),
		})
		var e *sfntypes.ResourceNotFound
		if !errors.As(err, &e) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ValidationException names the member", func(t *testing.T) {
		// The SDK checks required members itself; a length constraint is the
		// model's, and only the server enforces it.
		_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
			Name: aws.String(strings.Repeat("n", 81)), Definition: aws.String(helloWorld), RoleArn: aws.String(role),
		})
		var ae smithy.APIError
		if !errors.As(err, &ae) || ae.ErrorCode() != "ValidationException" || !strings.Contains(ae.ErrorMessage(), "name") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("delete is idempotent and the machine is gone", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			if _, err := c.DeleteStateMachine(ctx, &awssfn.DeleteStateMachineInput{StateMachineArn: aws.String(arn)}); err != nil {
				t.Fatalf("delete #%d: %v", i+1, err)
			}
		}
		var e *sfntypes.StateMachineDoesNotExist
		if _, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn)}); !errors.As(err, &e) {
			t.Fatalf("after delete: got %v", err)
		}
	})
}
