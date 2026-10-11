package sqs

// Behaviour the mutation baseline found nothing standing guard over: purge and
// delete of what is not there, how long an empty receive waits and what ends
// the wait, an explicit zero delay, binary attributes on both wires, and the
// small helpers a wire depends on.

import (
	"context"
	"encoding/base64"
	jsonpkg "encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestPurgeAndDeleteOfWhatIsNotThere(t *testing.T) {
	ts := testServer(t)
	for _, action := range []string{"PurgeQueue", "DeleteQueue"} {
		code, body := jsonBody(t, ts.URL, action, `{"QueueUrl":"`+ts.URL+`/000000000000/ghost"}`)
		if code != 400 || !strings.Contains(body, "QueueDoesNotExist") {
			t.Errorf("%s of a queue that is not there: %d %.160s", action, code, body)
		}
	}
}

func TestPurgeRemovesWhatIsHeldAndWhatIsWaiting(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, d := range []int{-1, -1, 600} {
		if _, err := s.Send("q", "m", nil, d, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Receive("q", 1, 0, -1); len(got) != 1 { // one held
		t.Fatalf("receive: %v", got)
	}
	if err := s.Purge("q"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Attributes("q")
	for _, k := range []string{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible", "ApproximateNumberOfMessagesDelayed"} {
		if a[k] != "0" {
			t.Errorf("after a purge %s = %s", k, a[k])
		}
	}
	// And the queue still works.
	if _, err := s.Send("q", "after", nil, -1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Receive("q", 1, 0, -1); len(got) != 1 || got[0].Body != "after" {
		t.Errorf("after a purge: %v", got)
	}
	if err := s.Purge("q"); err != nil { // an empty queue, too
		t.Errorf("purging a queue twice: %v", err)
	}
}

func timed(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
}

func TestHowLongAnEmptyReceiveWaits(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("short", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateQueue("long", map[string]string{"ReceiveMessageWaitTimeSeconds": "2"}, nil); err != nil {
		t.Fatal(err)
	}
	// The sentinel for "no wait given" is the queue's, and a given 0 is 0.
	if d := timed(func() { s.Receive("short", 1, -1, -1) }); d > 400*time.Millisecond {
		t.Errorf("an empty receive on a short-polling queue took %v", d)
	}
	if d := timed(func() { s.Receive("long", 1, 0, -1) }); d > 400*time.Millisecond {
		t.Errorf("an explicit wait of 0 took %v on a queue whose default is 2s", d)
	}
	if d := timed(func() { s.Receive("long", 1, -1, -1) }); d < 1500*time.Millisecond {
		t.Errorf("no wait given returned in %v; the queue's default is 2s", d)
	}
}

// A message that becomes visible during a long poll ends it then, not at the
// deadline.
func TestADelayedMessageEndsALongPollWhenItIsDue(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "later", nil, 1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	var got []message
	d := timed(func() { got, _ = s.Receive("q", 1, 10, -1) })
	if len(got) != 1 {
		t.Fatalf("received %v", got)
	}
	if d < 800*time.Millisecond || d > 3*time.Second {
		t.Errorf("a message due in 1s ended a 10s long poll after %v", d)
	}
}

// An explicit DelaySeconds of 0 overrides the queue's delay; leaving it out
// does not.
func TestAnExplicitZeroDelayOverridesTheQueues(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", map[string]string{"DelaySeconds": "30"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "now", nil, 0, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "queue-default", nil, -1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Receive("q", 10, 0, -1)
	if len(got) != 1 || got[0].Body != "now" {
		t.Errorf("received %v: want only the message sent with an explicit delay of 0", got)
	}
}

func TestPeekReturnsAsManyAsAskedFor(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		s.Send("q", "m"+strconv.Itoa(i), nil, -1, "", "", nil)
	}
	for ask, want := range map[int]int{3: 3, 0: 10, 12: 12, 50: 12} {
		if got, _ := s.Peek("q", ask); len(got) != want {
			t.Errorf("Peek(%d) returned %d, want %d", ask, len(got), want)
		}
	}
}

func TestACancelOfAnUnknownTaskIsNotFound(t *testing.T) {
	ts := testServer(t)
	code, body := jsonBody(t, ts.URL, "CancelMessageMoveTask", `{"TaskHandle":"no-such-task"}`)
	if code != 400 || !strings.Contains(body, "ResourceNotFoundException") {
		t.Errorf("%d %.200s", code, body)
	}
}

// A Binary attribute is the bytes, in the message and in the digest an SDK
// checks on the way in and on the way out.
func TestABinaryAttributeRoundTripsAndItsDigestHolds(t *testing.T) {
	ctx := context.Background()
	c := sdkClient(t)
	q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("binary")})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 1, 2, 250, 255}
	if _, err := c.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("b"),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"blob": {DataType: aws.String("Binary"), BinaryValue: payload},
			"text": {DataType: aws.String("String"), StringValue: aws.String("t")},
		}}); err != nil {
		t.Fatalf("send (the SDK checks the digest the service returns): %v", err)
	}
	got, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, MessageAttributeNames: []string{"All"}})
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("receive: %v %v", got, err)
	}
	v := got.Messages[0].MessageAttributes["blob"]
	if string(v.BinaryValue) != string(payload) || aws.ToString(v.DataType) != "Binary" {
		t.Errorf("blob came back as %v", v)
	}
	if s := got.Messages[0].MessageAttributes["text"]; aws.ToString(s.StringValue) != "t" {
		t.Errorf("text came back as %v", s)
	}
}

func TestABinaryAttributeOnTheQueryWire(t *testing.T) {
	ts := testServer(t)
	formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"qbin"}})
	q := ts.URL + "/000000000000/qbin"
	enc := base64.StdEncoding.EncodeToString([]byte{9, 8, 7})
	code, body := formPost(t, ts.URL, url.Values{"Action": {"SendMessage"}, "QueueUrl": {q}, "MessageBody": {"b"},
		"MessageAttribute.1.Name": {"blob"}, "MessageAttribute.1.Value.DataType": {"Binary"},
		"MessageAttribute.1.Value.BinaryValue": {enc}})
	if code != 200 {
		t.Fatalf("send: %d %.300s", code, body)
	}
	_, body = formPost(t, ts.URL, url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {q}, "MessageAttributeName.1": {"All"}})
	if !strings.Contains(body, "<BinaryValue>"+enc+"</BinaryValue>") || strings.Contains(body, "<StringValue>") {
		t.Errorf("the binary attribute came back as: %s", body)
	}
	// A message with no attributes carries no digest of them.
	formPost(t, ts.URL, url.Values{"Action": {"SendMessage"}, "QueueUrl": {q}, "MessageBody": {"plain"}})
	_, body = formPost(t, ts.URL, url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {q}, "MaxNumberOfMessages": {"10"}})
	if strings.Contains(body, "<MD5OfMessageAttributes></MD5OfMessageAttributes>") {
		t.Errorf("a message with no attributes has an empty digest element: %s", body)
	}
}

func TestQueryBatchesTakeEveryEntryAndTheirDelays(t *testing.T) {
	ts := testServer(t)
	formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"qdelay"}, "Attribute.1.Name": {"VisibilityTimeout"}, "Attribute.1.Value": {"0"}})
	q := ts.URL + "/000000000000/qdelay"
	_, body := formPost(t, ts.URL, url.Values{"Action": {"SendMessageBatch"}, "QueueUrl": {q},
		"SendMessageBatchRequestEntry.1.Id": {"a"}, "SendMessageBatchRequestEntry.1.MessageBody": {"now"},
		"SendMessageBatchRequestEntry.2.Id": {"b"}, "SendMessageBatchRequestEntry.2.MessageBody": {"later"},
		"SendMessageBatchRequestEntry.2.DelaySeconds": {"600"},
		"SendMessageBatchRequestEntry.3.Id":           {"c"}, "SendMessageBatchRequestEntry.3.MessageBody": {"too-long"},
		"SendMessageBatchRequestEntry.3.DelaySeconds": {"901"}})
	if n := strings.Count(body, "<SendMessageBatchResultEntry>"); n != 2 {
		t.Errorf("%d entries succeeded, want 2: %s", n, body)
	}
	if n := strings.Count(body, "<BatchResultErrorEntry>"); n != 1 || !strings.Contains(body, "<Id>c</Id>") {
		t.Errorf("want entry c failed alone: %s", body)
	}
	_, got := formPost(t, ts.URL, url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {q}, "MaxNumberOfMessages": {"10"}, "VisibilityTimeout": {"120"}})
	if strings.Contains(got, "<Body>later</Body>") || !strings.Contains(got, "<Body>now</Body>") {
		t.Errorf("the delayed entry was not delayed, or the other was: %s", got)
	}
	handles := between(got, "<ReceiptHandle>", "</ReceiptHandle>")
	if len(handles) != 1 {
		t.Fatalf("handles: %v", handles)
	}
	// Two-entry delete and visibility batches take both.
	_, body = formPost(t, ts.URL, url.Values{"Action": {"ChangeMessageVisibilityBatch"}, "QueueUrl": {q},
		"ChangeMessageVisibilityBatchRequestEntry.1.Id":                {"a"},
		"ChangeMessageVisibilityBatchRequestEntry.1.ReceiptHandle":     {handles[0]},
		"ChangeMessageVisibilityBatchRequestEntry.1.VisibilityTimeout": {"60"},
		"ChangeMessageVisibilityBatchRequestEntry.2.Id":                {"b"},
		"ChangeMessageVisibilityBatchRequestEntry.2.ReceiptHandle":     {handles[0]},
		"ChangeMessageVisibilityBatchRequestEntry.2.VisibilityTimeout": {"30"}})
	if n := strings.Count(body, "<ChangeMessageVisibilityBatchResultEntry>"); n != 2 {
		t.Errorf("visibility batch: %d succeeded, want 2: %s", n, body)
	}
	_, body = formPost(t, ts.URL, url.Values{"Action": {"DeleteMessageBatch"}, "QueueUrl": {q},
		"DeleteMessageBatchRequestEntry.1.Id": {"a"}, "DeleteMessageBatchRequestEntry.1.ReceiptHandle": {handles[0]},
		"DeleteMessageBatchRequestEntry.2.Id": {"b"}, "DeleteMessageBatchRequestEntry.2.ReceiptHandle": {handles[0]}})
	if n := strings.Count(body, "<DeleteMessageBatchResultEntry>"); n != 2 {
		t.Errorf("delete batch: %d succeeded, want 2: %s", n, body)
	}
}

func TestAServerErrorIsTheReceivers(t *testing.T) {
	for _, c := range []struct {
		status int
		class  string
	}{{500, "Receiver"}, {503, "Receiver"}, {400, "Sender"}, {404, "Sender"}} {
		w := httptest.NewRecorder()
		writeError(w, true, &apiError{Code: "Whatever", Status: c.status, Message: "m"})
		if got := w.Header().Get("x-amzn-query-error"); got != "Whatever;"+c.class {
			t.Errorf("JSON %d: x-amzn-query-error %q, want class %s", c.status, got, c.class)
		}
		w = httptest.NewRecorder()
		writeError(w, false, &apiError{Code: "Whatever", Status: c.status, Message: "m"})
		if !strings.Contains(w.Body.String(), "<Type>"+c.class+"</Type>") {
			t.Errorf("Query %d: %s, want type %s", c.status, w.Body.String(), c.class)
		}
	}
}

func TestQueueNameFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://h:1/000000000000/orders": "orders", "/000000000000/orders": "orders",
		"/orders": "orders", "orders": "orders", "": "",
	} {
		if got := queueNameFromURL(in); got != want {
			t.Errorf("queueNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddPermissionToAPolicyWithNoVersion(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"nover"}`)
	q := ts.URL + "/000000000000/nover"
	doc := `{"Statement":[{"Sid":"s","Effect":"Allow","Principal":"*","Action":"sqs:SendMessage","Resource":"*"}]}`
	set, _ := json_(map[string]any{"QueueUrl": q, "Attributes": map[string]string{"Policy": doc}})
	if code, body := jsonBody(t, ts.URL, "SetQueueAttributes", set); code != 200 {
		t.Skipf("a policy with no Version is refused here (%d %.100s); nothing to default", code, body)
	}
	if code, body := jsonBody(t, ts.URL, "AddPermission",
		`{"QueueUrl":"`+q+`","Label":"more","AWSAccountIds":["111122223333"],"Actions":["SendMessage"]}`); code != 200 {
		t.Fatalf("AddPermission: %d %s", code, body)
	}
	_, body := jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+q+`","AttributeNames":["Policy"]}`)
	if !strings.Contains(body, `2012-10-17`) {
		t.Errorf("the rewritten policy has no Version: %s", body)
	}
}

func TestADataTypeSuffixIsHeldTo256(t *testing.T) {
	long := func(n int) map[string]attr {
		return map[string]attr{"a": {DataType: "String." + strings.Repeat("x", n-len("String.")), StringValue: "v"}}
	}
	if err := validateMessageAttrs(long(256)); err != nil {
		t.Errorf("a data type of 256 characters: %v", err)
	}
	if err := validateMessageAttrs(long(257)); err == nil {
		t.Error("a data type of 257 characters was accepted")
	}
}

func TestAServerStartsWithNoLogger(t *testing.T) {
	s, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	// A refused request is logged; with no logger that must not panic.
	if code, _ := jsonBody(t, ts.URL, "GetQueueUrl", `{"QueueName":"nope"}`); code != 400 {
		t.Errorf("answered %d", code)
	}
}

// ReceiveRequestAttemptId: the edges of the five minutes, a retry that asks
// for no visibility, and the record that does not outlive its window.
func TestReceiveAttemptEdges(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	if _, err := s.CreateQueue("q.fifo", map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "true", "VisibilityTimeout": "3600"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"a", "b"} {
		if _, err := s.Send("q.fifo", b, nil, -1, b, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := s.ReceiveAttempt("q.fifo", "x", 10, 0, -1)
	if len(first) != 2 {
		t.Fatalf("first: %v", first)
	}
	// At exactly five minutes the id still matches; a retry asking for a
	// visibility of 0 puts the messages back in view.
	now = now.Add(5 * time.Minute)
	again, _ := s.ReceiveAttempt("q.fifo", "x", 10, 0, 0)
	if len(again) != 2 {
		t.Fatalf("at exactly five minutes the retry returned %d", len(again))
	}
	if got, _ := s.Receive("q.fifo", 10, 0, -1); len(got) != 2 {
		t.Errorf("a retry with VisibilityTimeout 0 left %d messages visible, want 2", len(got))
	}
	// A record older than the window is gone once another attempt is made.
	now = now.Add(6 * time.Minute)
	s.ReceiveAttempt("q.fifo", "y", 10, 0, -1)
	s.attempts.mu.Lock()
	n := len(s.attempts.by)
	s.attempts.mu.Unlock()
	if n > 1 {
		t.Errorf("%d attempt records held after the window passed, want only the new one", n)
	}
}

func json_(v any) (string, error) {
	b, err := jsonpkg.Marshal(v)
	return string(b), err
}
