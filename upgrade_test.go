package dozeaws_test

import (
	"flag"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
)

// The on-disk format is part of what 1.x promises: a data directory written by
// one release has to open under the next, with its resources still in it. The
// migration test above covers the one rearrangement that has happened; this
// covers the general case, and it is the test that notices a field renamed, a
// bucket reorganised, or an encoding changed under people who upgraded.
//
// testdata/datadir-1.0.0 is a data directory written by 1.0.0. It is a fixture,
// not a snapshot: it is recorded once, at the release, and never again. If a
// change makes this test fail, the change is the problem — a store that cannot
// read what 1.0.0 wrote has lost someone's data on upgrade — and the answer is
// a migration that keeps it readable, not a new fixture.
//
//	go test -run TestRecordUpgradeFixture -datadir.record .   (only ever at a release tag)
var recordFixture = flag.Bool("datadir.record", false, "write testdata/datadir-1.0.0 from the current code")

const fixtureDir = "testdata/datadir-1.0.0"

// every service that keeps state on disk and can be created with one call.
var fixtureServices = []string{"s3", "dynamodb", "sqs", "sns", "kms", "ssm", "secretsmanager", "eventbridge", "kinesis", "iam", "logs"}

type fixtureStep struct{ target, body string }

var fixtureSteps = []fixtureStep{
	{"AmazonSQS.CreateQueue", `{"QueueName":"harbour-orders"}`},
	{"DynamoDB_20120810.CreateTable", `{"TableName":"harbour-sessions","AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],"KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],"BillingMode":"PAY_PER_REQUEST"}`},
	{"DynamoDB_20120810.PutItem", `{"TableName":"harbour-sessions","Item":{"id":{"S":"s-1"},"who":{"S":"mina"}}}`},
	{"TrentService.CreateKey", `{"Description":"harbour signing"}`},
	{"AmazonSSM.PutParameter", `{"Name":"/harbour/region","Value":"eu-west-1","Type":"String"}`},
	{"secretsmanager.CreateSecret", `{"Name":"harbour/db","SecretString":"hunter2"}`},
	{"Kinesis_20131202.CreateStream", `{"StreamName":"harbour-events","ShardCount":1}`},
	{"Logs_20140328.CreateLogGroup", `{"logGroupName":"/harbour/app"}`},
	{"AWSEvents.CreateEventBus", `{"Name":"harbour-bus"}`},
	{"", `Action=CreateUser&UserName=deploy&Version=2010-05-08`},
	{"", `Action=CreateTopic&Name=harbour-alerts&Version=2010-03-31`},
}

// what a fresh stack over the fixture has to answer, call by call.
var fixtureChecks = []struct{ target, body, want string }{
	{"AmazonSQS.ListQueues", `{}`, "harbour-orders"},
	{"DynamoDB_20120810.ListTables", `{}`, "harbour-sessions"},
	{"DynamoDB_20120810.GetItem", `{"TableName":"harbour-sessions","Key":{"id":{"S":"s-1"}}}`, "mina"},
	{"TrentService.ListKeys", `{}`, "KeyId"},
	{"AmazonSSM.GetParameter", `{"Name":"/harbour/region"}`, "eu-west-1"},
	{"secretsmanager.GetSecretValue", `{"SecretId":"harbour/db"}`, "hunter2"},
	{"Kinesis_20131202.ListStreams", `{}`, "harbour-events"},
	{"Logs_20140328.DescribeLogGroups", `{}`, "/harbour/app"},
	{"AWSEvents.ListEventBuses", `{}`, "harbour-bus"},
	{"", `Action=GetUser&UserName=deploy&Version=2010-05-08`, "deploy"},
	{"", `Action=ListTopics&Version=2010-03-31`, "harbour-alerts"},
}

func TestRecordUpgradeFixture(t *testing.T) {
	if !*recordFixture {
		t.Skip("records the fixture; run with -datadir.record at a release tag")
	}
	dir := t.TempDir()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Services: fixtureServices})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(st.Handler())
	for _, s := range fixtureSteps {
		call(t, srv.URL, s.target, s.body)
	}
	// S3 is REST: a bucket and an object in it.
	rest(t, srv.URL, http.MethodPut, "/harbour-receipts", "")
	rest(t, srv.URL, http.MethodPut, "/harbour-receipts/2026/order-1.txt", "one pound of tea")
	srv.Close()
	st.Close()

	if err := os.RemoveAll(fixtureDir); err != nil {
		t.Fatal(err)
	}
	copyTree(t, dir, fixtureDir)
	t.Logf("recorded %s", fixtureDir)
}

func TestDataDirFromV1StillOpens(t *testing.T) {
	if _, err := os.Stat(fixtureDir); err != nil {
		t.Skipf("no fixture yet (%v): record one at the release", err)
	}
	dir := t.TempDir()
	copyTree(t, fixtureDir, dir)
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Services: fixtureServices})
	if err != nil {
		t.Fatalf("a data directory written by 1.0.0 does not open: %v", err)
	}
	defer st.Close()
	srv := httptest.NewServer(st.Handler())
	defer srv.Close()

	for _, c := range fixtureChecks {
		if out := call(t, srv.URL, c.target, c.body); !strings.Contains(out, c.want) {
			t.Errorf("%s: lost %q across the upgrade:\n%s", c.target, c.want, out)
		}
	}
	if got := rest(t, srv.URL, http.MethodGet, "/harbour-receipts/2026/order-1.txt", ""); got != "one pound of tea" {
		t.Errorf("S3 object after the upgrade = %q", got)
	}
	// And it is still writable: an upgrade that opens read-only is not one.
	call(t, srv.URL, "AmazonSQS.CreateQueue", `{"QueueName":"after-upgrade"}`)
	if out := call(t, srv.URL, "AmazonSQS.ListQueues", `{}`); !strings.Contains(out, "after-upgrade") || !strings.Contains(out, "harbour-orders") {
		t.Errorf("queues after a write on the upgraded directory: %s", out)
	}
}

func rest(t *testing.T, base, method, path, body string) string {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("%s %s -> %d: %s", method, path, resp.StatusCode, out)
	}
	return string(out)
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
