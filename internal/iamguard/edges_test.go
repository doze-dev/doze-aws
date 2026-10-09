package iamguard

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

// peekBody reads a request body to find a resource name and must hand the body
// back whole. The limit is an exact boundary, and off by one at either side
// either truncates what the service sees or reads a body it was told not to.
func TestPeekBodyBoundary(t *testing.T) {
	for _, c := range []struct {
		name       string
		size       int
		declared   int64
		wantPeeked bool
	}{
		{"empty", 0, 0, false},
		{"one byte", 1, 1, true},
		{"exactly the limit", maxPeek, maxPeek, true},
		{"one over, declared", maxPeek + 1, maxPeek + 1, false},
		{"one over, length unknown", maxPeek + 1, -1, false},
		{"far over, length unknown", maxPeek * 2, -1, false},
	} {
		payload := bytes.Repeat([]byte("x"), c.size)
		r, _ := http.NewRequest("POST", "http://h/", bytes.NewReader(payload))
		r.ContentLength = c.declared
		body, peeked := peekBody(r)
		if peeked != c.wantPeeked {
			t.Errorf("%s: peeked = %v, want %v", c.name, peeked, c.wantPeeked)
		}
		if peeked && len(body) != c.size {
			t.Errorf("%s: peeked %d bytes, want %d", c.name, len(body), c.size)
		}
		// Whatever happened, the service still receives every byte.
		rest, _ := io.ReadAll(r.Body)
		if len(rest) != c.size {
			t.Errorf("%s: the body the service reads has %d bytes, want %d", c.name, len(rest), c.size)
		}
	}
}

func TestQueueNamesFromURLsAndBareNames(t *testing.T) {
	id := awsident.Identity{AccountID: "123456789012", Region: "us-east-1"}
	rule := resourceRules["sqs"]
	for in, want := range map[string]string{
		"http://localhost:4566/123456789012/orders": "arn:aws:sqs:us-east-1:123456789012:orders",
		"/orders": "arn:aws:sqs:us-east-1:123456789012:orders",
		"orders":  "arn:aws:sqs:us-east-1:123456789012:orders",
	} {
		if got := rule.toARN(id, in); got != want {
			t.Errorf("toARN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLambdaReferences(t *testing.T) {
	id := awsident.Identity{AccountID: "123456789012", Region: "us-east-1"}
	for ref, want := range map[string]string{
		"worker":                                "arn:aws:lambda:us-east-1:123456789012:function:worker",
		"worker:prod":                           "arn:aws:lambda:us-east-1:123456789012:function:worker:prod",
		"123456789012:function:worker":          "arn:aws:lambda:us-east-1:123456789012:function:worker",
		":function:worker":                      "arn:aws:lambda:us-east-1:123456789012:function:worker",
		"arn:aws:lambda:eu-west-1:1:function:w": "arn:aws:lambda:us-east-1:123456789012:function:w",
	} {
		if got := LambdaFunctionARN(id, ref); got != want {
			t.Errorf("LambdaFunctionARN(%q) = %q, want %q", ref, got, want)
		}
	}
	for ref, want := range map[string]string{
		"worker":                       "worker",
		"worker:prod":                  "worker",
		"123456789012:function:worker": "worker",
		":function:worker:7":           "worker",
		"":                             "",
	} {
		if got := LambdaFunctionName(ref); got != want {
			t.Errorf("LambdaFunctionName(%q) = %q, want %q", ref, got, want)
		}
	}
}
