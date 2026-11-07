package s3_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/s3"
)

// Inside doze the instance's URL is its name (http://aws.shop.doze) and the
// suffix is that name. An SDK given that URL, with its default virtual-hosted
// addressing, puts the bucket in front of it: photos.aws.shop.doze. That has
// to read as the bucket "photos", not as a path-style request to the bare
// host, or every default client writes to the wrong place.
func TestABucketInFrontOfTheInstanceNameIsVirtualHosted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	const name = "aws.shop.doze"
	srv, err := s3.New(s3.Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Suffix: name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Every host the SDK makes up dials the test server, the way the name and
	// everything under it reach the instance through doze.
	var hosts []string
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ts.Listener.Addr().String())
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		return (&http.Transport{DialContext: dial}).RoundTrip(r)
	})}
	c := awss3.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
		HTTPClient:  client,
	}, func(o *awss3.Options) { o.BaseEndpoint = aws.String("http://" + name) })

	ctx := context.Background()
	if _, err := c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("photos")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("photos"), Key: aws.String("cat.txt"), Body: strings.NewReader("meow")}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	got, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("cat.txt")})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	body, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if string(body) != "meow" {
		t.Fatalf("GetObject = %q, want meow", body)
	}
	for _, h := range hosts {
		if h != "photos."+name {
			t.Fatalf("the SDK addressed %s, want photos.%s (this test is about virtual-hosted calls)", h, name)
		}
	}

	// The bucket is the one the SDK named, and the bare name still lists them.
	out, err := c.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	if len(out.Buckets) != 1 || aws.ToString(out.Buckets[0].Name) != "photos" {
		t.Fatalf("buckets = %+v, want just photos", out.Buckets)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
