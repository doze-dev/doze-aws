package sqs

// The edges of the rules, each side of each one. A range check that is off by
// one, or a character class that stops one code point short, passes every test
// that uses a value from the middle — which is how the mutation baseline found
// them still standing.

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestAttributeNameCharacterClasses(t *testing.T) {
	for _, c := range []struct {
		ch string
		ok bool
	}{
		{"a", true}, {"z", true}, {"A", true}, {"Z", true}, {"0", true}, {"9", true},
		{"_", true}, {"-", true},
		{"`", false}, {"{", false}, {"@", false}, {"[", false}, {"/", false}, {":", false},
		{" ", false}, {"é", false},
	} {
		got := validateAttrName("x" + c.ch + "y")
		if c.ok && got != nil || !c.ok && got == nil {
			t.Errorf("message attribute name with %q: refused=%v, want ok=%v", c.ch, got != nil, c.ok)
		}
	}
	// A period inside is fine; at either end or doubled it is not.
	for name, ok := range map[string]bool{"a.b": true, ".ab": false, "ab.": false, "a..b": false} {
		if got := validateAttrName(name); (got == nil) != ok {
			t.Errorf("name %q: ok=%v, want %v", name, got == nil, ok)
		}
	}
}

func TestFIFOIDPrintableRange(t *testing.T) {
	for _, c := range []struct {
		ch rune
		ok bool
	}{
		{0x20, false}, {0x21, true}, {0x7E, true}, {0x7F, false}, {'a', true}, {'~', true}, {'é', false},
	} {
		if got := validFIFOID("g" + string(c.ch)); got != c.ok {
			t.Errorf("id with %U: %v, want %v", c.ch, got, c.ok)
		}
	}
	if validFIFOID(strings.Repeat("a", 129)) || !validFIFOID(strings.Repeat("a", 128)) || validFIFOID("") {
		t.Error("the 1 to 128 length of a group or deduplication id is off")
	}
}

func TestPermissionLabelCharacterClasses(t *testing.T) {
	for _, c := range []struct {
		ch string
		ok bool
	}{
		{"a", true}, {"z", true}, {"A", true}, {"Z", true}, {"0", true}, {"9", true}, {"-", true}, {"_", true},
		{"`", false}, {"{", false}, {"@", false}, {"[", false}, {"/", false}, {":", false}, {".", false}, {" ", false},
	} {
		got := validPermission("l"+c.ch, []string{"111122223333"}, []string{"SendMessage"})
		if c.ok && got != nil || !c.ok && got == nil {
			t.Errorf("label with %q: refused=%v, want ok=%v", c.ch, got != nil, c.ok)
		}
	}
}

func TestMessageSizeCountsNameTypeAndValue(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", map[string]string{"MaximumMessageSize": "1024"}, nil); err != nil {
		t.Fatal(err)
	}
	// name "n" (1) + type "String" (6) + value "v" (1) = 8 bytes of attribute.
	attrs := map[string]attr{"n": {DataType: "String", StringValue: "v"}}
	if got := messageSize("", attrs); got != 8 {
		t.Fatalf("messageSize of an attribute is %d, want 8", got)
	}
	if _, err := s.Send("q", strings.Repeat("x", 1024-8), attrs, -1, "", "", nil); err != nil {
		t.Errorf("a message of exactly 1024 bytes was refused: %v", err)
	}
	if _, err := s.Send("q", strings.Repeat("x", 1024-8+1), attrs, -1, "", "", nil); err == nil {
		t.Error("a message of 1025 bytes was accepted")
	}
	// Binary counts its bytes, not their base64.
	bin := map[string]attr{"n": {DataType: "Binary", BinaryValue: []byte{1, 2, 3, 4}}}
	if got := messageSize("", bin); got != 1+6+4 {
		t.Errorf("a binary attribute counts %d, want 11", got)
	}
}

func TestRedrivePolicyCountAndAllowPolicyLimits(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("dlq", nil, nil); err != nil {
		t.Fatal(err)
	}
	arn := s.queueARN("dlq")
	for n, ok := range map[int]bool{0: false, 1: true, 1000: true, 1001: false} {
		pol, _ := json.Marshal(map[string]any{"deadLetterTargetArn": arn, "maxReceiveCount": n})
		_, err := s.CreateQueue("src"+strconv.Itoa(n), map[string]string{"RedrivePolicy": string(pol)}, nil)
		if ok && err != nil || !ok && err == nil {
			t.Errorf("maxReceiveCount %d: err=%v, want ok=%v", n, err, ok)
		}
	}
	arns := func(n int) string {
		var a []string
		for i := 0; i < n; i++ {
			a = append(a, "arn:aws:sqs:us-east-1:000000000000:q"+strconv.Itoa(i))
		}
		b, _ := json.Marshal(map[string]any{"redrivePermission": "byQueue", "sourceQueueArns": a})
		return string(b)
	}
	for n, ok := range map[int]bool{0: false, 1: true, 10: true, 11: false} {
		if got := validRedriveAllowPolicy(arns(n)); (got == nil) != ok {
			t.Errorf("%d sourceQueueArns: ok=%v, want %v", n, got == nil, ok)
		}
	}
}

func TestMoveTaskTimestampIsInMilliseconds(t *testing.T) {
	f := newRedriveFixture(t, "dlq")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	f.deadLetter("orders", "one")
	before := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := f.move(startMove(f.arns["dlq"])); err != nil {
		t.Fatal(err)
	}
	list, err := f.c.ListMessageMoveTasks(f.ctx, listMoves(f.arns["dlq"]))
	if err != nil || len(list.Results) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if got := list.Results[0].StartedTimestamp; got < before || got > time.Now().Add(time.Minute).UnixMilli() {
		t.Errorf("StartedTimestamp %d is not epoch milliseconds around now", got)
	}
}

func TestCancelAndUntagNeedWhatTheyNeed(t *testing.T) {
	ts := testServer(t)
	if code, _ := jsonBody(t, ts.URL, "CancelMessageMoveTask", `{"TaskHandle":""}`); code != 400 {
		t.Errorf("CancelMessageMoveTask with an empty handle answered %d", code)
	}
	if code, _ := jsonBody(t, ts.URL, "UntagQueue", `{"QueueUrl":"`+ts.URL+`/000000000000/nowhere","TagKeys":["a"]}`); code != 400 {
		t.Errorf("UntagQueue on a missing queue answered %d", code)
	}
}

func startMove(arn string) *awssqs.StartMessageMoveTaskInput {
	return &awssqs.StartMessageMoveTaskInput{SourceArn: aws.String(arn)}
}

func listMoves(arn string) *awssqs.ListMessageMoveTasksInput {
	return &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(arn)}
}

// A message is kept for the retention period and not a nanosecond more, on all
// three paths that read it: a receive, a peek, and the janitor's sweep.
func TestRetentionEdge(t *testing.T) {
	for _, path := range []string{"receive", "peek", "sweep"} {
		t.Run(path, func(t *testing.T) {
			s := testStore(t)
			t0 := time.Unix(1_700_000_000, 0)
			now := t0
			s.clock = func() time.Time { return now }
			if _, err := s.CreateQueue("q", map[string]string{"MessageRetentionPeriod": "60"}, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Send("q", "m", nil, -1, "", "", nil); err != nil {
				t.Fatal(err)
			}
			count := func() int {
				switch path {
				case "receive":
					got, _ := s.Receive("q", 10, 0, 0)
					return len(got)
				case "peek":
					got, _ := s.Peek("q", 10)
					return len(got)
				}
				s.Sweep()
				a, _ := s.Attributes("q")
				n, _ := strconv.Atoi(a["ApproximateNumberOfMessages"])
				return n
			}
			now = t0.Add(60 * time.Second)
			if got := count(); got != 1 {
				t.Errorf("at exactly the retention period the message was %d, want kept", got)
			}
			now = t0.Add(60*time.Second + time.Nanosecond)
			if got := count(); got != 0 {
				t.Errorf("a nanosecond past the retention period %d messages remain, want none", got)
			}
		})
	}
}

// The deduplication window is five minutes, to the second.
func TestDedupWindowEdge(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	if _, err := s.CreateQueue("q.fifo", map[string]string{"FifoQueue": "true"}, nil); err != nil {
		t.Fatal(err)
	}
	first, err := s.Send("q.fifo", "a", nil, -1, "g", "d", nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(300 * time.Second)
	again, err := s.Send("q.fifo", "a", nil, -1, "g", "d", nil)
	if err != nil || again.ID != first.ID {
		t.Errorf("at exactly five minutes a repeat was a new message: %v %v", again, err)
	}
	now = now.Add(2 * time.Second)
	later, err := s.Send("q.fifo", "a", nil, -1, "g", "d", nil)
	if err != nil || later.ID == first.ID {
		t.Errorf("past five minutes a repeat was still a duplicate: %v %v", later, err)
	}
}

// ChangeMessageVisibility needs the message to be held, and at the instant its
// timeout runs out it is not.
func TestNotInFlightEdge(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0)
	s.clock = func() time.Time { return now }
	if _, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "30"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "m", nil, -1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Receive("q", 1, 0, -1)
	h := got[0].Handle()
	now = now.Add(30*time.Second - time.Nanosecond)
	if err := s.ChangeVisibility("q", h, 10); err != nil {
		t.Errorf("a nanosecond before the timeout runs out: %v", err)
	}
	// That change moved the deadline to now+10s; step to it exactly.
	now = now.Add(10 * time.Second)
	if err := s.ChangeVisibility("q", h, 10); err == nil {
		t.Error("at the instant the timeout ran out the message was still treated as held")
	}
}

// Timestamps a consumer reads are epoch milliseconds, and the dedup and group
// ids come back only when the message has them.
func TestSystemAttributeValues(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 123_000_000)
	s.clock = func() time.Time { return now }
	if _, err := s.CreateQueue("q", nil, nil); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Send("q", "x", nil, -1, "tenant", "", nil)
	got := systemAttrs(*m, []string{"All"}, false)
	if got["SentTimestamp"] != "1700000000123" {
		t.Errorf("SentTimestamp %q, want 1700000000123", got["SentTimestamp"])
	}
	if _, has := got["ApproximateFirstReceiveTimestamp"]; has {
		t.Error("a message never received has an ApproximateFirstReceiveTimestamp")
	}
	if _, has := got["MessageDeduplicationId"]; has {
		t.Error("a message with no deduplication id reports one")
	}
	if got["MessageGroupId"] != "tenant" {
		t.Errorf("MessageGroupId %q", got["MessageGroupId"])
	}
	recv, _ := s.Receive("q", 1, 0, -1)
	got = systemAttrs(recv[0], []string{"All"}, false)
	if got["ApproximateFirstReceiveTimestamp"] != "1700000000123" {
		t.Errorf("ApproximateFirstReceiveTimestamp %q", got["ApproximateFirstReceiveTimestamp"])
	}
}

// A batch entry's own DelaySeconds is judged against 0 to 900 on its own.
func TestBatchEntryDelayEdge(t *testing.T) {
	ts := testServer(t)
	jsonBody(t, ts.URL, "CreateQueue", `{"QueueName":"edge"}`)
	for d, ok := range map[int]bool{0: true, 900: true, 901: false, -1: false} {
		_, body := jsonBody(t, ts.URL, "SendMessageBatch", `{"QueueUrl":"`+ts.URL+`/000000000000/edge","Entries":[{"Id":"a","MessageBody":"x","DelaySeconds":`+strconv.Itoa(d)+`}]}`)
		failed := strings.Contains(body, `"Failed":[{`)
		if failed == ok {
			t.Errorf("entry DelaySeconds %d: failed=%v, want ok=%v: %.200s", d, failed, ok, body)
		}
	}
}

// MD5OfMessageAttributes, from the algorithm as AWS documents it, written out
// here independently of md5.go: names in order, and for each the length-prefixed
// name, the length-prefixed data type, a transport byte (1 for a string or a
// number, 2 for binary) and the length-prefixed value.
func TestAttributeDigestMatchesTheDocumentedAlgorithm(t *testing.T) {
	attrs := map[string]attr{
		"b-blob": {DataType: "Binary", BinaryValue: []byte{0, 255, 7}},
		"a-text": {DataType: "String", StringValue: "hello"},
		"c-num":  {DataType: "Number.int", StringValue: "42"},
	}
	h := md5.New()
	field := func(b []byte) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(b)))
		h.Write(l[:])
		h.Write(b)
	}
	for _, n := range []string{"a-text", "b-blob", "c-num"} {
		a := attrs[n]
		field([]byte(n))
		field([]byte(a.DataType))
		if a.DataType == "Binary" {
			h.Write([]byte{2})
			field(a.BinaryValue)
		} else {
			h.Write([]byte{1})
			field([]byte(a.StringValue))
		}
	}
	want := fmt.Sprintf("%x", h.Sum(nil))
	if got := md5Attributes(attrs); got != want {
		t.Errorf("md5Attributes = %s, want %s", got, want)
	}
}
