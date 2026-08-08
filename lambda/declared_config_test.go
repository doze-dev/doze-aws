package lambda_test

// Function configuration declared at create time has to read back, and
// GetFunctionConfiguration has to answer at all — Terraform and the CLI both
// call it directly rather than going through GetFunction.
//
// None of these blocks do anything locally: there is no VPC to join, no EFS to
// mount, no X-Ray to trace into and no snapshot to restore from. Architectures
// is different in kind — reporting x86_64 for a function created as arm64 is
// not inert, it is wrong.

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	ltypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// tinyZip builds the smallest deployable package: these tests never invoke
// the function, they only assert what its configuration reports.
func tinyZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("exports.handler=async()=>({});")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDeclaredFunctionConfigReadsBack(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)

	if _, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName:     aws.String("declared-fn"),
		Runtime:          ltypes.RuntimeNodejs20x,
		Role:             aws.String("arn:aws:iam::000000000000:role/lambda"),
		Handler:          aws.String("index.handler"),
		Code:             &ltypes.FunctionCode{ZipFile: tinyZip(t)},
		Architectures:    []ltypes.Architecture{ltypes.ArchitectureArm64},
		EphemeralStorage: &ltypes.EphemeralStorage{Size: aws.Int32(1024)},
		TracingConfig:    &ltypes.TracingConfig{Mode: ltypes.TracingModeActive},
		KMSKeyArn:        aws.String("arn:aws:kms:us-east-1:000000000000:key/abc"),
		LoggingConfig:    &ltypes.LoggingConfig{LogFormat: ltypes.LogFormatJson},
		SnapStart:        &ltypes.SnapStart{ApplyOn: ltypes.SnapStartApplyOnPublishedVersions},
		VpcConfig: &ltypes.VpcConfig{
			SubnetIds: []string{"subnet-1"}, SecurityGroupIds: []string{"sg-1"},
		},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// GetFunctionConfiguration must be routed, not just GetFunction.
	cfg, err := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("declared-fn"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}

	if len(cfg.Architectures) != 1 || cfg.Architectures[0] != ltypes.ArchitectureArm64 {
		t.Errorf("architectures = %v, want [arm64]", cfg.Architectures)
	}
	if cfg.EphemeralStorage == nil || aws.ToInt32(cfg.EphemeralStorage.Size) != 1024 {
		t.Errorf("ephemeral storage = %+v", cfg.EphemeralStorage)
	}
	if cfg.TracingConfig == nil || cfg.TracingConfig.Mode != ltypes.TracingModeActive {
		t.Errorf("tracing = %+v", cfg.TracingConfig)
	}
	if aws.ToString(cfg.KMSKeyArn) == "" {
		t.Error("KMSKeyArn missing")
	}
	if cfg.LoggingConfig == nil || cfg.LoggingConfig.LogFormat != ltypes.LogFormatJson {
		t.Errorf("logging config = %+v", cfg.LoggingConfig)
	}
	if cfg.SnapStart == nil || cfg.SnapStart.ApplyOn != ltypes.SnapStartApplyOnPublishedVersions {
		t.Errorf("snap start = %+v", cfg.SnapStart)
	}
	if cfg.VpcConfig == nil || len(cfg.VpcConfig.SubnetIds) != 1 {
		t.Errorf("vpc config = %+v", cfg.VpcConfig)
	}
}

// A function that declared nothing gets the AWS defaults, not invented values.
func TestUndeclaredFunctionConfigDefaults(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	if _, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("bare-fn"),
		Runtime:      ltypes.RuntimeNodejs20x,
		Role:         aws.String("arn:aws:iam::000000000000:role/lambda"),
		Handler:      aws.String("index.handler"),
		Code:         &ltypes.FunctionCode{ZipFile: tinyZip(t)},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	cfg, err := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("bare-fn"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Architectures) != 1 || cfg.Architectures[0] != ltypes.ArchitectureX8664 {
		t.Errorf("default architecture = %v, want [x86_64]", cfg.Architectures)
	}
	if cfg.VpcConfig != nil || cfg.SnapStart != nil || cfg.TracingConfig != nil {
		t.Errorf("bare function invented config: vpc=%+v snap=%+v trace=%+v",
			cfg.VpcConfig, cfg.SnapStart, cfg.TracingConfig)
	}
}
