package stepfunctions_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Express workflows and TestState through aws-sdk-go-v2.
//
// Both operations carry AWS's `sync-` host prefix, which the SDK applies
// even to a custom endpoint — sync-127.0.0.1 resolves nowhere. This is the
// escape hatch, and it is what docs/api-support/stepfunctions.md tells IP
// endpoint users to do: an Initialize middleware that marks the hostname
// immutable. (Against aws.doze the prefix resolves, because doze-aws claims
// sync-aws.doze beside the apex.)
func noHostPrefix(o *awssfn.Options) {
	o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
		return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("NoHostPrefix",
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (
				middleware.InitializeOutput, middleware.Metadata, error) {
				return next.HandleInitialize(smithyhttp.SetHostnameImmutable(ctx, true), in)
			}), middleware.Before)
	})
}

func expressClient(t *testing.T) *awssfn.Client {
	t.Helper()
	base := sfnClient(t)
	return awssfn.New(base.Options(), noHostPrefix)
}

const role = "arn:aws:iam::000000000000:role/sfn"

func TestSDKExpressSyncExecution(t *testing.T) {
	ctx := context.Background()
	c := expressClient(t)
	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("quick"), RoleArn: aws.String(role), Type: sfntypes.StateMachineTypeExpress,
		Definition: aws.String(`{"StartAt":"Double","States":{
		  "Double":{"Type":"Pass","Parameters":{"twice.$":"States.MathAdd($.n, $.n)"},"Next":"Hold"},
		  "Hold":{"Type":"Wait","Seconds":1,"Next":"Done"},
		  "Done":{"Type":"Succeed"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	arn := aws.ToString(created.StateMachineArn)

	out, err := c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{
		StateMachineArn: aws.String(arn), Name: aws.String("run-1"), Input: aws.String(`{"n":21}`),
	})
	if err != nil {
		t.Fatalf("StartSyncExecution: %v", err)
	}
	if out.Status != sfntypes.SyncExecutionStatusSucceeded || aws.ToString(out.Output) != `{"twice":42}` {
		t.Fatalf("status=%s output=%s error=%s cause=%s", out.Status, aws.ToString(out.Output), aws.ToString(out.Error), aws.ToString(out.Cause))
	}
	if !strings.Contains(aws.ToString(out.ExecutionArn), ":express:quick:run-1:") {
		t.Errorf("Express execution ARN = %q, want the :express: form", aws.ToString(out.ExecutionArn))
	}
	if out.StartDate == nil || out.StopDate == nil || out.BillingDetails == nil || out.BillingDetails.BilledDurationInMilliseconds < 100 {
		t.Errorf("dates and billing should be filled: %+v", out)
	}
	// The same name again: Express allows it — the id tells them apart.
	if _, err := c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{
		StateMachineArn: aws.String(arn), Name: aws.String("run-1"), Input: aws.String(`{"n":1}`),
	}); err != nil {
		t.Errorf("a second Express execution with the same name should be allowed: %v", err)
	}
	// Nothing to describe or list afterwards, on AWS or here.
	var gone *sfntypes.ExecutionDoesNotExist
	if _, err := c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: out.ExecutionArn}); !errors.As(err, &gone) {
		t.Errorf("DescribeExecution on an Express execution: got %v, want ExecutionDoesNotExist", err)
	}
	var unsupported *sfntypes.StateMachineTypeNotSupported
	if _, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{StateMachineArn: aws.String(arn)}); !errors.As(err, &unsupported) {
		t.Errorf("ListExecutions on an EXPRESS machine: got %v, want StateMachineTypeNotSupported", err)
	}

	// Async start answers at once; the execution runs and is forgotten.
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: aws.String(arn), Input: aws.String(`{"n":2}`)})
	if err != nil || !strings.Contains(aws.ToString(started.ExecutionArn), ":express:") {
		t.Fatalf("StartExecution on EXPRESS: %v / %v", started, err)
	}
}

func TestSDKExpressFailureAndRefusals(t *testing.T) {
	ctx := context.Background()
	c := expressClient(t)
	failing, _ := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("boom"), RoleArn: aws.String(role), Type: sfntypes.StateMachineTypeExpress,
		Definition: aws.String(`{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"Custom.Boom","Cause":"as asked"}}}`),
	})
	out, err := c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{StateMachineArn: failing.StateMachineArn})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != sfntypes.SyncExecutionStatusFailed || aws.ToString(out.Error) != "Custom.Boom" || aws.ToString(out.Cause) != "as asked" {
		t.Errorf("failed sync execution = %+v", out)
	}
	if out.Output != nil {
		t.Errorf("a failed execution has no output, got %q", aws.ToString(out.Output))
	}

	// A task token cannot be redeemed on Express: the pattern fails, catchably.
	parking, _ := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("parks"), RoleArn: aws.String(role), Type: sfntypes.StateMachineTypeExpress,
		Definition: aws.String(`{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"arn:aws:states:::sqs:sendMessage.waitForTaskToken",
		  "Parameters":{"QueueUrl":"http://q","MessageBody":{"t.$":"$$.Task.Token"}},"End":true}}}`),
	})
	out, err = c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{StateMachineArn: parking.StateMachineArn})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != sfntypes.SyncExecutionStatusFailed || !strings.HasPrefix(aws.ToString(out.Error), "States.") {
		t.Errorf("token pattern on Express should fail with a States.* error: %+v", out)
	}

	standard, _ := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("std"), RoleArn: aws.String(role), Definition: aws.String(helloWorld),
	})
	var unsupported *sfntypes.StateMachineTypeNotSupported
	if _, err := c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{StateMachineArn: standard.StateMachineArn}); !errors.As(err, &unsupported) {
		t.Errorf("StartSyncExecution on STANDARD: got %v, want StateMachineTypeNotSupported", err)
	}
	if _, err := c.StartSyncExecution(ctx, &awssfn.StartSyncExecutionInput{StateMachineArn: failing.StateMachineArn, Input: aws.String("{nope")}); err == nil {
		t.Error("bad input should be InvalidExecutionInput")
	}
}

func TestSDKTestState(t *testing.T) {
	ctx := context.Background()
	c := expressClient(t)
	test := func(t *testing.T, in *awssfn.TestStateInput) *awssfn.TestStateOutput {
		t.Helper()
		in.RoleArn = aws.String(role)
		out, err := c.TestState(ctx, in)
		if err != nil {
			t.Fatalf("TestState: %v", err)
		}
		return out
	}

	t.Run("a lone Pass reports its output and next state", func(t *testing.T) {
		out := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Pass","Parameters":{"greeting.$":"States.Format('hi {}', $.name)"},"Next":"After"}`),
			Input:      aws.String(`{"name":"srini"}`),
		})
		if out.Status != sfntypes.TestExecutionStatusSucceeded || aws.ToString(out.NextState) != "After" || aws.ToString(out.Output) != `{"greeting":"hi srini"}` {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("a Choice reports where it would go", func(t *testing.T) {
		out := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Choice","Choices":[{"Variable":"$.n","NumericGreaterThan":5,"Next":"Big"}],"Default":"Small"}`),
			Input:      aws.String(`{"n":9}`),
		})
		if aws.ToString(out.NextState) != "Big" {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("a whole machine with stateName", func(t *testing.T) {
		out := test(t, &awssfn.TestStateInput{
			Definition: aws.String(helloWorld), StateName: aws.String("HelloWorld"), Input: aws.String(`{}`),
		})
		if out.Status != sfntypes.TestExecutionStatusSucceeded {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("a Task with a mocked result runs its result pipeline", func(t *testing.T) {
		out := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Task","Resource":"arn:aws:states:::lambda:invoke","Parameters":{"FunctionName":"f","Payload.$":"$"},
			  "ResultSelector":{"v.$":"$.Payload.value"},"ResultPath":"$.out","Next":"N"}`),
			Input:           aws.String(`{"a":1}`),
			InspectionLevel: sfntypes.InspectionLevelDebug,
			Mock:            &sfntypes.MockInput{Result: aws.String(`{"Payload":{"value":7}}`)},
		})
		if out.Status != sfntypes.TestExecutionStatusSucceeded || aws.ToString(out.Output) != `{"a":1,"out":{"v":7}}` {
			t.Fatalf("got %+v", out)
		}
		d := out.InspectionData
		if d == nil || aws.ToString(d.AfterParameters) != `{"FunctionName":"f","Payload":{"a":1}}` || aws.ToString(d.AfterResultSelector) != `{"v":7}` {
			t.Errorf("inspectionData = %+v", d)
		}
	})
	t.Run("a mocked error is caught, retried, or fails", func(t *testing.T) {
		caught := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Task","Resource":"arn:aws:states:::lambda:invoke","Parameters":{"FunctionName":"f"},
			  "Catch":[{"ErrorEquals":["Handler.Bad"],"ResultPath":"$.err","Next":"Recover"}],"Next":"N"}`),
			Input: aws.String(`{}`),
			Mock:  &sfntypes.MockInput{ErrorOutput: &sfntypes.MockErrorOutput{Error: aws.String("Handler.Bad"), Cause: aws.String("x")}},
		})
		if caught.Status != sfntypes.TestExecutionStatusCaughtError || aws.ToString(caught.NextState) != "Recover" || aws.ToString(caught.Error) != "Handler.Bad" {
			t.Errorf("caught = %+v", caught)
		}
		retriable := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Task","Resource":"arn:aws:states:::lambda:invoke","Parameters":{"FunctionName":"f"},
			  "Retry":[{"ErrorEquals":["States.ALL"]}],"Next":"N"}`),
			Input: aws.String(`{}`),
			Mock:  &sfntypes.MockInput{ErrorOutput: &sfntypes.MockErrorOutput{Error: aws.String("Any"), Cause: aws.String("x")}},
		})
		if retriable.Status != sfntypes.TestExecutionStatusRetriable || aws.ToString(retriable.Error) != "Any" {
			t.Errorf("retriable = %+v", retriable)
		}
		failed := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Fail","Error":"Custom.No","Cause":"nope"}`), Input: aws.String(`{}`),
		})
		if failed.Status != sfntypes.TestExecutionStatusFailed || aws.ToString(failed.Error) != "Custom.No" {
			t.Errorf("failed = %+v", failed)
		}
	})
	t.Run("a Wait does not wait", func(t *testing.T) {
		out := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Wait","Seconds":3600,"Next":"Later"}`), Input: aws.String(`{"k":1}`),
		})
		if out.Status != sfntypes.TestExecutionStatusSucceeded || aws.ToString(out.NextState) != "Later" || aws.ToString(out.Output) != `{"k":1}` {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("a Map with a mocked result", func(t *testing.T) {
		out := test(t, &awssfn.TestStateInput{
			Definition: aws.String(`{"Type":"Map","ItemsPath":"$.items","ItemProcessor":{"StartAt":"I","States":{"I":{"Type":"Pass","End":true}}},"ResultPath":"$.done","End":true}`),
			Input:      aws.String(`{"items":[1,2]}`),
			Mock:       &sfntypes.MockInput{Result: aws.String(`["mocked"]`)},
		})
		if out.Status != sfntypes.TestExecutionStatusSucceeded || aws.ToString(out.Output) != `{"done":["mocked"],"items":[1,2]}` {
			t.Errorf("got %+v", out)
		}
	})
	t.Run("refusals", func(t *testing.T) {
		var vex interface{ ErrorCode() string }
		if _, err := c.TestState(ctx, &awssfn.TestStateInput{RoleArn: aws.String(role),
			Definition: aws.String(`{"Type":"Pass","End":true}`), Mock: &sfntypes.MockInput{Result: aws.String(`{}`)},
		}); !errors.As(err, &vex) || vex.ErrorCode() != "ValidationException" {
			t.Errorf("mock on a Pass: got %v", err)
		}
		var bad *sfntypes.InvalidDefinition
		if _, err := c.TestState(ctx, &awssfn.TestStateInput{RoleArn: aws.String(role), Definition: aws.String(`{"Type":"Nope"}`)}); !errors.As(err, &bad) {
			t.Errorf("bad definition: got %v", err)
		}
	})

}
