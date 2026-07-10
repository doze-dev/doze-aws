// SDK contract tests: a real aws-sdk-go-v2 client driving the STS service
// through the shared-endpoint gateway, exactly as an application would.
package sts_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// startStack boots a full doze-aws stack on a loopback listener. Contract
// tests are gated behind -short like doze-kafka's integration tests.
func startStack(t *testing.T) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	srv := httptest.NewServer(stack.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func stsClient(srv *httptest.Server) *awssts.Client {
	return awssts.New(awssts.Options{
		Region:       awsident.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
		BaseEndpoint: aws.String(srv.URL),
	})
}

func TestSDKGetCallerIdentity(t *testing.T) {
	client := stsClient(startStack(t))
	out, err := client.GetCallerIdentity(context.Background(), &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(out.Account); got != awsident.AccountID {
		t.Errorf("Account = %q, want %q", got, awsident.AccountID)
	}
	if arn := aws.ToString(out.Arn); !strings.Contains(arn, "user/"+awsident.AccessKeyID) {
		t.Errorf("Arn = %q", arn)
	}
	if aws.ToString(out.UserId) == "" {
		t.Error("UserId is empty")
	}
}

func TestSDKAssumeRole(t *testing.T) {
	client := stsClient(startStack(t))
	out, err := client.AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::000000000000:role/service/my-role"),
		RoleSessionName: aws.String("test-session"),
		DurationSeconds: aws.Int32(1800),
	})
	if err != nil {
		t.Fatal(err)
	}
	creds := out.Credentials
	if creds == nil || aws.ToString(creds.AccessKeyId) == "" || aws.ToString(creds.SecretAccessKey) == "" || aws.ToString(creds.SessionToken) == "" {
		t.Fatalf("incomplete credentials: %+v", creds)
	}
	if !strings.HasPrefix(aws.ToString(creds.AccessKeyId), "ASIA") {
		t.Errorf("AccessKeyId = %q, want ASIA prefix", aws.ToString(creds.AccessKeyId))
	}
	// Expiration should honor DurationSeconds (~30 min from now).
	until := time.Until(aws.ToTime(creds.Expiration))
	if until < 25*time.Minute || until > 35*time.Minute {
		t.Errorf("Expiration %v from now, want ~30m", until)
	}
	if arn := aws.ToString(out.AssumedRoleUser.Arn); !strings.Contains(arn, "assumed-role/my-role/test-session") {
		t.Errorf("AssumedRoleUser.Arn = %q", arn)
	}
}

func TestSDKAssumeRoleValidation(t *testing.T) {
	client := stsClient(startStack(t))

	_, err := client.AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("not-an-arn"),
		RoleSessionName: aws.String("s1"),
	})
	assertAPIError(t, err, "ValidationError")

	_, err = client.AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::000000000000:role/r"),
		RoleSessionName: aws.String("ok-session"),
		DurationSeconds: aws.Int32(60), // below the 900s floor
	})
	assertAPIError(t, err, "ValidationError")
}

func TestSDKGetSessionTokenAndFederationToken(t *testing.T) {
	client := stsClient(startStack(t))

	st, err := client.GetSessionToken(context.Background(), &awssts.GetSessionTokenInput{})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(st.Credentials.SessionToken) == "" {
		t.Error("GetSessionToken returned empty session token")
	}

	ft, err := client.GetFederationToken(context.Background(), &awssts.GetFederationTokenInput{
		Name: aws.String("build-bot"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(ft.FederatedUser.FederatedUserId); got != awsident.AccountID+":build-bot" {
		t.Errorf("FederatedUserId = %q", got)
	}
}

func TestSDKAssumeRoleWithWebIdentity(t *testing.T) {
	client := stsClient(startStack(t))

	// An unsigned but structurally valid JWT; the service reflects its claims.
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-123","aud":"my-client","iss":"https://issuer.example.com"}`))
	token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + payload + ".x"

	out, err := client.AssumeRoleWithWebIdentity(context.Background(), &awssts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String("arn:aws:iam::000000000000:role/web-role"),
		RoleSessionName:  aws.String("web-session"),
		WebIdentityToken: aws.String(token),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(out.SubjectFromWebIdentityToken); got != "user-123" {
		t.Errorf("Subject = %q, want user-123", got)
	}
	if got := aws.ToString(out.Audience); got != "my-client" {
		t.Errorf("Audience = %q", got)
	}
	if got := aws.ToString(out.Provider); got != "issuer.example.com" {
		t.Errorf("Provider = %q", got)
	}
}

func TestSDKGetAccessKeyInfo(t *testing.T) {
	client := stsClient(startStack(t))
	out, err := client.GetAccessKeyInfo(context.Background(), &awssts.GetAccessKeyInfoInput{
		AccessKeyId: aws.String("AKIAEXAMPLEEXAMPLE"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(out.Account) != awsident.AccountID {
		t.Errorf("Account = %q", aws.ToString(out.Account))
	}
}

func TestSDKDecodeAuthorizationMessageIsHonestStub(t *testing.T) {
	client := stsClient(startStack(t))
	_, err := client.DecodeAuthorizationMessage(context.Background(), &awssts.DecodeAuthorizationMessageInput{
		EncodedMessage: aws.String("opaque"),
	})
	assertAPIError(t, err, "InvalidAuthorizationMessageException")
}

// assertAPIError requires err to be a smithy API error with the given code —
// proving the error envelope parses in the real SDK, not just that a call failed.
func assertAPIError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s error, got nil", code)
	}
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want smithy.APIError, got %T: %v", err, err)
	}
	if ae.ErrorCode() != code {
		t.Errorf("error code = %q, want %q (message: %s)", ae.ErrorCode(), code, ae.ErrorMessage())
	}
}
