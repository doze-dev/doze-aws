package s3_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestSDKCORSAndPreflight(t *testing.T) {
	ctx := context.Background()
	ts := startS3(t)
	c := s3Client(t, ts.URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("corsb")})

	if _, err := c.PutBucketCors(ctx, &awss3.PutBucketCorsInput{
		Bucket: aws.String("corsb"),
		CORSConfiguration: &s3types.CORSConfiguration{CORSRules: []s3types.CORSRule{{
			AllowedOrigins: []string{"https://app.example"},
			AllowedMethods: []string{"GET", "PUT"},
			AllowedHeaders: []string{"*"},
		}}},
	}); err != nil {
		t.Fatalf("PutBucketCors: %v", err)
	}
	if _, err := c.GetBucketCors(ctx, &awss3.GetBucketCorsInput{Bucket: aws.String("corsb")}); err != nil {
		t.Fatalf("GetBucketCors: %v", err)
	}

	// A real preflight OPTIONS request should be answered with CORS headers.
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/corsb/obj", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("preflight missing Access-Control-Allow-Origin (status %d)", resp.StatusCode)
	}

	if _, err := c.DeleteBucketCors(ctx, &awss3.DeleteBucketCorsInput{Bucket: aws.String("corsb")}); err != nil {
		t.Fatalf("DeleteBucketCors: %v", err)
	}
}

func TestSDKLifecycleWebsiteEncryption(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("cfgb")})

	if _, err := c.PutBucketLifecycleConfiguration(ctx, &awss3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String("cfgb"),
		LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{Rules: []s3types.LifecycleRule{{
			ID: aws.String("expire"), Status: s3types.ExpirationStatusEnabled,
			Filter:     &s3types.LifecycleRuleFilter{Prefix: aws.String("tmp/")},
			Expiration: &s3types.LifecycleExpiration{Days: aws.Int32(7)},
		}}},
	}); err != nil {
		t.Fatalf("PutBucketLifecycleConfiguration: %v", err)
	}
	if _, err := c.GetBucketLifecycleConfiguration(ctx, &awss3.GetBucketLifecycleConfigurationInput{Bucket: aws.String("cfgb")}); err != nil {
		t.Fatalf("GetBucketLifecycleConfiguration: %v", err)
	}

	if _, err := c.PutBucketWebsite(ctx, &awss3.PutBucketWebsiteInput{
		Bucket:               aws.String("cfgb"),
		WebsiteConfiguration: &s3types.WebsiteConfiguration{IndexDocument: &s3types.IndexDocument{Suffix: aws.String("index.html")}},
	}); err != nil {
		t.Fatalf("PutBucketWebsite: %v", err)
	}
	if _, err := c.GetBucketWebsite(ctx, &awss3.GetBucketWebsiteInput{Bucket: aws.String("cfgb")}); err != nil {
		t.Fatalf("GetBucketWebsite: %v", err)
	}
	if _, err := c.DeleteBucketWebsite(ctx, &awss3.DeleteBucketWebsiteInput{Bucket: aws.String("cfgb")}); err != nil {
		t.Fatalf("DeleteBucketWebsite: %v", err)
	}

	if _, err := c.PutBucketEncryption(ctx, &awss3.PutBucketEncryptionInput{
		Bucket: aws.String("cfgb"),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{Rules: []s3types.ServerSideEncryptionRule{{
			ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{SSEAlgorithm: s3types.ServerSideEncryptionAes256},
		}}},
	}); err != nil {
		t.Fatalf("PutBucketEncryption: %v", err)
	}
	if _, err := c.GetBucketEncryption(ctx, &awss3.GetBucketEncryptionInput{Bucket: aws.String("cfgb")}); err != nil {
		t.Fatalf("GetBucketEncryption: %v", err)
	}
}

func TestSDKObjectACLAndPartCopy(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("copyb")})
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("copyb"), Key: aws.String("src"), Body: strings.NewReader(strings.Repeat("s", 5<<20))})

	// Object ACL round-trip.
	if _, err := c.PutObjectAcl(ctx, &awss3.PutObjectAclInput{Bucket: aws.String("copyb"), Key: aws.String("src"), ACL: s3types.ObjectCannedACLPrivate}); err != nil {
		t.Fatalf("PutObjectAcl: %v", err)
	}
	if _, err := c.GetObjectAcl(ctx, &awss3.GetObjectAclInput{Bucket: aws.String("copyb"), Key: aws.String("src")}); err != nil {
		t.Fatalf("GetObjectAcl: %v", err)
	}

	// UploadPartCopy from the source object.
	cr, err := c.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String("copyb"), Key: aws.String("dst")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
		Bucket: aws.String("copyb"), Key: aws.String("dst"), UploadId: cr.UploadId,
		PartNumber: aws.Int32(1), CopySource: aws.String("copyb/src"),
	}); err != nil {
		t.Fatalf("UploadPartCopy: %v", err)
	}
}
