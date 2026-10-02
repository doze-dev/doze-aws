package lambda_test

// Refusals the boto3 conformance suite found missing
// (conformance/tests/test_lambda.py).

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/smithy-go"
)

func wantCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != code {
		t.Errorf("%s: got %v, want %s", what, err, code)
	}
}

// A published version is frozen. An update addressed to one used to have its
// qualifier dropped, change $LATEST instead, and report success.
func TestOnlyTheLiveFunctionCanBeChanged(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	createEcho(t, ctx, c, "frozen")
	if _, err := c.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String("frozen")}); err != nil {
		t.Fatal(err)
	}
	_, err := c.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("frozen:1"), MemorySize: aws.Int32(1024)})
	wantCode(t, "update of a published version", err, "InvalidParameterValueException")
	got, err := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{FunctionName: aws.String("frozen")})
	if err != nil || aws.ToInt32(got.MemorySize) == 1024 {
		t.Fatalf("the refused update changed $LATEST: memory %d, err %v", aws.ToInt32(got.MemorySize), err)
	}
	if _, err := c.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("frozen:$LATEST"), MemorySize: aws.Int32(1024)}); err != nil {
		t.Errorf("update addressed to $LATEST: %v", err)
	}
}

// An event is JSON. Anything else used to reach the runtime and fail there,
// as the function's own error.
func TestInvokeRefusesAPayloadThatIsNotJSON(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	createEcho(t, ctx, c, "strict")
	_, err := c.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String("strict"), Payload: []byte("{nope")})
	wantCode(t, "invoke with a payload that is not JSON", err, "InvalidRequestContentException")
}

// A mapping to a queue that is not there used to be accepted, and then polled
// nothing forever while reporting Enabled.
func TestMappingToAnAbsentQueueIsRefused(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	createEcho(t, ctx, c, "reader")
	_, err := c.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName: aws.String("reader"), EventSourceArn: aws.String("arn:aws:sqs:us-east-1:000000000000:never-made")})
	wantCode(t, "mapping to an absent queue", err, "InvalidParameterValueException")
}
