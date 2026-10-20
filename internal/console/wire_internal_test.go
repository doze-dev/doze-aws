package console

import (
	"strings"
	"testing"
	"time"
)

func TestAnswerSaysTheStateInAWordBesideTheStatus(t *testing.T) {
	for _, tc := range []struct {
		status      int
		state, word string
		want        string
	}{
		{200, "served", "", "200"},
		{400, "refused", "Refused", "400 refused"},
		{400, "refusedhere", "Refused here", "400 refused here"},
		{403, "denied", "Denied", "403 denied"},
		{501, "notbuilt", "Not built", "501 not built"},
		{500, "error", "Failed", "500 failed"},
		// A function error is a served 200 whose function raised: the board says
		// "it threw" and not the 200, which is true and is not what is wanted.
		{200, "threw", "It threw", "it threw"},
	} {
		if got := answerOf(tc.status, tc.state, tc.word); got != tc.want {
			t.Errorf("answerOf(%d, %s) = %q, want %q", tc.status, tc.state, got, tc.want)
		}
	}
}

func TestAnEventSourceInvokeIsNamedByTheMechanism(t *testing.T) {
	for _, tc := range []struct {
		svc, via, want string
	}{
		{"lambda", "sqs:orders", "the queue's event source mapping"},
		{"lambda", "dynamodb:orders", "the stream's event source mapping"},
		{"lambda", "kinesis:clicks", "the stream's event source mapping"},
		// Anything else is said as recorded: it is already the thing's own name.
		{"sqs", "s3:ObjectCreated:Put", "s3:ObjectCreated:Put"},
		{"sqs", "sns:uploads", "sns:uploads"},
		{"lambda", "function URL", "function URL"},
	} {
		if got := viaWords(TrafficEntry{Service: tc.svc, Via: tc.via}); got != tc.want {
			t.Errorf("viaWords(%s %q) = %q, want %q", tc.svc, tc.via, got, tc.want)
		}
	}
	if got := actionLabel("Invoke (event source)"); got != "Invoke" {
		t.Errorf("the label of an event source's invoke = %q, want Invoke", got)
	}
	if got := actionLabel("SendMessage"); got != "SendMessage" {
		t.Errorf("a plain action was changed: %q", got)
	}
}

func TestAFailingRowSaysWhatTheServiceSaidOrWhatTheFunctionRaised(t *testing.T) {
	if got := whyOf("refused", &Refusal{Code: "InvalidParameterValue", Message: "MessageDeduplicationId belongs to FIFO queues"}); got != "MessageDeduplicationId belongs to FIFO queues" {
		t.Errorf("a refusal says %q: the row carries the sentence, and the code is in the hover and the inspector", got)
	}
	if got := whyOf("threw", &Refusal{Code: "KeyError", Message: "'unit_price'"}); got != "KeyError: 'unit_price'" {
		t.Errorf("a function error says %q, want the type and what it said", got)
	}
	if got := whyOf("served", nil); got != "" {
		t.Errorf("a served call says %q", got)
	}
	if got := whyTitle(&Refusal{Code: "C", Message: "m"}); got != "C: m" {
		t.Errorf("the hover = %q, want the code and the message", got)
	}
}

// A long poll that waited is time spent waiting, not work: its row says seconds,
// and it is not the window's slowest call.
func TestALongPollIsAWaitAndNotTheSlowestCall(t *testing.T) {
	now := time.Now()
	poll := TrafficEntry{Seq: 1, At: now, Service: "sqs", Action: "ReceiveMessage", Resource: "q", Status: 200, Millis: 20001}
	put := TrafficEntry{Seq: 2, At: now, Service: "s3", Action: "PutObject", Resource: "b/k", Status: 200, Millis: 38.2}
	if !waited(poll) || waited(put) {
		t.Fatalf("waited: poll=%v put=%v", waited(poll), waited(put))
	}
	// Only an SQS receive is a wait: a slow PutObject is a slow PutObject.
	if waited(TrafficEntry{Service: "s3", Action: "GetObject", Millis: 30000}) {
		t.Error("a slow S3 call was read as a wait")
	}
	if got := millisCell(poll); got != "20 s" {
		t.Errorf("a long poll's cell = %q, want 20 s", got)
	}
	s := statsOf([]TrafficEntry{poll, put}, now)
	if s.Slowest != "38.2 ms · PutObject" {
		t.Errorf("slowest = %q, want the PutObject: the poll's twenty seconds were spent waiting", s.Slowest)
	}
	if s.Calls != 2 || s.Services != 2 || s.Rate != 2 {
		t.Errorf("stats = %+v", s)
	}
	rows := wireRows([]TrafficEntry{put, poll})
	for _, r := range rows {
		if r.Action == "ReceiveMessage" && (r.LaneWidth != 0 || !r.Wait) {
			t.Errorf("a wait drew a lane: %+v", r)
		}
	}
}

// The wire is newest first by when a call STARTED, which is what the When
// column says. The ring is in finishing order, and a long poll finishes last.
func TestTheWireIsOrderedByWhenCallsStarted(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	long := TrafficEntry{Seq: 1, At: t0, Service: "sqs", Action: "ReceiveMessage", Status: 200, Millis: 20000}
	late := TrafficEntry{Seq: 2, At: t0.Add(5 * time.Second), Service: "kms", Action: "Decrypt", Status: 200, Millis: 1}
	// Ring order, newest FINISHED first: the long poll finished last.
	rows := wireRows([]TrafficEntry{long, late})
	if len(rows) != 2 || rows[0].Action != "Decrypt" || rows[1].Action != "ReceiveMessage" {
		t.Fatalf("rows = %v, want Decrypt (started later) above ReceiveMessage", actions(rows))
	}
}

func actions(rows []trafficRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Action)
	}
	return out
}

// Lanes share one scale, the slowest call, and a child starts where it started
// inside its parent.
func TestLanesShareOneScaleAndAChildStartsInsideItsParent(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	parent := TrafficEntry{Seq: 1, At: t0, Service: "s3", Action: "PutObject", Status: 200, Millis: 10}
	child := TrafficEntry{Seq: 2, At: t0.Add(20 * time.Millisecond), Service: "sqs", Action: "SendMessage", Status: 200, Millis: 50, Parent: 1, Via: "s3:ObjectCreated:Put"}
	slow := TrafficEntry{Seq: 3, At: t0.Add(time.Second), Service: "lambda", Action: "Invoke", Status: 200, Millis: 100}
	rows := wireRows([]TrafficEntry{slow, child, parent})
	by := map[string]trafficRow{}
	for _, r := range rows {
		by[r.Action] = r
	}
	if got := by["Invoke"]; got.LaneLeft != 0 || got.LaneWidth != 100 {
		t.Errorf("the slowest call fills its lane: %+v", got)
	}
	if got := by["PutObject"]; got.LaneWidth != 10 {
		t.Errorf("10 ms of 100 = %d%% wide, want 10", got.LaneWidth)
	}
	if got := by["SendMessage"]; got.LaneLeft != 20 || got.LaneWidth != 50 || got.Depth != 1 {
		t.Errorf("the child started 20 ms into its parent and took 50 of 100: %+v", got)
	}
}

func TestNeedsALookSaysOnlyWhatTheRingSays(t *testing.T) {
	refusal := `{"__type":"ValidationException","message":"no"}`
	now := time.Now()
	raw := []TrafficEntry{
		{Seq: 1, At: now, Service: "sqs", Action: "SendMessage", Status: 400, RespBody: refusal, RespCT: "application/json"},
		{Seq: 2, At: now, Service: "sqs", Action: "SendMessage", Status: 400, RespBody: refusal, RespCT: "application/json"},
		{Seq: 3, At: now, Service: "lambda", Action: "Invoke", Resource: "calc", Status: 200, FnError: "Unhandled", RespBody: `{"errorType":"KeyError","errorMessage":"x"}`},
		{Seq: 4, At: now, Service: "s3", Action: "GetObject", Status: 200},
	}
	got := ringNeeds(raw, "/_console")
	if len(got) != 2 {
		t.Fatalf("needs = %+v, want a refusal and a function error", got)
	}
	if got[0].Title != "2 calls were refused" || got[0].Tone != "deny" || got[0].Note != "The same request would fail on AWS." {
		t.Errorf("refusal = %+v", got[0])
	}
	if got[1].Title != "calc answered with an error" || got[1].Note != "An Invoke came back with a function error 1 time in the last 500 calls." {
		t.Errorf("function error = %+v", got[1])
	}
	if n := ringNeeds([]TrafficEntry{{Seq: 1, At: now, Service: "s3", Action: "GetObject", Status: 200}}, "/_console"); len(n) != 0 {
		t.Errorf("a stack with nothing wrong has needs: %+v", n)
	}
	// Two functions are counted, not named.
	two := append([]TrafficEntry{}, raw[2], TrafficEntry{Seq: 5, At: now, Service: "lambda", Action: "Invoke", Resource: "other", Status: 200, FnError: "Unhandled", RespBody: `{"errorMessage":"y"}`})
	if n := ringNeeds(two, "/_console"); len(n) != 1 || n[0].Title != "2 functions answered with an error" || !strings.Contains(n[0].Note, "2 times") {
		t.Errorf("two functions: %+v", n)
	}
}

func TestACurlThatIsShownElidesTheBodyAndTheOneCopiedDoesNot(t *testing.T) {
	e := TrafficEntry{Method: "POST", Host: "127.0.0.1:4566", Path: "/", CT: "application/x-amz-json-1.0", Target: "AmazonSQS.SendMessage",
		ReqBody: `{"QueueUrl":"http://127.0.0.1:4566/0/q","MessageBody":"{\"orderId\":\"x\"}"}`}
	if !strings.HasSuffix(e.curlShown(), "--data '{\"QueueUrl\": …}'") {
		t.Errorf("shown = %q", e.curlShown())
	}
	if !strings.Contains(e.Curl(), `"MessageBody"`) {
		t.Errorf("the copied command lost the body: %q", e.Curl())
	}
	if got := (TrafficEntry{Method: "GET", Host: "h", Path: "/b"}).curlShown(); strings.Contains(got, "--data") {
		t.Errorf("a call with no body got a --data: %q", got)
	}
}

// A body is data. It is escaped, and only the spans are added.
func TestAHighlightedBodyCannotInjectMarkup(t *testing.T) {
	got := string(wireJSON("{\n  \"k\": \"<script>alert(1)</script>\",\n  \"n\": 3\n}"))
	if strings.Contains(got, "<script>") {
		t.Fatalf("the body injected markup: %s", got)
	}
	for _, want := range []string{`<span class="kw">&#34;k&#34;</span>`, `<span class="n">3</span>`, `&lt;script&gt;`} {
		if !strings.Contains(got, want) {
			t.Errorf("highlighted body lacks %q:\n%s", want, got)
		}
	}
	if got := string(wireJSON("<xml/>")); got != "&lt;xml/&gt;" {
		t.Errorf("a body that is not JSON = %q, want it escaped and plain", got)
	}
}

func TestTheServiceMenuListsWhatIsInTheWindowInTheRailsOrder(t *testing.T) {
	got := servicesOf([]TrafficEntry{{Service: "lambda"}, {Service: "sqs"}, {Service: "sqs"}, {Service: "sts"}, {Service: "s3"}})
	var names []string
	for _, s := range got {
		names = append(names, s.Key)
	}
	if strings.Join(names, ",") != "s3,sqs,lambda,sts" {
		t.Errorf("menu = %v, want s3,sqs,lambda then the service the rail has no entry for", names)
	}
	if got[1].N != 2 {
		t.Errorf("sqs has %d calls, want 2", got[1].N)
	}
}
