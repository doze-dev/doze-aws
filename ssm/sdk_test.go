// SDK contract tests: a real aws-sdk-go-v2 SSM client driving the parameter
// store — versions, labels, hierarchies, SecureString encryption.
package ssm_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/ssm"
)

func ssmClient(t *testing.T) *awsssm.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := ssm.New(ssm.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awsssm.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awsssm.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func put(t *testing.T, c *awsssm.Client, name, value string, ptype ssmtypes.ParameterType, overwrite bool) int64 {
	t.Helper()
	out, err := c.PutParameter(context.Background(), &awsssm.PutParameterInput{
		Name: aws.String(name), Value: aws.String(value), Type: ptype, Overwrite: aws.Bool(overwrite),
	})
	if err != nil {
		t.Fatalf("PutParameter %s: %v", name, err)
	}
	return out.Version
}

func TestSDKVersionsAndLabels(t *testing.T) {
	ctx := context.Background()
	c := ssmClient(t)

	if v := put(t, c, "/app/db/host", "db-v1", ssmtypes.ParameterTypeString, false); v != 1 {
		t.Fatalf("first version = %d", v)
	}
	// Re-put without Overwrite must fail with the AWS code.
	_, err := c.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/app/db/host"), Value: aws.String("x"), Type: ssmtypes.ParameterTypeString,
	})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "ParameterAlreadyExists" {
		t.Fatalf("re-put error = %v", err)
	}
	if v := put(t, c, "/app/db/host", "db-v2", ssmtypes.ParameterTypeString, true); v != 2 {
		t.Fatalf("second version = %d", v)
	}

	// Label v1, then fetch by version and by label.
	if _, err := c.LabelParameterVersion(ctx, &awsssm.LabelParameterVersionInput{
		Name: aws.String("/app/db/host"), ParameterVersion: aws.Int64(1), Labels: []string{"stable"},
	}); err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}
	byVer, err := c.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/db/host:1")})
	if err != nil || aws.ToString(byVer.Parameter.Value) != "db-v1" {
		t.Fatalf("get by version: %v %+v", err, byVer)
	}
	byLabel, err := c.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/db/host:stable")})
	if err != nil || aws.ToString(byLabel.Parameter.Value) != "db-v1" {
		t.Fatalf("get by label: %v", err)
	}
	latest, err := c.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/db/host")})
	if err != nil || aws.ToString(latest.Parameter.Value) != "db-v2" {
		t.Fatalf("get latest: %v", err)
	}

	// History shows both versions with the label on v1.
	hist, err := c.GetParameterHistory(ctx, &awsssm.GetParameterHistoryInput{Name: aws.String("/app/db/host")})
	if err != nil || len(hist.Parameters) != 2 {
		t.Fatalf("history: %v, %d entries", err, len(hist.Parameters))
	}
	if hist.Parameters[0].Labels[0] != "stable" {
		t.Errorf("v1 labels = %v", hist.Parameters[0].Labels)
	}
}

func TestSDKSecureString(t *testing.T) {
	ctx := context.Background()
	c := ssmClient(t)

	put(t, c, "/app/secret", "hunter2", ssmtypes.ParameterTypeSecureString, false)

	// Without decryption: not the plaintext.
	enc, err := c.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/secret")})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(enc.Parameter.Value) == "hunter2" {
		t.Fatal("SecureString returned plaintext without WithDecryption")
	}

	// With decryption: round-trips.
	dec, err := c.GetParameter(ctx, &awsssm.GetParameterInput{
		Name: aws.String("/app/secret"), WithDecryption: aws.Bool(true),
	})
	if err != nil || aws.ToString(dec.Parameter.Value) != "hunter2" {
		t.Fatalf("WithDecryption: %v %q", err, aws.ToString(dec.Parameter.Value))
	}
}

func TestSDKByPathAndDescribe(t *testing.T) {
	ctx := context.Background()
	c := ssmClient(t)

	put(t, c, "/app/db/host", "h", ssmtypes.ParameterTypeString, false)
	put(t, c, "/app/db/port", "5432", ssmtypes.ParameterTypeString, false)
	put(t, c, "/app/features", `{"beta":true}`, ssmtypes.ParameterTypeString, false)
	put(t, c, "/other/x", "y", ssmtypes.ParameterTypeString, false)

	// Non-recursive: direct children only.
	flat, err := c.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{Path: aws.String("/app")})
	if err != nil || len(flat.Parameters) != 1 {
		t.Fatalf("non-recursive: %v, %d params", err, len(flat.Parameters))
	}
	// Recursive: the whole subtree.
	deep, err := c.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
		Path: aws.String("/app"), Recursive: aws.Bool(true),
	})
	if err != nil || len(deep.Parameters) != 3 {
		t.Fatalf("recursive: %v, %d params", err, len(deep.Parameters))
	}

	// GetParameters mixes found and missing.
	multi, err := c.GetParameters(ctx, &awsssm.GetParametersInput{
		Names: []string{"/app/db/host", "/nope"},
	})
	if err != nil || len(multi.Parameters) != 1 || len(multi.InvalidParameters) != 1 {
		t.Fatalf("GetParameters: %v %+v", err, multi)
	}

	// DescribeParameters with a BeginsWith filter.
	desc, err := c.DescribeParameters(ctx, &awsssm.DescribeParametersInput{
		ParameterFilters: []ssmtypes.ParameterStringFilter{
			{Key: aws.String("Name"), Option: aws.String("BeginsWith"), Values: []string{"/app/db"}},
		},
	})
	if err != nil || len(desc.Parameters) != 2 {
		t.Fatalf("DescribeParameters: %v, %d", err, len(desc.Parameters))
	}
	if !strings.HasPrefix(aws.ToString(desc.Parameters[0].ARN), "arn:aws:ssm:") {
		t.Errorf("ARN = %q", aws.ToString(desc.Parameters[0].ARN))
	}
}

func TestSDKDeleteAndTags(t *testing.T) {
	ctx := context.Background()
	c := ssmClient(t)

	put(t, c, "/tagme", "v", ssmtypes.ParameterTypeString, false)
	if _, err := c.AddTagsToResource(ctx, &awsssm.AddTagsToResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String("/tagme"),
		Tags:         []ssmtypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	}); err != nil {
		t.Fatalf("AddTagsToResource: %v", err)
	}
	tags, err := c.ListTagsForResource(ctx, &awsssm.ListTagsForResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter, ResourceId: aws.String("/tagme"),
	})
	if err != nil || len(tags.TagList) != 1 || aws.ToString(tags.TagList[0].Key) != "env" {
		t.Fatalf("ListTagsForResource: %v %+v", err, tags.TagList)
	}

	del, err := c.DeleteParameters(ctx, &awsssm.DeleteParametersInput{Names: []string{"/tagme", "/nope"}})
	if err != nil || len(del.DeletedParameters) != 1 || len(del.InvalidParameters) != 1 {
		t.Fatalf("DeleteParameters: %v %+v", err, del)
	}
	_, err = c.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/tagme")})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "ParameterNotFound" {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestSDKFleetOpsAnswerHonestly(t *testing.T) {
	ctx := context.Background()
	c := ssmClient(t)
	_, err := c.SendCommand(ctx, &awsssm.SendCommandInput{DocumentName: aws.String("AWS-RunShellScript")})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "UnsupportedOperationException" {
		t.Fatalf("SendCommand error = %v", err)
	}
}
