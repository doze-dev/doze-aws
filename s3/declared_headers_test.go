package s3_test

// Headers declared on upload have to come back on read. Nothing local
// enciphers an object or serves a redirect, but an object that accepts the
// header and reports nothing is drift for anyone checking encryption — and a
// broken static site for anyone relying on the redirect.

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestDeclaredObjectHeadersReadBack(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("hdrs")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("hdrs"), Key: aws.String("o.txt"),
		Body:                    strings.NewReader("hello"),
		WebsiteRedirectLocation: aws.String("/elsewhere"),
		ServerSideEncryption:    s3types.ServerSideEncryptionAwsKms,
		SSEKMSKeyId:             aws.String("alias/aws/s3"),
		BucketKeyEnabled:        aws.Bool(true),
		Tagging:                 aws.String("env=dev&team=core"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	head, err := c.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String("hdrs"), Key: aws.String("o.txt"),
	})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if aws.ToString(head.WebsiteRedirectLocation) != "/elsewhere" {
		t.Errorf("redirect = %q", aws.ToString(head.WebsiteRedirectLocation))
	}
	if head.ServerSideEncryption != s3types.ServerSideEncryptionAwsKms {
		t.Errorf("SSE = %q, want aws:kms", head.ServerSideEncryption)
	}
	if aws.ToString(head.SSEKMSKeyId) != "alias/aws/s3" {
		t.Errorf("SSE key = %q", aws.ToString(head.SSEKMSKeyId))
	}
	// The bucket-key flag has to be a lowercase boolean or no SDK parses it.
	if !aws.ToBool(head.BucketKeyEnabled) {
		t.Error("BucketKeyEnabled did not parse as true")
	}

	// TagCount is a GetObject field rather than a HeadObject one.
	got, err := c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("hdrs"), Key: aws.String("o.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	if aws.ToInt32(got.TagCount) != 2 {
		t.Errorf("TagCount = %v, want 2", got.TagCount)
	}
}

// An object that declared none of them must not invent any.
func TestUndeclaredObjectHeadersStayEmpty(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bare")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("bare"), Key: aws.String("o.txt"), Body: strings.NewReader("x"),
	}); err != nil {
		t.Fatal(err)
	}
	head, err := c.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String("bare"), Key: aws.String("o.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(head.WebsiteRedirectLocation) != "" || head.ServerSideEncryption != "" {
		t.Errorf("plain object reports redirect=%q sse=%q",
			aws.ToString(head.WebsiteRedirectLocation), head.ServerSideEncryption)
	}
}
