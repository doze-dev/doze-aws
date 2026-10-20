package console_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/console"
)

// send makes a call through the recorded path, the way an SDK does.
func send(rec *console.Recorder, target, contentType, body string) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("X-Amz-Target", target)
	r.Header.Set("Content-Type", contentType)
	rec.ServeHTTP(httptest.NewRecorder(), r)
}

func sqsCall(rec *console.Recorder, op, body string) {
	send(rec, "AmazonSQS."+op, "application/x-amz-json-1.0", body)
}

func wire(t *testing.T, c http.Handler) string {
	t.Helper()
	return req(t, c, "GET", "/_console/traffic", nil).Body.String()
}

// "Needs a look" says what is wrong, from what exists: a dead-letter queue that
// holds a message and what a message waits for before it lands there, an alarm
// in ALARM and since when, and what the ring recorded as refused. Each only when
// it is so.
func TestTheWireSaysWhatNeedsALook(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())

	// A call, so it is the wire and not the first-run page.
	sqsCall(rec, "ListQueues", `{}`)
	if body := wire(t, c); !strings.Contains(body, "Nothing needs a look") {
		t.Fatalf("a stack with nothing wrong does not say so:\n%.600s", body)
	}

	// An alarm that is firing, through the recorded path.
	send(rec, "GraniteServiceVersion20100801.PutMetricAlarm", "application/x-amz-json-1.0",
		`{"AlarmName":"declines","Namespace":"Shop","MetricName":"Declines","Statistic":"Sum","Period":60,"EvaluationPeriods":1,"Threshold":5,"ComparisonOperator":"GreaterThanThreshold","TreatMissingData":"breaching"}`)
	send(rec, "GraniteServiceVersion20100801.SetAlarmState", "application/x-amz-json-1.0",
		`{"AlarmName":"declines","StateValue":"ALARM","StateReason":"over"}`)

	// What the product says is read at most every few seconds, and a mutation made
	// in the console shows at once: this one drops what was read above.
	create(t, c, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}})
	req(t, c, "POST", "/_console/sqs/work-dlq/send", url.Values{"body": {`{"n":1}`}})

	// A refusal: a deduplication id on a standard queue.
	sqsCall(rec, "SendMessage", `{"QueueUrl":"http://127.0.0.1/000000000000/work","MessageBody":"x","MessageDeduplicationId":"d"}`)

	body := wire(t, c)
	for _, want := range []string{
		"work-dlq holds 1 message",
		"1 alarm is firing",
		"1 call was refused",
		"The same request would fail on AWS.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("needs a look lacks %q", want)
		}
	}
	if !regexp.MustCompile(`Messages land here after \d+ failed receives?\.`).MatchString(body) {
		t.Errorf("the dead-letter callout does not say how many receives a message gets:\n%s", excerpt(body, "work-dlq holds"))
	}
	if !regexp.MustCompile(`declines, since \d\d:\d\d\.`).MatchString(body) {
		t.Errorf("the alarm callout does not say since when:\n%s", excerpt(body, "alarm is firing"))
	}
	if strings.Contains(body, "Nothing needs a look") {
		t.Error("the panel says nothing needs a look beside three things that do")
	}
	// The window counts what the ring holds.
	if !regexp.MustCompile(`(?s)<span>Refused</span>\s*<span class="m b deny">1</span>`).MatchString(body) {
		t.Errorf("the window does not count the refusal:\n%s", excerpt(body, "In this window"))
	}
}

// A stack nobody has touched is the first-run page, and the first thing that
// exists or is recorded ends it. Creating something in the console is enough:
// the console's own calls are not on the wire, and "nothing exists" is the other
// half of the question.
func TestTheFirstRunWireEndsWhenSomethingExistsOrIsCalled(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())

	body := wire(t, c)
	for _, want := range []string{
		"Nothing has called yet.",
		"1 · Point your code at it",
		"export AWS_ACCESS_KEY_ID=test",
		"export AWS_REGION=us-east-1",
		"2 · Or make something from here",
		"waiting for a call…",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the first-run wire lacks %q", want)
		}
	}
	if m := regexp.MustCompile(`export AWS_ENDPOINT_URL=http://[^\s<]+`).FindString(body); !strings.HasPrefix(m, "export AWS_ENDPOINT_URL=http://example.com") {
		t.Errorf("the endpoint line is %q, want this request's host", m)
	}
	if strings.Contains(body, "The last 500 calls") || strings.Contains(body, "the last 500 calls") {
		t.Error("the first-run page draws the wire's table")
	}

	// The poll's answer on the first run is the page, and when something exists
	// it is the wire.
	create(t, c, "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}})
	feed := req(t, c, "GET", "/_console/traffic/feed", nil).Body.String()
	if strings.Contains(feed, "Nothing has called yet.") {
		t.Error("a queue exists and the first-run page is still drawn")
	}

	// And a recorded call ends it for a stack that is otherwise empty.
	c2, rec2, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	_ = rec
	sqsCall(rec2, "ListQueues", `{}`)
	if body := wire(t, c2); strings.Contains(body, "Nothing has called yet.") || !strings.Contains(body, "ListQueues") {
		t.Errorf("a recorded call did not end the first-run page:\n%.400s", body)
	}
}

// The inspector of a refused call is the reason, what was sent, the resource it
// was sent to, and the same operation in the ledger: each read from the call, the
// queue and the ledger, and the sentence under the queue only when it is so.
func TestTheInspectorOfARefusedCallExplainsItFromTheQueueAndTheLedger(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	create(t, c, "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}})
	sqsCall(rec, "SendMessage", `{"QueueUrl":"http://127.0.0.1/000000000000/orders","MessageBody":"{\"a\":1}","MessageDeduplicationId":"d1"}`)

	entry := req(t, c, "GET", "/_console/traffic/entry?seq=1", nil).Body.String()
	for _, want := range []string{
		`<span class="m b">InvalidParameterValue</span>`, // the real code
		"MessageDeduplicationId",                         // and the real message
		"What it was sent",
		"The queue it was sent to",
		"standard",
		"has no FIFO attributes",
		"The request names a parameter that only FIFO queues take, and this queue is standard.",
		"The same operation, in the ledger",
		"F · functional",
		"The SQS ledger",
		"Copy as curl",
		"Copy the error",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("the refused call's inspector lacks %q:\n%s", want, entry)
		}
	}
	// A refusal has no tabs: there is no answer to read beside the reason.
	if strings.Contains(entry, `role="tablist"`) {
		t.Error("a refused call has tabs")
	}
	// What Copy the error copies is the code and the message.
	if !regexp.MustCompile(`data-copy="InvalidParameterValue: [^"]*MessageDeduplicationId[^"]*"`).MatchString(entry) {
		t.Errorf("Copy the error does not copy the code and the message:\n%s", excerpt(entry, "Copy the error"))
	}

	// The same refusal about a FIFO queue's group id says the opposite sentence,
	// and a standard queue refused for something else says neither.
	create(t, c, "/_console/sqs/create", url.Values{"name": {"line.fifo"}, "dlq_mode": {"none"}, "fifo": {"on"}})
	sqsCall(rec, "SendMessage", `{"QueueUrl":"http://127.0.0.1/000000000000/line.fifo","MessageBody":"x"}`)
	fifo := req(t, c, "GET", "/_console/traffic/entry?seq=2", nil).Body.String()
	if !strings.Contains(fifo, "FIFO") || strings.Contains(fifo, "this queue is standard") {
		t.Errorf("a FIFO queue's refusal is explained as a standard queue's:\n%s", excerpt(fifo, "The queue it was sent to"))
	}
}

// A call that was served has the three tabs of the board, and its request is
// readable: where it was sent, the operation, the body, the command.
func TestTheInspectorOfAServedCallHasItsThreeTabs(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	sqsCall(rec, "CreateQueue", `{"QueueName":"orders"}`)
	entry := req(t, c, "GET", "/_console/traffic/entry?seq=1", nil).Body.String()
	for _, want := range []string{
		`role="tablist"`, ">Request<", ">Response<", "What it caused",
		"Sent to", "Operation", "AmazonSQS.CreateQueue", "Content type",
		"secrets are masked, long bodies cut", "The call, as curl", "Copy as curl", "Copy the body",
		"Only work doze-aws did itself is nested here.",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("a served call's inspector lacks %q", want)
		}
	}
}

// While the feed is paused the page asks how many calls it is missing, in a
// number and not a page.
func TestThePausedFeedCountsWhatItIsMissing(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	sqsCall(rec, "ListQueues", `{}`)
	after := regexp.MustCompile(`data-last="(\d+)"`).FindStringSubmatch(wire(t, c))
	if after == nil {
		t.Fatal("the region does not say the newest call it holds")
	}
	if got := strings.TrimSpace(req(t, c, "GET", "/_console/traffic/feed?after="+after[1], nil).Body.String()); got != "0" {
		t.Errorf("nothing is new and the count is %q", got)
	}
	sqsCall(rec, "ListQueues", `{}`)
	sqsCall(rec, "ListQueues", `{}`)
	if got := strings.TrimSpace(req(t, c, "GET", "/_console/traffic/feed?after="+after[1], nil).Body.String()); got != "2" {
		t.Errorf("two calls are new and the count is %q", got)
	}
}

// The rail's word beside The wire is whether a call started in the last minute,
// on the first paint and in the counts poll.
func TestTheRailSaysLiveOrQuiet(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	note := regexp.MustCompile(`data-wire-note>(\w+)<`)
	if m := note.FindStringSubmatch(wire(t, c)); m == nil || m[1] != "quiet" {
		t.Errorf("a stack nothing has called: the rail says %v, want quiet", m)
	}
	if _, warn := counts(t, c); len(warn) != 0 {
		t.Fatalf("a fresh stack has dots: %v", warn)
	}
	sqsCall(rec, "ListQueues", `{}`)
	if m := note.FindStringSubmatch(wire(t, c)); m == nil || m[1] != "live" {
		t.Errorf("a call just made: the rail says %v, want live", m)
	}
	n, _ := counts(t, c)
	if n["wire"] != 1 {
		t.Errorf("the counts poll's wire figure = %v, want the one call", n["wire"])
	}
}

// What the board calls an answer is the state in a word: a refused call's chip
// is the word, never only a colour. Checked on the page, over every state, in
// wire_state_test.go; this checks the filters' attributes a row carries.
func TestARowCarriesWhatTheFiltersReadFromIt(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())
	sqsCall(rec, "CreateQueue", `{"QueueName":"orders"}`)
	sqsCall(rec, "SendMessage", `{"QueueUrl":"http://127.0.0.1/000000000000/orders","MessageBody":"x","MessageDeduplicationId":"d"}`)
	body := wire(t, c)
	rows := regexp.MustCompile(`<div id="r-\d+" class="tr-row[^"]*" data-seq="\d+" data-depth="\d+" data-svc="(\w+)" data-err="(\d?)" data-refused="(\d?)"`).FindAllStringSubmatch(body, -1)
	if len(rows) != 2 {
		t.Fatalf("rows with filter attributes = %d, want 2:\n%s", len(rows), excerpt(body, `class="tr-row`))
	}
	// Newest first: the refused send, then the create.
	if rows[0][1] != "sqs" || rows[0][2] != "1" || rows[0][3] != "1" {
		t.Errorf("the refused send carries %v, want sqs, an error, refused", rows[0][1:])
	}
	if rows[1][2] != "" || rows[1][3] != "" {
		t.Errorf("the create carries %v, want neither an error nor refused", rows[1][1:])
	}
}
