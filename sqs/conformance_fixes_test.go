package sqs

// Four refusals and one acceptance that the boto3 conformance suite found
// missing (conformance/tests/test_sqs.py). None is in the service model, which
// is why the model-derived audit could not have found them.

import (
	"net/http"
	"strings"
	"testing"
)

// A second CreateQueue may only agree with the first. It used to rewrite the
// queue's attributes and answer 200.
func TestCreateQueueDoesNotRewriteAnExistingQueue(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "45"}, nil); err != nil {
		t.Fatal(err)
	}
	// The same name with nothing, and with what it already has, is the same queue.
	for _, attrs := range []map[string]string{nil, {"VisibilityTimeout": "45"}} {
		if _, err := s.CreateQueue("q", attrs, nil); err != nil {
			t.Fatalf("re-create with %v: %v", attrs, err)
		}
	}

	_, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "77", "DelaySeconds": "0"}, nil)
	ae := asAPIError(err)
	if err == nil || ae.Code != "QueueAlreadyExists" ||
		!strings.HasSuffix(ae.Message, "different value for attribute VisibilityTimeout") {
		t.Fatalf("re-create with a different timeout: %v", err)
	}
	if jsonErrorType(ae.Code) != "QueueNameExists" {
		t.Errorf("JSON shape = %q, want QueueNameExists", jsonErrorType(ae.Code))
	}
	attrs, _ := s.Attributes("q")
	if attrs["VisibilityTimeout"] != "45" {
		t.Fatalf("the refused call changed the queue: VisibilityTimeout = %s", attrs["VisibilityTimeout"])
	}
}

// A redrive policy reads back with a numeric maxReceiveCount however it was
// written, so a re-create that spells it as a string is still the same queue.
func TestCreateQueueComparesPoliciesAsDocuments(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("dlq", nil, nil); err != nil {
		t.Fatal(err)
	}
	rp := `{"deadLetterTargetArn":"` + s.queueARN("dlq") + `","maxReceiveCount":"3"}`
	for i := 0; i < 2; i++ {
		if _, err := s.CreateQueue("main", map[string]string{"RedrivePolicy": rp}, nil); err != nil {
			t.Fatalf("create #%d: %v", i+1, err)
		}
	}
	other := strings.Replace(rp, `"3"`, `"4"`, 1)
	if _, err := s.CreateQueue("main", map[string]string{"RedrivePolicy": other}, nil); err == nil {
		t.Fatal("a different maxReceiveCount was accepted")
	}
}

func TestBatchIsRefusedWholeForItsIds(t *testing.T) {
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = string(rune('a' + i))
	}
	for _, tc := range []struct {
		name string
		ids  []string
		code string
	}{
		{"empty", nil, "AWS.SimpleQueueService.EmptyBatchRequest"},
		{"eleven", eleven, "AWS.SimpleQueueService.TooManyEntriesInBatchRequest"},
		{"repeated", []string{"a", "b", "a"}, "AWS.SimpleQueueService.BatchEntryIdsNotDistinct"},
		{"bad id", []string{"has space"}, "AWS.SimpleQueueService.InvalidBatchEntryId"},
		{"long id", []string{strings.Repeat("x", 81)}, "AWS.SimpleQueueService.InvalidBatchEntryId"},
	} {
		aerr := checkBatch("SendMessageBatchRequestEntry", tc.ids)
		if aerr == nil || aerr.Code != tc.code {
			t.Errorf("%s: got %v, want %s", tc.name, aerr, tc.code)
		}
	}
	if aerr := checkBatch("x", []string{"a", "B-2", "c_3"}); aerr != nil {
		t.Errorf("a valid batch: %v", aerr)
	}
}

// Over the wire: a repeated Id sends nothing, where it used to send both.
func TestSendMessageBatchWithARepeatedIdSendsNothing(t *testing.T) {
	ts := sqsAuditServer(t)
	call(t, ts, "CreateQueue", map[string]any{"QueueName": "b"})
	url := "http://sqs.doze-aws.internal/000000000000/b"

	code, body := call(t, ts, "SendMessageBatch", map[string]any{"QueueUrl": url, "Entries": []map[string]any{
		{"Id": "a", "MessageBody": "one"}, {"Id": "a", "MessageBody": "two"},
	}})
	if code != http.StatusBadRequest || !strings.Contains(body, "BatchEntryIdsNotDistinct") {
		t.Fatalf("repeated Id = %d: %s", code, body)
	}
	_, body = call(t, ts, "GetQueueAttributes", map[string]any{
		"QueueUrl": url, "AttributeNames": []string{"ApproximateNumberOfMessages"}})
	if !strings.Contains(body, `"ApproximateNumberOfMessages":"0"`) {
		t.Fatalf("the refused batch still sent something: %s", body)
	}
}

// ReceiveMessage.AttributeNames takes message attributes, whatever enum the
// model types it with. It is the spelling boto3's documentation uses.
func TestReceiveMessageAcceptsTheDeprecatedAttributeNames(t *testing.T) {
	ts := sqsAuditServer(t)
	call(t, ts, "CreateQueue", map[string]any{"QueueName": "r"})
	url := "http://sqs.doze-aws.internal/000000000000/r"
	call(t, ts, "SendMessage", map[string]any{"QueueUrl": url, "MessageBody": "x"})

	for _, name := range []string{"SentTimestamp", "ApproximateReceiveCount", "SenderId", "All"} {
		code, body := call(t, ts, "ReceiveMessage", map[string]any{
			"QueueUrl": url, "AttributeNames": []string{name}, "VisibilityTimeout": 0})
		if code != http.StatusOK || !strings.Contains(body, `"Attributes"`) {
			t.Errorf("AttributeNames=[%s] = %d: %s", name, code, body)
		}
	}
	// What neither spelling knows is still refused.
	code, body := call(t, ts, "ReceiveMessage", map[string]any{
		"QueueUrl": url, "AttributeNames": []string{"NoSuchAttribute"}})
	if code != http.StatusBadRequest {
		t.Errorf("an unknown attribute name = %d: %s", code, body)
	}
}

func TestChangeVisibilityIsHeldToTwelveHours(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "x", nil, -1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Receive("q", 1, 0, 30)
	if err != nil || len(got) != 1 {
		t.Fatalf("receive: %v, %d", err, len(got))
	}
	for _, timeout := range []int{-1, 43201} {
		if err := s.ChangeVisibility("q", got[0].Handle(), timeout); err == nil ||
			asAPIError(err).Code != "InvalidParameterValue" {
			t.Errorf("VisibilityTimeout=%d: %v", timeout, err)
		}
	}
	for _, timeout := range []int{0, 43200} {
		if err := s.ChangeVisibility("q", got[0].Handle(), timeout); err != nil {
			t.Errorf("VisibilityTimeout=%d: %v", timeout, err)
		}
	}
}

// An attribute SQS does not have is refused, not stored and handed back.
func TestUnknownQueueAttributesAreRefused(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", map[string]string{"KmsMasterKeyId": "alias/aws/sqs"}, nil); err != nil {
		t.Fatalf("a real attribute with no local behaviour: %v", err)
	}
	err := s.SetAttributes("q", map[string]string{"VisibiltyTimeout": "5"})
	if err == nil || asAPIError(err).Code != "InvalidAttributeName" {
		t.Fatalf("a misspelt attribute: %v", err)
	}
	if _, err := s.CreateQueue("other", map[string]string{"NoSuchAttribute": "1"}, nil); err == nil {
		t.Fatal("CreateQueue accepted an attribute SQS does not have")
	}
	attrs, _ := s.Attributes("q")
	if _, ok := attrs["VisibiltyTimeout"]; ok {
		t.Fatal("the refused attribute was stored")
	}
}

// A FIFO message has a sequence number, on the send and on the receive, and
// it rises. A standard queue's has none.
func TestFIFOMessagesCarryASequenceNumber(t *testing.T) {
	s := testStore(t)
	s.CreateQueue("q.fifo", map[string]string{"FifoQueue": "true"}, nil)
	s.CreateQueue("plain", nil, nil)
	a, _ := s.Send("q.fifo", "one", nil, -1, "g", "d1", nil)
	b, _ := s.Send("q.fifo", "two", nil, -1, "g", "d2", nil)
	p, _ := s.Send("plain", "x", nil, -1, "", "", nil)
	sa, sb := sequenceNumber(a), sequenceNumber(b)
	if len(sa) != 20 || len(sb) != 20 || sb <= sa {
		t.Errorf("sequence numbers %q then %q: want twenty digits, rising", sa, sb)
	}
	if sequenceNumber(p) != "" {
		t.Errorf("a standard queue's message has a sequence number: %q", sequenceNumber(p))
	}
	if got := systemAttrs(*a, []string{"All"})["SequenceNumber"]; got != sa {
		t.Errorf("received SequenceNumber %q, sent %q", got, sa)
	}
}
