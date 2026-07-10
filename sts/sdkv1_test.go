// SDK v1 contract tests: the same scenarios driven by the legacy aws-sdk-go
// (v1) client — the second independent client generation, per the doze-aws
// dual-SDK requirement.
package sts_test

import (
	"strings"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	stsv1 "github.com/aws/aws-sdk-go/service/sts"

	"github.com/doze-dev/doze-aws/awsident"
)

func stsV1Client(t *testing.T, url string) *stsv1.STS {
	t.Helper()
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(url).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return stsv1.New(sess)
}

func TestSDKV1GetCallerIdentity(t *testing.T) {
	client := stsV1Client(t, startStack(t).URL)
	out, err := client.GetCallerIdentity(&stsv1.GetCallerIdentityInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got := awsv1.StringValue(out.Account); got != awsident.AccountID {
		t.Errorf("Account = %q, want %q", got, awsident.AccountID)
	}
	if arn := awsv1.StringValue(out.Arn); !strings.Contains(arn, "user/"+awsident.AccessKeyID) {
		t.Errorf("Arn = %q", arn)
	}
}

func TestSDKV1AssumeRole(t *testing.T) {
	client := stsV1Client(t, startStack(t).URL)
	out, err := client.AssumeRole(&stsv1.AssumeRoleInput{
		RoleArn:         awsv1.String("arn:aws:iam::000000000000:role/legacy-role"),
		RoleSessionName: awsv1.String("v1-session"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if awsv1.StringValue(out.Credentials.AccessKeyId) == "" || awsv1.StringValue(out.Credentials.SessionToken) == "" {
		t.Fatalf("incomplete credentials: %+v", out.Credentials)
	}
	if arn := awsv1.StringValue(out.AssumedRoleUser.Arn); !strings.Contains(arn, "assumed-role/legacy-role/v1-session") {
		t.Errorf("AssumedRoleUser.Arn = %q", arn)
	}
}

func TestSDKV1ErrorEnvelope(t *testing.T) {
	client := stsV1Client(t, startStack(t).URL)
	// The v1 SDK validates lengths client-side, so use input that passes the
	// client but fails the server: a session name with a forbidden character.
	_, err := client.AssumeRole(&stsv1.AssumeRoleInput{
		RoleArn:         awsv1.String("arn:aws:iam::000000000000:role/r"),
		RoleSessionName: awsv1.String("bad name"),
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	aerr, ok := err.(awserr.Error)
	if !ok {
		t.Fatalf("want awserr.Error, got %T: %v", err, err)
	}
	if aerr.Code() != "ValidationError" {
		t.Errorf("code = %q, want ValidationError (message: %s)", aerr.Code(), aerr.Message())
	}
}

func TestSDKV1GetSessionToken(t *testing.T) {
	client := stsV1Client(t, startStack(t).URL)
	out, err := client.GetSessionToken(&stsv1.GetSessionTokenInput{})
	if err != nil {
		t.Fatal(err)
	}
	if awsv1.StringValue(out.Credentials.SessionToken) == "" {
		t.Error("empty session token")
	}
}
