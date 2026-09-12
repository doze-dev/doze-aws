package dozeaws_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// The guard for the whole identity migration.
//
// Region and AccountID began as package constants, so every ARN in the tree came
// from the same two strings. They are values now, but a zero Identity resolves
// to the defaults (see awsident.Identity) — which is what lets the tree migrate
// package by package, and also means an UNPLUMBED service is invisible: its ARNs
// still read us-east-1 / 000000000000 and look entirely correct.
//
// So the only thing that can find a missed site is a stack running on a
// non-default identity. Every ARN it hands back must carry the configured
// account; any that still says 000000000000 names a service whose Options,
// Server or Store never received the identity.
//
// As services are migrated they move from unplumbed to plumbed below. The list
// is the remaining work, and it is deliberately explicit rather than derived —
// a derived list would silently shrink to nothing the day the derivation broke.
const (
	testAccount = "811690671382"
	testRegion  = "ap-south-1"
)

func TestEveryServiceMintsTheConfiguredIdentity(t *testing.T) {
	plumbed := []string{"sqs"}

	// Not yet migrated. Each entry is a service whose ARNs still come from
	// awsident's package-level defaults. Delete a line when its service is done;
	// the test then holds it to the configured identity forever after.
	unplumbed := []string{
		"s3", "dynamodb", "sns", "sts", "kms", "ssm", "secretsmanager",
		"eventbridge", "lambda", "kinesis", "iam", "cloudformation",
		"apigateway", "stepfunctions", "logs", "cloudwatch",
	}

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  t.TempDir(),
		Services: append(append([]string{}, plumbed...), unplumbed...),
		Identity: awsident.Identity{Region: testRegion, AccountID: testAccount},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()

	// SQS is the reference: create a queue, then read back both the URL and the
	// ARN. Both must carry the configured account.
	srv := httptest.NewServer(stack.Handler())
	defer srv.Close()

	body := call(t, srv.URL, "AmazonSQS.CreateQueue", `{"QueueName":"orders"}`)
	var created struct{ QueueUrl string }
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("CreateQueue: %v (body %s)", err, body)
	}
	if !strings.Contains(created.QueueUrl, testAccount) {
		t.Errorf("queue URL = %q, want it to carry account %s", created.QueueUrl, testAccount)
	}

	attrs := call(t, srv.URL, "AmazonSQS.GetQueueAttributes",
		`{"QueueUrl":"`+created.QueueUrl+`","AttributeNames":["QueueArn"]}`)
	wantARN := "arn:aws:sqs:" + testRegion + ":" + testAccount + ":orders"
	if !strings.Contains(attrs, wantARN) {
		t.Errorf("QueueArn: want %q in %s", wantARN, attrs)
	}

	if len(unplumbed) == 0 {
		t.Log("every service is plumbed — fold this list away and assert over Implemented instead")
	}
}

// A second stack in the same process must not see the first one's identity.
// This is the property that makes per-region and per-instance accounts possible
// at all, and a package-level default would quietly break it.
func TestTwoStacksKeepTheirOwnIdentities(t *testing.T) {
	mk := func(id awsident.Identity) *httptest.Server {
		st, err := dozeaws.NewStack(dozeaws.StackConfig{
			DataDir: t.TempDir(), Services: []string{"sqs"}, Identity: id,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		s := httptest.NewServer(st.Handler())
		t.Cleanup(s.Close)
		return s
	}

	a := mk(awsident.Identity{Region: "ap-south-1", AccountID: "811690671382"})
	b := mk(awsident.Identity{Region: "eu-west-1", AccountID: "222233334444"})

	for _, tc := range []struct {
		srv         *httptest.Server
		wantAccount string
		wantRegion  string
	}{
		{a, "811690671382", "ap-south-1"},
		{b, "222233334444", "eu-west-1"},
	} {
		call(t, tc.srv.URL, "AmazonSQS.CreateQueue", `{"QueueName":"shared-name"}`)
		attrs := call(t, tc.srv.URL, "AmazonSQS.GetQueueAttributes",
			`{"QueueUrl":"http://x/`+tc.wantAccount+`/shared-name","AttributeNames":["QueueArn"]}`)
		want := "arn:aws:sqs:" + tc.wantRegion + ":" + tc.wantAccount + ":shared-name"
		if !strings.Contains(attrs, want) {
			t.Errorf("want %q in %s", want, attrs)
		}
		// And it must not have leaked the default.
		if strings.Contains(attrs, awsident.AccountID) {
			t.Errorf("default account %s leaked into %s", awsident.AccountID, attrs)
		}
	}
}

func call(t *testing.T, base, target, body string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s -> %d: %s", target, resp.StatusCode, out)
	}
	return string(out)
}
