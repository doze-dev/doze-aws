// SDK v1 contract tests: the legacy aws-sdk-go (v1) Secrets Manager client.
package secretsmanager_test

import (
	"net/http/httptest"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	smv1 "github.com/aws/aws-sdk-go/service/secretsmanager"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/secretsmanager"
)

func smV1Client(t *testing.T) *smv1.SecretsManager {
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
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return smv1.New(sess)
}

func TestSDKV1SecretRoundTrip(t *testing.T) {
	c := smV1Client(t)

	created, err := c.CreateSecret(&smv1.CreateSecretInput{
		Name: awsv1.String("v1-secret"), SecretString: awsv1.String("legacy value"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if awsv1.StringValue(created.ARN) == "" {
		t.Fatal("empty ARN")
	}
	got, err := c.GetSecretValue(&smv1.GetSecretValueInput{SecretId: awsv1.String("v1-secret")})
	if err != nil || awsv1.StringValue(got.SecretString) != "legacy value" {
		t.Fatalf("GetSecretValue: %v", err)
	}

	_, err = c.GetSecretValue(&smv1.GetSecretValueInput{SecretId: awsv1.String("absent")})
	type coder interface{ Code() string }
	if ce, ok := err.(coder); !ok || ce.Code() != "ResourceNotFoundException" {
		t.Fatalf("missing secret error = %v", err)
	}
}
