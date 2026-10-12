package console_test

// The console's own calls under another account AND region.
//
// identity_test.go pins what the console shows. These pin what it does: the
// ARNs and scopes it builds to act on a resource. Each was written for the
// default identity, so each worked in every other test here and did nothing
// useful on an instance started with --account-id or --region.
//
// The instance is built the way the binary builds it, as a Regions with the
// console over the default region's stack, because that is the arrangement in
// which a region in a request decides which stack answers it.

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/peers"
)

const (
	euAccount = "111122223333"
	euRegion  = "eu-west-1"
)

// consoleInEU returns the console and the wire endpoint of an instance running
// as euAccount in euRegion.
func consoleInEU(t *testing.T) (con, wire http.Handler) {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	id := awsident.Identity{Region: euRegion, AccountID: euAccount}
	regions, err := dozeaws.NewRegions(dozeaws.StackConfig{DataDir: t.TempDir(), Identity: id, Logf: dozetest.Logf(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { regions.Close() })
	c, err := console.New(console.Options{Peers: peers.InProcess(regions.Stack(regions.Default()).Service), Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	return c, regions
}

// awsJSON posts one JSON-protocol call to the wire endpoint, unsigned, which is
// the default region's.
func awsJSON(t *testing.T, wire http.Handler, target, body string) string {
	t.Helper()
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("X-Amz-Target", target)
	r.Header.Set("Content-Type", "application/x-amz-json-1.1")
	rec := httptest.NewRecorder()
	wire.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// An archive made from the console captures the bus's events, and a replay of
// it delivers them again. The console named the bus and the archive by ARNs in
// the default account and region: the archive watched a bus that was not this
// one and captured nothing, and the replay named an archive that did not exist.
func TestAnArchiveMadeInTheConsoleCapturesAndReplays(t *testing.T) {
	con, wire := consoleInEU(t)

	queueURL := "http://localhost/" + euAccount + "/audit-sink"
	awsJSON(t, wire, "AmazonSQS.CreateQueue", `{"QueueName":"audit-sink"}`)
	awsJSON(t, wire, "AWSEvents.PutRule", `{"Name":"all-billing","EventPattern":"{\"source\":[\"billing\"]}"}`)
	awsJSON(t, wire, "AWSEvents.PutTargets", `{"Rule":"all-billing","Targets":[{"Id":"q","Arn":"arn:aws:sqs:`+euRegion+`:`+euAccount+`:audit-sink"}]}`)
	delivered := func() string {
		var out struct{ Attributes map[string]string }
		body := awsJSON(t, wire, "AmazonSQS.GetQueueAttributes", `{"QueueUrl":"`+queueURL+`","AttributeNames":["ApproximateNumberOfMessages"]}`)
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		return out.Attributes["ApproximateNumberOfMessages"]
	}
	waitFor := func(want, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for delivered() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the queue holds %s messages, want %s", what, delivered(), want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	if w := req(t, con, "POST", "/_console/eb/default/create-archive", url.Values{"name": {"audit"}}); w.Code >= 400 {
		t.Fatalf("create-archive: %d %s", w.Code, w.Body.String())
	}
	if w := req(t, con, "POST", "/_console/eb/default/test-event", url.Values{
		"source": {"billing"}, "detail_type": {"Invoiced"}, "detail": {`{"id":"1"}`}}); w.Code >= 400 {
		t.Fatalf("test-event: %d %s", w.Code, w.Body.String())
	}
	waitFor("1", "after the event was published")

	var arc struct {
		EventCount     int
		EventSourceArn string
	}
	body := awsJSON(t, wire, "AWSEvents.DescribeArchive", `{"ArchiveName":"audit"}`)
	if err := json.Unmarshal([]byte(body), &arc); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if want := "arn:aws:events:" + euRegion + ":" + euAccount + ":event-bus/default"; arc.EventSourceArn != want {
		t.Errorf("the archive watches %q, want this instance's bus %q", arc.EventSourceArn, want)
	}
	if arc.EventCount != 1 {
		t.Errorf("the archive captured %d events, want the one published", arc.EventCount)
	}

	rep := req(t, con, "POST", "/_console/eb/default/replay", url.Values{"name": {"audit"}})
	if rep.Code >= 400 {
		t.Fatalf("replay: %d %s", rep.Code, rep.Body.String())
	}
	waitFor("2", "after the replay")

	// And the archives tab lists it: the listing is asked for by the bus's ARN.
	if tab := req(t, con, "GET", "/_console/eb/default?tab=archives", nil).Body.String(); !strings.Contains(tab, "audit") {
		t.Errorf("the archive is missing from the bus's archives tab")
	}
}

// The live matcher asks the service which rules catch the event being typed,
// and the event it sends carries an account and a region. A rule that matches
// on either is a rule about this instance's.
func TestTheLiveMatcherTestsAnEventFromThisInstance(t *testing.T) {
	con, wire := consoleInEU(t)
	awsJSON(t, wire, "AWSEvents.PutRule",
		`{"Name":"ours","EventPattern":"{\"account\":[\"`+euAccount+`\"],\"region\":[\"`+euRegion+`\"]}"}`)

	body := req(t, con, "POST", "/_console/eb/default/match", url.Values{
		"source": {"billing"}, "detail_type": {"Invoiced"}, "detail": {`{}`}}).Body.String()
	if !strings.Contains(body, "1 of 1 match this event") {
		t.Errorf("a rule on this instance's account and region does not match the console's test event:\n%s", body)
	}
}

// A function made in the console is given a role, in this account.
func TestAFunctionMadeInTheConsoleHasARoleInThisAccount(t *testing.T) {
	con, wire := consoleInEU(t)
	code := t.TempDir()
	if err := os.WriteFile(filepath.Join(code, "bootstrap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	create(t, con, "/_console/lambda/create", url.Values{
		"name": {"resize"}, "runtime": {"provided.al2"}, "handler": {"bootstrap"}, "code": {code}})

	rec := httptest.NewRecorder()
	wire.ServeHTTP(rec, httptest.NewRequest("GET", "/2015-03-31/functions/resize/configuration", nil))
	out, _ := io.ReadAll(rec.Body)
	var cfg struct{ Role string }
	if err := json.Unmarshal(out, &cfg); err != nil || rec.Code != 200 {
		t.Fatalf("GetFunctionConfiguration: %d %s", rec.Code, out)
	}
	if want := "arn:aws:iam::" + euAccount + ":role/console"; cfg.Role != want {
		t.Errorf("Role = %q, want %q", cfg.Role, want)
	}
}

// A share link is a presigned URL, and its credential scope names a region.
// The endpoint serves each region from its own stack and picks by that scope,
// so a link scoped to us-east-1 asked the wrong stack for the object.
func TestAShareLinkServesTheObjectInThisRegion(t *testing.T) {
	con, wire := consoleInEU(t)
	put := func(target, body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		wire.ServeHTTP(rec, httptest.NewRequest("PUT", target, strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("PUT %s: %d %s", target, rec.Code, rec.Body.String())
		}
	}
	put("/docs", "")
	put("/docs/a.txt", "the contents")

	share := req(t, con, "POST", "/_console/s3/docs/presign", url.Values{"key": {"a.txt"}, "ttl": {"15m"}}).Body.String()
	i := strings.Index(share, "http://")
	if i < 0 {
		t.Fatalf("no share link generated:\n%s", share)
	}
	link := html.UnescapeString(share[i : i+strings.IndexAny(share[i:], `"<`)])
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("share link unparseable: %v", err)
	}
	if cred := u.Query().Get("X-Amz-Credential"); !strings.Contains(cred, "/"+euRegion+"/s3/") {
		t.Errorf("the link's credential scope is %q, want region %s", cred, euRegion)
	}
	got := req(t, wire, "GET", u.Path+"?"+u.RawQuery, nil)
	if got.Code != 200 || got.Body.String() != "the contents" {
		t.Errorf("the share link does not serve the object: %d %q", got.Code, got.Body.String())
	}
}
