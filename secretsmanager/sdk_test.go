// SDK contract tests: a real aws-sdk-go-v2 Secrets Manager client — version
// stage movement, recovery-window deletion, and value round-trips.
package secretsmanager_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/secretsmanager"
)

func smClient(t *testing.T) *awssm.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := secretsmanager.New(secretsmanager.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awssm.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awssm.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestSDKSecretLifecycle(t *testing.T) {
	ctx := context.Background()
	c := smClient(t)

	created, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("db-credentials"),
		SecretString: aws.String(`{"username":"app","password":"v1"}`),
		Description:  aws.String("app database"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if arn := aws.ToString(created.ARN); arn == "" {
		t.Fatal("empty ARN")
	}

	// Re-creating fails with the AWS code.
	_, err = c.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("db-credentials"), SecretString: aws.String("x"),
	})
	assertCode(t, err, "ResourceExistsException")

	// Get by name returns AWSCURRENT.
	got, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db-credentials")})
	if err != nil || aws.ToString(got.SecretString) != `{"username":"app","password":"v1"}` {
		t.Fatalf("GetSecretValue: %v %q", err, aws.ToString(got.SecretString))
	}

	// New version: stages move, AWSPREVIOUS reaches v1.
	if _, err := c.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("db-credentials"), SecretString: aws.String(`{"password":"v2"}`),
	}); err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}
	cur, _ := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db-credentials")})
	if aws.ToString(cur.SecretString) != `{"password":"v2"}` {
		t.Fatalf("AWSCURRENT = %q", aws.ToString(cur.SecretString))
	}
	prev, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("db-credentials"), VersionStage: aws.String("AWSPREVIOUS"),
	})
	if err != nil || aws.ToString(prev.SecretString) != `{"username":"app","password":"v1"}` {
		t.Fatalf("AWSPREVIOUS: %v %q", err, aws.ToString(prev.SecretString))
	}

	// Get by ARN works too (name embedded with the suffix).
	byARN, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: created.ARN})
	if err != nil || aws.ToString(byARN.SecretString) != `{"password":"v2"}` {
		t.Fatalf("get by ARN: %v", err)
	}
}

func TestSDKDeletionRecoveryWindow(t *testing.T) {
	ctx := context.Background()
	c := smClient(t)

	if _, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("doomed"), SecretString: aws.String("bye"),
	}); err != nil {
		t.Fatal(err)
	}
	del, err := c.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("doomed")})
	if err != nil || del.DeletionDate == nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	// Scheduled-for-deletion secrets refuse reads with InvalidRequestException.
	_, err = c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("doomed")})
	assertCode(t, err, "InvalidRequestException")

	// Hidden from ListSecrets unless IncludePlannedDeletion.
	plain, _ := c.ListSecrets(ctx, &awssm.ListSecretsInput{})
	if len(plain.SecretList) != 0 {
		t.Fatalf("deleted secret still listed: %+v", plain.SecretList)
	}
	withDeleted, _ := c.ListSecrets(ctx, &awssm.ListSecretsInput{IncludePlannedDeletion: aws.Bool(true)})
	if len(withDeleted.SecretList) != 1 || withDeleted.SecretList[0].DeletedDate == nil {
		t.Fatalf("IncludePlannedDeletion: %+v", withDeleted.SecretList)
	}

	// Restore brings it back.
	if _, err := c.RestoreSecret(ctx, &awssm.RestoreSecretInput{SecretId: aws.String("doomed")}); err != nil {
		t.Fatalf("RestoreSecret: %v", err)
	}
	back, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("doomed")})
	if err != nil || aws.ToString(back.SecretString) != "bye" {
		t.Fatalf("after restore: %v", err)
	}

	// Force delete removes immediately.
	if _, err := c.DeleteSecret(ctx, &awssm.DeleteSecretInput{
		SecretId: aws.String("doomed"), ForceDeleteWithoutRecovery: aws.Bool(true),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("doomed")})
	assertCode(t, err, "ResourceNotFoundException")
}

func TestSDKStagesAndVersionIds(t *testing.T) {
	ctx := context.Background()
	c := smClient(t)

	created, _ := c.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("staged"), SecretString: aws.String("v1"),
	})
	v1 := aws.ToString(created.VersionId)
	put, _ := c.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("staged"), SecretString: aws.String("v2"),
	})
	v2 := aws.ToString(put.VersionId)

	// Move a custom stage onto v1, then shift it to v2.
	if _, err := c.UpdateSecretVersionStage(ctx, &awssm.UpdateSecretVersionStageInput{
		SecretId: aws.String("staged"), VersionStage: aws.String("BLUE"), MoveToVersionId: aws.String(v1),
	}); err != nil {
		t.Fatalf("UpdateSecretVersionStage: %v", err)
	}
	blue, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("staged"), VersionStage: aws.String("BLUE"),
	})
	if err != nil || aws.ToString(blue.SecretString) != "v1" {
		t.Fatalf("BLUE -> %q, %v", aws.ToString(blue.SecretString), err)
	}
	if _, err := c.UpdateSecretVersionStage(ctx, &awssm.UpdateSecretVersionStageInput{
		SecretId: aws.String("staged"), VersionStage: aws.String("BLUE"), MoveToVersionId: aws.String(v2),
	}); err != nil {
		t.Fatal(err)
	}
	blue, _ = c.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("staged"), VersionStage: aws.String("BLUE"),
	})
	if aws.ToString(blue.SecretString) != "v2" {
		t.Fatalf("BLUE after move = %q", aws.ToString(blue.SecretString))
	}

	ids, err := c.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{SecretId: aws.String("staged")})
	if err != nil || len(ids.Versions) != 2 {
		t.Fatalf("ListSecretVersionIds: %v, %d", err, len(ids.Versions))
	}

	desc, err := c.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("staged")})
	if err != nil || len(desc.VersionIdsToStages) != 2 {
		t.Fatalf("DescribeSecret: %v %+v", err, desc.VersionIdsToStages)
	}
}

func TestSDKBinaryTagsAndRandomPassword(t *testing.T) {
	ctx := context.Background()
	c := smClient(t)

	if _, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("binary"), SecretBinary: []byte{0x00, 0x01, 0xfe, 0xff},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("binary")})
	if err != nil || len(got.SecretBinary) != 4 || got.SecretBinary[3] != 0xff {
		t.Fatalf("binary round trip: %v %v", err, got.SecretBinary)
	}

	pw, err := c.GetRandomPassword(ctx, &awssm.GetRandomPasswordInput{PasswordLength: aws.Int64(20)})
	if err != nil || len(aws.ToString(pw.RandomPassword)) != 20 {
		t.Fatalf("GetRandomPassword: %v", err)
	}

	// Rotation is functional (see TestRotateSecretViaLambda); with no rotation
	// Lambda configured it's a real validation error, not a stub.
	_, err = c.RotateSecret(ctx, &awssm.RotateSecretInput{SecretId: aws.String("binary")})
	assertCode(t, err, "InvalidParameterException")
}
