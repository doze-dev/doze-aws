package sqs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// testStore opens a fresh white-box store (no server, no janitor).
func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "sqs.bolt"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return newStore(db)
}

// testServer boots the real service via the public constructor. Gated behind
// -short like every socket-bound test in this repo.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping server test in -short mode")
	}
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

// query POSTs a Query-protocol (form) request and returns the XML body.
func query(t *testing.T, base string, form url.Values) string {
	t.Helper()
	resp, err := http.PostForm(base+"/", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("query %s -> %s\n%s", form.Get("Action"), resp.Status, b)
	}
	return string(b)
}

// jsonCall POSTs an AWS JSON 1.0 request and returns the JSON body.
func jsonCall(t *testing.T, base, action, body string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("json %s -> %s\n%s", action, resp.Status, b)
	}
	return string(b)
}

func TestQueryProtocol(t *testing.T) {
	ts := testServer(t)

	// CreateQueue (XML response carries the queue URL).
	out := query(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"jobs"}})
	if !strings.Contains(out, "<QueueUrl>") || !strings.Contains(out, "/jobs") {
		t.Fatalf("CreateQueue XML missing QueueUrl:\n%s", out)
	}
	qurl := ts.URL + "/000000000000/jobs"

	// SendMessage.
	out = query(t, ts.URL, url.Values{"Action": {"SendMessage"}, "QueueUrl": {qurl}, "MessageBody": {"hi"}})
	if !strings.Contains(out, "<MD5OfMessageBody>49f68a5c8493ec2c0bf489821c21fc3b</MD5OfMessageBody>") {
		t.Fatalf("SendMessage MD5 wrong:\n%s", out)
	}

	// ReceiveMessage.
	out = query(t, ts.URL, url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {qurl}})
	if !strings.Contains(out, "<Body>hi</Body>") || !strings.Contains(out, "<ReceiptHandle>") {
		t.Fatalf("ReceiveMessage XML wrong:\n%s", out)
	}
}

func TestJSONProtocol(t *testing.T) {
	ts := testServer(t)
	qurl := ts.URL + "/000000000000/jobs"

	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"jobs"}`)
	send := jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+qurl+`","MessageBody":"hi"}`)
	if !strings.Contains(send, `"MD5OfMessageBody":"49f68a5c8493ec2c0bf489821c21fc3b"`) {
		t.Fatalf("JSON SendMessage MD5 wrong:\n%s", send)
	}
	recv := jsonCall(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+qurl+`","MaxNumberOfMessages":1}`)
	if !strings.Contains(recv, `"Body":"hi"`) || !strings.Contains(recv, `"ReceiptHandle"`) {
		t.Fatalf("JSON ReceiveMessage wrong:\n%s", recv)
	}
}

// TestPersistenceAcrossReopen closes the service and reopens it over the same
// data dir: queues and messages must survive.
func TestPersistenceAcrossReopen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping server test in -short mode")
	}
	dir := t.TempDir()
	s1, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.store.CreateQueue("durable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.store.Send("durable", "survives", nil, -1, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.store.Receive("durable", 1, 0, -1)
	if err != nil || len(got) != 1 || got[0].Body != "survives" {
		t.Fatalf("after reopen: %v %+v", err, got)
	}
}
