package sqs

// The model's constraints are written against one shape, and a Query request
// has to be rendered into it whole. Rendering only the first element of each
// list, and leaving a flattened map as a list of pairs, checked the first batch
// entry and none of the attributes: a second entry with no body was refused
// whole on JSON and answered as one failed entry on Query.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func jsonParams(t *testing.T, body string) params {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatal(err)
	}
	return params{obj: obj}
}

// scalars renders every leaf as its string, because the Query wire has no
// types and the validator reads numeric strings as numbers.
func scalars(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = scalars(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = scalars(e)
		}
		return out
	case nil:
		return nil
	default:
		return fmt.Sprint(x)
	}
}

func TestAQueryRequestIsRenderedIntoTheSameShapeAsItsJSONTwin(t *testing.T) {
	for _, c := range []struct {
		name string
		json string
		form url.Values
	}{
		{"SendMessage with attributes", `{"QueueUrl":"u","MessageBody":"b","MessageAttributes":{
			"k":{"DataType":"String","StringValue":"v"},"n":{"DataType":"Number","StringValue":"1"}},
			"MessageSystemAttributes":{"AWSTraceHeader":{"DataType":"String","StringValue":"Root=1"}}}`,
			url.Values{"QueueUrl": {"u"}, "MessageBody": {"b"},
				"MessageAttribute.1.Name": {"k"}, "MessageAttribute.1.Value.DataType": {"String"}, "MessageAttribute.1.Value.StringValue": {"v"},
				"MessageAttribute.2.Name": {"n"}, "MessageAttribute.2.Value.DataType": {"Number"}, "MessageAttribute.2.Value.StringValue": {"1"},
				"MessageSystemAttribute.1.Name": {"AWSTraceHeader"}, "MessageSystemAttribute.1.Value.DataType": {"String"},
				"MessageSystemAttribute.1.Value.StringValue": {"Root=1"}}},
		{"SendMessageBatch of two", `{"QueueUrl":"u","Entries":[
			{"Id":"a","MessageBody":"x","MessageAttributes":{"k":{"DataType":"String","StringValue":"v"}}},
			{"Id":"b","MessageBody":"y"}]}`,
			url.Values{"QueueUrl": {"u"},
				"SendMessageBatchRequestEntry.1.Id": {"a"}, "SendMessageBatchRequestEntry.1.MessageBody": {"x"},
				"SendMessageBatchRequestEntry.1.MessageAttribute.1.Name":              {"k"},
				"SendMessageBatchRequestEntry.1.MessageAttribute.1.Value.DataType":    {"String"},
				"SendMessageBatchRequestEntry.1.MessageAttribute.1.Value.StringValue": {"v"},
				"SendMessageBatchRequestEntry.2.Id":                                   {"b"}, "SendMessageBatchRequestEntry.2.MessageBody": {"y"}}},
		{"DeleteMessageBatch of two", `{"QueueUrl":"u","Entries":[{"Id":"a","ReceiptHandle":"h1"},{"Id":"b","ReceiptHandle":"h2"}]}`,
			url.Values{"QueueUrl": {"u"},
				"DeleteMessageBatchRequestEntry.1.Id": {"a"}, "DeleteMessageBatchRequestEntry.1.ReceiptHandle": {"h1"},
				"DeleteMessageBatchRequestEntry.2.Id": {"b"}, "DeleteMessageBatchRequestEntry.2.ReceiptHandle": {"h2"}}},
		{"ChangeMessageVisibilityBatch", `{"QueueUrl":"u","Entries":[{"Id":"a","ReceiptHandle":"h","VisibilityTimeout":5}]}`,
			url.Values{"QueueUrl": {"u"},
				"ChangeMessageVisibilityBatchRequestEntry.1.Id":                {"a"},
				"ChangeMessageVisibilityBatchRequestEntry.1.ReceiptHandle":     {"h"},
				"ChangeMessageVisibilityBatchRequestEntry.1.VisibilityTimeout": {"5"}}},
		{"SetQueueAttributes", `{"QueueUrl":"u","Attributes":{"DelaySeconds":"5","VisibilityTimeout":"9"}}`,
			url.Values{"QueueUrl": {"u"}, "Attribute.1.Name": {"DelaySeconds"}, "Attribute.1.Value": {"5"},
				"Attribute.2.Name": {"VisibilityTimeout"}, "Attribute.2.Value": {"9"}}},
		{"TagQueue", `{"QueueUrl":"u","Tags":{"a":"1","b":"2"}}`,
			url.Values{"QueueUrl": {"u"}, "Tag.1.Key": {"a"}, "Tag.1.Value": {"1"}, "Tag.2.Key": {"b"}, "Tag.2.Value": {"2"}}},
		{"UntagQueue", `{"QueueUrl":"u","TagKeys":["a","b"]}`,
			url.Values{"QueueUrl": {"u"}, "TagKey.1": {"a"}, "TagKey.2": {"b"}}},
		{"AddPermission", `{"QueueUrl":"u","Label":"l","AWSAccountIds":["1","2"],"Actions":["SendMessage","ReceiveMessage"]}`,
			url.Values{"QueueUrl": {"u"}, "Label": {"l"}, "AWSAccountId.1": {"1"}, "AWSAccountId.2": {"2"},
				"ActionName.1": {"SendMessage"}, "ActionName.2": {"ReceiveMessage"}}},
		{"GetQueueAttributes", `{"QueueUrl":"u","AttributeNames":["All","DelaySeconds"]}`,
			url.Values{"QueueUrl": {"u"}, "AttributeName.1": {"All"}, "AttributeName.2": {"DelaySeconds"}}},
		{"ReceiveMessage", `{"QueueUrl":"u","MessageAttributeNames":["a","b.*"],"MessageSystemAttributeNames":["All","SentTimestamp"]}`,
			url.Values{"QueueUrl": {"u"}, "MessageAttributeName.1": {"a"}, "MessageAttributeName.2": {"b.*"},
				"AttributeName.1": {"All"}, "AttributeName.2": {"SentTimestamp"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := scalars(jsonParams(t, c.json).asMap())
			got := scalars(params{form: c.form}.asMap())
			// A Query request also carries what it was spelled as; compare the
			// members the JSON twin has.
			wm, gm := want.(map[string]any), got.(map[string]any)
			for k, w := range wm {
				if !reflect.DeepEqual(gm[k], w) {
					t.Errorf("%s: Query renders %v, JSON renders %v", k, gm[k], w)
				}
			}
		})
	}
}

func TestEveryEntryOfAQueryBatchIsValidated(t *testing.T) {
	ts := testServer(t)
	formPost(t, ts.URL, url.Values{"Action": {"CreateQueue"}, "QueueName": {"shape"}})
	q := ts.URL + "/000000000000/shape"
	// The second entry has no MessageBody: the whole request is refused, as on JSON.
	code, body := formPost(t, ts.URL, url.Values{"Action": {"SendMessageBatch"}, "QueueUrl": {q},
		"SendMessageBatchRequestEntry.1.Id": {"a"}, "SendMessageBatchRequestEntry.1.MessageBody": {"x"},
		"SendMessageBatchRequestEntry.2.Id": {"b"}})
	if code != 400 || !strings.Contains(body, "ValidationError") {
		t.Errorf("Query: %d %.200s, want the request refused for the second entry", code, body)
	}
	jcode, jbody := jsonBody(t, ts.URL, "SendMessageBatch", `{"QueueUrl":"`+q+`","Entries":[{"Id":"a","MessageBody":"x"},{"Id":"b"}]}`)
	if jcode != 400 || !strings.Contains(jbody, "ValidationException") {
		t.Errorf("JSON: %d %.200s", jcode, jbody)
	}
	// A DeleteMessageBatch entry with no receipt handle, second of two.
	code, body = formPost(t, ts.URL, url.Values{"Action": {"DeleteMessageBatch"}, "QueueUrl": {q},
		"DeleteMessageBatchRequestEntry.1.Id": {"a"}, "DeleteMessageBatchRequestEntry.1.ReceiptHandle": {"AAAAAAAAAAFhYWE="},
		"DeleteMessageBatchRequestEntry.2.Id": {"b"}})
	if code != 400 || !strings.Contains(body, "ValidationError") {
		t.Errorf("DeleteMessageBatch Query: %d %.200s", code, body)
	}
	// A message attribute with no DataType, on Query.
	code, body = formPost(t, ts.URL, url.Values{"Action": {"SendMessage"}, "QueueUrl": {q}, "MessageBody": {"x"},
		"MessageAttribute.1.Name": {"k"}, "MessageAttribute.1.Value.StringValue": {"v"}})
	if code != 400 || !strings.Contains(body, "ValidationError") {
		t.Errorf("an attribute with no DataType over Query: %d %.200s", code, body)
	}
	// And the enum on the second system attribute name.
	code, body = formPost(t, ts.URL, url.Values{"Action": {"ReceiveMessage"}, "QueueUrl": {q},
		"AttributeName.1": {"All"}, "AttributeName.2": {"NotAnAttribute"}})
	if code != 400 {
		t.Errorf("an unknown system attribute name second in the list over Query: %d %.200s", code, body)
	}
}
