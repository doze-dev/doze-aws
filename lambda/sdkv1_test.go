// SDK v1 contract tests: the same Lambda scenarios driven by the legacy
// aws-sdk-go (v1) client — the second independent client generation, per the
// doze-aws dual-SDK requirement.
package lambda_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	lambdav1 "github.com/aws/aws-sdk-go/service/lambda"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func lambdaV1Client(t *testing.T) *lambdav1.Lambda {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return lambdav1.New(sess)
}

func TestSDKV1CreateInvokeDelete(t *testing.T) {
	c := lambdaV1Client(t)
	codeDir := buildBootstrap(t) // reused from the v2 test file (same package)

	if _, err := c.CreateFunction(&lambdav1.CreateFunctionInput{
		FunctionName: awsv1.String("v1fn"),
		Runtime:      awsv1.String("provided.al2"),
		Handler:      awsv1.String("bootstrap"),
		Role:         awsv1.String("arn:aws:iam::000000000000:role/r"),
		Code:         &lambdav1.FunctionCode{S3Bucket: awsv1.String("_local_"), S3Key: awsv1.String(codeDir)},
		Timeout:      awsv1.Int64(10),
		Environment:  &lambdav1.Environment{Variables: map[string]*string{"GREETING": awsv1.String("v1hi")}},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// GetFunction / ListFunctions round-trip through the legacy client.
	if _, err := c.GetFunction(&lambdav1.GetFunctionInput{FunctionName: awsv1.String("v1fn")}); err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	lf, err := c.ListFunctions(&lambdav1.ListFunctionsInput{})
	if err != nil || len(lf.Functions) != 1 {
		t.Fatalf("ListFunctions = %d err=%v", len(lf.Functions), err)
	}

	// Synchronous invoke: the real child process echoes the event + env back.
	out, err := c.Invoke(&lambdav1.InvokeInput{
		FunctionName: awsv1.String("v1fn"),
		Payload:      []byte(`{"hello":"v1"}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.FunctionError != nil {
		t.Fatalf("function error: %s (%s)", awsv1.StringValue(out.FunctionError), out.Payload)
	}
	if body := string(out.Payload); !strings.Contains(body, `"hello":"v1"`) || !strings.Contains(body, `"env":"v1hi"`) {
		t.Fatalf("invoke result = %s", body)
	}

	if _, err := c.DeleteFunction(&lambdav1.DeleteFunctionInput{FunctionName: awsv1.String("v1fn")}); err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}
}
