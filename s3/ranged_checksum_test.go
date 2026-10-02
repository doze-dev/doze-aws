package s3_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// A ranged read does not carry the whole object's checksum.
//
// Found by boto3, not by this suite: botocore asks for the checksum on every
// GetObject and validates what comes back against the bytes it received, so a
// 206 answered with the full-object CRC raised FlexibleChecksumError on every
// ranged read. The Go SDK only validates when ChecksumMode is set, and the one
// test here that used a Range did not set it.
func TestRangedReadCarriesNoFullObjectChecksum(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	ctx := context.Background()
	c := s3Client(t, startS3(t).URL, true)
	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("ranges")})
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("ranges"), Key: aws.String("k"),
		Body:              strings.NewReader("hello world"),
		ChecksumAlgorithm: s3types.ChecksumAlgorithmCrc32,
	}); err != nil {
		t.Fatal(err)
	}
	get := func(rng string) *awss3.GetObjectOutput {
		t.Helper()
		in := &awss3.GetObjectInput{
			Bucket: aws.String("ranges"), Key: aws.String("k"),
			ChecksumMode: s3types.ChecksumModeEnabled,
		}
		if rng != "" {
			in.Range = aws.String(rng)
		}
		out, err := c.GetObject(ctx, in)
		if err != nil {
			t.Fatalf("GetObject %q: %v", rng, err)
		}
		return out
	}

	whole := get("")
	if aws.ToString(whole.ChecksumCRC32) == "" {
		t.Error("a whole read lost its checksum")
	}
	whole.Body.Close()

	for _, rng := range []string{"bytes=1-3", "bytes=-5", "bytes=6-"} {
		part := get(rng)
		data, err := io.ReadAll(part.Body) // the SDK validates here, if there is anything to validate
		part.Body.Close()
		if err != nil {
			t.Errorf("%s: reading the body: %v", rng, err)
		}
		if part.ChecksumCRC32 != nil {
			t.Errorf("%s: carried the full-object checksum %q with %d bytes of it",
				rng, aws.ToString(part.ChecksumCRC32), len(data))
		}
	}

	// A range that cannot be satisfied is an error and only an error: it used
	// to go out with the object's ETag and checksum attached.
	_, err := c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("ranges"), Key: aws.String("k"),
		Range: aws.String("bytes=50-60"), ChecksumMode: s3types.ChecksumModeEnabled,
	})
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "InvalidRange" {
		t.Fatalf("range past the end: got %v, want InvalidRange", err)
	}
}
