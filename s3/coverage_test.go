package s3_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestSDKBucketOps(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)

	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bkalpha")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bkbeta")}); err != nil {
		t.Fatal(err)
	}
	// ListBuckets
	lb, err := c.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil || len(lb.Buckets) != 2 {
		t.Fatalf("ListBuckets = %d err=%v", len(lb.Buckets), err)
	}
	// HeadBucket (exists + missing)
	if _, err := c.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String("bkalpha")}); err != nil {
		t.Fatalf("HeadBucket: %v", err)
	}
	if _, err := c.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String("nope")}); err == nil {
		t.Fatal("HeadBucket(missing) should error")
	}
	// GetBucketLocation
	loc, err := c.GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: aws.String("bkalpha")})
	if err != nil {
		t.Fatalf("GetBucketLocation: %v", err)
	}
	_ = loc
	// DeleteBucket
	if _, err := c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String("bkbeta")}); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
}

func TestSDKVersioningAndTagging(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bkvers")})

	if _, err := c.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String("bkvers"),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	gv, err := c.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: aws.String("bkvers")})
	if err != nil || gv.Status != s3types.BucketVersioningStatusEnabled {
		t.Fatalf("GetBucketVersioning = %v err=%v", gv.Status, err)
	}

	// Bucket tagging.
	if _, err := c.PutBucketTagging(ctx, &awss3.PutBucketTaggingInput{
		Bucket:  aws.String("bkvers"),
		Tagging: &s3types.Tagging{TagSet: []s3types.Tag{{Key: aws.String("env"), Value: aws.String("dev")}}},
	}); err != nil {
		t.Fatalf("PutBucketTagging: %v", err)
	}
	gt, err := c.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{Bucket: aws.String("bkvers")})
	if err != nil || len(gt.TagSet) != 1 || aws.ToString(gt.TagSet[0].Value) != "dev" {
		t.Fatalf("GetBucketTagging = %+v err=%v", gt.TagSet, err)
	}
}

func TestSDKPolicyAndACL(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bkpol")})

	pol := `{"Version":"2012-10-17","Statement":[]}`
	if _, err := c.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{Bucket: aws.String("bkpol"), Policy: aws.String(pol)}); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	gp, err := c.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: aws.String("bkpol")})
	if err != nil || !strings.Contains(aws.ToString(gp.Policy), "2012-10-17") {
		t.Fatalf("GetBucketPolicy = %v err=%v", aws.ToString(gp.Policy), err)
	}

	if _, err := c.PutBucketAcl(ctx, &awss3.PutBucketAclInput{Bucket: aws.String("bkpol"), ACL: s3types.BucketCannedACLPrivate}); err != nil {
		t.Fatalf("PutBucketAcl: %v", err)
	}
	if _, err := c.GetBucketAcl(ctx, &awss3.GetBucketAclInput{Bucket: aws.String("bkpol")}); err != nil {
		t.Fatalf("GetBucketAcl: %v", err)
	}
}

func TestSDKObjectTaggingAndAttributes(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bkobj")})
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("bkobj"), Key: aws.String("k"), Body: strings.NewReader("hello")})

	if _, err := c.PutObjectTagging(ctx, &awss3.PutObjectTaggingInput{
		Bucket: aws.String("bkobj"), Key: aws.String("k"),
		Tagging: &s3types.Tagging{TagSet: []s3types.Tag{{Key: aws.String("a"), Value: aws.String("b")}}},
	}); err != nil {
		t.Fatalf("PutObjectTagging: %v", err)
	}
	got, err := c.GetObjectTagging(ctx, &awss3.GetObjectTaggingInput{Bucket: aws.String("bkobj"), Key: aws.String("k")})
	if err != nil || len(got.TagSet) != 1 {
		t.Fatalf("GetObjectTagging = %+v err=%v", got.TagSet, err)
	}
	if _, err := c.DeleteObjectTagging(ctx, &awss3.DeleteObjectTaggingInput{Bucket: aws.String("bkobj"), Key: aws.String("k")}); err != nil {
		t.Fatalf("DeleteObjectTagging: %v", err)
	}

	attrs, err := c.GetObjectAttributes(ctx, &awss3.GetObjectAttributesInput{
		Bucket: aws.String("bkobj"), Key: aws.String("k"),
		ObjectAttributes: []s3types.ObjectAttributes{s3types.ObjectAttributesObjectSize, s3types.ObjectAttributesEtag},
	})
	if err != nil || aws.ToInt64(attrs.ObjectSize) != 5 {
		t.Fatalf("GetObjectAttributes size = %d err=%v", aws.ToInt64(attrs.ObjectSize), err)
	}
}

func TestSDKObjectLock(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String("bklock"), ObjectLockEnabledForBucket: aws.Bool(true),
	}); err != nil {
		t.Fatalf("CreateBucket(lock): %v", err)
	}
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("bklock"), Key: aws.String("k"), Body: strings.NewReader("x")})

	// Legal hold on/off.
	if _, err := c.PutObjectLegalHold(ctx, &awss3.PutObjectLegalHoldInput{
		Bucket: aws.String("bklock"), Key: aws.String("k"),
		LegalHold: &s3types.ObjectLockLegalHold{Status: s3types.ObjectLockLegalHoldStatusOn},
	}); err != nil {
		t.Fatalf("PutObjectLegalHold: %v", err)
	}
	lh, err := c.GetObjectLegalHold(ctx, &awss3.GetObjectLegalHoldInput{Bucket: aws.String("bklock"), Key: aws.String("k")})
	if err != nil || lh.LegalHold.Status != s3types.ObjectLockLegalHoldStatusOn {
		t.Fatalf("GetObjectLegalHold = %v err=%v", lh, err)
	}
}

func TestSDKMultipartFull(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bkmp")})

	cr, err := c.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String("bkmp"), Key: aws.String("big")})
	if err != nil {
		t.Fatal(err)
	}
	// ListMultipartUploads shows the in-progress upload.
	lm, err := c.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{Bucket: aws.String("bkmp")})
	if err != nil || len(lm.Uploads) != 1 {
		t.Fatalf("ListMultipartUploads = %d err=%v", len(lm.Uploads), err)
	}
	// One 5MB+ part.
	part := bytes.Repeat([]byte("a"), 5<<20)
	up, err := c.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket: aws.String("bkmp"), Key: aws.String("big"), UploadId: cr.UploadId,
		PartNumber: aws.Int32(1), Body: bytes.NewReader(part),
	})
	if err != nil {
		t.Fatalf("UploadPart: %v", err)
	}
	// ListParts.
	lp, err := c.ListParts(ctx, &awss3.ListPartsInput{Bucket: aws.String("bkmp"), Key: aws.String("big"), UploadId: cr.UploadId})
	if err != nil || len(lp.Parts) != 1 {
		t.Fatalf("ListParts = %d err=%v", len(lp.Parts), err)
	}
	// Complete.
	if _, err := c.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String("bkmp"), Key: aws.String("big"), UploadId: cr.UploadId,
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: []s3types.CompletedPart{{ETag: up.ETag, PartNumber: aws.Int32(1)}}},
	}); err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	// A second upload we abort.
	cr2, _ := c.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String("bkmp"), Key: aws.String("gone")})
	if _, err := c.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{Bucket: aws.String("bkmp"), Key: aws.String("gone"), UploadId: cr2.UploadId}); err != nil {
		t.Fatalf("AbortMultipartUpload: %v", err)
	}
}
