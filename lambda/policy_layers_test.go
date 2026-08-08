// Contract tests for the surface CloudFormation needs: function resource
// policies (AWS::Lambda::Permission) and the layers family.
package lambda_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKAddPermission covers the exact shape AWS::Lambda::Permission produces:
// a service principal with a SourceArn condition.
func TestSDKAddPermission(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	mustCreateFunction(t, c, "handler")

	out, err := c.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("handler"),
		StatementId:  aws.String("AllowS3Invoke"),
		Action:       aws.String("lambda:InvokeFunction"),
		Principal:    aws.String("s3.amazonaws.com"),
		SourceArn:    aws.String("arn:aws:s3:::uploads"),
	})
	if err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	// The returned statement is a JSON string.
	var stmt map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(out.Statement)), &stmt); err != nil {
		t.Fatalf("AddPermission statement is not JSON: %v", err)
	}
	if stmt["Sid"] != "AllowS3Invoke" || stmt["Effect"] != "Allow" {
		t.Fatalf("statement = %v", stmt)
	}
	// A service principal must be spelled {"Service": ...}.
	principal, ok := stmt["Principal"].(map[string]any)
	if !ok || principal["Service"] != "s3.amazonaws.com" {
		t.Fatalf("Principal = %v, want {\"Service\":\"s3.amazonaws.com\"}", stmt["Principal"])
	}
	// SourceArn becomes the ArnLike condition AWS synthesizes.
	cond, ok := stmt["Condition"].(map[string]any)
	if !ok {
		t.Fatalf("no Condition synthesized from SourceArn: %v", stmt)
	}
	arnLike, ok := cond["ArnLike"].(map[string]any)
	if !ok || arnLike["AWS:SourceArn"] != "arn:aws:s3:::uploads" {
		t.Fatalf("ArnLike condition = %v", cond)
	}

	// A duplicate statement id conflicts.
	_, err = c.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("handler"), StatementId: aws.String("AllowS3Invoke"),
		Action: aws.String("lambda:InvokeFunction"), Principal: aws.String("s3.amazonaws.com"),
	})
	if err == nil || !strings.Contains(err.Error(), "ResourceConflict") {
		t.Fatalf("duplicate StatementId should conflict, got %v", err)
	}

	// GetPolicy returns the whole document.
	pol, err := c.GetPolicy(ctx, &awslambda.GetPolicyInput{FunctionName: aws.String("handler")})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	var doc struct {
		Version   string           `json:"Version"`
		Statement []map[string]any `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(aws.ToString(pol.Policy)), &doc); err != nil {
		t.Fatalf("GetPolicy document is not JSON: %v", err)
	}
	if doc.Version != "2012-10-17" || len(doc.Statement) != 1 {
		t.Fatalf("policy document = %+v", doc)
	}

	if _, err := c.RemovePermission(ctx, &awslambda.RemovePermissionInput{
		FunctionName: aws.String("handler"), StatementId: aws.String("AllowS3Invoke"),
	}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}
	// With no statements left, GetPolicy is a 404 — as in AWS.
	_, err = c.GetPolicy(ctx, &awslambda.GetPolicyInput{FunctionName: aws.String("handler")})
	if err == nil || !strings.Contains(err.Error(), "ResourceNotFound") {
		t.Fatalf("GetPolicy on an empty policy should 404, got %v", err)
	}
}

func TestSDKAddPermissionAccountPrincipal(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	mustCreateFunction(t, c, "handler")

	out, err := c.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("handler"), StatementId: aws.String("AllowAccount"),
		Action: aws.String("lambda:InvokeFunction"), Principal: aws.String("000000000000"),
	})
	if err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	var stmt map[string]any
	json.Unmarshal([]byte(aws.ToString(out.Statement)), &stmt)
	principal, ok := stmt["Principal"].(map[string]any)
	if !ok || principal["AWS"] != "000000000000" {
		t.Fatalf("account principal should be {\"AWS\": ...}, got %v", stmt["Principal"])
	}
}

func TestSDKLayerLifecycle(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)

	published, err := c.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName:          aws.String("shared-deps"),
		Description:        aws.String("common libraries"),
		Content:            &lamtypes.LayerVersionContentInput{ZipFile: []byte("fake-zip-content")},
		CompatibleRuntimes: []lamtypes.Runtime{lamtypes.RuntimeProvidedal2, lamtypes.RuntimePython313},
	})
	if err != nil {
		t.Fatalf("PublishLayerVersion: %v", err)
	}
	if published.Version != 1 {
		t.Fatalf("first version = %d, want 1", published.Version)
	}
	wantARN := "arn:aws:lambda:us-east-1:000000000000:layer:shared-deps:1"
	if got := aws.ToString(published.LayerVersionArn); got != wantARN {
		t.Fatalf("LayerVersionArn = %q, want %q", got, wantARN)
	}
	if published.Content == nil || published.Content.CodeSize != int64(len("fake-zip-content")) {
		t.Fatalf("Content = %+v", published.Content)
	}
	if len(published.CompatibleRuntimes) != 2 {
		t.Fatalf("CompatibleRuntimes = %v", published.CompatibleRuntimes)
	}

	// A second publish increments.
	second, err := c.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName: aws.String("shared-deps"),
		Content:   &lamtypes.LayerVersionContentInput{ZipFile: []byte("v2")},
	})
	if err != nil || second.Version != 2 {
		t.Fatalf("second publish = %d, %v", second.Version, err)
	}

	versions, err := c.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{
		LayerName: aws.String("shared-deps"),
	})
	if err != nil || len(versions.LayerVersions) != 2 {
		t.Fatalf("ListLayerVersions = %d, %v", len(versions.LayerVersions), err)
	}
	// Newest first, as AWS returns them.
	if versions.LayerVersions[0].Version != 2 {
		t.Fatalf("versions should be newest-first, got %d first", versions.LayerVersions[0].Version)
	}

	layers, err := c.ListLayers(ctx, &awslambda.ListLayersInput{})
	if err != nil || len(layers.Layers) != 1 {
		t.Fatalf("ListLayers = %v, %v", layers, err)
	}
	if layers.Layers[0].LatestMatchingVersion.Version != 2 {
		t.Fatal("ListLayers should report the latest version")
	}

	got, err := c.GetLayerVersion(ctx, &awslambda.GetLayerVersionInput{
		LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(1),
	})
	if err != nil {
		t.Fatalf("GetLayerVersion: %v", err)
	}
	if aws.ToString(got.Description) != "common libraries" {
		t.Fatalf("Description = %q", aws.ToString(got.Description))
	}

	byArn, err := c.GetLayerVersionByArn(ctx, &awslambda.GetLayerVersionByArnInput{Arn: aws.String(wantARN)})
	if err != nil {
		t.Fatalf("GetLayerVersionByArn: %v", err)
	}
	if byArn.Version != 1 {
		t.Fatalf("GetLayerVersionByArn returned version %d", byArn.Version)
	}

	if _, err := c.DeleteLayerVersion(ctx, &awslambda.DeleteLayerVersionInput{
		LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(1),
	}); err != nil {
		t.Fatalf("DeleteLayerVersion: %v", err)
	}
	_, err = c.GetLayerVersion(ctx, &awslambda.GetLayerVersionInput{
		LayerName: aws.String("shared-deps"), VersionNumber: aws.Int64(1),
	})
	if err == nil {
		t.Fatal("deleted layer version should be gone")
	}
}

func TestSDKLayerVersionPolicy(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)

	c.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName: aws.String("shared"),
		Content:   &lamtypes.LayerVersionContentInput{ZipFile: []byte("z")},
	})
	if _, err := c.AddLayerVersionPermission(ctx, &awslambda.AddLayerVersionPermissionInput{
		LayerName: aws.String("shared"), VersionNumber: aws.Int64(1),
		StatementId: aws.String("share"), Action: aws.String("lambda:GetLayerVersion"),
		Principal: aws.String("000000000000"),
	}); err != nil {
		t.Fatalf("AddLayerVersionPermission: %v", err)
	}
	pol, err := c.GetLayerVersionPolicy(ctx, &awslambda.GetLayerVersionPolicyInput{
		LayerName: aws.String("shared"), VersionNumber: aws.Int64(1),
	})
	if err != nil {
		t.Fatalf("GetLayerVersionPolicy: %v", err)
	}
	if !strings.Contains(aws.ToString(pol.Policy), "lambda:GetLayerVersion") {
		t.Fatalf("policy = %q", aws.ToString(pol.Policy))
	}
	if _, err := c.RemoveLayerVersionPermission(ctx, &awslambda.RemoveLayerVersionPermissionInput{
		LayerName: aws.String("shared"), VersionNumber: aws.Int64(1),
		StatementId: aws.String("share"),
	}); err != nil {
		t.Fatalf("RemoveLayerVersionPermission: %v", err)
	}
}

func TestSDKGetAccountSettings(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	mustCreateFunction(t, c, "one")

	got, err := c.GetAccountSettings(ctx, &awslambda.GetAccountSettingsInput{})
	if err != nil {
		t.Fatalf("GetAccountSettings: %v", err)
	}
	if got.AccountUsage == nil || got.AccountUsage.FunctionCount != 1 {
		t.Fatalf("AccountUsage = %+v, want FunctionCount 1", got.AccountUsage)
	}
	if got.AccountLimit == nil || got.AccountLimit.ConcurrentExecutions == 0 {
		t.Fatalf("AccountLimit = %+v", got.AccountLimit)
	}
}

// mustCreateFunction creates a minimal function; these tests never invoke it,
// so the code directory only has to exist.
func mustCreateFunction(t *testing.T, c *awslambda.Client, name string) {
	t.Helper()
	if _, err := c.CreateFunction(context.Background(), &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lamtypes.RuntimeProvidedal2,
		Handler:      aws.String("bootstrap"),
		Role:         aws.String("arn:aws:iam::000000000000:role/r"),
		Code:         &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(t.TempDir())},
	}); err != nil {
		t.Fatalf("CreateFunction(%s): %v", name, err)
	}
}

// TestSDKListVersionsAndCodeSigning covers two subresources Terraform reads on
// every function refresh. Neither was routed, so the whole resource failed.
func TestSDKListVersionsAndCodeSigning(t *testing.T) {
	ctx := context.Background()
	c, _ := lambdaClient(t)
	mustCreateFunction(t, c, "versioned")

	vs, err := c.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{
		FunctionName: aws.String("versioned"),
	})
	if err != nil {
		t.Fatalf("ListVersionsByFunction: %v", err)
	}
	if len(vs.Versions) != 1 || aws.ToString(vs.Versions[0].Version) != "$LATEST" {
		t.Fatalf("versions = %+v, want just $LATEST", vs.Versions)
	}

	if _, err := c.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String("versioned"),
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	vs, err = c.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{
		FunctionName: aws.String("versioned"),
	})
	if err != nil || len(vs.Versions) != 2 {
		t.Fatalf("after publish: %d versions, %v", len(vs.Versions), err)
	}
	if aws.ToString(vs.Versions[1].Version) != "1" {
		t.Fatalf("published version = %q", aws.ToString(vs.Versions[1].Version))
	}

	// A function with no code signing reports exactly that, rather than 404ing.
	cs, err := c.GetFunctionCodeSigningConfig(ctx, &awslambda.GetFunctionCodeSigningConfigInput{
		FunctionName: aws.String("versioned"),
	})
	if err != nil {
		t.Fatalf("GetFunctionCodeSigningConfig: %v", err)
	}
	if aws.ToString(cs.FunctionName) != "versioned" {
		t.Fatalf("FunctionName = %q", aws.ToString(cs.FunctionName))
	}
}
