package console_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/peers"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// The copyable queue URL on a queue's page must be the URL SQS itself would
// mint: the host the page was asked through, and the account this instance
// actually mints ARNs for.
//
// Both halves were wrong. The account was the literal "000000000000", which
// stopped being right the moment --account-id existed; and the host preferred
// AWS_ENDPOINT_URL_SQS, left over from a fronting router that served the
// service under a path prefix — a topology nothing publishes any more.
//
// A NON-DEFAULT account is the whole point of this test. The e2e suite asserts
// this chip too, but with the default account, so it passes whether the value
// is read or hardcoded.
func TestQueueURLChipUsesTheConfiguredAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	const account = "811690671382"

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  t.TempDir(),
		Logf:     dozetest.Logf(t),
		Identity: awsident.Identity{AccountID: account, Region: "ap-south-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })

	c, err := console.New(console.Options{
		Peers:    peers.InProcess(stack.Service),
		Identity: awsident.Identity{AccountID: account, Region: "ap-south-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if w := req(t, c, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}}); w.Code >= 400 {
		t.Fatalf("create queue: %d %s", w.Code, w.Body.String())
	}

	w := reqHost(t, c, "GET", "/_console/sqs/orders", "aws.harbour.doze")
	body := w.Body.String()

	want := "http://aws.harbour.doze/" + account + "/orders"
	if !strings.Contains(body, want) {
		t.Errorf("queue URL chip does not read %q", want)
	}
	// Narrow on purpose. Asserting the page carries no "000000000000" anywhere
	// FAILS today, and correctly so: the ARN chip beside this one goes through
	// console.QueueARN → the deprecated package-level awsident.ARN, which mints
	// the default account whatever the instance is configured with. That is 37
	// call sites across the tree and its own piece of work; this test is about
	// the URL chip.
	if strings.Contains(body, "http://aws.harbour.doze/"+awsident.AccountID+"/orders") {
		t.Errorf("the queue URL chip still carries the default account %s", awsident.AccountID)
	}
}

// reqHost issues a GET with an explicit Host, since half the thing under test
// is that the URL is built from the host the page was asked through.
func reqHost(t *testing.T, h http.Handler, method, target, host string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(""))
	r.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
