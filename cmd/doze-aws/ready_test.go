package main

import (
	"strings"
	"testing"
)

// The first block a new user reads, pinned.
//
// It is pinned for the same reason dispatch_test.go pins the command table:
// this is user-facing text that nothing else would notice losing. The README
// never mentioned the console at all until recently — thirty thousand lines of
// the one thing a local AWS can do that real AWS cannot, invisible in the first
// document anybody opens — and the way that happens is a line going missing and
// no test caring.

func banner(r ready) string {
	var b strings.Builder
	r.write(&b)
	return b.String()
}

func TestTheReadyBlockAnswersTheFirstThreeQuestions(t *testing.T) {
	got := banner(ready{
		endpoint: "http://aws.harbour.doze",
		console:  "http://aws.harbour.doze/_console/",
		services: []string{"s3", "sqs", "lambda", "dynamodb", "sns"},
		region:   "us-east-1",
		account:  "000000000000",
	})
	t.Logf("\n%s", got)

	for _, want := range []struct{ substr, why string }{
		{"http://aws.harbour.doze", "the endpoint is the whole point"},
		{"/_console/", "the console is the biggest thing here and was undiscoverable"},
		{`eval "$(doze-aws env)"`, "how you point a shell at it, verbatim so it can be copied"},
		{"doze-aws doctor", "the command to run when something looks wrong"},
		{"5 services", "how much is running"},
		{"us-east-1", "which region, because ARNs carry it"},
		{"000000000000", "which account, for the same reason"},
	} {
		if !strings.Contains(got, want.substr) {
			t.Errorf("the ready block no longer mentions %q — %s", want.substr, want.why)
		}
	}
}

// With --console=false there is no console to point at, and a line naming a URL
// that answers 404 is worse than no line.
func TestTheReadyBlockOmitsAConsoleThatIsNotServed(t *testing.T) {
	got := banner(ready{
		endpoint: "http://127.0.0.1:4566",
		services: []string{"sqs"},
		region:   "us-east-1", account: "000000000000",
	})
	if strings.Contains(got, "_console") {
		t.Errorf("the block offers a console that is switched off:\n%s", got)
	}
	if !strings.Contains(got, "doze-aws doctor") {
		t.Error("doctor is worth naming whether or not the console is on")
	}
}

// Nothing to say when there is nothing to point at. A banner announcing an
// empty endpoint would be a bug report waiting to happen.
func TestTheReadyBlockSaysNothingWithoutAnEndpoint(t *testing.T) {
	if got := banner(ready{region: "us-east-1"}); got != "" {
		t.Errorf("printed a banner with no endpoint:\n%s", got)
	}
}

// Few services are named; many are counted. Somebody who passed --services
// chose a handful and wants to see the handful; nobody wants seventeen names.
func TestServiceCountReadsLikeAPersonWroteIt(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, "no services"},
		{[]string{"sqs"}, "sqs"},
		{[]string{"sqs", "s3"}, "sqs and s3"},
		{[]string{"sqs", "s3", "lambda"}, "sqs, s3 and lambda"},
		{[]string{"a", "b", "c", "d", "e"}, "5 services"},
	} {
		if got := countServices(tc.in); got != tc.want {
			t.Errorf("countServices(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
