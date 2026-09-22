package s3_test

// SDK breadth over the bucket and object CONFIGURATION surface: policy, ACL,
// tagging, versioning, object lock, CORS, lifecycle, website, encryption and
// multipart. sdk_test.go covers the data path (put, get, copy, list); this is
// everything you can configure about where that data lives.
//
// This was coverage_test.go plus coverage2_test.go. The "2" was a size split,
// not a subject split, so it said nothing about what was in either.

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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

	// Retention (distinct from legal hold): set GOVERNANCE until a future date,
	// read it back, then prove shortening without bypass is refused.
	until := time.Unix(4102444800, 0).UTC() // 2100-01-01
	if _, err := c.PutObjectRetention(ctx, &awss3.PutObjectRetentionInput{
		Bucket: aws.String("bklock"), Key: aws.String("k"),
		Retention: &s3types.ObjectLockRetention{Mode: s3types.ObjectLockRetentionModeGovernance, RetainUntilDate: aws.Time(until)},
	}); err != nil {
		t.Fatalf("PutObjectRetention: %v", err)
	}
	ret, err := c.GetObjectRetention(ctx, &awss3.GetObjectRetentionInput{Bucket: aws.String("bklock"), Key: aws.String("k")})
	if err != nil || ret.Retention.Mode != s3types.ObjectLockRetentionModeGovernance {
		t.Fatalf("GetObjectRetention = %v err=%v", ret, err)
	}
	// Shortening GOVERNANCE retention without the bypass header must be denied.
	if _, err := c.PutObjectRetention(ctx, &awss3.PutObjectRetentionInput{
		Bucket: aws.String("bklock"), Key: aws.String("k"),
		Retention: &s3types.ObjectLockRetention{Mode: s3types.ObjectLockRetentionModeGovernance, RetainUntilDate: aws.Time(time.Unix(1704067200, 0).UTC())},
	}); err == nil {
		t.Fatal("shortening retention without bypass should be denied")
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

// TestDeleteConfigKeepsBucket guards against the DELETE dispatch falling through
// to deleteBucket for subresources it doesn't explicitly list: a config-cleanup
// call like DeleteBucketEncryption must remove only the config, never the bucket.
func TestDeleteConfigKeepsBucket(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("keepb")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("keepb"), Key: aws.String("k"), Body: strings.NewReader("v"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if _, err := c.DeleteBucketEncryption(ctx, &awss3.DeleteBucketEncryptionInput{Bucket: aws.String("keepb")}); err != nil {
		t.Fatalf("DeleteBucketEncryption: %v", err)
	}
	// The bucket and its object must survive.
	if _, err := c.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String("keepb")}); err != nil {
		t.Fatalf("bucket gone after DeleteBucketEncryption: %v", err)
	}
	if _, err := c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("keepb"), Key: aws.String("k")}); err != nil {
		t.Fatalf("object gone after DeleteBucketEncryption: %v", err)
	}
	// A no-subresource DELETE on a non-empty bucket must still be rejected.
	if _, err := c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String("keepb")}); err == nil {
		t.Fatal("DeleteBucket on non-empty bucket should fail with BucketNotEmpty")
	}
}

// TestCopyToSelfRejected: copying an object onto itself without changing
// metadata is an InvalidRequest, but a self-copy with REPLACE is allowed.
func TestCopyToSelfRejected(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("selfcp")})
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("selfcp"), Key: aws.String("k"), Body: strings.NewReader("v")})

	if _, err := c.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("selfcp"), Key: aws.String("k"), CopySource: aws.String("/selfcp/k"),
	}); err == nil {
		t.Fatal("self-copy without REPLACE should be InvalidRequest")
	}
	// With REPLACE it's allowed.
	if _, err := c.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("selfcp"), Key: aws.String("k"), CopySource: aws.String("/selfcp/k"),
		MetadataDirective: s3types.MetadataDirectiveReplace,
		Metadata:          map[string]string{"x": "y"},
	}); err != nil {
		t.Fatalf("self-copy with REPLACE: %v", err)
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
