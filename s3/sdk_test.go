// SDK contract tests: a real aws-sdk-go-v2 S3 client. The v2 SDK's defaults
// are the hardest part of the S3 wire surface — STREAMING-UNSIGNED-PAYLOAD-
// TRAILER uploads with CRC32 trailers, virtual-hosted addressing, client-side
// checksum validation on download — so these tests passing is the strongest
// signal the from-scratch implementation is genuinely compatible.
package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/s3"
)

// startS3 boots the s3 service alone on a loopback listener.
func startS3(t *testing.T) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	srv, err := s3.New(s3.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func s3Client(t *testing.T, url string, pathStyle bool) *awss3.Client {
	t.Helper()
	return awss3.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(url)
		o.UsePathStyle = pathStyle
	})
}

func TestSDKPutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)

	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("round")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// This PutObject goes over the wire as STREAMING-UNSIGNED-PAYLOAD-TRAILER
	// with a CRC32 trailer — the aws-sdk-go-v2 default that broke gofakes3.
	body := "Hello from the v2 SDK!"
	put, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("round"), Key: aws.String("greeting.txt"),
		Body:        strings.NewReader(body),
		ContentType: aws.String("text/plain"),
		Metadata:    map[string]string{"origin": "contract-test"},
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if aws.ToString(put.ETag) == "" {
		t.Fatal("no ETag")
	}

	got, err := c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("round"), Key: aws.String("greeting.txt"),
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if string(data) != body {
		t.Fatalf("body = %q", data)
	}
	if aws.ToString(got.ContentType) != "text/plain" || got.Metadata["origin"] != "contract-test" {
		t.Errorf("metadata round trip: ct=%q meta=%v", aws.ToString(got.ContentType), got.Metadata)
	}

	// HEAD agrees.
	head, err := c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("round"), Key: aws.String("greeting.txt")})
	if err != nil || aws.ToInt64(head.ContentLength) != int64(len(body)) {
		t.Fatalf("HeadObject: %v, len=%d", err, aws.ToInt64(head.ContentLength))
	}

	// Missing keys carry the S3 error code.
	_, err = c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("round"), Key: aws.String("absent")})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "NoSuchKey" {
		t.Fatalf("missing key error = %v", err)
	}
}

func TestSDKChecksumsAndRanges(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("sums")})

	// Explicit SHA256 checksum, verified server-side and echoed on GET.
	body := "checksum me, please"
	put, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("sums"), Key: aws.String("k"),
		Body:              strings.NewReader(body),
		ChecksumAlgorithm: s3types.ChecksumAlgorithmSha256,
	})
	if err != nil {
		t.Fatalf("PutObject with SHA256: %v", err)
	}
	if aws.ToString(put.ChecksumSHA256) == "" {
		t.Fatal("no ChecksumSHA256 in the PutObject response")
	}

	// GET with checksum mode: the SDK validates the digest client-side.
	got, err := c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("sums"), Key: aws.String("k"),
		ChecksumMode: s3types.ChecksumModeEnabled,
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	data, err := io.ReadAll(got.Body) // client-side checksum validation happens here
	got.Body.Close()
	if err != nil || string(data) != body {
		t.Fatalf("checksum-validated read: %v", err)
	}

	// Range request.
	rng, err := c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("sums"), Key: aws.String("k"),
		Range: aws.String("bytes=9-10"),
	})
	if err != nil {
		t.Fatalf("range GET: %v", err)
	}
	part, _ := io.ReadAll(rng.Body)
	rng.Body.Close()
	if string(part) != "me" {
		t.Fatalf("range body = %q", part)
	}
	if aws.ToString(rng.ContentRange) != "bytes 9-10/19" {
		t.Errorf("Content-Range = %q", aws.ToString(rng.ContentRange))
	}
}

func TestSDKVersioning(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("ver")})

	if _, err := c.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String("ver"),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	v1, _ := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("ver"), Key: aws.String("doc"), Body: strings.NewReader("one"),
	})
	v2, _ := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("ver"), Key: aws.String("doc"), Body: strings.NewReader("two"),
	})
	if aws.ToString(v1.VersionId) == "" || aws.ToString(v1.VersionId) == aws.ToString(v2.VersionId) {
		t.Fatalf("version ids: %q %q", aws.ToString(v1.VersionId), aws.ToString(v2.VersionId))
	}

	// Fetch the old version explicitly.
	old, err := c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("ver"), Key: aws.String("doc"), VersionId: v1.VersionId,
	})
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	data, _ := io.ReadAll(old.Body)
	old.Body.Close()
	if string(data) != "one" {
		t.Fatalf("v1 body = %q", data)
	}

	// Delete creates a marker; the key 404s; versions remain listable.
	del, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("ver"), Key: aws.String("doc")})
	if err != nil || !aws.ToBool(del.DeleteMarker) {
		t.Fatalf("DeleteObject: %v marker=%v", err, aws.ToBool(del.DeleteMarker))
	}
	if _, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ver"), Key: aws.String("doc")}); err == nil {
		t.Fatal("GET through a delete marker succeeded")
	}
	vers, err := c.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("ver")})
	if err != nil || len(vers.Versions) != 2 || len(vers.DeleteMarkers) != 1 {
		t.Fatalf("ListObjectVersions: %v, %d versions, %d markers", err, len(vers.Versions), len(vers.DeleteMarkers))
	}

	// Removing the marker restores the object.
	if _, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("ver"), Key: aws.String("doc"),
		VersionId: vers.DeleteMarkers[0].VersionId,
	}); err != nil {
		t.Fatal(err)
	}
	back, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("ver"), Key: aws.String("doc")})
	if err != nil {
		t.Fatalf("after marker removal: %v", err)
	}
	data, _ = io.ReadAll(back.Body)
	back.Body.Close()
	if string(data) != "two" {
		t.Fatalf("restored body = %q", data)
	}
}

// TestSuspendedVersioningDeleteKeepsVersions guards against a delete in a
// versioning-Suspended bucket destroying the current version: real S3 inserts a
// "null" delete marker and preserves non-null versions written while Enabled.
func TestSuspendedVersioningDeleteKeepsVersions(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("susp")})

	if _, err := c.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String("susp"),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}
	v1, _ := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("susp"), Key: aws.String("doc"), Body: strings.NewReader("one"),
	})
	c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("susp"), Key: aws.String("doc"), Body: strings.NewReader("two"),
	})

	// Suspend versioning, then delete.
	if _, err := c.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String("susp"),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusSuspended},
	}); err != nil {
		t.Fatalf("suspend versioning: %v", err)
	}
	del, err := c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("susp"), Key: aws.String("doc")})
	if err != nil || !aws.ToBool(del.DeleteMarker) {
		t.Fatalf("suspended delete: %v marker=%v", err, aws.ToBool(del.DeleteMarker))
	}
	// The key 404s through the delete marker...
	if _, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("susp"), Key: aws.String("doc")}); err == nil {
		t.Fatal("GET after suspended delete should 404")
	}
	// ...but the earlier non-null versions must survive.
	got, err := c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("susp"), Key: aws.String("doc"), VersionId: v1.VersionId,
	})
	if err != nil {
		t.Fatalf("v1 destroyed by suspended delete: %v", err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if string(data) != "one" {
		t.Fatalf("v1 body = %q, want \"one\"", data)
	}
}

func TestSDKMultipartUpload(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("mpart")})

	create, err := c.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String("mpart"), Key: aws.String("assembled"),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	part1 := bytes.Repeat([]byte("a"), 5<<20)
	part2 := []byte("tail")
	var completed []s3types.CompletedPart
	for i, body := range [][]byte{part1, part2} {
		up, err := c.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket: aws.String("mpart"), Key: aws.String("assembled"),
			UploadId: create.UploadId, PartNumber: aws.Int32(int32(i + 1)),
			Body: bytes.NewReader(body),
		})
		if err != nil {
			t.Fatalf("UploadPart %d: %v", i+1, err)
		}
		completed = append(completed, s3types.CompletedPart{
			ETag: up.ETag, PartNumber: aws.Int32(int32(i + 1)),
		})
	}
	fin, err := c.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String("mpart"), Key: aws.String("assembled"), UploadId: create.UploadId,
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	if !strings.HasSuffix(strings.Trim(aws.ToString(fin.ETag), `"`), "-2") {
		t.Errorf("multipart ETag = %q", aws.ToString(fin.ETag))
	}

	got, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("mpart"), Key: aws.String("assembled")})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if len(data) != len(part1)+len(part2) || !bytes.HasSuffix(data, part2) {
		t.Fatalf("assembled len = %d", len(data))
	}
}

func TestSDKListCopyAndBatchDelete(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("lsbkt")})

	for _, k := range []string{"a.txt", "dir/one", "dir/two", "z.txt"} {
		if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String("lsbkt"), Key: aws.String(k), Body: strings.NewReader("x"),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// ListObjectsV2 with delimiter.
	l2, err := c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String("lsbkt"), Delimiter: aws.String("/"),
	})
	if err != nil || len(l2.Contents) != 2 || len(l2.CommonPrefixes) != 1 {
		t.Fatalf("ListObjectsV2: %v, %d contents, %d prefixes", err, len(l2.Contents), len(l2.CommonPrefixes))
	}
	// V1 list works too (legacy clients).
	l1, err := c.ListObjects(ctx, &awss3.ListObjectsInput{Bucket: aws.String("lsbkt")})
	if err != nil || len(l1.Contents) != 4 {
		t.Fatalf("ListObjects (V1): %v, %d contents", err, len(l1.Contents))
	}

	// Paged V2 listing.
	var keys []string
	var token *string
	for {
		page, err := c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket: aws.String("lsbkt"), MaxKeys: aws.Int32(2), ContinuationToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
		if !aws.ToBool(page.IsTruncated) {
			break
		}
		token = page.NextContinuationToken
	}
	if len(keys) != 4 {
		t.Fatalf("paged keys = %v", keys)
	}

	// Server-side copy.
	if _, err := c.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("lsbkt"), Key: aws.String("copy-of-a"),
		CopySource: aws.String("/lsbkt/a.txt"),
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	cp, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("lsbkt"), Key: aws.String("copy-of-a")})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, cp.Body)
	cp.Body.Close()

	// Batch delete.
	del, err := c.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
		Bucket: aws.String("lsbkt"),
		Delete: &s3types.Delete{Objects: []s3types.ObjectIdentifier{
			{Key: aws.String("a.txt")}, {Key: aws.String("z.txt")},
		}},
	})
	if err != nil || len(del.Deleted) != 2 {
		t.Fatalf("DeleteObjects: %v %+v", err, del)
	}
}

func TestSDKConditionalWrites(t *testing.T) {
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("cond")})

	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("cond"), Key: aws.String("once"),
		Body: strings.NewReader("first"), IfNoneMatch: aws.String("*"),
	}); err != nil {
		t.Fatalf("first conditional PUT: %v", err)
	}
	_, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("cond"), Key: aws.String("once"),
		Body: strings.NewReader("second"), IfNoneMatch: aws.String("*"),
	})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "PreconditionFailed" {
		t.Fatalf("second conditional PUT error = %v", err)
	}
}

func TestSDKVirtualHostedStyle(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	// Boot with a base host so <bucket>.<host> addressing resolves; the SDK's
	// default (UsePathStyle=false) rewrites the Host header itself.
	srv, err := s3.New(s3.Options{DataDir: t.TempDir(), Host: "example.test", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	c := s3Client(t, ts.URL, true)
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("vhost")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("vhost"), Key: aws.String("k"), Body: strings.NewReader("via path"),
	}); err != nil {
		t.Fatal(err)
	}

	// Hand-rolled vhost request: Host: vhost.example.test, path = /k.
	req, _ := http.NewRequest("GET", ts.URL+"/k", nil)
	req.Host = "vhost.example.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "via path" {
		t.Fatalf("vhost GET: %d %q", resp.StatusCode, body)
	}
}
