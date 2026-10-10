package sqs

// Every operation, once, on the Query wire, spelled the way the API reference
// spells it, and expected to be ACCEPTED.
//
// aws-sdk-go v1 speaks AWS JSON 1.0 for SQS now, so the sdkv1 tests have not
// been exercising the Query/XML wire for some time, and what exercised it was a
// handful of raw posts that all happened to avoid the members whose wire names
// differ from the model's (Tag.N.Key for Tags, SendMessageBatchRequestEntry.N
// for Entries, Attribute.N.Name for Attributes, TagKey.N for TagKeys). The
// model's required-member check read the model's names, found none of them,
// and refused TagQueue, UntagQueue, SetQueueAttributes and all three batch
// operations as "Member must not be null" — on every valid Query request.
//
// doc: the API reference's Query examples for each action.

import (
	"net/url"
	"strings"
	"testing"
)

func TestEveryOperationIsAcceptedOnTheQueryWire(t *testing.T) {
	ts := testServer(t)
	q := ts.URL + "/000000000000/qwire"
	dlqARN := "arn:aws:sqs:us-east-1:000000000000:qwire-dlq"
	formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"qwire-dlq"}})

	// A message in flight, for the receipt-handle operations.
	step := func(name string, f url.Values) string {
		t.Helper()
		code, body := formPost(t, ts.URL, f)
		if code != 200 {
			t.Errorf("%s answered %d: %.300s", name, code, body)
		}
		return body
	}

	step("CreateQueue", url.Values{"Action": {"CreateQueue"}, "QueueName": {"qwire"},
		"Attribute.1.Name": {"VisibilityTimeout"}, "Attribute.1.Value": {"60"},
		"Attribute.2.Name": {"RedrivePolicy"}, "Attribute.2.Value": {`{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":"5"}`},
		"Tag.1.Key": {"env"}, "Tag.1.Value": {"dev"}})
	step("GetQueueUrl", url.Values{"Action": {"GetQueueUrl"}, "QueueName": {"qwire"}})
	step("ListQueues", url.Values{"Action": {"ListQueues"}, "QueueNamePrefix": {"qw"}, "MaxResults": {"5"}})
	step("GetQueueAttributes", url.Values{"Action": {"GetQueueAttributes"}, "QueueUrl": {q}, "AttributeName.1": {"All"}})
	step("SetQueueAttributes", url.Values{"Action": {"SetQueueAttributes"}, "QueueUrl": {q},
		"Attribute.1.Name": {"DelaySeconds"}, "Attribute.1.Value": {"0"},
		"Attribute.2.Name": {"MessageRetentionPeriod"}, "Attribute.2.Value": {"86400"}})
	step("TagQueue", url.Values{"Action": {"TagQueue"}, "QueueUrl": {q}, "Tag.1.Key": {"team"}, "Tag.1.Value": {"core"}, "Tag.2.Key": {"tier"}, "Tag.2.Value": {"1"}})
	tags := step("ListQueueTags", url.Values{"Action": {"ListQueueTags"}, "QueueUrl": {q}})
	for _, want := range []string{"env", "team", "tier"} {
		if !strings.Contains(tags, "<Key>"+want+"</Key>") {
			t.Errorf("ListQueueTags lacks %s: %s", want, tags)
		}
	}
	step("UntagQueue", url.Values{"Action": {"UntagQueue"}, "QueueUrl": {q}, "TagKey.1": {"tier"}})
	step("AddPermission", url.Values{"Action": {"AddPermission"}, "QueueUrl": {q}, "Label": {"friend"},
		"AWSAccountId.1": {"111122223333"}, "ActionName.1": {"SendMessage"}, "ActionName.2": {"ReceiveMessage"}})
	step("RemovePermission", url.Values{"Action": {"RemovePermission"}, "QueueUrl": {q}, "Label": {"friend"}})
	step("ListDeadLetterSourceQueues", url.Values{"Action": {"ListDeadLetterSourceQueues"},
		"QueueUrl": {ts.URL + "/000000000000/qwire-dlq"}})

	step("SendMessage", url.Values{"Action": {"SendMessage"}, "QueueUrl": {q}, "MessageBody": {"one"},
		"MessageAttribute.1.Name": {"k"}, "MessageAttribute.1.Value.DataType": {"String"}, "MessageAttribute.1.Value.StringValue": {"v"},
		"MessageSystemAttribute.1.Name": {"AWSTraceHeader"}, "MessageSystemAttribute.1.Value.DataType": {"String"},
		"MessageSystemAttribute.1.Value.StringValue": {traceA}})
	step("SendMessageBatch", url.Values{"Action": {"SendMessageBatch"}, "QueueUrl": {q},
		"SendMessageBatchRequestEntry.1.Id": {"a"}, "SendMessageBatchRequestEntry.1.MessageBody": {"two"},
		"SendMessageBatchRequestEntry.1.MessageAttribute.1.Name":              {"k"},
		"SendMessageBatchRequestEntry.1.MessageAttribute.1.Value.DataType":    {"String"},
		"SendMessageBatchRequestEntry.1.MessageAttribute.1.Value.StringValue": {"v"},
		"SendMessageBatchRequestEntry.2.Id":                                   {"b"},
		"SendMessageBatchRequestEntry.2.MessageBody":                          {"three"},
		"SendMessageBatchRequestEntry.2.DelaySeconds":                         {"0"}})

	recv := step("ReceiveMessage", url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {q}, "MaxNumberOfMessages": {"3"},
		"AttributeName.1": {"All"}, "MessageAttributeName.1": {"All"}, "VisibilityTimeout": {"120"}})
	handles := between(recv, "<ReceiptHandle>", "</ReceiptHandle>")
	if len(handles) < 3 {
		t.Fatalf("received %d handles: %s", len(handles), recv)
	}
	step("ChangeMessageVisibility", url.Values{"Action": {"ChangeMessageVisibility"}, "QueueUrl": {q},
		"ReceiptHandle": {handles[0]}, "VisibilityTimeout": {"30"}})
	step("ChangeMessageVisibilityBatch", url.Values{"Action": {"ChangeMessageVisibilityBatch"}, "QueueUrl": {q},
		"ChangeMessageVisibilityBatchRequestEntry.1.Id":                {"a"},
		"ChangeMessageVisibilityBatchRequestEntry.1.ReceiptHandle":     {handles[1]},
		"ChangeMessageVisibilityBatchRequestEntry.1.VisibilityTimeout": {"45"}})
	step("DeleteMessage", url.Values{"Action": {"DeleteMessage"}, "QueueUrl": {q}, "ReceiptHandle": {handles[0]}})
	step("DeleteMessageBatch", url.Values{"Action": {"DeleteMessageBatch"}, "QueueUrl": {q},
		"DeleteMessageBatchRequestEntry.1.Id":            {"a"},
		"DeleteMessageBatchRequestEntry.1.ReceiptHandle": {handles[1]}})

	step("StartMessageMoveTask", url.Values{"Action": {"StartMessageMoveTask"}, "SourceArn": {dlqARN}})
	step("ListMessageMoveTasks", url.Values{"Action": {"ListMessageMoveTasks"}, "SourceArn": {dlqARN}, "MaxResults": {"2"}})
	// CancelMessageMoveTask has nothing running to cancel: a refusal is its
	// valid answer, and it must be the service's, not a missing member.
	code, body := formPost(t, ts.URL, url.Values{"Action": {"CancelMessageMoveTask"}, "TaskHandle": {"no-such-task"}})
	if code == 200 || strings.Contains(body, "ValidationError") {
		t.Errorf("CancelMessageMoveTask: %d %.200s", code, body)
	}

	step("PurgeQueue", url.Values{"Action": {"PurgeQueue"}, "QueueUrl": {q}})
	step("DeleteQueue", url.Values{"Action": {"DeleteQueue"}, "QueueUrl": {q}})
}

// between collects what lies between each open and close marker.
func between(s, open, close string) []string {
	var out []string
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return out
		}
		s = s[i+len(open):]
		j := strings.Index(s, close)
		if j < 0 {
			return out
		}
		out = append(out, s[:j])
		s = s[j+len(close):]
	}
}
