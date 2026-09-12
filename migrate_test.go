package dozeaws_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// The migration's whole job is that nothing is lost. A rename that drops a
// store looks exactly like a data directory that was never written, so the
// assertion is on the RESOURCES, end to end: create them under the old layout,
// move, then read them back through a fresh stack.
func TestMigrationKeepsEveryResource(t *testing.T) {
	dir := t.TempDir()

	// Build the pre-region layout by hand: a stack whose services sit directly
	// under the data dir, which is what every installation before this looked
	// like.
	old, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  dir,
		Services: []string{"sqs", "dynamodb", "iam"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(old.Handler())
	call(t, srv.URL, "AmazonSQS.CreateQueue", `{"QueueName":"harbour-orders"}`)
	call(t, srv.URL, "DynamoDB_20120810.CreateTable",
		`{"TableName":"harbour-sessions","AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],`+
			`"KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],"BillingMode":"PAY_PER_REQUEST"}`)
	call(t, srv.URL, "", `Action=CreateUser&UserName=deploy&Version=2010-05-08`)
	srv.Close()
	old.Close()

	// Rearrange into the shape the old binary left behind. NewStack already
	// writes the new layout, so undo it — this is the fixture, not the code
	// under test.
	for _, svc := range []string{"sqs", "dynamodb"} {
		if err := os.Rename(filepath.Join(dir, awsident.Region, svc), filepath.Join(dir, svc)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(filepath.Join(dir, dozeaws.GlobalDir, "iam"), filepath.Join(dir, "iam")); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, awsident.Region))
	_ = os.Remove(filepath.Join(dir, dozeaws.GlobalDir))

	if !dozeaws.NeedsMigration(dir) {
		t.Fatal("NeedsMigration = false on an old-layout directory; the fixture did not take")
	}

	plan, err := dozeaws.Migrate(dir, awsident.Region)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 3 {
		t.Fatalf("moved %d services, want 3: %+v", len(plan.Moves), plan.Moves)
	}
	if dozeaws.NeedsMigration(dir) {
		t.Error("still reports NeedsMigration after migrating")
	}

	// IAM is region-less, so it belongs under _global rather than the region.
	if _, err := os.Stat(filepath.Join(dir, dozeaws.GlobalDir, "iam")); err != nil {
		t.Errorf("iam did not land in %s: %v", dozeaws.GlobalDir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, awsident.Region, "sqs")); err != nil {
		t.Errorf("sqs did not land under the region: %v", err)
	}

	// The point of the whole exercise: the resources are still there.
	next, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  dir,
		Services: []string{"sqs", "dynamodb", "iam"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	srv2 := httptest.NewServer(next.Handler())
	defer srv2.Close()

	queues := call(t, srv2.URL, "AmazonSQS.ListQueues", `{}`)
	if !strings.Contains(queues, "harbour-orders") {
		t.Errorf("queue lost in migration: %s", queues)
	}
	tables := call(t, srv2.URL, "DynamoDB_20120810.ListTables", `{}`)
	if !strings.Contains(tables, "harbour-sessions") {
		t.Errorf("table lost in migration: %s", tables)
	}
	user := call(t, srv2.URL, "", `Action=GetUser&UserName=deploy&Version=2010-05-08`)
	if !strings.Contains(user, "deploy") {
		t.Errorf("iam user lost in migration: %s", user)
	}
}

// Refusing to overwrite is the property that makes the migration safe to run
// automatically: two stores for one service is a question for a person.
func TestMigrationRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("sqs")                                 // old layout
	mk(filepath.Join(awsident.Region, "sqs")) // and a new-layout one already there

	_, err := dozeaws.Migrate(dir, awsident.Region)
	if err == nil {
		t.Fatal("migrated over an existing destination; want a refusal")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should say what is in the way, got: %v", err)
	}
	// And nothing moved.
	if _, serr := os.Stat(filepath.Join(dir, "sqs")); serr != nil {
		t.Errorf("source was moved despite the refusal: %v", serr)
	}
}

// A directory that is not a service is not the migration's business.
func TestMigrationIgnoresUnknownDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if dozeaws.NeedsMigration(dir) {
		t.Error("a stray directory triggered a migration")
	}
	plan, err := dozeaws.Migrate(dir, awsident.Region)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 0 {
		t.Errorf("moved %+v, want nothing", plan.Moves)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes")); err != nil {
		t.Errorf("stray directory was touched: %v", err)
	}
}

// Two regions hold same-named resources independently — the property the whole
// folder-per-region mechanism exists to provide.
func TestRegionsAreIsolated(t *testing.T) {
	dir := t.TempDir()
	mk := func(region string) *httptest.Server {
		st, err := dozeaws.NewStack(dozeaws.StackConfig{
			DataDir:  dir,
			Services: []string{"sqs"},
			Identity: awsident.Identity{Region: region},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		s := httptest.NewServer(st.Handler())
		t.Cleanup(s.Close)
		return s
	}

	a := mk("us-east-1")
	call(t, a.URL, "AmazonSQS.CreateQueue", `{"QueueName":"shared"}`)
	call(t, a.URL, "AmazonSQS.CreateQueue", `{"QueueName":"only-in-us"}`)

	b := mk("ap-south-1")
	call(t, b.URL, "AmazonSQS.CreateQueue", `{"QueueName":"shared"}`)

	var listB struct{ QueueUrls []string }
	if err := json.Unmarshal([]byte(call(t, b.URL, "AmazonSQS.ListQueues", `{}`)), &listB); err != nil {
		t.Fatal(err)
	}
	if len(listB.QueueUrls) != 1 {
		t.Errorf("ap-south-1 sees %v, want only its own queue", listB.QueueUrls)
	}

	// And on disk they are simply two folders.
	for _, region := range []string{"us-east-1", "ap-south-1"} {
		if _, err := os.Stat(filepath.Join(dir, region, "sqs")); err != nil {
			t.Errorf("no store for %s: %v", region, err)
		}
	}
}
