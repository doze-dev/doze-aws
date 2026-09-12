package cloudformation_test

// A bucket's public access block, ownership controls and bucket policy come
// through a template: the block is applied before the policy, so a public
// policy under BlockPublicPolicy fails the deploy with S3's own message,
// and with the block lifted it lands, reads back as public, and exports.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const bucketAccessTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Site:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: site-assets
      PublicAccessBlockConfiguration:
        BlockPublicAcls: true
        IgnorePublicAcls: true
        BlockPublicPolicy: BLOCK_POLICY
        RestrictPublicBuckets: false
      OwnershipControls:
        Rules:
          - ObjectOwnership: BucketOwnerEnforced
  SitePolicy:
    Type: AWS::S3::BucketPolicy
    Properties:
      Bucket: !Ref Site
      PolicyDocument:
        Version: "2012-10-17"
        Statement:
          - Effect: Allow
            Principal: "*"
            Action: s3:GetObject
            Resource: !Sub "${Site.Arn}/*"
`

func TestApplyBucketAccessSettings(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	transpile := func(block string) *provision.Stack {
		t.Helper()
		tmpl, err := cloudformation.Parse([]byte(strings.ReplaceAll(bucketAccessTemplate, "BLOCK_POLICY", block)))
		if err != nil {
			t.Fatal(err)
		}
		sf, _, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "site"})
		if err != nil {
			t.Fatal(err)
		}
		return sf
	}
	blocked := transpile("true")
	b := blocked.Buckets["site-assets"]
	if b.PublicAccess == nil || !b.PublicAccess.BlockPublicPolicy || b.Ownership != "BucketOwnerEnforced" || !strings.Contains(b.Policy.JSON, `"Principal":"*"`) {
		t.Fatalf("the access settings did not map: %+v", b)
	}
	// Blocked: the deploy fails on the policy, naming the setting.
	if _, err := provision.Apply(ctx, stack.Handler(), blocked, awsident.Default()); err == nil || !strings.Contains(err.Error(), "BlockPublicPolicy") {
		t.Fatalf("a public policy under BlockPublicPolicy should fail the apply with S3's message, got %v", err)
	}
	// Lifted: it lands, twice.
	open := transpile("false")
	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), open, awsident.Default()); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = aws.String(ts.URL); o.UsePathStyle = true })
	status, err := s3c.GetBucketPolicyStatus(ctx, &awss3.GetBucketPolicyStatusInput{Bucket: aws.String("site-assets")})
	if err != nil || !aws.ToBool(status.PolicyStatus.IsPublic) {
		t.Errorf("the deployed policy should read as public: %v %v", status, err)
	}
	oc, err := s3c.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{Bucket: aws.String("site-assets")})
	if err != nil || string(oc.OwnershipControls.Rules[0].ObjectOwnership) != "BucketOwnerEnforced" {
		t.Errorf("ownership controls did not deploy: %v %v", oc, err)
	}

	// Export carries all three back as a template that transpiles the same.
	exported, err := provision.Export(ctx, stack.Handler(), awsident.Default())
	if err != nil {
		t.Fatal(err)
	}
	out, err := cloudformation.Emit(exported)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := cloudformation.Parse(out)
	round, _, err := cloudformation.Transpile(again, cloudformation.TranspileOptions{StackName: "site"})
	if err != nil {
		t.Fatalf("exported template does not transpile: %v\n%s", err, out)
	}
	rb := round.Buckets["site-assets"]
	if rb.PublicAccess == nil || rb.PublicAccess.BlockPublicPolicy || rb.Ownership != "BucketOwnerEnforced" || rb.Policy.IsZero() {
		t.Errorf("round trip lost an access setting: %+v\n%s", rb, out)
	}
}
