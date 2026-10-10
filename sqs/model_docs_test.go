package sqs

// What the SQS service model's own documentation says, which the first pass
// over these rules took from memory and from the developer guide's older pages.
// The model (`.audit-models/sqs.json`) is the current statement, and where it
// differs it is the one a deploy meets:
//
//   - a message and a batch are 1 MiB, not 256 KiB (SendMessage, SendMessageBatch,
//     the MaximumMessageSize attribute: "1,024 bytes to 1,048,576 bytes, default
//     1,048,576");
//   - MessageGroupId is accepted on a standard queue ("fair queues");
//   - a body with a character outside the set is InvalidMessageContents;
//   - a receipt handle that is not one is ReceiptHandleIsInvalid;
//   - the move-task operations say ResourceNotFoundException for a queue that
//     is not there, and ListMessageMoveTasks has an upper limit of 10;
//   - "adding more than 50 tags to a queue isn't recommended" is advice, not a
//     refusal;
//   - AddPermission's Actions are "the name of any action or *".

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestMessageSizeIsOneMebibyte(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"big"}`)
	qurl := ts.URL + "/000000000000/big"

	_, body := jsonBody(t, ts.URL, "GetQueueAttributes", `{"QueueUrl":"`+qurl+`","AttributeNames":["MaximumMessageSize"]}`)
	if !strings.Contains(body, `"1048576"`) {
		t.Errorf("a new queue's MaximumMessageSize reads %s, want 1048576", body)
	}

	send := func(n int) int {
		code, _ := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+qurl+`","MessageBody":"`+strings.Repeat("x", n)+`"}`)
		return code
	}
	if got := send(1048576); got != 200 {
		t.Errorf("a body of 1 MiB answered %d, want 200", got)
	}
	if got := send(1048577); got != 400 {
		t.Errorf("a body of 1 MiB + 1 answered %d, want 400", got)
	}

	// The attribute's own range.
	for v, ok := range map[string]bool{"1023": false, "1024": true, "1048576": true, "1048577": false} {
		code, _ := jsonBody(t, ts.URL, "SetQueueAttributes",
			`{"QueueUrl":"`+qurl+`","Attributes":{"MaximumMessageSize":"`+v+`"}}`)
		if ok && code != 200 || !ok && code != 400 {
			t.Errorf("MaximumMessageSize %s answered %d, ok=%v", v, code, ok)
		}
	}
}

func TestABatchMayTotalOneMebibyte(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"bigbatch"}`)
	qurl := ts.URL + "/000000000000/bigbatch"
	batch := func(sizes ...int) (int, string) {
		var e []string
		for i, n := range sizes {
			e = append(e, `{"Id":"m`+strconv.Itoa(i)+`","MessageBody":"`+strings.Repeat("x", n)+`"}`)
		}
		return jsonBody(t, ts.URL, "SendMessageBatch", `{"QueueUrl":"`+qurl+`","Entries":[`+strings.Join(e, ",")+`]}`)
	}
	if code, body := batch(262144, 262144, 262144, 262144); code != 200 {
		t.Errorf("four messages of 256 KiB answered %d: %.200s", code, body)
	}
	code, body := batch(262144, 262144, 262144, 262145)
	if code != 400 || !strings.Contains(body, "BatchRequestTooLong") {
		t.Errorf("a batch of 1 MiB + 1 answered %d: %.200s", code, body)
	}
}

func TestAStandardQueueTakesAMessageGroupId(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"fair"}`)
	qurl := ts.URL + "/000000000000/fair"

	for _, tenant := range []string{"tenant-a", "tenant-a", "tenant-b"} {
		code, body := jsonBody(t, ts.URL, "SendMessage",
			`{"QueueUrl":"`+qurl+`","MessageBody":"x","MessageGroupId":"`+tenant+`"}`)
		if code != 200 {
			t.Fatalf("a standard queue refused MessageGroupId %s: %d %.200s", tenant, code, body)
		}
	}
	// Not a FIFO group: nothing is held back for another message of the same group.
	_, body := jsonBody(t, ts.URL, "ReceiveMessage",
		`{"QueueUrl":"`+qurl+`","MaxNumberOfMessages":10,"MessageSystemAttributeNames":["MessageGroupId"]}`)
	if got := strings.Count(body, `"MessageGroupId"`); got != 3 {
		t.Errorf("one receive returned %d messages with a MessageGroupId, want all 3: %.400s", got, body)
	}

	// The format is still held to.
	code, _ := jsonBody(t, ts.URL, "SendMessage",
		`{"QueueUrl":"`+qurl+`","MessageBody":"x","MessageGroupId":"`+strings.Repeat("g", 129)+`"}`)
	if code != 400 {
		t.Errorf("a MessageGroupId of 129 characters answered %d, want 400", code)
	}
	// And in a batch.
	code, body = jsonBody(t, ts.URL, "SendMessageBatch",
		`{"QueueUrl":"`+qurl+`","Entries":[{"Id":"a","MessageBody":"x","MessageGroupId":"t"}]}`)
	if code != 200 || strings.Contains(body, `"Failed"`) && !strings.Contains(body, `"Failed":[]`) {
		t.Errorf("a batch entry with a MessageGroupId on a standard queue: %d %.200s", code, body)
	}
}

func TestInvalidCharactersAreInvalidMessageContents(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"chars"}`)
	qurl := ts.URL + "/000000000000/chars"

	code, body := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+qurl+`","MessageBody":"a\u0000b"}`)
	if code != 400 || !strings.Contains(body, "InvalidMessageContents") {
		t.Errorf("JSON: %d %.200s, want 400 InvalidMessageContents", code, body)
	}
	code, body = formPost(t, ts.URL, url.Values{"Action": {"SendMessage"}, "QueueUrl": {qurl}, "MessageBody": {"a\x01b"}})
	if code != 400 || !strings.Contains(body, "InvalidMessageContents") {
		t.Errorf("Query: %d %.200s, want 400 InvalidMessageContents", code, body)
	}
	// In a batch it is that entry's code.
	_, body = jsonBody(t, ts.URL, "SendMessageBatch",
		`{"QueueUrl":"`+qurl+`","Entries":[{"Id":"ok","MessageBody":"fine"},{"Id":"bad","MessageBody":"a\u0000b"}]}`)
	if !strings.Contains(body, "InvalidMessageContents") || !strings.Contains(body, `"ok"`) {
		t.Errorf("batch: %.300s, want the bad entry failed with InvalidMessageContents and the other sent", body)
	}
}

func TestAMalformedReceiptHandleIsReceiptHandleIsInvalid(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"handles"}`)
	qurl := ts.URL + "/000000000000/handles"

	for _, c := range []struct{ action, extra string }{
		{"DeleteMessage", ""},
		{"ChangeMessageVisibility", `,"VisibilityTimeout":5`},
	} {
		code, body := jsonBody(t, ts.URL, c.action, `{"QueueUrl":"`+qurl+`","ReceiptHandle":"not-a-handle"`+c.extra+`}`)
		if code/100 != 4 || !strings.Contains(body, "ReceiptHandleIsInvalid") {
			t.Errorf("JSON %s: %d %.200s, want ReceiptHandleIsInvalid", c.action, code, body)
		}
		form := url.Values{"Action": {c.action}, "QueueUrl": {qurl}, "ReceiptHandle": {"not-a-handle"}}
		if c.extra != "" {
			form.Set("VisibilityTimeout", "5")
		}
		code, body = formPost(t, ts.URL, form)
		if code/100 != 4 || !strings.Contains(body, "ReceiptHandleIsInvalid") {
			t.Errorf("Query %s: %d %.200s, want ReceiptHandleIsInvalid", c.action, code, body)
		}
	}
	// In a batch it is that entry's code.
	_, body := jsonBody(t, ts.URL, "DeleteMessageBatch",
		`{"QueueUrl":"`+qurl+`","Entries":[{"Id":"a","ReceiptHandle":"not-a-handle"}]}`)
	if !strings.Contains(body, "ReceiptHandleIsInvalid") {
		t.Errorf("DeleteMessageBatch: %.300s, want the entry failed with ReceiptHandleIsInvalid", body)
	}
}

func TestMoveTaskOperationsSayResourceNotFound(t *testing.T) {
	ts := testServer(t)
	missing := "arn:aws:sqs:us-east-1:000000000000:nowhere"
	for _, c := range []struct{ action, body string }{
		{"StartMessageMoveTask", `{"SourceArn":"` + missing + `"}`},
		{"ListMessageMoveTasks", `{"SourceArn":"` + missing + `"}`},
	} {
		code, body := jsonBody(t, ts.URL, c.action, c.body)
		if code/100 != 4 || !strings.Contains(body, "ResourceNotFoundException") {
			t.Errorf("%s on a queue that is not there: %d %.200s, want ResourceNotFoundException", c.action, code, body)
		}
	}
}

func TestListMessageMoveTasksUpperLimitIsTen(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"dlq-lim"}`)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"src-lim","Attributes":{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:dlq-lim\",\"maxReceiveCount\":\"3\"}"}}`)
	arn := "arn:aws:sqs:us-east-1:000000000000:dlq-lim"
	for n, ok := range map[int]bool{0: false, 1: true, 10: true, 11: false} {
		code, body := jsonBody(t, ts.URL, "ListMessageMoveTasks",
			`{"SourceArn":"`+arn+`","MaxResults":`+strconv.Itoa(n)+`}`)
		if ok && code != 200 || !ok && code != 400 {
			t.Errorf("MaxResults %d answered %d, ok=%v: %.200s", n, code, ok, body)
		}
	}
}

func TestMoreThanFiftyTagsIsAdviceNotARefusal(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"tagged"}`)
	qurl := ts.URL + "/000000000000/tagged"
	var tags []string
	for i := 0; i < 55; i++ {
		tags = append(tags, `"k`+strconv.Itoa(i)+`":"v"`)
	}
	code, body := jsonBody(t, ts.URL, "TagQueue", `{"QueueUrl":"`+qurl+`","Tags":{`+strings.Join(tags, ",")+`}}`)
	if code != 200 {
		t.Errorf("55 tags answered %d: %.200s — the model says only that more than 50 isn't recommended", code, body)
	}
}

func TestAddPermissionTakesTheNameOfAnyAction(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"anyact"}`)
	qurl := ts.URL + "/000000000000/anyact"
	i := 0
	for _, a := range []string{"CreateQueue", "SetQueueAttributes", "ListQueueTags", "TagQueue", "StartMessageMoveTask", "*"} {
		i++
		code, body := jsonBody(t, ts.URL, "AddPermission", `{"QueueUrl":"`+qurl+`","Label":"l`+strconv.Itoa(i)+
			`","AWSAccountIds":["111122223333"],"Actions":["`+a+`"]}`)
		if code != 200 {
			t.Errorf("AddPermission with Actions [%s] answered %d: %.200s", a, code, body)
		}
	}
}

// "An integer representing seconds, between 60 seconds (1 minute) and 86,400
// seconds (24 hours)"; and "Valid values are true and false" for the three
// boolean attributes.
func TestKmsReusePeriodAndTheBooleansAreHeldToTheirValues(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"enc"}`)
	qurl := ts.URL + "/000000000000/enc"
	set := func(name, v string) int {
		code, _ := jsonBody(t, ts.URL, "SetQueueAttributes",
			`{"QueueUrl":"`+qurl+`","Attributes":{"`+name+`":"`+v+`"}}`)
		return code
	}
	for v, ok := range map[string]bool{"59": false, "60": true, "86400": true, "86401": false, "soon": false} {
		if got := set("KmsDataKeyReusePeriodSeconds", v); ok && got != 200 || !ok && got != 400 {
			t.Errorf("KmsDataKeyReusePeriodSeconds %q answered %d, ok=%v", v, got, ok)
		}
	}
	if got := set("SqsManagedSseEnabled", "perhaps"); got != 400 {
		t.Errorf("SqsManagedSseEnabled perhaps answered %d, want 400", got)
	}
	if got := set("ContentBasedDeduplication", "yes"); got != 400 {
		t.Errorf("ContentBasedDeduplication yes answered %d, want 400", got)
	}
	code, _ := jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"maybe.fifo","Attributes":{"FifoQueue":"yes"}}`)
	if code != 400 {
		t.Errorf("FifoQueue yes answered %d, want 400", code)
	}
}

// ChangeMessageVisibility: "If you attempt to set the VisibilityTimeout to a
// value greater than the maximum time left, Amazon SQS returns an error." The
// maximum is 12 hours from the receive, so the time left shrinks as the
// message stays in flight, and a new receive starts the count again.
func TestAVisibilityChangeCannotPassTwelveHoursFromTheReceive(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	if _, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "3600"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "m", nil, -1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Receive("q", 1, 0, -1)
	if err != nil || len(got) != 1 {
		t.Fatalf("receive: %v %v", got, err)
	}
	h := got[0].Handle()

	// At the receive, the whole twelve hours are left.
	if err := s.ChangeVisibility("q", h, 43200); err != nil {
		t.Fatalf("12 hours from the receive: %v", err)
	}
	// Eleven hours on, an hour is left: an hour and a second is too much.
	now = now.Add(11 * time.Hour)
	if err := s.ChangeVisibility("q", h, 3601); err == nil {
		t.Error("11 hours in, a change to 3601 seconds passes the 12 hours and was accepted")
	}
	if err := s.ChangeVisibility("q", h, 3600); err != nil {
		t.Errorf("11 hours in, a change to the 3600 seconds left: %v", err)
	}
	// A new receive is a new count.
	now = now.Add(time.Hour + time.Second)
	got, err = s.Receive("q", 1, 0, -1)
	if err != nil || len(got) != 1 {
		t.Fatalf("second receive: %v %v", got, err)
	}
	if err := s.ChangeVisibility("q", got[0].Handle(), 43200); err != nil {
		t.Errorf("after a fresh receive: %v", err)
	}
}

// A queue's URL is https://host/<account>/<name>, and the account is its
// owner. A queue in another account is not this one, whatever its name:
// resolving it by name alone sends a hard-coded account id through locally
// and fails on deploy.
func TestAQueueInAnotherAccountIsNotThisQueue(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"mine"}`)
	code, _ := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+ts.URL+`/000000000000/mine","MessageBody":"x"}`)
	if code != 200 {
		t.Fatalf("this account's queue URL answered %d", code)
	}
	code, body := jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+ts.URL+`/999999999999/mine","MessageBody":"x"}`)
	if code != 400 || !strings.Contains(body, "QueueDoesNotExist") {
		t.Errorf("another account's URL answered %d %.200s, want QueueDoesNotExist", code, body)
	}
	code, body = jsonBody(t, ts.URL, "GetQueueUrl", `{"QueueName":"mine","QueueOwnerAWSAccountId":"999999999999"}`)
	if code != 400 || !strings.Contains(body, "QueueDoesNotExist") {
		t.Errorf("GetQueueUrl for another owner answered %d %.200s, want QueueDoesNotExist", code, body)
	}
	code, _ = jsonBody(t, ts.URL, "GetQueueUrl", `{"QueueName":"mine","QueueOwnerAWSAccountId":"000000000000"}`)
	if code != 200 {
		t.Errorf("GetQueueUrl for this owner answered %d", code)
	}
}

// ListMessageMoveTasksResultEntry: the TaskHandle "is only populated for tasks
// that are in RUNNING status" and the DestinationArn is absent when the task
// named none.
func TestAFinishedTasksEntryHasNoHandleAndNoInventedDestination(t *testing.T) {
	f := newRedriveFixture(t, "dlq")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	f.deadLetter("orders", "one")
	if _, err := f.c.StartMessageMoveTask(f.ctx, &awssqs.StartMessageMoveTaskInput{SourceArn: aws.String(f.arns["dlq"])}); err != nil {
		t.Fatal(err)
	}
	list, err := f.c.ListMessageMoveTasks(f.ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(f.arns["dlq"])})
	if err != nil || len(list.Results) != 1 {
		t.Fatalf("ListMessageMoveTasks: %v %v", list, err)
	}
	r := list.Results[0]
	if aws.ToString(r.Status) != "COMPLETED" {
		t.Fatalf("status %q", aws.ToString(r.Status))
	}
	if r.TaskHandle != nil {
		t.Errorf("a COMPLETED task lists TaskHandle %q; only a RUNNING one does", aws.ToString(r.TaskHandle))
	}
	if r.DestinationArn != nil {
		t.Errorf("a task started with no DestinationArn lists %q", aws.ToString(r.DestinationArn))
	}
}

// AddPermission: "can have a maximum of seven actions per statement" and "does
// not support adding a non-account principal".
func TestAddPermissionTakesAccountsAndAtMostSevenActions(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"seven"}`)
	qurl := ts.URL + "/000000000000/seven"
	add := func(label, accounts, actions string) int {
		code, _ := jsonBody(t, ts.URL, "AddPermission", `{"QueueUrl":"`+qurl+`","Label":"`+label+
			`","AWSAccountIds":`+accounts+`,"Actions":`+actions+`}`)
		return code
	}
	seven := `["SendMessage","ReceiveMessage","DeleteMessage","ChangeMessageVisibility","GetQueueAttributes","GetQueueUrl","PurgeQueue"]`
	eight := `["SendMessage","ReceiveMessage","DeleteMessage","ChangeMessageVisibility","GetQueueAttributes","GetQueueUrl","PurgeQueue","ListQueues"]`
	if got := add("seven", `["111122223333"]`, seven); got != 200 {
		t.Errorf("seven actions answered %d, want 200", got)
	}
	if got := add("eight", `["111122223333"]`, eight); got < 400 {
		t.Errorf("eight actions answered %d, want a refusal", got)
	}
	if got := add("arn", `["arn:aws:iam::111122223333:user/alice"]`, `["SendMessage"]`); got != 400 {
		t.Errorf("a user ARN as the principal answered %d, want 400", got)
	}
}
