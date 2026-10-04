package dozeaws_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/doze-dev/doze-aws/awsident"
)

// A policy is written against the permissions AWS names, and enforce mode has
// to honour exactly those. The REST services used to guess the permission from
// the path: a policy that allowed lambda:AddPermission denied AddPermission
// (the guess was lambda:CreatePermission), and one that allowed
// s3:PutBucketCORS could not delete a bucket's CORS (the guess was
// s3:DeleteBucketCORS) — both denied locally and fine on AWS, which is the one
// direction a local emulator must never differ in.
func TestEnforceHonoursThePermissionsAWSNamesForRESTServices(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	ctx := context.Background()
	cfg, url := enforceStack(t, "iam", "s3", "lambda")
	iamc := awsiam.NewFromConfig(cfg, func(o *awsiam.Options) { o.BaseEndpoint = aws.String(url) })
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) })
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true })

	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("index.py")
	w.Write([]byte("def handler(event, context):\n    return event\n"))
	zw.Close()
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("worker"), Runtime: lamtypes.RuntimePython312, Handler: aws.String("index.handler"),
		Role: aws.String("arn:aws:iam::000000000000:role/r"), Code: &lamtypes.FunctionCode{ZipFile: zb.Bytes()},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("docs")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.PutBucketCors(ctx, &awss3.PutBucketCorsInput{Bucket: aws.String("docs"), CORSConfiguration: &s3types.CORSConfiguration{
		CORSRules: []s3types.CORSRule{{AllowedMethods: []string{"GET"}, AllowedOrigins: []string{"*"}}},
	}}); err != nil {
		t.Fatalf("PutBucketCors: %v", err)
	}

	// A user allowed exactly the permissions AWS names for what it does.
	user := func(name, doc string) (lamc *awslambda.Client, s3u *awss3.Client) {
		t.Helper()
		if _, err := iamc.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String(name)}); err != nil {
			t.Fatal(err)
		}
		if _, err := iamc.PutUserPolicy(ctx, &awsiam.PutUserPolicyInput{UserName: aws.String(name), PolicyName: aws.String("p"), PolicyDocument: aws.String(doc)}); err != nil {
			t.Fatal(err)
		}
		key, err := iamc.CreateAccessKey(ctx, &awsiam.CreateAccessKeyInput{UserName: aws.String(name)})
		if err != nil {
			t.Fatal(err)
		}
		c := aws.Config{Region: awsident.Region, Credentials: credentials.NewStaticCredentialsProvider(
			aws.ToString(key.AccessKey.AccessKeyId), aws.ToString(key.AccessKey.SecretAccessKey), "")}
		return awslambda.NewFromConfig(c, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) }),
			awss3.NewFromConfig(c, func(o *awss3.Options) { o.BaseEndpoint = aws.String(url); o.UsePathStyle = true })
	}
	named := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[
	  "lambda:AddPermission","lambda:GetPolicy","lambda:RemovePermission","lambda:ListVersionsByFunction",
	  "s3:PutBucketCORS","s3:GetBucketAcl","s3:ListBucketVersions"],"Resource":"*"}]}`
	lamAWS, s3AWS := user("aws-names", named)

	if _, err := lamAWS.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("worker"), StatementId: aws.String("sid"), Action: aws.String("lambda:InvokeFunction"), Principal: aws.String("s3.amazonaws.com"),
	}); err != nil {
		t.Errorf("AddPermission with lambda:AddPermission allowed: %v", err)
	}
	if _, err := lamAWS.GetPolicy(ctx, &awslambda.GetPolicyInput{FunctionName: aws.String("worker")}); err != nil {
		t.Errorf("GetPolicy with lambda:GetPolicy allowed: %v", err)
	}
	if _, err := lamAWS.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{FunctionName: aws.String("worker")}); err != nil {
		t.Errorf("ListVersionsByFunction with lambda:ListVersionsByFunction allowed: %v", err)
	}
	if _, err := lamAWS.RemovePermission(ctx, &awslambda.RemovePermissionInput{FunctionName: aws.String("worker"), StatementId: aws.String("sid")}); err != nil {
		t.Errorf("RemovePermission with lambda:RemovePermission allowed: %v", err)
	}
	if _, err := s3AWS.GetBucketAcl(ctx, &awss3.GetBucketAclInput{Bucket: aws.String("docs")}); err != nil {
		t.Errorf("GetBucketAcl with s3:GetBucketAcl allowed: %v", err)
	}
	if _, err := s3AWS.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("docs")}); err != nil {
		t.Errorf("ListObjectVersions with s3:ListBucketVersions allowed: %v", err)
	}
	if _, err := s3AWS.DeleteBucketCors(ctx, &awss3.DeleteBucketCorsInput{Bucket: aws.String("docs")}); err != nil {
		t.Errorf("DeleteBucketCors with s3:PutBucketCORS allowed: %v", err)
	}

	// The control: the names the old guess produced are not permissions AWS
	// has, so allowing them grants nothing.
	guessed := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[
	  "lambda:CreatePermission","lambda:GetPermission","s3:GetAcl","s3:DeleteBucketCORS"],"Resource":"*"}]}`
	lamGuess, s3Guess := user("old-guesses", guessed)
	if _, err := lamGuess.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("worker"), StatementId: aws.String("sid2"), Action: aws.String("lambda:InvokeFunction"), Principal: aws.String("s3.amazonaws.com"),
	}); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("AddPermission with only lambda:CreatePermission allowed = %v, want AccessDenied", err)
	}
	if _, err := s3Guess.GetBucketAcl(ctx, &awss3.GetBucketAclInput{Bucket: aws.String("docs")}); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("GetBucketAcl with only s3:GetAcl allowed = %v, want AccessDenied", err)
	}
}
