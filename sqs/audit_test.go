package sqs

// What the audit of stage 1 found still standing (2026-10-12), each against the
// model's own text (.audit-models/sqs.json) and on both wires where the
// operation has two. Every quote below is the model's.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// model: FifoQueue "You can provide this attribute only during queue creation.
// You can't change it for an existing queue."
func TestSetQueueAttributesCannotChangeTheQueueType(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"flip"}`)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"flop.fifo","Attributes":{"FifoQueue":"true"}}`)
	q, f := ts.URL+"/000000000000/flip", ts.URL+"/000000000000/flop.fifo"

	if code, body := jsonBody(t, ts.URL, "SetQueueAttributes", `{"QueueUrl":"`+q+`","Attributes":{"FifoQueue":"true"}}`); code != 400 {
		t.Errorf("JSON: FifoQueue=true on a standard queue answered %d: %s", code, body)
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"SetQueueAttributes"}, "QueueUrl": {f},
		"Attribute.1.Name": {"FifoQueue"}, "Attribute.1.Value": {"false"}}); code != 400 {
		t.Errorf("Query: FifoQueue=false on a FIFO queue answered %d: %s", code, body)
	}
	// And neither changed: the standard queue takes a plain send, the FIFO
	// queue still wants a group.
	if code, body := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"x"}`); code != 200 {
		t.Errorf("the standard queue no longer takes a plain send: %d %s", code, body)
	}
	if code, _ := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+f+`","MessageBody":"x"}`); code != 400 {
		t.Errorf("the FIFO queue took a send with no group id: %d", code)
	}
}

// model: RedriveAllowPolicy allowAll "Any source queues in this Amazon Web
// Services account in the same Region can specify this queue as the dead-letter
// queue"; StartMessageMoveTask's errors name ResourceNotFoundException. An ARN
// that names another account, another Region or another service is not the
// queue here that happens to share its last segment (SQS-23, for ARNs).
func TestAnArnElsewhereIsNotTheQueueHere(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"m-dlq"}`)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"m-src","Attributes":{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:m-dlq\",\"maxReceiveCount\":\"1\"}"}}`)
	here := "arn:aws:sqs:us-east-1:000000000000:m-dlq"
	for _, arn := range []string{
		"arn:aws:sqs:us-east-1:999999999999:m-dlq",
		"arn:aws:sqs:eu-west-1:000000000000:m-dlq",
		"arn:aws:sns:us-east-1:000000000000:m-dlq",
		"m-dlq",
	} {
		if code, body := jsonBody(t, ts.URL, "StartMessageMoveTask", `{"SourceArn":"`+arn+`"}`); code == 200 {
			t.Errorf("JSON StartMessageMoveTask SourceArn %s answered 200: %s", arn, body)
		} else if !strings.Contains(body, "ResourceNotFoundException") {
			t.Errorf("JSON StartMessageMoveTask SourceArn %s: %d %s, want ResourceNotFoundException", arn, code, body)
		}
		if code, body := formPost(t, ts.URL, url.Values{"Action": {"ListMessageMoveTasks"}, "SourceArn": {arn}}); code == 200 {
			t.Errorf("Query ListMessageMoveTasks SourceArn %s answered 200: %s", arn, body)
		}
		if code, body := jsonBody(t, ts.URL, "StartMessageMoveTask", `{"SourceArn":"`+here+`","DestinationArn":"`+strings.Replace(arn, "m-dlq", "m-src", 1)+`"}`); code == 200 {
			t.Errorf("StartMessageMoveTask DestinationArn %s answered 200: %s", arn, body)
		}
		rp := `{\"deadLetterTargetArn\":\"` + arn + `\",\"maxReceiveCount\":\"3\"}`
		if code, body := jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"other-src","Attributes":{"RedrivePolicy":"`+rp+`"}}`); code == 200 {
			t.Errorf("RedrivePolicy deadLetterTargetArn %s answered 200: %s", arn, body)
		}
	}
}

// model: "Amazon SQS uses a SHA-256 hash to generate the
// MessageDeduplicationId using the body of the message".
func TestContentBasedDeduplicationIsASHA256OfTheBody(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"cbd.fifo","Attributes":{"FifoQueue":"true","ContentBasedDeduplication":"true"}}`)
	q := ts.URL + "/000000000000/cbd.fifo"
	jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"same body","MessageGroupId":"g"}`)
	sum := sha256.Sum256([]byte("same body"))
	// A second message that names the first one's generated id is its duplicate.
	jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"another body","MessageGroupId":"g","MessageDeduplicationId":"`+hex.EncodeToString(sum[:])+`"}`)
	got := jsonCall(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+q+`","AttributeNames":["ApproximateNumberOfMessages"]}`)
	if !strings.Contains(got, `"ApproximateNumberOfMessages":"1"`) {
		t.Errorf("a message carrying the SHA-256 of an earlier body as its deduplication id was not a duplicate: %s", got)
	}
}

// model: SequenceNumber "This parameter applies only to FIFO (first-in-first-out)
// queues."
func TestAStandardQueueGivesNoSequenceNumber(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"fair"}`)
	q := ts.URL + "/000000000000/fair"
	if got := jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"x","MessageGroupId":"tenant-a"}`); strings.Contains(got, "SequenceNumber") {
		t.Errorf("SendMessage on a standard queue answered a SequenceNumber: %s", got)
	}
	if _, got := formPost(t, ts.URL, url.Values{"Action": {"SendMessageBatch"}, "QueueUrl": {q},
		"SendMessageBatchRequestEntry.1.Id": {"a"}, "SendMessageBatchRequestEntry.1.MessageBody": {"x"},
		"SendMessageBatchRequestEntry.1.MessageGroupId": {"tenant-a"}}); strings.Contains(got, "SequenceNumber") {
		t.Errorf("SendMessageBatch on a standard queue answered a SequenceNumber: %s", got)
	}
	got := jsonCall(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+q+`","MaxNumberOfMessages":10,"MessageSystemAttributeNames":["All"]}`)
	if strings.Contains(got, "SequenceNumber") {
		t.Errorf("ReceiveMessage on a standard queue returned a SequenceNumber: %s", got)
	}
	if !strings.Contains(got, `"MessageGroupId":"tenant-a"`) {
		t.Errorf("the group id is still the message's on a standard queue: %s", got)
	}
}

// model: "Valid values are true and false" (FifoQueue, ContentBasedDeduplication,
// SqsManagedSseEnabled).
func TestABooleanAttributeIsSpelledTrueOrFalse(t *testing.T) {
	ts := testServer(t)
	for _, v := range []string{"TRUE", "True", "False", " true"} {
		if code, body := jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"b.fifo","Attributes":{"FifoQueue":"true","ContentBasedDeduplication":"`+v+`"}}`); code != 400 {
			t.Errorf("ContentBasedDeduplication=%q at create answered %d: %s", v, code, body)
		}
		if code, body := formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"b2"},
			"Attribute.1.Name": {"SqsManagedSseEnabled"}, "Attribute.1.Value": {v}}); code != 400 {
			t.Errorf("Query SqsManagedSseEnabled=%q at create answered %d: %s", v, code, body)
		}
	}
}

// model: "the only supported message system attribute is AWSTraceHeader. Its
// type must be String".
func TestAMessageSystemAttributeIsTheTraceHeaderAsAString(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"sys"}`)
	q := ts.URL + "/000000000000/sys"
	for name, attr := range map[string]string{
		"another name": `"Other":{"DataType":"String","StringValue":"x"}`,
		"a Number":     `"AWSTraceHeader":{"DataType":"Number","StringValue":"1"}`,
		"a Binary":     `"AWSTraceHeader":{"DataType":"Binary","BinaryValue":"eA=="}`,
	} {
		if code, body := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"x","MessageSystemAttributes":{`+attr+`}}`); code != 400 {
			t.Errorf("JSON SendMessage with %s as a system attribute answered %d: %s", name, code, body)
		}
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"SendMessage"}, "QueueUrl": {q}, "MessageBody": {"x"},
		"MessageSystemAttribute.1.Name": {"Other"}, "MessageSystemAttribute.1.Value.DataType": {"String"},
		"MessageSystemAttribute.1.Value.StringValue": {"x"}}); code != 400 {
		t.Errorf("Query SendMessage with another system attribute answered %d: %s", code, body)
	}
	if code, body := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"x","MessageSystemAttributes":{"AWSTraceHeader":{"DataType":"String","StringValue":"Root=1-5759e988-bd862e3fe1be46a994272793"}}}`); code != 200 {
		t.Errorf("the trace header itself answered %d: %s", code, body)
	}
}

// model: ListQueues "You must set MaxResults to receive a value for NextToken in
// the response", and without it "the maximum number of queues that can be
// returned" is 1,000.
func TestListQueuesGivesATokenOnlyWhenMaxResultsIsSet(t *testing.T) {
	ts := testServer(t)
	for i := 0; i < maxListQueues+3; i++ {
		jsonCall(t, ts.URL, "CreateQueue", fmt.Sprintf(`{"QueueName":"many-%04d"}`, i))
	}
	got := jsonCall(t, ts.URL, "ListQueues", `{}`)
	if strings.Contains(got, "NextToken") {
		t.Errorf("JSON ListQueues with no MaxResults answered a NextToken")
	}
	if n := strings.Count(got, "/many-"); n != maxListQueues {
		t.Errorf("JSON ListQueues with no MaxResults returned %d queues, want %d", n, maxListQueues)
	}
	if _, body := formPost(t, ts.URL, url.Values{"Action": {"ListQueues"}}); strings.Contains(body, "NextToken") {
		t.Errorf("Query ListQueues with no MaxResults answered a NextToken")
	}
	if got := jsonCall(t, ts.URL, "ListQueues", `{"MaxResults":1000}`); !strings.Contains(got, "NextToken") {
		t.Errorf("ListQueues with MaxResults and more to come answered no NextToken")
	}
}

// model: the batch operations' errors name EmptyBatchRequest (Query code
// AWS.SimpleQueueService.EmptyBatchRequest).
func TestAnEmptyBatchIsEmptyBatchRequestOnBothWires(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"eb"}`)
	q := ts.URL + "/000000000000/eb"
	for _, op := range []string{"SendMessageBatch", "DeleteMessageBatch", "ChangeMessageVisibilityBatch"} {
		if code, body := formPost(t, ts.URL, url.Values{"Action": {op}, "QueueUrl": {q}}); code != 400 || !strings.Contains(body, "AWS.SimpleQueueService.EmptyBatchRequest") {
			t.Errorf("Query %s with no entries: %d %s", op, code, body)
		}
		if code, body := jsonBody(t, ts.URL, op, `{"QueueUrl":"`+q+`","Entries":[]}`); code != 400 || !strings.Contains(body, "EmptyBatchRequest") {
			t.Errorf("JSON %s with no entries: %d %s", op, code, body)
		}
	}
}

// model: the error shapes' httpError: ReceiptHandleIsInvalid 404,
// ResourceNotFoundException 404, PurgeQueueInProgress 403, OverLimit 403. The
// JSON wire carries the shape's status as the Query wire does.
func TestAnErrorCarriesTheModelsStatusOnBothWires(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"st"}`)
	q := ts.URL + "/000000000000/st"
	if code, body := jsonBody(t, ts.URL, "DeleteMessage", `{"QueueUrl":"`+q+`","ReceiptHandle":"not-a-handle"}`); code != 404 || !strings.Contains(body, "ReceiptHandleIsInvalid") {
		t.Errorf("JSON DeleteMessage with a malformed handle: %d %s, want 404 ReceiptHandleIsInvalid", code, body)
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"DeleteMessage"}, "QueueUrl": {q}, "ReceiptHandle": {"not-a-handle"}}); code != 404 {
		t.Errorf("Query DeleteMessage with a malformed handle: %d %s, want 404", code, body)
	}
	if code, body := jsonBody(t, ts.URL, "ListMessageMoveTasks", `{"SourceArn":"arn:aws:sqs:us-east-1:000000000000:nope"}`); code != 404 || !strings.Contains(body, "ResourceNotFoundException") {
		t.Errorf("JSON ListMessageMoveTasks on a missing queue: %d %s, want 404 ResourceNotFoundException", code, body)
	}
}

// model: maxReceiveCount "Default: 10."
func TestRedrivePolicyDefaultsMaxReceiveCountToTen(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"d-dlq"}`)
	code, body := jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"d-src","Attributes":{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:d-dlq\"}"}}`)
	if code != 200 {
		t.Fatalf("a RedrivePolicy with no maxReceiveCount answered %d: %s", code, body)
	}
	got := jsonCall(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+ts.URL+`/000000000000/d-src","AttributeNames":["RedrivePolicy"]}`)
	if !strings.Contains(got, `\"maxReceiveCount\":10`) {
		t.Errorf("the policy read back without the default of 10: %s", got)
	}
}

// A queue URL is https://host/<account>/<name>. One with more path than that
// names no queue; one with no account is the in-process spelling and names the
// queue here (peercall).
func TestAQueueURLHasAnAccountAndAName(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"shape"}`)
	for _, u := range []string{ts.URL + "/999999999999/extra/shape", ts.URL + "/000000000000/extra/shape", ts.URL + "/a/b/c/shape"} {
		if code, body := jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+u+`","AttributeNames":["All"]}`); code == 200 {
			t.Errorf("queue URL %s was served: %s", u, body)
		}
	}
	for _, u := range []string{ts.URL + "/000000000000/shape", ts.URL + "/shape", "http://peer.invalid/shape"} {
		if code, body := jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+u+`","AttributeNames":["All"]}`); code != 200 {
			t.Errorf("queue URL %s answered %d: %s", u, code, body)
		}
	}
}

// A move that is running when the service stops: what the next start finds.
// The task was the process's (L9), so it is not resumed; the record says it
// stopped and how far it got, and the messages it had not reached are still in
// the dead-letter queue, none lost and none doubled.
func TestAStopMidMoveLeavesTheRestWhereItWas(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the service twice")
	}
	pace(t, 0, 500, 20*time.Millisecond)
	dir := t.TempDir()
	boot := func() (*Server, *httptest.Server) {
		s, err := New(Options{DataDir: dir, Logf: dozetest.Logf(t)})
		if err != nil {
			t.Fatal(err)
		}
		return s, httptest.NewServer(s)
	}
	s, ts := boot()
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"stop-dlq"}`)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"stop-src","Attributes":{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:stop-dlq\",\"maxReceiveCount\":\"1\"}"}}`)
	for i := 0; i < 12; i++ {
		jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+ts.URL+`/000000000000/stop-dlq","MessageBody":"m"}`)
	}
	jsonCall(t, ts.URL, "StartMessageMoveTask", `{"SourceArn":"arn:aws:sqs:us-east-1:000000000000:stop-dlq","DestinationArn":"arn:aws:sqs:us-east-1:000000000000:stop-src","MaxNumberOfMessagesPerSecond":1}`)
	time.Sleep(150 * time.Millisecond)
	ts.Close()
	s.Close()

	s, ts = boot()
	defer s.Close()
	defer ts.Close()
	tasks := jsonCall(t, ts.URL, "ListMessageMoveTasks", `{"SourceArn":"arn:aws:sqs:us-east-1:000000000000:stop-dlq","MaxResults":10}`)
	if strings.Contains(tasks, `"RUNNING"`) {
		t.Errorf("a task is RUNNING after a restart, and nothing is running it: %s", tasks)
	}
	if !strings.Contains(tasks, `"CANCELLED"`) {
		t.Errorf("the stopped task is not recorded as CANCELLED: %s", tasks)
	}
	count := func(q string) int {
		got := jsonCall(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+ts.URL+`/000000000000/`+q+`","AttributeNames":["ApproximateNumberOfMessages"]}`)
		m := regexp.MustCompile(`"ApproximateNumberOfMessages":"(\d+)"`).FindStringSubmatch(got)
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if left, moved := count("stop-dlq"), count("stop-src"); left+moved != 12 || left == 0 {
		t.Errorf("after the restart %d are in the dead-letter queue and %d in the source: want 12 in all, and some not moved", left, moved)
	}
	// And a new task can be started: the stopped one does not hold the queue.
	if code, body := jsonBody(t, ts.URL, "StartMessageMoveTask", `{"SourceArn":"arn:aws:sqs:us-east-1:000000000000:stop-dlq"}`); code != 200 {
		t.Errorf("a new move after the restart answered %d: %s", code, body)
	}
}

// model: CreateQueue "returns the URL of the existing queue if the exact same
// names and values are given". A value the model gives a default for reads
// back as that default, so asking again with or without it is the same ask:
// the SQS-40 default of 10 made a re-created RedrivePolicy a conflict with
// itself, and the model's "Default: 300" for KmsDataKeyReusePeriodSeconds the
// same.
func TestRecreatingAQueueWithDefaultedValuesIsTheSameAsk(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"again-dlq"}`)
	rp := `{"QueueName":"again","Attributes":{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:again-dlq\"}","KmsDataKeyReusePeriodSeconds":"300"}}`
	jsonCall(t, ts.URL, "CreateQueue", rp)
	if code, body := jsonBody(t, ts.URL, "CreateQueue", rp); code != 200 {
		t.Errorf("JSON: the same CreateQueue twice answered %d: %s", code, body)
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"again"},
		"Attribute.1.Name": {"RedrivePolicy"}, "Attribute.1.Value": {`{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:again-dlq","maxReceiveCount":10}`}}); code != 200 {
		t.Errorf("Query: the explicit default spelled out answered %d: %s", code, body)
	}
	// And a genuinely different value is still a conflict.
	if code, body := jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"again","Attributes":{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:again-dlq\",\"maxReceiveCount\":3}"}}`); code != 400 || codeOf(body) != "QueueNameExists" && codeOf(body) != "QueueAlreadyExists" {
		t.Errorf("a different maxReceiveCount answered %d %s, want the conflict", code, codeOf(body))
	}
}

// model: GetQueueAttributes' errors name InvalidAttributeName; a name that is
// not an attribute is that, not the generic validation error.
func TestAnUnknownAttributeNameIsInvalidAttributeName(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"names"}`)
	q := ts.URL + "/000000000000/names"
	if code, body := jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+q+`","AttributeNames":["All","Bogus"]}`); code != 400 || codeOf(body) != "InvalidAttributeName" {
		t.Errorf("JSON: %d %s, want 400 InvalidAttributeName: %s", code, codeOf(body), body)
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"GetQueueAttributes"}, "QueueUrl": {q}, "AttributeName.1": {"Bogus"}}); code != 400 || codeOf(body) != "InvalidAttributeName" {
		t.Errorf("Query: %d %s, want 400 InvalidAttributeName: %s", code, codeOf(body), body)
	}
}

// model: RedrivePolicy is an attribute, and a value it cannot read is
// InvalidAttributeValue like every other refusal of one.
func TestAMalformedRedrivePolicyIsInvalidAttributeValue(t *testing.T) {
	ts := testServer(t)
	for _, v := range []string{`{`, `[]`, `"x"`} {
		body, _ := json.Marshal(map[string]any{"QueueName": "rp", "Attributes": map[string]string{"RedrivePolicy": v}})
		if code, out := jsonBody(t, ts.URL, "CreateQueue", string(body)); code != 400 || codeOf(out) != "InvalidAttributeValue" {
			t.Errorf("RedrivePolicy %q: %d %s, want 400 InvalidAttributeValue", v, code, codeOf(out))
		}
	}
}

// model: ChangeMessageVisibility's errors name MessageNotInflight, for a
// message that is not in flight; a message that was deleted is not.
func TestChangingTheVisibilityOfADeletedMessageIsMessageNotInflight(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"gone"}`)
	q := ts.URL + "/000000000000/gone"
	jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"x"}`)
	got := jsonCall(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+q+`"}`)
	m := regexp.MustCompile(`"ReceiptHandle":"([^"]+)"`).FindStringSubmatch(got)
	if m == nil {
		t.Fatal("nothing received")
	}
	jsonCall(t, ts.URL, "DeleteMessage", `{"QueueUrl":"`+q+`","ReceiptHandle":"`+m[1]+`"}`)
	if code, body := jsonBody(t, ts.URL, "ChangeMessageVisibility", `{"QueueUrl":"`+q+`","ReceiptHandle":"`+m[1]+`","VisibilityTimeout":5}`); code != 400 || codeOf(body) != "MessageNotInflight" {
		t.Errorf("JSON: %d %s, want 400 MessageNotInflight", code, codeOf(body))
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"ChangeMessageVisibility"}, "QueueUrl": {q}, "ReceiptHandle": {m[1]}, "VisibilityTimeout": {"5"}}); code != 400 || !strings.Contains(body, "MessageNotInflight") {
		t.Errorf("Query: %d %s, want MessageNotInflight", code, codeOf(body))
	}
}

// model: SendMessageBatchRequestEntry.Id is required and "InvalidBatchEntryId"
// is the code for an id that is not a legal one; on the Query wire an entry
// with an empty Id is an entry, not the end of the list.
func TestAnEmptyBatchEntryIdIsInvalidBatchEntryIdOnBothWires(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"eid"}`)
	q := ts.URL + "/000000000000/eid"
	if code, body := jsonBody(t, ts.URL, "SendMessageBatch", `{"QueueUrl":"`+q+`","Entries":[{"Id":"","MessageBody":"x"}]}`); code != 400 || !strings.Contains(body, "InvalidBatchEntryId") {
		t.Errorf("JSON: %d %s", code, body)
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"SendMessageBatch"}, "QueueUrl": {q},
		"SendMessageBatchRequestEntry.1.Id": {""}, "SendMessageBatchRequestEntry.1.MessageBody": {"x"}}); code != 400 || !strings.Contains(body, "InvalidBatchEntryId") {
		t.Errorf("Query: %d %s", code, body)
	}
}

// Every error shape the model gives an AWS.SimpleQueueService.* Query code is
// answered under its shape's own name on the JSON wire, which is what an SDK's
// errors.As matches on. Read from the model: no hand list to go stale.
func TestEveryQueryCodeHasItsShapeNameOnTheJSONWire(t *testing.T) {
	raw, err := os.ReadFile("../.audit-models/sqs.json")
	if err != nil {
		t.Skipf("no vendored model: %v", err)
	}
	var m struct {
		Shapes map[string]struct {
			Traits map[string]json.RawMessage `json:"traits"`
		} `json:"shapes"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for id, sh := range m.Shapes {
		q, ok := sh.Traits["aws.protocols#awsQueryError"]
		if !ok {
			continue
		}
		var e struct{ Code string }
		if json.Unmarshal(q, &e) != nil || !strings.HasPrefix(e.Code, "AWS.SimpleQueueService.") {
			continue
		}
		shape := id[strings.Index(id, "#")+1:]
		if got := jsonErrorType(e.Code); got != shape {
			t.Errorf("Query code %s is answered as %q on the JSON wire, the model's shape is %q", e.Code, got, shape)
		}
		checked++
	}
	if checked < 8 {
		t.Errorf("only %d error shapes checked; the test is not reading the model", checked)
	}
}

// model: the batch results' Successful and Failed, and
// ListDeadLetterSourceQueues' queueUrls, are required members: on the JSON wire
// an empty list is [] and not an absent member.
func TestRequiredListsAreAlwaysThereOnTheJSONWire(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"req"}`)
	q := ts.URL + "/000000000000/req"
	got := jsonCall(t, ts.URL, "SendMessageBatch", `{"QueueUrl":"`+q+`","Entries":[{"Id":"a","MessageBody":"x"}]}`)
	if !strings.Contains(got, `"Failed":[]`) {
		t.Errorf("SendMessageBatch with nothing failed has no Failed list: %s", got)
	}
	got = jsonCall(t, ts.URL, "DeleteMessageBatch", `{"QueueUrl":"`+q+`","Entries":[{"Id":"a","ReceiptHandle":"not-a-handle"}]}`)
	if !strings.Contains(got, `"Successful":[]`) {
		t.Errorf("DeleteMessageBatch with nothing deleted has no Successful list: %s", got)
	}
	got = jsonCall(t, ts.URL, "ListDeadLetterSourceQueues", `{"QueueUrl":"`+q+`"}`)
	if !strings.Contains(got, `"queueUrls":[]`) {
		t.Errorf("ListDeadLetterSourceQueues with no sources has no queueUrls: %s", got)
	}
}
