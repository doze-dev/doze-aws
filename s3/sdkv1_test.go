// SDK v1 contract tests: the legacy aws-sdk-go (v1) S3 client, which produces
// the other half of the upload matrix — signed aws-chunked bodies
// (STREAMING-AWS4-HMAC-SHA256-PAYLOAD) — plus presigned URLs.
package s3_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	s3v1 "github.com/aws/aws-sdk-go/service/s3"

	"github.com/doze-dev/doze-aws/awsident"
)

func s3V1Client(t *testing.T, url string) *s3v1.S3 {
	t.Helper()
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(url).
		WithS3ForcePathStyle(true).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return s3v1.New(sess)
}

func TestSDKV1RoundTrip(t *testing.T) {
	c := s3V1Client(t, startS3(t).URL)

	if _, err := c.CreateBucket(&s3v1.CreateBucketInput{Bucket: awsv1.String("legacy")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// The v1 SDK signs the payload; PutObject arrives with a plain body and
	// x-amz-content-sha256 (or signed-chunked for streams).
	if _, err := c.PutObject(&s3v1.PutObjectInput{
		Bucket: awsv1.String("legacy"), Key: awsv1.String("k"),
		Body: strings.NewReader("from the v1 sdk"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	got, err := c.GetObject(&s3v1.GetObjectInput{Bucket: awsv1.String("legacy"), Key: awsv1.String("k")})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if string(data) != "from the v1 sdk" {
		t.Fatalf("body = %q", data)
	}

	list, err := c.ListObjectsV2(&s3v1.ListObjectsV2Input{Bucket: awsv1.String("legacy")})
	if err != nil || len(list.Contents) != 1 {
		t.Fatalf("ListObjectsV2: %v, %d", err, len(list.Contents))
	}

	// Coded error envelope through the v1 deserializer.
	_, err = c.GetObject(&s3v1.GetObjectInput{Bucket: awsv1.String("legacy"), Key: awsv1.String("nope")})
	type coder interface{ Code() string }
	if ce, ok := err.(coder); !ok || ce.Code() != "NoSuchKey" {
		t.Fatalf("missing key error = %v", err)
	}
}

func TestSDKV1PresignedGetAndPut(t *testing.T) {
	url := startS3(t).URL
	c := s3V1Client(t, url)

	c.CreateBucket(&s3v1.CreateBucketInput{Bucket: awsv1.String("presign")})

	// Presigned PUT: an anonymous HTTP client uploads through the signed URL.
	putReq, _ := c.PutObjectRequest(&s3v1.PutObjectInput{
		Bucket: awsv1.String("presign"), Key: awsv1.String("upload"),
	})
	putURL, err := putReq.Presign(5 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	httpReq, _ := http.NewRequest("PUT", putURL, strings.NewReader("presigned payload"))
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("presigned PUT: %v %v", err, resp)
	}
	resp.Body.Close()

	// Presigned GET round-trips the content.
	getReq, _ := c.GetObjectRequest(&s3v1.GetObjectInput{
		Bucket: awsv1.String("presign"), Key: awsv1.String("upload"),
	})
	getURL, err := getReq.Presign(5 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(getURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "presigned payload" {
		t.Fatalf("presigned GET: %d %q", resp.StatusCode, body)
	}

	// Expired presigned URLs are refused with AccessDenied.
	shortReq, _ := c.GetObjectRequest(&s3v1.GetObjectInput{
		Bucket: awsv1.String("presign"), Key: awsv1.String("upload"),
	})
	shortURL, err := shortReq.Presign(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	resp, err = http.Get(shortURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("expired presigned GET status = %d, want 403", resp.StatusCode)
	}
}
