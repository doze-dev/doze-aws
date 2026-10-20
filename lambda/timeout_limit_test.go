package lambda_test

// A standard function's timeout is at most 900 seconds. The model's range trait
// is 1 to 5400, and its member documentation says why there are two numbers:
// "the maximum allowed value is 900 seconds. For functions using Lambda Managed
// Instances, asynchronous invocations and event source mapping invocations
// (except Amazon MQ and Amazon DocumentDB) support a maximum allowed value of
// 5,400 seconds". doze-aws has no managed instances, so 900 is the limit of
// every function it serves; the table generated from the trait alone let
// 901 to 5400 through, which AWS refuses. Direction before: more forgiving.
// The code is the one this service's validation already answers
// (InvalidParameterValueException, 400); the message is `memory` and is only
// held to name the member and the bound.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

func TestATimeoutAboveNineHundredSecondsIsRefused(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	dir := t.TempDir()
	create := func(name string, timeout int32) error {
		_, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
			FunctionName: aws.String(name), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
			Role: aws.String("arn:aws:iam::000000000000:role/r"),
			Code: &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(dir)}, Timeout: aws.Int32(timeout),
		})
		return err
	}
	for _, timeout := range []int32{901, 5000, 5400} {
		err := create("too-long", timeout)
		wantCode(t, fmt.Sprintf("CreateFunction with timeout %d", timeout), err, "InvalidParameterValueException")
		if err == nil || !strings.Contains(err.Error(), "'timeout'") || !strings.Contains(err.Error(), "less than or equal to 900") {
			t.Errorf("timeout %d: the refusal does not name the member and its bound: %v", timeout, err)
		}
	}
	if _, err := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{FunctionName: aws.String("too-long")}); err == nil {
		t.Error("a refused create left a function behind")
	}
	if err := create("at-the-limit", 900); err != nil {
		t.Errorf("900 is the limit and is allowed: %v", err)
	}

	// The same bound on an update, which must leave the function as it was.
	_, err := c.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("at-the-limit"), Timeout: aws.Int32(5000)})
	wantCode(t, "UpdateFunctionConfiguration with timeout 5000", err, "InvalidParameterValueException")
	got, gerr := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{FunctionName: aws.String("at-the-limit")})
	if gerr != nil || aws.ToInt32(got.Timeout) != 900 {
		t.Errorf("the refused update changed the function: timeout %d, err %v", aws.ToInt32(got.Timeout), gerr)
	}
}
