package gateway

// Routing runs on every request of every service, and until now was
// unmeasurable for the same reason sigparse was: inside BenchmarkRequest* it
// sits under ~7.5 ms of fsync, so a 10x regression here moves that number by
// roughly 0.01%.
//
// What makes this worth measuring separately is that routeService is an ORDERED
// CASCADE, not a map lookup. Rule 1 is a header map hit; the S3 fallback is
// rule 10 and has tried every rule above it first. So the interesting output is
// not one number but the SPREAD between the first rule and the last — that
// spread is what a new rule inserted near the top would cost every request that
// currently falls past it.

import (
	"net/http"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

func benchRoute(b *testing.B, want string, build func() *http.Request) {
	b.Helper()
	id := awsident.Default()
	r := build()
	if got := Route(id, "", r); got != want {
		b.Fatalf("Route = %q, want %q — the fixture does not exercise the rule it claims", got, want)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if got := Route(id, "", r); got != want {
			b.Fatalf("Route = %q, want %q", got, want)
		}
	}
}

func req(b *testing.B, method, url string, hdr map[string]string) *http.Request {
	b.Helper()
	r, err := http.NewRequest(method, url, nil)
	if err != nil {
		b.Fatal(err)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func sigFor(service string) string {
	return `AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260913/us-east-1/` + service +
		`/aws4_request, SignedHeaders=host, Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7`
}

// Rule 1: the X-Amz-Target prefix. One map lookup, the cheapest exit.
func BenchmarkRouteByTargetHeader(b *testing.B) {
	benchRoute(b, "dynamodb", func() *http.Request {
		return req(b, "POST", "http://127.0.0.1:4566/", map[string]string{
			"X-Amz-Target": "DynamoDB_20120810.PutItem"})
	})
}

// Rule 2: the signature scope. Pays a sigparse.Parse before its map lookup.
func BenchmarkRouteBySignatureScope(b *testing.B) {
	benchRoute(b, "sqs", func() *http.Request {
		return req(b, "POST", "http://127.0.0.1:4566/", map[string]string{
			"Authorization": sigFor("sqs")})
	})
}

// Rule 3: a Lambda API path, reached only after the two above have missed.
func BenchmarkRouteByLambdaPath(b *testing.B) {
	benchRoute(b, "lambda", func() *http.Request {
		return req(b, "POST", "http://127.0.0.1:4566/2015-03-31/functions/fn/invocations", nil)
	})
}

// The far end of the cascade: an unsigned request with no recognisable shape
// falls through every rule to the S3 fallback. This is the worst case, and the
// number a new rule near the top would be added to.
func BenchmarkRouteFallbackToS3(b *testing.B) {
	benchRoute(b, "s3", func() *http.Request {
		return req(b, "GET", "http://127.0.0.1:4566/some-bucket/some/key.txt", nil)
	})
}

// A signed S3 request, which is what the fallback normally looks like in
// practice — it exits at rule 2 rather than walking the whole cascade.
func BenchmarkRouteSignedS3(b *testing.B) {
	benchRoute(b, "s3", func() *http.Request {
		return req(b, "GET", "http://127.0.0.1:4566/some-bucket/some/key.txt", map[string]string{
			"Authorization": sigFor("s3")})
	})
}
