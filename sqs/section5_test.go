package sqs

// The rest of the SQS rules AWS states for names, messages, batches and FIFO
// queues, each refused with a 400 on both wire protocols, and the ends of each
// accepted. The model carries none of these, so none is generated.
//
// doc: the SQS developer guide (quotas, queue naming, message attributes) and
// the API reference page for each action.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// jsonBody posts a JSON request whose body is given whole, for nested shapes.
func jsonBody(t *testing.T, base, action, body string) (int, string) {
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
	return resp.StatusCode, string(b)
}

func formPost(t *testing.T, base string, form url.Values) (int, string) {
	t.Helper()
	resp, err := http.PostForm(base+"/", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestQueueNameRules(t *testing.T) {
	ts := testServer(t)
	for _, c := range []struct {
		name  string
		attrs map[string]string
		ok    bool
	}{
		{"", nil, false},
		{strings.Repeat("a", 81), nil, false},
		{strings.Repeat("a", 80), nil, true},
		{"has space", nil, false},
		{"has.dot", nil, false},
		{"emoji-🙂", nil, false},
		{"with_under-score09", nil, true},
		{"orders.fifo", nil, false},                               // .fifo needs FifoQueue
		{"orders", map[string]string{"FifoQueue": "true"}, false}, // FifoQueue needs .fifo
		{"orders.fifo", map[string]string{"FifoQueue": "true"}, true},
		{strings.Repeat("a", 75) + ".fifo", map[string]string{"FifoQueue": "true"}, true},  // 80 with the suffix
		{strings.Repeat("a", 76) + ".fifo", map[string]string{"FifoQueue": "true"}, false}, // 81
	} {
		jb := `{"QueueName":` + strconv.Quote(c.name)
		form := url.Values{"Action": {"CreateQueue"}, "QueueName": {c.name}}
		if c.attrs != nil {
			a, i := []string{}, 1
			for k, v := range c.attrs {
				a = append(a, strconv.Quote(k)+":"+strconv.Quote(v))
				form.Set("Attribute."+strconv.Itoa(i)+".Name", k)
				form.Set("Attribute."+strconv.Itoa(i)+".Value", v)
				i++
			}
			jb += `,"Attributes":{` + strings.Join(a, ",") + `}`
		}
		jb += "}"
		for protocol, do := range map[string]func() (int, string){
			"json":  func() (int, string) { return jsonBody(t, ts.URL, "CreateQueue", jb) },
			"query": func() (int, string) { return formPost(t, ts.URL, form) },
		} {
			code, body := do()
			if c.ok && code != 200 || !c.ok && code != 400 {
				t.Errorf("%s CreateQueue %.20q %v: answered %d (ok=%v)\n%.160s", protocol, c.name, c.attrs, code, c.ok, body)
			}
		}
	}
}

func TestMessageBodyAndAttributeLimits(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"limits"}`)
	qurl := ts.URL + "/000000000000/limits"
	send := func(extra string) (int, string) {
		return jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+qurl+`",`+extra+`}`)
	}
	attr := func(name, dtype, val string) string {
		return `"MessageAttributes":{` + strconv.Quote(name) + `:{"DataType":` + strconv.Quote(dtype) + `,"StringValue":` + strconv.Quote(val) + `}}`
	}
	many := func(n int) string {
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, `"a`+strconv.Itoa(i)+`":{"DataType":"String","StringValue":"v"}`)
		}
		return `"MessageAttributes":{` + strings.Join(parts, ",") + `}`
	}
	for _, c := range []struct {
		name  string
		extra string
		ok    bool
	}{
		{"an empty body", `"MessageBody":""`, false},
		{"a body of one character", `"MessageBody":"x"`, true},
		{"a body at the limit", `"MessageBody":"` + strings.Repeat("x", 262144) + `"`, true},
		{"a body over the limit", `"MessageBody":"` + strings.Repeat("x", 262145) + `"`, false},
		{"a body with a character SQS does not allow", `"MessageBody":"a\u0000b"`, false},
		{"a body with tab, newline and a return", `"MessageBody":"a\tb\nc\rd"`, true},
		{"ten message attributes", `"MessageBody":"x",` + many(10), true},
		{"eleven message attributes", `"MessageBody":"x",` + many(11), false},
		{"an attribute name that starts AWS.", `"MessageBody":"x",` + attr("AWS.thing", "String", "v"), false},
		{"an attribute name that starts Amazon.", `"MessageBody":"x",` + attr("Amazon.thing", "String", "v"), false},
		{"an attribute name with a space", `"MessageBody":"x",` + attr("a b", "String", "v"), false},
		{"an attribute name of 256 characters", `"MessageBody":"x",` + attr(strings.Repeat("a", 256), "String", "v"), true},
		{"an attribute name of 257 characters", `"MessageBody":"x",` + attr(strings.Repeat("a", 257), "String", "v"), false},
		{"a data type that is not one", `"MessageBody":"x",` + attr("a", "Integer", "1"), false},
		{"a custom type suffix", `"MessageBody":"x",` + attr("a", "String.json", "{}"), true},
		{"a Number that is not one", `"MessageBody":"x",` + attr("a", "Number", "twelve"), false},
		{"a Number that is", `"MessageBody":"x",` + attr("a", "Number", "-12.5e3"), true},
		{"a String attribute with an empty value", `"MessageBody":"x",` + attr("a", "String", ""), false},
		{"the body and attributes together over the limit", `"MessageBody":"` + strings.Repeat("x", 262140) + `",` + attr("aaaaaa", "String", "valuevalue"), false},
	} {
		code, body := send(c.extra)
		if c.ok && code != 200 || !c.ok && code != 400 {
			t.Errorf("SendMessage with %s: answered %d (ok=%v)\n%.200s", c.name, code, c.ok, body)
		}
	}

	// And the Query wire for the two that are about the form encoding.
	form := url.Values{"Action": {"SendMessage"}, "QueueUrl": {qurl}, "MessageBody": {"x"},
		"MessageAttribute.1.Name": {"AWS.thing"}, "MessageAttribute.1.Value.DataType": {"String"}, "MessageAttribute.1.Value.StringValue": {"v"}}
	if code, body := formPost(t, ts.URL, form); code != 400 {
		t.Errorf("Query: a reserved attribute name answered %d\n%.200s", code, body)
	}
	form = url.Values{"Action": {"SendMessage"}, "QueueUrl": {qurl}, "MessageBody": {""}}
	if code, body := formPost(t, ts.URL, form); code != 400 {
		t.Errorf("Query: an empty body answered %d\n%.200s", code, body)
	}
}

func TestBatchRules(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"batches"}`)
	qurl := ts.URL + "/000000000000/batches"
	entries := func(n int, body string) string {
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, `{"Id":"e`+strconv.Itoa(i)+`","MessageBody":`+strconv.Quote(body)+`}`)
		}
		return `[` + strings.Join(parts, ",") + `]`
	}
	for _, c := range []struct {
		name    string
		entries string
		ok      bool
	}{
		{"no entries", `[]`, false},
		{"one entry", entries(1, "x"), true},
		{"ten entries", entries(10, "x"), true},
		{"eleven entries", entries(11, "x"), false},
		{"a repeated id", `[{"Id":"a","MessageBody":"x"},{"Id":"a","MessageBody":"y"}]`, false},
		{"an id with a space", `[{"Id":"a b","MessageBody":"x"}]`, false},
		{"an id of 81 characters", `[{"Id":"` + strings.Repeat("a", 81) + `","MessageBody":"x"}]`, false},
		{"an id of 80 characters", `[{"Id":"` + strings.Repeat("a", 80) + `","MessageBody":"x"}]`, true},
		// Each body is under the limit; together they are over it.
		{"entries whose bodies together exceed the limit", entries(3, strings.Repeat("x", 100000)), false},
		{"entries whose bodies together are at the limit", entries(2, strings.Repeat("x", 131072)), true},
	} {
		code, body := jsonBody(t, ts.URL, "SendMessageBatch", `{"QueueUrl":"`+qurl+`","Entries":`+c.entries+`}`)
		if c.ok && code != 200 || !c.ok && code != 400 {
			t.Errorf("SendMessageBatch with %s: answered %d (ok=%v)\n%.200s", c.name, code, c.ok, body)
		}
	}
}

func TestFIFORules(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"f.fifo","Attributes":{"FifoQueue":"true"}}`)
	qurl := ts.URL + "/000000000000/f.fifo"
	send := func(extra string) (int, string) {
		return jsonBody(t, ts.URL, "SendMessage", `{"QueueUrl":"`+qurl+`",`+extra+`}`)
	}
	for _, c := range []struct {
		name, extra string
		ok          bool
	}{
		{"no group id", `"MessageBody":"x","MessageDeduplicationId":"d"`, false},
		{"no deduplication id without content-based dedup", `"MessageBody":"x","MessageGroupId":"g"`, false},
		{"a group id of 129 characters", `"MessageBody":"x","MessageGroupId":"` + strings.Repeat("g", 129) + `","MessageDeduplicationId":"d1"`, false},
		{"a group id of 128 characters", `"MessageBody":"x","MessageGroupId":"` + strings.Repeat("g", 128) + `","MessageDeduplicationId":"d2"`, true},
		{"a group id with a space", `"MessageBody":"x","MessageGroupId":"a b","MessageDeduplicationId":"d3"`, false}, // alphanumerics and punctuation only: the space is neither
		{"a deduplication id of 129 characters", `"MessageBody":"x","MessageGroupId":"g","MessageDeduplicationId":"` + strings.Repeat("d", 129) + `"`, false},
		// A per-message delay is not allowed on a FIFO queue; only the queue's.
		{"a per-message delay", `"MessageBody":"x","MessageGroupId":"g","MessageDeduplicationId":"d4","DelaySeconds":5`, false},
	} {
		code, body := send(c.extra)
		if c.ok && code != 200 || !c.ok && code != 400 {
			t.Errorf("FIFO SendMessage with %s: answered %d (ok=%v)\n%.200s", c.name, code, c.ok, body)
		}
	}

	// The same deduplication id inside the window is the first message.
	_, first := send(`"MessageBody":"one","MessageGroupId":"g","MessageDeduplicationId":"same"`)
	_, again := send(`"MessageBody":"two","MessageGroupId":"g","MessageDeduplicationId":"same"`)
	pick := func(body, key string) string {
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		s, _ := m[key].(string)
		return s
	}
	if pick(first, "MessageId") == "" || pick(first, "MessageId") != pick(again, "MessageId") {
		t.Errorf("a repeated deduplication id should answer the original MessageId:\n%s\n%s", first, again)
	}
	if pick(first, "SequenceNumber") == "" || pick(first, "SequenceNumber") != pick(again, "SequenceNumber") {
		t.Errorf("a repeated deduplication id should answer the original SequenceNumber:\n%s\n%s", first, again)
	}
}
