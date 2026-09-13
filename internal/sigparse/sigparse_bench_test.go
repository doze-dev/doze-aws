package sigparse

// Parse runs on every request of every service, and until now was unmeasurable.
//
// It is called inside routeService, which is called inside the gateway's
// ServeHTTP — so it does sit in BenchmarkRequest*, but there it is buried under
// an fsync. SQS SendMessage is ~7.5 ms end to end, essentially all of it one
// fsync; a 10x regression in a ~200 ns parse moves that number by about 0.003%,
// which no benchmark threshold would ever catch. Measured alone, a regression
// is visible.
//
// The cases are ordered by how often they actually occur: an SDK sends the V4
// header, which is the first branch and the common path; presigned URLs skip
// the header entirely and pay a url.Values parse; and an unsigned request (a
// deployed API Gateway stage, a Lambda function URL) falls all the way through
// every branch and is the most expensive way to learn nothing.

import (
	"net/http"
	"net/url"
	"testing"
)

const v4Header = `AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260913/us-east-1/sqs/aws4_request, ` +
	`SignedHeaders=host;x-amz-content-sha256;x-amz-date, ` +
	`Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7`

func benchRequest(b *testing.B, header, rawQuery string) *http.Request {
	b.Helper()
	r, err := http.NewRequest("POST", "http://127.0.0.1:4566/?"+rawQuery, nil)
	if err != nil {
		b.Fatal(err)
	}
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	return r
}

// The common path: an SDK's signed request, caught by the first branch.
func BenchmarkParseV4Header(b *testing.B) {
	r := benchRequest(b, v4Header, "")
	b.ReportAllocs()
	for b.Loop() {
		if s, ok := Parse(r); !ok || s.Service != "sqs" {
			b.Fatalf("Parse = %+v %v", s, ok)
		}
	}
}

// Presigned: no Authorization header, so this pays the query parse.
func BenchmarkParsePresigned(b *testing.B) {
	q := url.Values{
		"X-Amz-Algorithm":     {"AWS4-HMAC-SHA256"},
		"X-Amz-Credential":    {"AKIAIOSFODNN7EXAMPLE/20260913/us-east-1/s3/aws4_request"},
		"X-Amz-Date":          {"20260913T101500Z"},
		"X-Amz-Expires":       {"900"},
		"X-Amz-SignedHeaders": {"host"},
		"X-Amz-Signature":     {"5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"},
	}
	r := benchRequest(b, "", q.Encode())
	b.ReportAllocs()
	for b.Loop() {
		if s, ok := Parse(r); !ok || s.Service != "s3" {
			b.Fatalf("Parse = %+v %v", s, ok)
		}
	}
}

// Unsigned: every branch tried, nothing found. This is what a request to a
// deployed API Gateway stage or a Lambda function URL costs.
func BenchmarkParseUnsigned(b *testing.B) {
	r := benchRequest(b, "", "")
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := Parse(r); ok {
			b.Fatal("an unsigned request should not parse")
		}
	}
}

// The header parse without the request wrapper, for comparison: the difference
// between this and BenchmarkParseV4Header is what Parse's own dispatch costs.
func BenchmarkParseAuthorizationOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if s, ok := ParseAuthorization(v4Header); !ok || s.Service != "sqs" {
			b.Fatalf("ParseAuthorization = %+v %v", s, ok)
		}
	}
}
