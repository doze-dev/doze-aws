package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

// Every URL doze-aws hands a user has to reach the service that issued it,
// unsigned, exactly as pasting it into a browser would send it.
//
// This is the case the rest of the routing tests do not cover. An SDK call is
// signed, so the credential scope names the service and routing is never in
// doubt. A URL that a person copies out of the console — a queue URL, an API
// Gateway invoke URL, a function URL, an S3 object — carries no signature and
// no X-Amz-Target, so it has to be recognisable by shape alone. When it is
// not, it reaches the S3 fallback and the answer is NoSuchBucket, which names
// neither the service asked for nor the mistake.
//
// The queue URL is the one that went wrong: /{account}/{queue} is AWS's own
// shape for it, and it is indistinguishable from S3 path-style /{bucket}/{key}
// unless the account id is treated as the marker it is.
func TestPublishedURLsRouteToTheirService(t *testing.T) {
	acct := awsident.AccountID

	for _, tc := range []struct {
		what   string
		method string
		url    string
		want   string
	}{
		{"a queue URL, as the console prints it",
			"GET", "http://127.0.0.1:4566/" + acct + "/harbour-checkout-orders", "sqs"},
		{"a queue URL with a trailing slash",
			"GET", "http://127.0.0.1:4566/" + acct + "/harbour-checkout-orders/", "sqs"},
		{"a FIFO queue URL",
			"GET", "http://127.0.0.1:4566/" + acct + "/harbour-depot-dispatch.fifo", "sqs"},
		{"an unsigned SendMessage to a queue URL",
			"POST", "http://127.0.0.1:4566/" + acct + "/orders?Action=SendMessage", "sqs"},

		{"a REST API invoke URL",
			"POST", "http://127.0.0.1:4566/_aws/execute-api/6kkufgikxm/prod/orders", "apigateway"},
		{"an HTTP API invoke URL",
			"POST", "http://127.0.0.1:4566/_aws/execute-api/iay36kikxm/scan", "apigateway"},
		{"a Lambda function URL",
			"GET", "http://127.0.0.1:4566/_aws/lambda-url/abc123/", "lambda"},

		{"an S3 object, path style",
			"GET", "http://127.0.0.1:4566/harbour-receipts/HRB-2026-04801.txt", "s3"},
		{"an S3 object in a folder",
			"GET", "http://127.0.0.1:4566/harbour-product-images/catalogue/bakery/BAK-SRD-800.png", "s3"},
		{"a bucket listing",
			"GET", "http://127.0.0.1:4566/harbour-www", "s3"},

		{"the Lambda control plane",
			"POST", "http://127.0.0.1:4566/2015-03-31/functions/harbour-order-validator/invocations", "lambda"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.url, strings.NewReader(""))
			if got := Route(r); got != tc.want {
				t.Errorf("%s\n  %s %s\n  routed to %q, want %q",
					tc.what, tc.method, tc.url, got, tc.want)
			}
		})
	}
}

// The account id is what makes a queue URL recognisable, so a bucket whose name
// merely looks numeric must still reach S3. doze-aws mints one account id and
// only that exact prefix is claimed.
func TestOnlyTheRealAccountIDClaimsAQueueURL(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/" + awsident.AccountID + "/orders", "sqs"},
		{"/000000000001/orders", "s3"}, // a different account: not ours to claim
		{"/12345/orders", "s3"},        // too short to be an account id
		{"/" + awsident.AccountID, "s3"},
		{"/" + awsident.AccountID + "/a/b", "s3"}, // a queue name has no slash in it
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4566"+tc.path, nil)
			if got := Route(r); got != tc.want {
				t.Errorf("GET %s routed to %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
