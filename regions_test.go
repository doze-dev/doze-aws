package dozeaws_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// signedAs builds a request carrying a SigV4 credential scope for a region.
// The signature itself is never verified — this is a local emulator, the
// identity is asserted rather than authenticated — so the scope is the whole
// point of the header.
func signedAs(t *testing.T, base, region, service, target, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	// An empty target means the Query protocol, which is how IAM and STS speak.
	if target == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.Header.Set("X-Amz-Target", target)
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	}
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20260101/"+region+"/"+service+"/aws4_request, "+
			"SignedHeaders=host, Signature=deadbeef")
	return req
}

func doReq(t *testing.T, req *http.Request) string {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := make([]byte, 1<<16)
	n, _ := resp.Body.Read(out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s -> %d: %s", req.Header.Get("X-Amz-Target"), resp.StatusCode, out[:n])
	}
	return string(out[:n])
}

// One process, two regions, told apart by the credential scope alone.
func TestOneProcessServesSeveralRegions(t *testing.T) {
	dir := t.TempDir()
	rs, err := dozeaws.NewRegions(dozeaws.StackConfig{
		DataDir:  dir,
		Services: []string{"sqs", "iam"},
		Identity: awsident.Identity{Region: "us-east-1", AccountID: "811690671382"},
	}, []string{"ap-south-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	srv := httptest.NewServer(rs)
	defer srv.Close()

	// Both regions get a queue of the same name.
	doReq(t, signedAs(t, srv.URL, "us-east-1", "sqs", "AmazonSQS.CreateQueue", `{"QueueName":"shared"}`))
	doReq(t, signedAs(t, srv.URL, "us-east-1", "sqs", "AmazonSQS.CreateQueue", `{"QueueName":"only-us"}`))
	doReq(t, signedAs(t, srv.URL, "ap-south-1", "sqs", "AmazonSQS.CreateQueue", `{"QueueName":"shared"}`))

	var us, ap struct{ QueueUrls []string }
	if err := json.Unmarshal([]byte(doReq(t, signedAs(t, srv.URL, "us-east-1", "sqs", "AmazonSQS.ListQueues", `{}`))), &us); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(doReq(t, signedAs(t, srv.URL, "ap-south-1", "sqs", "AmazonSQS.ListQueues", `{}`))), &ap); err != nil {
		t.Fatal(err)
	}
	if len(us.QueueUrls) != 2 {
		t.Errorf("us-east-1 sees %v, want both its queues", us.QueueUrls)
	}
	if len(ap.QueueUrls) != 1 {
		t.Errorf("ap-south-1 sees %v, want only its own", ap.QueueUrls)
	}

	// The ARN each region mints carries its own region.
	arn := doReq(t, signedAs(t, srv.URL, "ap-south-1", "sqs", "AmazonSQS.GetQueueAttributes",
		`{"QueueUrl":"http://h/811690671382/shared","AttributeNames":["QueueArn"]}`))
	if want := "arn:aws:sqs:ap-south-1:811690671382:shared"; !strings.Contains(arn, want) {
		t.Errorf("want %q in %s", want, arn)
	}
}

// A region nobody listed is created when a request first names it, and the
// creation is logged — an empty region that appeared because of a typo looks
// exactly like lost data, and the log line is what tells them apart.
func TestARegionIsCreatedOnFirstUseAndSaidSo(t *testing.T) {
	dir := t.TempDir()
	var logged []string
	rs, err := dozeaws.NewRegions(dozeaws.StackConfig{
		DataDir:  dir,
		Services: []string{"sqs"},
		Logf:     func(f string, a ...any) { logged = append(logged, f) },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	srv := httptest.NewServer(rs)
	defer srv.Close()

	before := rs.Serving()
	if slices.Contains(before, "eu-west-1") {
		t.Fatalf("eu-west-1 already running: %v", before)
	}
	doReq(t, signedAs(t, srv.URL, "eu-west-1", "sqs", "AmazonSQS.CreateQueue", `{"QueueName":"late"}`))

	if !slices.Contains(rs.Serving(), "eu-west-1") {
		t.Errorf("eu-west-1 not serving after a request named it: %v", rs.Serving())
	}
	if _, err := os.Stat(filepath.Join(dir, "eu-west-1", "sqs")); err != nil {
		t.Errorf("no store for the new region: %v", err)
	}
	var said bool
	for _, l := range logged {
		if strings.Contains(l, "regions: serving") {
			said = true
		}
	}
	if !said {
		t.Errorf("creating a region was silent; logged: %v", logged)
	}
}

// An unsigned request has no region in it — a browser opening a queue URL, a
// webhook, curl — and must land in the configured default rather than nowhere.
func TestUnsignedRequestsGoToTheDefaultRegion(t *testing.T) {
	dir := t.TempDir()
	rs, err := dozeaws.NewRegions(dozeaws.StackConfig{
		DataDir:  dir,
		Services: []string{"sqs"},
		Identity: awsident.Identity{Region: "ap-south-1"},
	}, []string{"us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	srv := httptest.NewServer(rs)
	defer srv.Close()

	if got := rs.Default(); got != "ap-south-1" {
		t.Fatalf("Default = %q, want ap-south-1", got)
	}
	// Unsigned: the plain helper sends no Authorization header.
	call(t, srv.URL, "AmazonSQS.CreateQueue", `{"QueueName":"from-a-browser"}`)

	var ap struct{ QueueUrls []string }
	if err := json.Unmarshal([]byte(doReq(t, signedAs(t, srv.URL, "ap-south-1", "sqs", "AmazonSQS.ListQueues", `{}`))), &ap); err != nil {
		t.Fatal(err)
	}
	if len(ap.QueueUrls) != 1 {
		t.Errorf("the default region has %v, want the unsigned queue", ap.QueueUrls)
	}
}

// IAM has no region, so every region must see the same users — and there must
// be exactly one _global store, because bbolt is single-writer and a second
// opener would block rather than fail loudly.
func TestGlobalServicesAreSharedAcrossRegions(t *testing.T) {
	dir := t.TempDir()
	rs, err := dozeaws.NewRegions(dozeaws.StackConfig{
		DataDir:  dir,
		Services: []string{"sqs", "iam"},
	}, []string{"us-east-1", "ap-south-1", "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	srv := httptest.NewServer(rs)
	defer srv.Close()

	// Created through one region...
	doReq(t, signedAs(t, srv.URL, "us-east-1", "iam", "", `Action=CreateUser&UserName=deploy&Version=2010-05-08`))

	// ...and visible from another, because a user is account-wide.
	got := doReq(t, signedAs(t, srv.URL, "eu-west-1", "iam", "", `Action=GetUser&UserName=deploy&Version=2010-05-08`))
	if !strings.Contains(got, "deploy") {
		t.Errorf("iam user not visible from another region: %s", got)
	}

	// One store on disk, not one per region.
	if _, err := os.Stat(filepath.Join(dir, dozeaws.GlobalDir, "iam")); err != nil {
		t.Errorf("no shared iam store: %v", err)
	}
	for _, region := range rs.Serving() {
		if _, err := os.Stat(filepath.Join(dir, region, "iam")); err == nil {
			t.Errorf("%s has its own iam store; it must share _global", region)
		}
	}
}
