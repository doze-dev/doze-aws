package s3_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Public access block, ownership controls and policy status through the SDK:
// a new bucket blocks public policies, lifting the block lets one through,
// the status reports it, and the three families round-trip and delete.

func TestPublicAccessBlockGatesPublicPolicies(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("guarded")}); err != nil {
		t.Fatal(err)
	}
	// Born blocked, all four.
	pab, err := c.GetPublicAccessBlock(ctx, &awss3.GetPublicAccessBlockInput{Bucket: aws.String("guarded")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := pab.PublicAccessBlockConfiguration
	if !aws.ToBool(cfg.BlockPublicAcls) || !aws.ToBool(cfg.IgnorePublicAcls) || !aws.ToBool(cfg.BlockPublicPolicy) || !aws.ToBool(cfg.RestrictPublicBuckets) {
		t.Errorf("a new bucket should block everything, got %+v", cfg)
	}
	public := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::guarded/*"}]}`
	private := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:user/test"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::guarded/*"}]}`
	_, err = c.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("guarded"), Policy: aws.String(public)})
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "AccessDenied" {
		t.Fatalf("a public policy under BlockPublicPolicy should be AccessDenied, got %v", err)
	}
	// A policy that names a principal is fine under the block.
	if _, err := c.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("guarded"), Policy: aws.String(private)}); err != nil {
		t.Fatalf("a non-public policy should be accepted: %v", err)
	}
	status, err := c.GetBucketPolicyStatus(ctx, &awss3.GetBucketPolicyStatusInput{Bucket: aws.String("guarded")})
	if err != nil || aws.ToBool(status.PolicyStatus.IsPublic) {
		t.Errorf("a principal-scoped policy is not public: %+v %v", status, err)
	}
	// Lift the block: the public policy goes in, and the status says so.
	if _, err := c.PutPublicAccessBlock(ctx, &awss3.PutPublicAccessBlockInput{Bucket: aws.String("guarded"),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{BlockPublicPolicy: aws.Bool(false)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("guarded"), Policy: aws.String(public)}); err != nil {
		t.Fatalf("with BlockPublicPolicy off the public policy should be accepted: %v", err)
	}
	status, _ = c.GetBucketPolicyStatus(ctx, &awss3.GetBucketPolicyStatusInput{Bucket: aws.String("guarded")})
	if !aws.ToBool(status.PolicyStatus.IsPublic) {
		t.Errorf("a Principal * Allow with no condition is public")
	}
	// Deleting the block reads back as not found, as on AWS.
	if _, err := c.DeletePublicAccessBlock(ctx, &awss3.DeletePublicAccessBlockInput{Bucket: aws.String("guarded")}); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetPublicAccessBlock(ctx, &awss3.GetPublicAccessBlockInput{Bucket: aws.String("guarded")})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchPublicAccessBlockConfiguration" {
		t.Errorf("after delete the block should be NoSuchPublicAccessBlockConfiguration, got %v", err)
	}
	// No policy: no status.
	c.DeleteBucketPolicy(ctx, &awss3.DeleteBucketPolicyInput{Bucket: aws.String("guarded")})
	_, err = c.GetBucketPolicyStatus(ctx, &awss3.GetBucketPolicyStatusInput{Bucket: aws.String("guarded")})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchBucketPolicy" {
		t.Errorf("no policy should be NoSuchBucketPolicy, got %v", err)
	}
}

func TestOwnershipControlsRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("owned")}); err != nil {
		t.Fatal(err)
	}
	var apiErr smithy.APIError
	_, err := c.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{Bucket: aws.String("owned")})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "OwnershipControlsNotFoundError" {
		t.Fatalf("unset controls should be OwnershipControlsNotFoundError, got %v", err)
	}
	if _, err := c.PutBucketOwnershipControls(ctx, &awss3.PutBucketOwnershipControlsInput{Bucket: aws.String("owned"),
		OwnershipControls: &s3types.OwnershipControls{Rules: []s3types.OwnershipControlsRule{{ObjectOwnership: s3types.ObjectOwnershipBucketOwnerEnforced}}}}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{Bucket: aws.String("owned")})
	if err != nil || len(got.OwnershipControls.Rules) != 1 || got.OwnershipControls.Rules[0].ObjectOwnership != s3types.ObjectOwnershipBucketOwnerEnforced {
		t.Errorf("ownership controls did not round-trip: %+v %v", got, err)
	}
	if _, err := c.DeleteBucketOwnershipControls(ctx, &awss3.DeleteBucketOwnershipControlsInput{Bucket: aws.String("owned")}); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{Bucket: aws.String("owned")})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "OwnershipControlsNotFoundError" {
		t.Errorf("after delete the controls should be gone, got %v", err)
	}
	// Encryption, now on the route table, still round-trips.
	enc, err := c.GetBucketEncryption(ctx, &awss3.GetBucketEncryptionInput{Bucket: aws.String("owned")})
	if err != nil || enc.ServerSideEncryptionConfiguration == nil {
		t.Errorf("GetBucketEncryption = %v %v", enc, err)
	}
}
