package sqs

// Strict mode, off by default: the two minute-long rules AWS has that a local
// delete or purge, being immediate, has no reason to keep.
//
// doc: the model's DeleteQueue ("you must wait at least 60 seconds before
// creating a queue with the same name"), PurgeQueue ("takes up to 60 seconds")
// and the QueueDeletedRecently and PurgeQueueInProgress error shapes.
//
// Why a switch and not a rule: a test suite that deletes and re-creates a queue
// of one name, or purges between cases, works against doze-aws and fails the
// first time it meets AWS. Making that the default would put a minute's wait in
// every local test loop. So it is opt-in, and the person who wants to find the
// problem before the deploy can ask for it.

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func strictStore(t *testing.T) (*store, *time.Time) {
	t.Helper()
	s := testStore(t)
	s.strict = true
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	return s, &now
}

func TestStrictRefusesReCreatingADeletedNameForAMinute(t *testing.T) {
	s, now := strictStore(t)
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteQueue("q"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(59 * time.Second)
	_, err := s.CreateQueue("q", nil, nil)
	ae, ok := err.(*apiError)
	if !ok || ae.Code != "AWS.SimpleQueueService.QueueDeletedRecently" {
		t.Fatalf("re-creating inside the minute: %v, want QueueDeletedRecently", err)
	}
	if !strings.Contains(ae.Message, "60 seconds") {
		t.Errorf("message %q does not say how long", ae.Message)
	}
	// Another name is not affected, and the minute does end.
	if _, err := s.CreateQueue("other", nil, nil); err != nil {
		t.Errorf("a different name was refused: %v", err)
	}
	*now = now.Add(time.Second)
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Errorf("re-creating after 60 seconds: %v", err)
	}
}

func TestStrictRefusesASecondPurgeForAMinute(t *testing.T) {
	s, now := strictStore(t)
	s.CreateQueue("q", nil, nil)
	s.CreateQueue("p", nil, nil)
	if err := s.Purge("q"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(59 * time.Second)
	err := s.Purge("q")
	ae, ok := err.(*apiError)
	if !ok || ae.Code != "AWS.SimpleQueueService.PurgeQueueInProgress" {
		t.Fatalf("a second purge inside the minute: %v, want PurgeQueueInProgress", err)
	}
	if err := s.Purge("p"); err != nil {
		t.Errorf("another queue's first purge: %v", err)
	}
	*now = now.Add(time.Second)
	if err := s.Purge("q"); err != nil {
		t.Errorf("a purge after 60 seconds: %v", err)
	}
}

func TestWithoutStrictBothAreImmediate(t *testing.T) {
	s := testStore(t)
	s.CreateQueue("q", nil, nil)
	if err := s.DeleteQueue("q"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Errorf("re-create at once: %v", err)
	}
	if err := s.Purge("q"); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge("q"); err != nil {
		t.Errorf("purge twice: %v", err)
	}
}

func TestStrictOverTheWireSpellsTheErrorsTheModelDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a server")
	}
	s, err := New(Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"w"}`)
	jsonBody(t, ts.URL, "DeleteQueue", `{"QueueUrl":"`+ts.URL+`/000000000000/w"}`)
	code, body := jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"w"}`)
	if code != 400 || !strings.Contains(body, `"QueueDeletedRecently"`) {
		t.Errorf("JSON re-create: %d %.200s", code, body)
	}

	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"pw"}`)
	u := ts.URL + "/000000000000/pw"
	if code, _ := jsonBody(t, ts.URL, "PurgeQueue", `{"QueueUrl":"`+u+`"}`); code != 200 {
		t.Fatalf("first purge %d", code)
	}
	code, body = jsonBody(t, ts.URL, "PurgeQueue", `{"QueueUrl":"`+u+`"}`)
	if code != 400 || !strings.Contains(body, `"PurgeQueueInProgress"`) {
		t.Errorf("JSON second purge: %d %.200s", code, body)
	}
}
