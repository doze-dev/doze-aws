package sqs

// The refusals stage 1 closed, held to their code and status on BOTH wires.
//
// The audit of 2026-10-12 found these fixed in the running service and held by
// tests that asserted less: "some 400", an error that was not nil, or a test
// named "...OverQuery" that went through aws-sdk-go v1, which speaks JSON for
// SQS now. Every request here is a raw post, so the wire is the one named.

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

var wireCode = regexp.MustCompile(`<Code>([^<]*)</Code>|"__type":"([^"]*)"`)

func codeOf(body string) string {
	m := wireCode.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

func TestARefusalHasItsCodeAndStatusOnBothWires(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"w"}`)
	q := ts.URL + "/000000000000/w"
	gone := ts.URL + "/000000000000/nope"
	long, wide := strings.Repeat("k", 129), strings.Repeat("v", 257)
	eight := []string{"SendMessage", "ReceiveMessage", "DeleteMessage", "ChangeMessageVisibility", "GetQueueAttributes", "GetQueueUrl", "PurgeQueue", "ListQueues"}
	eightJSON := `"` + strings.Join(eight, `","`) + `"`
	eightForm := func(label string) url.Values {
		v := url.Values{"Action": {"AddPermission"}, "QueueUrl": {q}, "Label": {label}, "AWSAccountId.1": {"111122223333"}}
		for i, a := range eight {
			v.Set("ActionName."+strconv.Itoa(i+1), a)
		}
		return v
	}

	// jsonCode is the shape name, queryCode the awsQueryError code; where the
	// model gives the shape none they are the same word.
	for _, c := range []struct {
		name                string
		op, json            string
		form                url.Values
		status              int
		jsonCode, queryCode string
	}{
		// SQS-5, SQS-10. model: the members' documented ranges; the code is the
		// one "every action can raise for an argument it will not take".
		{"ReceiveMessage MaxNumberOfMessages 11", "ReceiveMessage", `{"QueueUrl":"` + q + `","MaxNumberOfMessages":11}`,
			url.Values{"QueueUrl": {q}, "MaxNumberOfMessages": {"11"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		{"ReceiveMessage MaxNumberOfMessages 0", "ReceiveMessage", `{"QueueUrl":"` + q + `","MaxNumberOfMessages":0}`,
			url.Values{"QueueUrl": {q}, "MaxNumberOfMessages": {"0"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		{"ReceiveMessage WaitTimeSeconds 21", "ReceiveMessage", `{"QueueUrl":"` + q + `","WaitTimeSeconds":21}`,
			url.Values{"QueueUrl": {q}, "WaitTimeSeconds": {"21"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		{"SendMessage DelaySeconds 901", "SendMessage", `{"QueueUrl":"` + q + `","MessageBody":"x","DelaySeconds":901}`,
			url.Values{"QueueUrl": {q}, "MessageBody": {"x"}, "DelaySeconds": {"901"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		// SQS-8. model: SetQueueAttributes' errors name InvalidAttributeValue.
		{"a Policy that is no document", "SetQueueAttributes", `{"QueueUrl":"` + q + `","Attributes":{"Policy":"not json"}}`,
			url.Values{"QueueUrl": {q}, "Attribute.1.Name": {"Policy"}, "Attribute.1.Value": {"not json"}}, 400, "InvalidAttributeValue", "InvalidAttributeValue"},
		{"a Policy with no Principal", "SetQueueAttributes", `{"QueueUrl":"` + q + `","Attributes":{"Policy":"{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"sqs:SendMessage\",\"Resource\":\"*\"}]}"}}`,
			url.Values{"QueueUrl": {q}, "Attribute.1.Name": {"Policy"}, "Attribute.1.Value": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:SendMessage","Resource":"*"}]}`}}, 400, "InvalidAttributeValue", "InvalidAttributeValue"},
		// SQS-9.
		{"a tag key of 129", "TagQueue", `{"QueueUrl":"` + q + `","Tags":{"` + long + `":"v"}}`,
			url.Values{"QueueUrl": {q}, "Tag.1.Key": {long}, "Tag.1.Value": {"v"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		{"a tag value of 257", "TagQueue", `{"QueueUrl":"` + q + `","Tags":{"k":"` + wide + `"}}`,
			url.Values{"QueueUrl": {q}, "Tag.1.Key": {"k"}, "Tag.1.Value": {wide}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		{"a tag key under aws:", "TagQueue", `{"QueueUrl":"` + q + `","Tags":{"aws:x":"v"}}`,
			url.Values{"QueueUrl": {q}, "Tag.1.Key": {"aws:x"}, "Tag.1.Value": {"v"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		// The second tag, not the first: the Query walk used to read one element.
		{"a second tag key of 129", "TagQueue", `{"QueueUrl":"` + q + `","Tags":{"a":"v","` + long + `":"v"}}`,
			url.Values{"QueueUrl": {q}, "Tag.1.Key": {"a"}, "Tag.1.Value": {"v"}, "Tag.2.Key": {long}, "Tag.2.Value": {"v"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		// SQS-15. model: ReceiptHandleIsInvalid, httpError 404.
		{"a malformed receipt handle", "DeleteMessage", `{"QueueUrl":"` + q + `","ReceiptHandle":"zzz"}`,
			url.Values{"QueueUrl": {q}, "ReceiptHandle": {"zzz"}}, 404, "ReceiptHandleIsInvalid", "ReceiptHandleIsInvalid"},
		{"a malformed handle on a visibility change", "ChangeMessageVisibility", `{"QueueUrl":"` + q + `","ReceiptHandle":"zzz","VisibilityTimeout":5}`,
			url.Values{"QueueUrl": {q}, "ReceiptHandle": {"zzz"}, "VisibilityTimeout": {"5"}}, 404, "ReceiptHandleIsInvalid", "ReceiptHandleIsInvalid"},
		// model: QueueDoesNotExist, Query code AWS.SimpleQueueService.NonExistentQueue, 400.
		{"a queue that is not there", "SendMessage", `{"QueueUrl":"` + gone + `","MessageBody":"x"}`,
			url.Values{"QueueUrl": {gone}, "MessageBody": {"x"}}, 400, "QueueDoesNotExist", "AWS.SimpleQueueService.NonExistentQueue"},
		// SQS-17. model: ResourceNotFoundException, httpError 404.
		{"a move task on a missing queue", "StartMessageMoveTask", `{"SourceArn":"arn:aws:sqs:us-east-1:000000000000:nope"}`,
			url.Values{"SourceArn": {"arn:aws:sqs:us-east-1:000000000000:nope"}}, 404, "ResourceNotFoundException", "ResourceNotFoundException"},
		{"listing move tasks of a missing queue", "ListMessageMoveTasks", `{"SourceArn":"arn:aws:sqs:us-east-1:000000000000:nope"}`,
			url.Values{"SourceArn": {"arn:aws:sqs:us-east-1:000000000000:nope"}}, 404, "ResourceNotFoundException", "ResourceNotFoundException"},
		{"cancelling a task that is not there", "CancelMessageMoveTask", `{"TaskHandle":"no-such-task"}`,
			url.Values{"TaskHandle": {"no-such-task"}}, 404, "ResourceNotFoundException", "ResourceNotFoundException"},
		// SQS-18. model: AddPermission's errors name OverLimit, httpError 403.
		{"eight actions in one permission", "AddPermission", `{"QueueUrl":"` + q + `","Label":"l","AWSAccountIds":["111122223333"],"Actions":[` + eightJSON + `]}`,
			eightForm("l"), 403, "OverLimit", "OverLimit"},
		{"an ARN as the principal", "AddPermission", `{"QueueUrl":"` + q + `","Label":"l2","AWSAccountIds":["arn:aws:iam::111122223333:root"],"Actions":["SendMessage"]}`,
			url.Values{"QueueUrl": {q}, "Label": {"l2"}, "AWSAccountId.1": {"arn:aws:iam::111122223333:root"}, "ActionName.1": {"SendMessage"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
		{"removing a label that is not there", "RemovePermission", `{"QueueUrl":"` + q + `","Label":"nobody"}`,
			url.Values{"QueueUrl": {q}, "Label": {"nobody"}}, 400, "InvalidParameterValue", "InvalidParameterValue"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if code, body := jsonBody(t, ts.URL, c.op, c.json); code != c.status || codeOf(body) != c.jsonCode {
				t.Errorf("JSON: %d %s, want %d %s\n%.300s", code, codeOf(body), c.status, c.jsonCode, body)
			}
			c.form.Set("Action", c.op)
			if code, body := formPost(t, ts.URL, c.form); code != c.status || codeOf(body) != c.queryCode {
				t.Errorf("Query: %d %s, want %d %s\n%.300s", code, codeOf(body), c.status, c.queryCode, body)
			}
		})
	}
}

// SQS-6, on the wire it is named for.
func TestListQueuesPagesOnTheQueryWire(t *testing.T) {
	ts := testServer(t)
	names := []string{"pq-a", "pq-b", "pq-c", "pq-d", "pq-e"}
	for _, n := range names {
		formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {n}})
	}
	urlRE := regexp.MustCompile(`<QueueUrl>[^<]*/([^</]+)</QueueUrl>`)
	tokenRE := regexp.MustCompile(`<NextToken>([^<]+)</NextToken>`)
	dozetest.AssertPaginates(t, func(limit int, token string) ([]string, string, error) {
		f := url.Values{"Action": {"ListQueues"}, "QueueNamePrefix": {"pq-"}, "MaxResults": {strconv.Itoa(limit)}}
		if token != "" {
			f.Set("NextToken", token)
		}
		code, body := formPost(t, ts.URL, f)
		if code != 200 {
			return nil, "", &apiError{Code: codeOf(body), Status: code, Message: body}
		}
		var ids []string
		for _, m := range urlRE.FindAllStringSubmatch(body, -1) {
			ids = append(ids, m[1])
		}
		next := ""
		if m := tokenRE.FindStringSubmatch(body); m != nil {
			next = m[1]
		}
		return ids, next, nil
	}, names, dozetest.PagingOptions{Ordered: true, BadToken: "not-a-token"})
}

// SQS-19, through the wire. model: ChangeMessageVisibility "the new timeout
// period takes effect from the time you call the ChangeMessageVisibility action",
// and the total is at most 12 hours from the receive: an attempt past it "results
// in an error" rather than being shortened.
func TestTwelveHoursFromTheReceiveOnBothWires(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"twelve"}`)
	q := ts.URL + "/000000000000/twelve"
	jsonCall(t, ts.URL, "SendMessage", `{"QueueUrl":"`+q+`","MessageBody":"x"}`)
	got := jsonCall(t, ts.URL, "ReceiveMessage", `{"QueueUrl":"`+q+`"}`)
	m := regexp.MustCompile(`"ReceiptHandle":"([^"]+)"`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("nothing received: %s", got)
	}
	time.Sleep(1100 * time.Millisecond) // a second of the twelve hours is spent
	if code, body := jsonBody(t, ts.URL, "ChangeMessageVisibility", `{"QueueUrl":"`+q+`","ReceiptHandle":"`+m[1]+`","VisibilityTimeout":43200}`); code != 400 || codeOf(body) != "InvalidParameterValue" {
		t.Errorf("JSON: twelve more hours a second after the receive: %d %s\n%.300s", code, codeOf(body), body)
	}
	if code, body := formPost(t, ts.URL, url.Values{"Action": {"ChangeMessageVisibility"}, "QueueUrl": {q}, "ReceiptHandle": {m[1]}, "VisibilityTimeout": {"43200"}}); code != 400 || codeOf(body) != "InvalidParameterValue" {
		t.Errorf("Query: twelve more hours a second after the receive: %d %s\n%.300s", code, codeOf(body), body)
	}
	if code, body := jsonBody(t, ts.URL, "ChangeMessageVisibility", `{"QueueUrl":"`+q+`","ReceiptHandle":"`+m[1]+`","VisibilityTimeout":43000}`); code != 200 {
		t.Errorf("a change inside the twelve hours answered %d: %s", code, body)
	}
}

// SQS-20, on the Query wire: a batch entry's trace header is kept, digested and
// returned.
func TestABatchEntrysTraceHeaderOnTheQueryWire(t *testing.T) {
	ts := testServer(t)
	formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"traced"}})
	q := ts.URL + "/000000000000/traced"
	const header = "Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1"
	code, body := formPost(t, ts.URL, url.Values{"Action": {"SendMessageBatch"}, "QueueUrl": {q},
		"SendMessageBatchRequestEntry.1.Id": {"a"}, "SendMessageBatchRequestEntry.1.MessageBody": {"one"},
		"SendMessageBatchRequestEntry.1.MessageSystemAttribute.1.Name":              {"AWSTraceHeader"},
		"SendMessageBatchRequestEntry.1.MessageSystemAttribute.1.Value.DataType":    {"String"},
		"SendMessageBatchRequestEntry.1.MessageSystemAttribute.1.Value.StringValue": {header},
		"SendMessageBatchRequestEntry.2.Id":                                         {"b"}, "SendMessageBatchRequestEntry.2.MessageBody": {"two"},
		"SendMessageBatchRequestEntry.2.MessageSystemAttribute.1.Name":              {"NotTheTraceHeader"},
		"SendMessageBatchRequestEntry.2.MessageSystemAttribute.1.Value.DataType":    {"String"},
		"SendMessageBatchRequestEntry.2.MessageSystemAttribute.1.Value.StringValue": {"x"}})
	if code != 200 || !strings.Contains(body, "<MD5OfMessageSystemAttributes>") {
		t.Fatalf("the batch: %d %s", code, body)
	}
	// The entry with another system attribute fails alone.
	if !strings.Contains(body, "<BatchResultErrorEntry>") || !strings.Contains(body, "<Id>b</Id>") || strings.Count(body, "<SendMessageBatchResultEntry>") != 1 {
		t.Errorf("entry b should have failed alone and entry a been sent: %s", body)
	}
	_, got := formPost(t, ts.URL, url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {q}, "MaxNumberOfMessages": {"10"}, "MessageSystemAttributeName.1": {"AWSTraceHeader"}})
	if !strings.Contains(got, "<Name>AWSTraceHeader</Name>") || !strings.Contains(got, header) {
		t.Errorf("the trace header did not come back on the Query wire: %s", got)
	}
}

// SQS-22, on the Query wire: a retry with the same attempt id is the same
// receive.
func TestReceiveRequestAttemptIdOnTheQueryWire(t *testing.T) {
	ts := testServer(t)
	formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"again.fifo"}, "Attribute.1.Name": {"FifoQueue"}, "Attribute.1.Value": {"true"}})
	q := ts.URL + "/000000000000/again.fifo"
	formPost(t, ts.URL, url.Values{"Action": {"SendMessage"}, "QueueUrl": {q}, "MessageBody": {"x"}, "MessageGroupId": {"g"}, "MessageDeduplicationId": {"d"}})
	receive := func() string {
		_, body := formPost(t, ts.URL, url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {q}, "ReceiveRequestAttemptId": {"attempt-1"}})
		m := regexp.MustCompile(`<ReceiptHandle>([^<]+)</ReceiptHandle>`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("nothing received: %s", body)
		}
		return m[1]
	}
	if first, second := receive(), receive(); first != second {
		t.Errorf("the retry was a new receive: handle %q then %q", first, second)
	}
}
