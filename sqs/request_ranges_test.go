package sqs

// The ranges AWS states for a request's own numbers, refused on both wire
// protocols with a 400 and a code, not accepted and quietly clamped or ignored.
//
// SQS's service model carries no @range at all, so none of these is generated:
// each is hand-derived from the API reference, and the page is named beside
// the row. They were found one at a time: ReceiveMessage with
// MaxNumberOfMessages 11 answered one message (SQS-5), SendMessage with
// DelaySeconds 9999 answered 200 (SQS-10), and a long poll of 3600 seconds was
// silently shortened to 20.

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// status sends one request, JSON or Query, and returns the HTTP status and body.
func status(t *testing.T, base string, protocol, action string, params map[string]any) (int, string) {
	t.Helper()
	var resp *http.Response
	var err error
	if protocol == "json" {
		var parts []string
		for k, v := range params {
			switch x := v.(type) {
			case string:
				parts = append(parts, strconv.Quote(k)+":"+strconv.Quote(x))
			default:
				parts = append(parts, strconv.Quote(k)+":"+toString(x))
			}
		}
		req, _ := http.NewRequest(http.MethodPost, base+"/", strings.NewReader("{"+strings.Join(parts, ",")+"}"))
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
		resp, err = http.DefaultClient.Do(req)
	} else {
		form := url.Values{"Action": {action}}
		for k, v := range params {
			form.Set(k, toString(v))
		}
		resp, err = http.PostForm(base+"/", form)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func toString(v any) string {
	switch x := v.(type) {
	case int:
		return strconv.Itoa(x)
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func TestARequestsOwnNumbersAreHeldToTheirRanges(t *testing.T) {
	ts := testServer(t)
	qurl := ts.URL + "/000000000000/ranges"
	if code, body := status(t, ts.URL, "json", "CreateQueue", map[string]any{"QueueName": "ranges"}); code != 200 {
		t.Fatalf("CreateQueue: %d %s", code, body)
	}

	// Every row is a request AWS refuses. doc: the API reference page for the
	// action, the parameter's description (range and units).
	for _, c := range []struct {
		name   string
		action string
		params map[string]any
	}{
		{"ReceiveMessage MaxNumberOfMessages 0", "ReceiveMessage", map[string]any{"MaxNumberOfMessages": 0}},
		{"ReceiveMessage MaxNumberOfMessages 11", "ReceiveMessage", map[string]any{"MaxNumberOfMessages": 11}},
		{"ReceiveMessage MaxNumberOfMessages -1", "ReceiveMessage", map[string]any{"MaxNumberOfMessages": -1}},
		{"ReceiveMessage WaitTimeSeconds 21", "ReceiveMessage", map[string]any{"WaitTimeSeconds": 21}},
		{"ReceiveMessage WaitTimeSeconds -1", "ReceiveMessage", map[string]any{"WaitTimeSeconds": -1}},
		{"ReceiveMessage VisibilityTimeout 43201", "ReceiveMessage", map[string]any{"VisibilityTimeout": 43201}},
		{"ReceiveMessage VisibilityTimeout -1", "ReceiveMessage", map[string]any{"VisibilityTimeout": -1}},
		{"SendMessage DelaySeconds 901", "SendMessage", map[string]any{"MessageBody": "x", "DelaySeconds": 901}},
		{"SendMessage DelaySeconds 9999", "SendMessage", map[string]any{"MessageBody": "x", "DelaySeconds": 9999}},
		{"SendMessage DelaySeconds -1", "SendMessage", map[string]any{"MessageBody": "x", "DelaySeconds": -1}},
	} {
		for _, protocol := range []string{"json", "query"} {
			t.Run(c.name+"/"+protocol, func(t *testing.T) {
				p := map[string]any{"QueueUrl": qurl}
				for k, v := range c.params {
					p[k] = v
				}
				code, body := status(t, ts.URL, protocol, c.action, p)
				if code != 400 {
					t.Errorf("answered %d, want 400:\n%s", code, body)
				}
			})
		}
	}

	// The ends of each range are accepted.
	for _, c := range []struct {
		name   string
		action string
		params map[string]any
	}{
		{"MaxNumberOfMessages 1", "ReceiveMessage", map[string]any{"MaxNumberOfMessages": 1}},
		{"MaxNumberOfMessages 10", "ReceiveMessage", map[string]any{"MaxNumberOfMessages": 10}},
		{"WaitTimeSeconds 0", "ReceiveMessage", map[string]any{"WaitTimeSeconds": 0}},
		{"VisibilityTimeout 43200", "ReceiveMessage", map[string]any{"VisibilityTimeout": 43200}},
		{"DelaySeconds 0", "SendMessage", map[string]any{"MessageBody": "x", "DelaySeconds": 0}},
		{"DelaySeconds 900", "SendMessage", map[string]any{"MessageBody": "x", "DelaySeconds": 900}},
	} {
		t.Run("accepts "+c.name, func(t *testing.T) {
			p := map[string]any{"QueueUrl": qurl}
			for k, v := range c.params {
				p[k] = v
			}
			if code, body := status(t, ts.URL, "json", c.action, p); code != 200 {
				t.Errorf("answered %d, want 200:\n%s", code, body)
			}
		})
	}
}

// One entry's delay out of range fails that entry, in Failed, and sends the
// others: a batch's entries are judged one at a time.
func TestABatchEntryWithAnOutOfRangeDelayFailsAlone(t *testing.T) {
	ts := testServer(t)
	jsonCall(t, ts.URL, "CreateQueue", `{"QueueName":"batchdelay"}`)
	out := jsonCall(t, ts.URL, "SendMessageBatch", `{"QueueUrl":"`+ts.URL+`/000000000000/batchdelay","Entries":[`+
		`{"Id":"ok","MessageBody":"a"},{"Id":"late","MessageBody":"b","DelaySeconds":901}]}`)
	if !strings.Contains(out, `"Failed":[{`) || !strings.Contains(out, `"Id":"late"`) || !strings.Contains(out, `"SenderFault":true`) {
		t.Errorf("the out-of-range entry should be in Failed:\n%s", out)
	}
	if !strings.Contains(out, `"Successful":[{`) || !strings.Contains(out, `"Id":"ok"`) {
		t.Errorf("the good entry should have been sent:\n%s", out)
	}
}
