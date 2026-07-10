// SDK v1 contract tests: the legacy aws-sdk-go (v1) SSM client.
package ssm_test

import (
	"net/http/httptest"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	ssmv1 "github.com/aws/aws-sdk-go/service/ssm"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/ssm"
)

func ssmV1Client(t *testing.T) *ssmv1.SSM {
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
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return ssmv1.New(sess)
}

func TestSDKV1ParameterRoundTrip(t *testing.T) {
	c := ssmV1Client(t)

	if _, err := c.PutParameter(&ssmv1.PutParameterInput{
		Name: awsv1.String("/v1/config"), Value: awsv1.String("legacy"), Type: awsv1.String("String"),
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}
	out, err := c.GetParameter(&ssmv1.GetParameterInput{Name: awsv1.String("/v1/config")})
	if err != nil || awsv1.StringValue(out.Parameter.Value) != "legacy" {
		t.Fatalf("GetParameter: %v", err)
	}

	// SecureString decrypts through the v1 client too.
	if _, err := c.PutParameter(&ssmv1.PutParameterInput{
		Name: awsv1.String("/v1/secret"), Value: awsv1.String("sst"), Type: awsv1.String("SecureString"),
	}); err != nil {
		t.Fatal(err)
	}
	sec, err := c.GetParameter(&ssmv1.GetParameterInput{
		Name: awsv1.String("/v1/secret"), WithDecryption: awsv1.Bool(true),
	})
	if err != nil || awsv1.StringValue(sec.Parameter.Value) != "sst" {
		t.Fatalf("SecureString via v1: %v", err)
	}

	_, err = c.GetParameter(&ssmv1.GetParameterInput{Name: awsv1.String("/nope")})
	type coder interface{ Code() string }
	if ce, ok := err.(coder); !ok || ce.Code() != "ParameterNotFound" {
		t.Fatalf("missing param error = %v", err)
	}
}
