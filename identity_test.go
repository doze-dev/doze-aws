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

// unplumbed names the services whose ARNs still come from awsident's
// package-level defaults. Delete an entry as its service is migrated, and add
// it to the table in TestEveryServiceMintsTheConfiguredIdentity — the two
// together are the migration's progress bar.
var unplumbed = []string{
	"lambda", "iam", "cloudformation", "apigateway",
}

func TestEveryServiceMintsTheConfiguredIdentity(t *testing.T) {
	// Each row creates a resource and then reads it back. The assertion is
	// deliberately blunt — the reply must carry the configured account and must
	// never contain the default — because a service that mints one ARN correctly
	// and another from the constants is exactly the failure being hunted.
	migrated := []struct {
		svc    string
		create [2]string // X-Amz-Target, body
		read   [2]string
		want   string // an ARN that must appear in the read reply
	}{
		{
			svc:    "sqs",
			create: [2]string{"AmazonSQS.CreateQueue", `{"QueueName":"orders"}`},
			read: [2]string{"AmazonSQS.GetQueueAttributes",
				`{"QueueUrl":"http://h/` + testAccount + `/orders","AttributeNames":["QueueArn"]}`},
			want: "arn:aws:sqs:" + testRegion + ":" + testAccount + ":orders",
		},
		{
			svc:    "ssm",
			create: [2]string{"AmazonSSM.PutParameter", `{"Name":"/app/db","Value":"x","Type":"String"}`},
			read:   [2]string{"AmazonSSM.GetParameter", `{"Name":"/app/db"}`},
			want:   "arn:aws:ssm:" + testRegion + ":" + testAccount + ":parameter/app/db",
		},
		{
			svc:    "secretsmanager",
			create: [2]string{"secretsmanager.CreateSecret", `{"Name":"db-password","SecretString":"s"}`},
			read:   [2]string{"secretsmanager.DescribeSecret", `{"SecretId":"db-password"}`},
			want:   "arn:aws:secretsmanager:" + testRegion + ":" + testAccount + ":secret:db-password",
		},
		{
			svc:    "kms",
			create: [2]string{"TrentService.CreateKey", `{"Description":"test"}`},
			read:   [2]string{"TrentService.ListKeys", `{}`},
			want:   "arn:aws:kms:" + testRegion + ":" + testAccount + ":key/",
		},
		{
			svc: "dynamodb",
			create: [2]string{"DynamoDB_20120810.CreateTable",
				`{"TableName":"orders","AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],` +
					`"KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],"BillingMode":"PAY_PER_REQUEST"}`},
			read: [2]string{"DynamoDB_20120810.DescribeTable", `{"TableName":"orders"}`},
			want: "arn:aws:dynamodb:" + testRegion + ":" + testAccount + ":table/orders",
		},
		{
			svc:    "kinesis",
			create: [2]string{"Kinesis_20131202.CreateStream", `{"StreamName":"events","ShardCount":1}`},
			read:   [2]string{"Kinesis_20131202.DescribeStreamSummary", `{"StreamName":"events"}`},
			want:   "arn:aws:kinesis:" + testRegion + ":" + testAccount + ":stream/events",
		},
		{
			svc:    "logs",
			create: [2]string{"Logs_20140328.CreateLogGroup", `{"logGroupName":"/app/api"}`},
			read:   [2]string{"Logs_20140328.DescribeLogGroups", `{}`},
			want:   "arn:aws:logs:" + testRegion + ":" + testAccount + ":log-group:/app/api",
		},
		{
			svc: "cloudwatch",
			create: [2]string{"GraniteServiceVersion20100801.PutDashboard",
				`{"DashboardName":"ops","DashboardBody":"{\"widgets\":[]}"}`},
			read: [2]string{"GraniteServiceVersion20100801.GetDashboard", `{"DashboardName":"ops"}`},
			want: "arn:aws:cloudwatch:" + testRegion + ":" + testAccount + ":dashboard/ops",
		},
		{
			svc:    "eventbridge",
			create: [2]string{"AWSEvents.CreateEventBus", `{"Name":"orders-bus"}`},
			read:   [2]string{"AWSEvents.DescribeEventBus", `{"Name":"orders-bus"}`},
			want:   "arn:aws:events:" + testRegion + ":" + testAccount + ":event-bus/orders-bus",
		},
		{
			svc: "stepfunctions",
			create: [2]string{"AWSStepFunctions.CreateStateMachine",
				`{"name":"pipeline","roleArn":"arn:aws:iam::` + testAccount + `:role/sfn",` +
					`"definition":"{\"StartAt\":\"Done\",\"States\":{\"Done\":{\"Type\":\"Succeed\"}}}"}`},
			read: [2]string{"AWSStepFunctions.DescribeStateMachine",
				`{"stateMachineArn":"arn:aws:states:` + testRegion + `:` + testAccount + `:stateMachine:pipeline"}`},
			want: "arn:aws:states:" + testRegion + ":" + testAccount + ":stateMachine:pipeline",
		},
	}

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  t.TempDir(),
		Services: dozeaws.Implemented,
		Identity: awsident.Identity{Region: testRegion, AccountID: testAccount},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	srv := httptest.NewServer(stack.Handler())
	defer srv.Close()

	for _, tc := range migrated {
		t.Run(tc.svc, func(t *testing.T) {
			created := call(t, srv.URL, tc.create[0], tc.create[1])
			got := call(t, srv.URL, tc.read[0], tc.read[1])

			if !strings.Contains(got, tc.want) {
				t.Errorf("want %q in the reply\ncreate: %s\nread:   %s", tc.want, created, got)
			}
			// The default account appearing anywhere means some path still
			// mints from the package constants.
			if strings.Contains(got, awsident.AccountID) {
				t.Errorf("default account %s leaked into %s", awsident.AccountID, got)
			}
		})
	}

	// SQS additionally hands back a URL, which carries the account outside any
	// ARN — the shape that started this work.
	body := call(t, srv.URL, "AmazonSQS.CreateQueue", `{"QueueName":"url-check"}`)
	var created struct{ QueueUrl string }
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("CreateQueue: %v (body %s)", err, body)
	}
	if !strings.Contains(created.QueueUrl, testAccount) {
		t.Errorf("queue URL = %q, want it to carry account %s", created.QueueUrl, testAccount)
	}

	if len(unplumbed) == 0 {
		t.Log("every service is plumbed — fold the list away and assert over Implemented instead")
	} else {
		t.Logf("still on the package defaults: %s", strings.Join(unplumbed, ", "))
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
