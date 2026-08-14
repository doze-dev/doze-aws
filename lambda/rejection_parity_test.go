package lambda_test

// Rejection parity: the sizing inputs real Lambda refuses.
//
// MemorySize was the gap docs/api-support/lambda.md carried as "unchecked
// (AWS: 128–32768)". doze-aws does not allocate memory per function, so the
// value has no local effect and nothing had ever looked at it — which is
// precisely why it was accepted. A member the emulator ignores still has to be
// refused when it is invalid, or a function CloudFormation would reject
// deploys clean here and fails in the account.
//
// The bounds come from Lambda's own service model
// (`dzaudit list --op CreateFunction lambda`), not from the docs prose.

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
)

func TestCreateFunctionRejectsSizingAWSRejects(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	codeDir := buildBootstrap(t)

	cases := []struct {
		name    string
		memory  int32
		timeout int32
		ok      bool
	}{
		{"defaults (both absent)", 0, 0, true},
		{"at the memory minimum", 128, 3, true},
		{"at the memory maximum", 32768, 3, true},
		{"below the memory minimum", 127, 3, false},
		{"above the memory maximum", 32769, 3, false},
		{"negative memory", -1, 3, false},
		{"at the timeout maximum", 512, 5400, true},
		{"above the timeout maximum", 512, 5401, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &awslambda.CreateFunctionInput{
				FunctionName: aws.String(fnName(i)),
				Runtime:      lamtypes.RuntimeProvidedal2,
				Handler:      aws.String("bootstrap"),
				Role:         aws.String("arn:aws:iam::000000000000:role/r"),
				Code:         &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(codeDir)},
			}
			if tc.memory != 0 {
				in.MemorySize = aws.Int32(tc.memory)
			}
			if tc.timeout != 0 {
				in.Timeout = aws.Int32(tc.timeout)
			}
			_, err := c.CreateFunction(ctx, in)
			if tc.ok {
				if err != nil {
					t.Fatalf("CreateFunction(memory=%d timeout=%d) = %v, want accepted",
						tc.memory, tc.timeout, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CreateFunction(memory=%d timeout=%d) was accepted; AWS refuses it",
					tc.memory, tc.timeout)
			}
			var ae smithy.APIError
			if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidParameterValueException" {
				t.Errorf("code = %v, want InvalidParameterValueException", err)
			}
		})
	}
}

func fnName(i int) string { return "sizing" + string(rune('a'+i)) }
