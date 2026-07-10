// Feature tests for the functional bucket configs: CORS preflight evaluation,
// website index/error serving, and lifecycle expiration under a fake clock.
package s3_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/doze-dev/doze-aws/s3"
)

func TestCORSPreflight(t *testing.T) {
	ctx := context.Background()
	ts := startS3(t)
	c := s3Client(t, ts.URL, true)

	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("webby")})
	if _, err := c.PutBucketCors(ctx, &awss3.PutBucketCorsInput{
		Bucket: aws.String("webby"),
		CORSConfiguration: &s3types.CORSConfiguration{CORSRules: []s3types.CORSRule{{
			AllowedOrigins: []string{"https://app.example.com"},
			AllowedMethods: []string{"GET", "PUT"},
			AllowedHeaders: []string{"*"},
			ExposeHeaders:  []string{"ETag"},
			MaxAgeSeconds:  aws.Int32(600),
		}}},
	}); err != nil {
		t.Fatalf("PutBucketCors: %v", err)
	}

	// Matching preflight is allowed.
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/webby/some-key", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("preflight status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("Max-Age = %q", got)
	}

	// Wrong origin is refused.
	req, _ = http.NewRequest("OPTIONS", ts.URL+"/webby/some-key", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("evil preflight status = %d", resp.StatusCode)
	}

	// Method not in the rule is refused.
	req, _ = http.NewRequest("OPTIONS", ts.URL+"/webby/some-key", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DELETE preflight status = %d", resp.StatusCode)
	}
}

func TestWebsiteServing(t *testing.T) {
	ctx := context.Background()
	ts := startS3(t)
	c := s3Client(t, ts.URL, true)

	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("site")})
	if _, err := c.PutBucketWebsite(ctx, &awss3.PutBucketWebsiteInput{
		Bucket: aws.String("site"),
		WebsiteConfiguration: &s3types.WebsiteConfiguration{
			IndexDocument: &s3types.IndexDocument{Suffix: aws.String("index.html")},
			ErrorDocument: &s3types.ErrorDocument{Key: aws.String("404.html")},
		},
	}); err != nil {
		t.Fatalf("PutBucketWebsite: %v", err)
	}
	for key, body := range map[string]string{
		"index.html":      "<h1>home</h1>",
		"docs/index.html": "<h1>docs</h1>",
		"404.html":        "<h1>lost</h1>",
	} {
		if _, err := c.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String("site"), Key: aws.String(key),
			Body: strings.NewReader(body), ContentType: aws.String("text/html"),
		}); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path string) (int, string) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Directory request serves the index document.
	if code, body := get("/site/docs/"); code != 200 || body != "<h1>docs</h1>" {
		t.Errorf("docs/ -> %d %q", code, body)
	}
	// Missing keys serve the error document with 404.
	if code, body := get("/site/missing-page"); code != 404 || body != "<h1>lost</h1>" {
		t.Errorf("missing -> %d %q", code, body)
	}
}

func TestLifecycleExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping feature test in -short mode")
	}
	ctx := context.Background()

	// A controllable clock: objects age when we advance it.
	var offsetDays int64
	clock := func() time.Time {
		return time.Now().Add(time.Duration(atomic.LoadInt64(&offsetDays)) * 24 * time.Hour)
	}
	srv, err := s3.New(s3.Options{DataDir: t.TempDir(), Logf: t.Logf, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()
	c := s3Client(t, ts.URL, true)

	c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("aging")})
	if _, err := c.PutBucketLifecycleConfiguration(ctx, &awss3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String("aging"),
		LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
			Rules: []s3types.LifecycleRule{{
				ID:         aws.String("expire-tmp"),
				Status:     s3types.ExpirationStatusEnabled,
				Filter:     &s3types.LifecycleRuleFilter{Prefix: aws.String("tmp/")},
				Expiration: &s3types.LifecycleExpiration{Days: aws.Int32(7)},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketLifecycleConfiguration: %v", err)
	}

	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("aging"), Key: aws.String("tmp/old"), Body: strings.NewReader("x")})
	c.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("aging"), Key: aws.String("keep/safe"), Body: strings.NewReader("x")})

	// Advance 8 days and force a sweep via the exported test hook.
	atomic.StoreInt64(&offsetDays, 8)
	srv.SweepLifecycleNow()

	if _, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("aging"), Key: aws.String("tmp/old")}); err == nil {
		t.Error("tmp/old survived lifecycle expiration")
	}
	if _, err := c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("aging"), Key: aws.String("keep/safe")}); err != nil {
		t.Errorf("keep/safe was wrongly expired: %v", err)
	}
}
