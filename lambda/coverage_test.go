package lambda_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

func createEcho(t *testing.T, ctx context.Context, c *awslambda.Client, name string) {
	t.Helper()
	codeDir := buildBootstrap(t)
	if _, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lamtypes.RuntimeProvidedal2,
		Handler:      aws.String("bootstrap"),
		Role:         aws.String("arn:aws:iam::000000000000:role/r"),
		Code:         &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(codeDir)},
		Timeout:      aws.Int32(10),
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
}

func TestSDKFunctionManagement(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	createEcho(t, ctx, c, "mgmt")
	arn := "arn:aws:lambda:us-east-1:000000000000:function:mgmt"

	// UpdateFunctionConfiguration.
	if _, err := c.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("mgmt"), Timeout: aws.Int32(20),
		Environment: &lamtypes.Environment{Variables: map[string]string{"A": "b"}},
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration: %v", err)
	}
	// UpdateFunctionCode (re-point at a fresh build).
	if _, err := c.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String("mgmt"),
		S3Bucket:     aws.String("_local_"), S3Key: aws.String(buildBootstrap(t)),
	}); err != nil {
		t.Fatalf("UpdateFunctionCode: %v", err)
	}
	// PublishVersion.
	if _, err := c.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String("mgmt")}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	// Concurrency config.
	if _, err := c.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName: aws.String("mgmt"), ReservedConcurrentExecutions: aws.Int32(3),
	}); err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}
	if gc, err := c.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{FunctionName: aws.String("mgmt")}); err != nil || aws.ToInt32(gc.ReservedConcurrentExecutions) != 3 {
		t.Fatalf("GetFunctionConcurrency = %v err=%v", gc, err)
	}
	if _, err := c.DeleteFunctionConcurrency(ctx, &awslambda.DeleteFunctionConcurrencyInput{FunctionName: aws.String("mgmt")}); err != nil {
		t.Fatalf("DeleteFunctionConcurrency: %v", err)
	}

	// Tags.
	if _, err := c.TagResource(ctx, &awslambda.TagResourceInput{Resource: aws.String(arn), Tags: map[string]string{"team": "core"}}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
	lt, err := c.ListTags(ctx, &awslambda.ListTagsInput{Resource: aws.String(arn)})
	if err != nil || lt.Tags["team"] != "core" {
		t.Fatalf("ListTags = %v err=%v", lt.Tags, err)
	}
	if _, err := c.UntagResource(ctx, &awslambda.UntagResourceInput{Resource: aws.String(arn), TagKeys: []string{"team"}}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}
}

func TestSDKEventSourceMappingCRUD(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	createEcho(t, ctx, c, "esm")

	m, err := c.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		FunctionName:   aws.String("esm"),
		EventSourceArn: aws.String("arn:aws:sqs:us-east-1:000000000000:jobs"),
		BatchSize:      aws.Int32(5),
		Enabled:        aws.Bool(false),
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}
	if _, err := c.GetEventSourceMapping(ctx, &awslambda.GetEventSourceMappingInput{UUID: m.UUID}); err != nil {
		t.Fatalf("GetEventSourceMapping: %v", err)
	}
	if _, err := c.UpdateEventSourceMapping(ctx, &awslambda.UpdateEventSourceMappingInput{UUID: m.UUID, BatchSize: aws.Int32(10)}); err != nil {
		t.Fatalf("UpdateEventSourceMapping: %v", err)
	}
	lm, err := c.ListEventSourceMappings(ctx, &awslambda.ListEventSourceMappingsInput{FunctionName: aws.String("esm")})
	if err != nil || len(lm.EventSourceMappings) != 1 {
		t.Fatalf("ListEventSourceMappings = %d err=%v", len(lm.EventSourceMappings), err)
	}
	if _, err := c.DeleteEventSourceMapping(ctx, &awslambda.DeleteEventSourceMappingInput{UUID: m.UUID}); err != nil {
		t.Fatalf("DeleteEventSourceMapping: %v", err)
	}
}

// TestSDKZipFileCode exercises the ZipFile code path (base64 zip → unzip →
// extracted bootstrap → invoke), the alternative to the _local_ extension every
// other test uses.
func TestSDKZipFileCode(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)

	// Compile a bootstrap, then pack it into an in-memory zip the way the real
	// AWS ZipFile upload does.
	binDir := buildBootstrap(t)
	raw, err := os.ReadFile(filepath.Join(binDir, "bootstrap"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
	hdr.SetMode(0o755) // executable, or the runtime can't exec it
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(raw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("zipped"),
		Runtime:      lamtypes.RuntimeProvidedal2,
		Handler:      aws.String("bootstrap"),
		Role:         aws.String("arn:aws:iam::000000000000:role/r"),
		Code:         &lamtypes.FunctionCode{ZipFile: buf.Bytes()},
		Timeout:      aws.Int32(10),
	}); err != nil {
		t.Fatalf("CreateFunction(ZipFile): %v", err)
	}
	out, err := c.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("zipped"), Payload: []byte(`{"z":1}`),
	})
	if err != nil || out.FunctionError != nil {
		t.Fatalf("Invoke zipped: err=%v funcErr=%v payload=%s", err, out.FunctionError, out.Payload)
	}
	if !strings.Contains(string(out.Payload), `"z":1`) {
		t.Fatalf("zipped invoke result = %s", out.Payload)
	}
}
