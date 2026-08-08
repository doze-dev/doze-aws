// SDK contract tests: a real aws-sdk-go-v2 IAM client over the full CRUD
// surface, the synthesized managed policies, and policy simulation.
package iam_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

const adminPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`

func client(t *testing.T) *awsiam.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := iam.New(iam.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awsiam.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awsiam.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestSDKUserLifecycle(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	created, err := c.CreateUser(ctx, &awsiam.CreateUserInput{
		UserName: aws.String("alice"), Path: aws.String("/eng/"),
		Tags: []iamtypes.Tag{{Key: aws.String("team"), Value: aws.String("shop")}},
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if got := aws.ToString(created.User.Arn); got != "arn:aws:iam::000000000000:user/eng/alice" {
		t.Fatalf("ARN = %q", got)
	}
	if id := aws.ToString(created.User.UserId); !strings.HasPrefix(id, "AIDA") {
		t.Fatalf("UserId %q should carry the AIDA prefix", id)
	}

	_, err = c.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("alice")})
	assertCode(t, err, "EntityAlreadyExists")

	got, err := c.GetUser(ctx, &awsiam.GetUserInput{UserName: aws.String("alice")})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if len(got.User.Tags) != 1 {
		t.Fatalf("tags = %v", got.User.Tags)
	}

	list, err := c.ListUsers(ctx, &awsiam.ListUsersInput{})
	if err != nil || len(list.Users) != 1 {
		t.Fatalf("ListUsers = %v, %v", list, err)
	}

	if _, err := c.DeleteUser(ctx, &awsiam.DeleteUserInput{UserName: aws.String("alice")}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	_, err = c.GetUser(ctx, &awsiam.GetUserInput{UserName: aws.String("alice")})
	assertCode(t, err, "NoSuchEntity")
}

func TestSDKRoleAndTrustPolicy(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	trust := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	created, err := c.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName: aws.String("lambda-exec"), AssumeRolePolicyDocument: aws.String(trust),
		Description: aws.String("execution role"),
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	// The trust document round-trips URL-encoded, as AWS returns it.
	decoded, _ := url.QueryUnescape(aws.ToString(created.Role.AssumeRolePolicyDocument))
	if !strings.Contains(decoded, "lambda.amazonaws.com") {
		t.Fatalf("trust policy did not round-trip: %q", decoded)
	}

	// A malformed trust policy is rejected at create time.
	_, err = c.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName: aws.String("bad"), AssumeRolePolicyDocument: aws.String(`{"Statement":[{"Effect":"Sideways"}]}`),
	})
	assertCode(t, err, "MalformedPolicyDocument")

	if _, err := c.PutRolePolicy(ctx, &awsiam.PutRolePolicyInput{
		RoleName: aws.String("lambda-exec"), PolicyName: aws.String("inline-s3"),
		PolicyDocument: aws.String(adminPolicy),
	}); err != nil {
		t.Fatalf("PutRolePolicy: %v", err)
	}
	inline, err := c.ListRolePolicies(ctx, &awsiam.ListRolePoliciesInput{RoleName: aws.String("lambda-exec")})
	if err != nil || len(inline.PolicyNames) != 1 {
		t.Fatalf("ListRolePolicies = %v, %v", inline, err)
	}
	gp, err := c.GetRolePolicy(ctx, &awsiam.GetRolePolicyInput{
		RoleName: aws.String("lambda-exec"), PolicyName: aws.String("inline-s3"),
	})
	if err != nil {
		t.Fatalf("GetRolePolicy: %v", err)
	}
	doc, _ := url.QueryUnescape(aws.ToString(gp.PolicyDocument))
	if !strings.Contains(doc, "s3:*") {
		t.Fatalf("inline document did not round-trip: %q", doc)
	}
}

func TestSDKManagedPolicyVersions(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	created, err := c.CreatePolicy(ctx, &awsiam.CreatePolicyInput{
		PolicyName: aws.String("app-access"), PolicyDocument: aws.String(adminPolicy),
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	arn := created.Policy.Arn
	if aws.ToString(arn) != "arn:aws:iam::000000000000:policy/app-access" {
		t.Fatalf("policy ARN = %q", aws.ToString(arn))
	}

	// Four more versions reach the AWS ceiling of five.
	for i := range 4 {
		if _, err := c.CreatePolicyVersion(ctx, &awsiam.CreatePolicyVersionInput{
			PolicyArn: arn, PolicyDocument: aws.String(adminPolicy), SetAsDefault: i == 3,
		}); err != nil {
			t.Fatalf("CreatePolicyVersion %d: %v", i, err)
		}
	}
	_, err = c.CreatePolicyVersion(ctx, &awsiam.CreatePolicyVersionInput{
		PolicyArn: arn, PolicyDocument: aws.String(adminPolicy),
	})
	assertCode(t, err, "LimitExceeded")

	versions, err := c.ListPolicyVersions(ctx, &awsiam.ListPolicyVersionsInput{PolicyArn: arn})
	if err != nil || len(versions.Versions) != 5 {
		t.Fatalf("ListPolicyVersions = %d versions, %v", len(versions.Versions), err)
	}
	got, err := c.GetPolicy(ctx, &awsiam.GetPolicyInput{PolicyArn: arn})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if v := aws.ToString(got.Policy.DefaultVersionId); v != "v5" {
		t.Fatalf("default version = %q, want v5", v)
	}
	// The default version cannot be deleted.
	_, err = c.DeletePolicyVersion(ctx, &awsiam.DeletePolicyVersionInput{PolicyArn: arn, VersionId: aws.String("v5")})
	assertCode(t, err, "DeleteConflict")
	if _, err := c.DeletePolicyVersion(ctx, &awsiam.DeletePolicyVersionInput{
		PolicyArn: arn, VersionId: aws.String("v2"),
	}); err != nil {
		t.Fatalf("DeletePolicyVersion: %v", err)
	}
}

func TestSDKAttachmentAndDeleteGuard(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	c.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("bob")})
	created, _ := c.CreatePolicy(ctx, &awsiam.CreatePolicyInput{
		PolicyName: aws.String("p"), PolicyDocument: aws.String(adminPolicy),
	})
	arn := created.Policy.Arn

	if _, err := c.AttachUserPolicy(ctx, &awsiam.AttachUserPolicyInput{
		UserName: aws.String("bob"), PolicyArn: arn,
	}); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}
	att, err := c.ListAttachedUserPolicies(ctx, &awsiam.ListAttachedUserPoliciesInput{UserName: aws.String("bob")})
	if err != nil || len(att.AttachedPolicies) != 1 {
		t.Fatalf("ListAttachedUserPolicies = %v, %v", att, err)
	}
	if name := aws.ToString(att.AttachedPolicies[0].PolicyName); name != "p" {
		t.Fatalf("attached policy name = %q", name)
	}

	// An attached policy cannot be deleted, and neither can the user.
	_, err = c.DeletePolicy(ctx, &awsiam.DeletePolicyInput{PolicyArn: arn})
	assertCode(t, err, "DeleteConflict")
	_, err = c.DeleteUser(ctx, &awsiam.DeleteUserInput{UserName: aws.String("bob")})
	assertCode(t, err, "DeleteConflict")

	ents, err := c.ListEntitiesForPolicy(ctx, &awsiam.ListEntitiesForPolicyInput{PolicyArn: arn})
	if err != nil || len(ents.PolicyUsers) != 1 {
		t.Fatalf("ListEntitiesForPolicy = %v, %v", ents, err)
	}

	if _, err := c.DetachUserPolicy(ctx, &awsiam.DetachUserPolicyInput{
		UserName: aws.String("bob"), PolicyArn: arn,
	}); err != nil {
		t.Fatalf("DetachUserPolicy: %v", err)
	}
	if _, err := c.DeletePolicy(ctx, &awsiam.DeletePolicyInput{PolicyArn: arn}); err != nil {
		t.Fatalf("DeletePolicy after detach: %v", err)
	}
}

// TestSDKSynthesizedManagedPolicies is the test for the "lighter" choice:
// AWS-managed policies are derived from their names rather than vendored.
func TestSDKSynthesizedManagedPolicies(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	cases := []struct{ name, wantAction string }{
		{"AdministratorAccess", `"*"`},
		{"AmazonS3FullAccess", `"s3:*"`},
		{"AmazonSQSFullAccess", `"sqs:*"`},
		{"AmazonDynamoDBFullAccess", `"dynamodb:*"`},
		{"AmazonKinesisFullAccess", `"kinesis:*"`},
		{"AmazonS3ReadOnlyAccess", `"s3:Get*"`},
		{"AWSLambdaBasicExecutionRole", `"logs:CreateLogGroup"`},
		{"AWSLambdaKinesisExecutionRole", `"kinesis:GetRecords"`},
		{"IAMFullAccess", `"iam:*"`},
	}
	for _, tc := range cases {
		arn := aws.String("arn:aws:iam::aws:policy/" + tc.name)
		pol, err := c.GetPolicy(ctx, &awsiam.GetPolicyInput{PolicyArn: arn})
		if err != nil {
			t.Fatalf("GetPolicy(%s): %v", tc.name, err)
		}
		if aws.ToString(pol.Policy.PolicyName) != tc.name {
			t.Fatalf("name = %q, want %q", aws.ToString(pol.Policy.PolicyName), tc.name)
		}
		v, err := c.GetPolicyVersion(ctx, &awsiam.GetPolicyVersionInput{
			PolicyArn: arn, VersionId: aws.String("v1"),
		})
		if err != nil {
			t.Fatalf("GetPolicyVersion(%s): %v", tc.name, err)
		}
		doc, _ := url.QueryUnescape(aws.ToString(v.PolicyVersion.Document))
		if !strings.Contains(doc, tc.wantAction) {
			t.Fatalf("%s document %q lacks %s", tc.name, doc, tc.wantAction)
		}
	}

	// PowerUserAccess is the NotAction shape.
	v, _ := c.GetPolicyVersion(ctx, &awsiam.GetPolicyVersionInput{
		PolicyArn: aws.String("arn:aws:iam::aws:policy/PowerUserAccess"), VersionId: aws.String("v1"),
	})
	doc, _ := url.QueryUnescape(aws.ToString(v.PolicyVersion.Document))
	if !strings.Contains(doc, "NotAction") || !strings.Contains(doc, "iam:*") {
		t.Fatalf("PowerUserAccess should exclude IAM via NotAction, got %q", doc)
	}

	// A name matching no convention is reported missing rather than invented.
	_, err := c.GetPolicy(ctx, &awsiam.GetPolicyInput{
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonMadeUpNonsenseAccess"),
	})
	assertCode(t, err, "NoSuchEntity")

	// A managed policy attaches like any other.
	c.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName: aws.String("r"), AssumeRolePolicyDocument: aws.String(adminPolicy),
	})
	if _, err := c.AttachRolePolicy(ctx, &awsiam.AttachRolePolicyInput{
		RoleName: aws.String("r"), PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonS3FullAccess"),
	}); err != nil {
		t.Fatalf("AttachRolePolicy(managed): %v", err)
	}
}

func TestSDKGroupsAndMembership(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	c.CreateGroup(ctx, &awsiam.CreateGroupInput{GroupName: aws.String("devs")})
	c.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("carol")})
	if _, err := c.AddUserToGroup(ctx, &awsiam.AddUserToGroupInput{
		GroupName: aws.String("devs"), UserName: aws.String("carol"),
	}); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}
	g, err := c.GetGroup(ctx, &awsiam.GetGroupInput{GroupName: aws.String("devs")})
	if err != nil || len(g.Users) != 1 {
		t.Fatalf("GetGroup = %v, %v", g, err)
	}
	forUser, err := c.ListGroupsForUser(ctx, &awsiam.ListGroupsForUserInput{UserName: aws.String("carol")})
	if err != nil || len(forUser.Groups) != 1 {
		t.Fatalf("ListGroupsForUser = %v, %v", forUser, err)
	}
	if _, err := c.RemoveUserFromGroup(ctx, &awsiam.RemoveUserFromGroupInput{
		GroupName: aws.String("devs"), UserName: aws.String("carol"),
	}); err != nil {
		t.Fatalf("RemoveUserFromGroup: %v", err)
	}
	forUser, _ = c.ListGroupsForUser(ctx, &awsiam.ListGroupsForUserInput{UserName: aws.String("carol")})
	if len(forUser.Groups) != 0 {
		t.Fatal("membership survived removal")
	}
}

func TestSDKAccessKeys(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	c.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("dave")})

	created, err := c.CreateAccessKey(ctx, &awsiam.CreateAccessKeyInput{UserName: aws.String("dave")})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}
	id := aws.ToString(created.AccessKey.AccessKeyId)
	if !strings.HasPrefix(id, "AKIA") {
		t.Fatalf("access key id %q should carry the AKIA prefix", id)
	}
	if aws.ToString(created.AccessKey.SecretAccessKey) == "" {
		t.Fatal("CreateAccessKey must return the secret once")
	}

	keys, err := c.ListAccessKeys(ctx, &awsiam.ListAccessKeysInput{UserName: aws.String("dave")})
	if err != nil || len(keys.AccessKeyMetadata) != 1 {
		t.Fatalf("ListAccessKeys = %v, %v", keys, err)
	}
	if _, err := c.UpdateAccessKey(ctx, &awsiam.UpdateAccessKeyInput{
		AccessKeyId: aws.String(id), Status: iamtypes.StatusTypeInactive, UserName: aws.String("dave"),
	}); err != nil {
		t.Fatalf("UpdateAccessKey: %v", err)
	}
	keys, _ = c.ListAccessKeys(ctx, &awsiam.ListAccessKeysInput{UserName: aws.String("dave")})
	if keys.AccessKeyMetadata[0].Status != iamtypes.StatusTypeInactive {
		t.Fatal("status did not persist")
	}
	if _, err := c.DeleteAccessKey(ctx, &awsiam.DeleteAccessKeyInput{
		AccessKeyId: aws.String(id), UserName: aws.String("dave"),
	}); err != nil {
		t.Fatalf("DeleteAccessKey: %v", err)
	}
}

func TestSDKInstanceProfiles(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	c.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName: aws.String("ec2-role"), AssumeRolePolicyDocument: aws.String(adminPolicy),
	})
	if _, err := c.CreateInstanceProfile(ctx, &awsiam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String("web"),
	}); err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}
	if _, err := c.AddRoleToInstanceProfile(ctx, &awsiam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String("web"), RoleName: aws.String("ec2-role"),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}
	got, err := c.GetInstanceProfile(ctx, &awsiam.GetInstanceProfileInput{InstanceProfileName: aws.String("web")})
	if err != nil || len(got.InstanceProfile.Roles) != 1 {
		t.Fatalf("GetInstanceProfile = %v, %v", got, err)
	}
	// AWS allows exactly one role per profile.
	c.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName: aws.String("other"), AssumeRolePolicyDocument: aws.String(adminPolicy),
	})
	_, err = c.AddRoleToInstanceProfile(ctx, &awsiam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String("web"), RoleName: aws.String("other"),
	})
	assertCode(t, err, "LimitExceeded")
}

// TestSDKSimulatePrincipalPolicy checks that the simulator reports exactly what
// the enforcement engine would decide.
func TestSDKSimulatePrincipalPolicy(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	c.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("erin")})
	c.PutUserPolicy(ctx, &awsiam.PutUserPolicyInput{
		UserName: aws.String("erin"), PolicyName: aws.String("scoped"),
		PolicyDocument: aws.String(`{"Statement":[
			{"Sid":"ReadPhotos","Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/*"},
			{"Sid":"NoDeletes","Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`),
	})

	res, err := c.SimulatePrincipalPolicy(ctx, &awsiam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String("arn:aws:iam::000000000000:user/erin"),
		ActionNames:     []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"},
		ResourceArns:    []string{"arn:aws:s3:::photos/cat.jpg"},
	})
	if err != nil {
		t.Fatalf("SimulatePrincipalPolicy: %v", err)
	}
	want := map[string]string{
		"s3:GetObject":    "allowed",
		"s3:PutObject":    "implicitDeny",
		"s3:DeleteObject": "explicitDeny",
	}
	if len(res.EvaluationResults) != 3 {
		t.Fatalf("got %d results, want 3", len(res.EvaluationResults))
	}
	for _, r := range res.EvaluationResults {
		action := aws.ToString(r.EvalActionName)
		if got := string(r.EvalDecision); got != want[action] {
			t.Errorf("%s = %s, want %s", action, got, want[action])
		}
	}

	// Out of scope: the same action on a different bucket is not allowed.
	res, _ = c.SimulatePrincipalPolicy(ctx, &awsiam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String("arn:aws:iam::000000000000:user/erin"),
		ActionNames:     []string{"s3:GetObject"},
		ResourceArns:    []string{"arn:aws:s3:::documents/tax.pdf"},
	})
	if got := string(res.EvaluationResults[0].EvalDecision); got != "implicitDeny" {
		t.Fatalf("out-of-scope resource = %s, want implicitDeny", got)
	}
}

func TestSDKSimulateCustomPolicy(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	res, err := c.SimulateCustomPolicy(ctx, &awsiam.SimulateCustomPolicyInput{
		PolicyInputList: []string{`{"Statement":[{"Effect":"Allow","Action":"sqs:SendMessage","Resource":"*"}]}`},
		ActionNames:     []string{"sqs:SendMessage", "sqs:DeleteQueue"},
	})
	if err != nil {
		t.Fatalf("SimulateCustomPolicy: %v", err)
	}
	if string(res.EvaluationResults[0].EvalDecision) != "allowed" {
		t.Fatal("SendMessage should be allowed")
	}
	if string(res.EvaluationResults[1].EvalDecision) != "implicitDeny" {
		t.Fatal("DeleteQueue should be implicitly denied")
	}

	_, err = c.SimulateCustomPolicy(ctx, &awsiam.SimulateCustomPolicyInput{
		PolicyInputList: []string{`not json`},
		ActionNames:     []string{"sqs:SendMessage"},
	})
	assertCode(t, err, "MalformedPolicyDocument")
}

func TestSDKAccountSurface(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	if _, err := c.CreateAccountAlias(ctx, &awsiam.CreateAccountAliasInput{
		AccountAlias: aws.String("doze-local"),
	}); err != nil {
		t.Fatalf("CreateAccountAlias: %v", err)
	}
	aliases, err := c.ListAccountAliases(ctx, &awsiam.ListAccountAliasesInput{})
	if err != nil || len(aliases.AccountAliases) != 1 || aliases.AccountAliases[0] != "doze-local" {
		t.Fatalf("ListAccountAliases = %v, %v", aliases, err)
	}

	c.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("u")})
	summary, err := c.GetAccountSummary(ctx, &awsiam.GetAccountSummaryInput{})
	if err != nil {
		t.Fatalf("GetAccountSummary: %v", err)
	}
	if summary.SummaryMap["Users"] != 1 {
		t.Fatalf("summary Users = %d, want 1", summary.SummaryMap["Users"])
	}

	// An account that never set a password policy answers NoSuchEntity, as AWS does.
	_, err = c.GetAccountPasswordPolicy(ctx, &awsiam.GetAccountPasswordPolicyInput{})
	assertCode(t, err, "NoSuchEntity")
}

func TestSDKStubbedOperationsAnswerHonestly(t *testing.T) {
	ctx := context.Background()
	c := client(t)

	// A deliberately unsupported operation must not silently succeed.
	_, err := c.ListVirtualMFADevices(ctx, &awsiam.ListVirtualMFADevicesInput{})
	if err == nil {
		t.Fatal("ListVirtualMFADevices should refuse rather than return an empty list")
	}
	if !strings.Contains(err.Error(), "MFA") {
		t.Fatalf("refusal should say why, got %v", err)
	}
}
