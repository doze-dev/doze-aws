package sqs

// Rejection parity for SQS, driven by the cases dzaudit derives from AWS's own
// service model (`dzaudit cases sqs`).
//
// Same rule as everywhere else: a request refused for the WRONG reason looks
// exactly like a pass, so every case is a mutation of a baseline this test
// first proves the service accepts.
//
// Driven over the JSON protocol, which SQS accepts alongside Query. The
// validation itself runs on a protocol-neutral view (params.asMap), so this
// exercises the same table a Query client would hit — and sqs_test.go's
// existing SDK tests cover the Query side end to end.
//
// This audit supplements the hand-derived checks rather than replacing them.
// SQS's model states almost nothing about queue names, visibility timeouts or
// redrive policies — those constraints live only in prose, which is why
// rejection_parity's older tests in this file exist at all.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/doze-dev/doze-aws/internal/auditkit"
)

type auditCase struct {
	Operation  string `json:"operation"`
	Target     string `json:"target"`
	Path       string `json:"path"`
	Why        string `json:"why"`
	Value      any    `json:"value"`
	Constraint string `json:"constraint"`
}

func sqsAuditServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

func call(t *testing.T, ts *httptest.Server, target string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader(raw))
	req.Header.Set("X-Amz-Target", "AmazonSQS."+target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/sqs/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

const (
	auditQueue = "audit-q"
	auditURL   = "http://sqs.doze-aws.internal/000000000000/audit-q"
	auditARN   = "arn:aws:sqs:us-east-1:000000000000:audit-q"

	auditDLQ    = "audit-dlq"
	auditDLQARN = "arn:aws:sqs:us-east-1:000000000000:audit-dlq"
)

// receipt is a live receipt handle from the fixture queue. Operations that
// address a message need one that exists — an invented handle is refused as
// invalid, which is a refusal for the wrong reason.
var receipt string

func setUpFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	if code, body := call(t, ts, "CreateQueue", map[string]any{"QueueName": auditQueue}); code != http.StatusOK {
		t.Fatalf("fixture CreateQueue = %d: %s", code, body)
	}
	if code, body := call(t, ts, "SendMessage", map[string]any{
		"QueueUrl": auditURL, "MessageBody": "audit",
	}); code != http.StatusOK {
		t.Fatalf("fixture SendMessage = %d: %s", code, body)
	}
	code, body := call(t, ts, "ReceiveMessage", map[string]any{
		"QueueUrl": auditURL, "MaxNumberOfMessages": 1, "VisibilityTimeout": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("fixture ReceiveMessage = %d: %s", code, body)
	}
	var out struct {
		Messages []struct {
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Messages) == 0 {
		t.Fatalf("fixture ReceiveMessage gave no handle: %s", body)
	}
	receipt = out.Messages[0].ReceiptHandle

	// StartMessageMoveTask's source has to be a real dead-letter queue, so the
	// fixture builds the pair rather than pointing the operation at itself.
	if code, body := call(t, ts, "CreateQueue", map[string]any{"QueueName": auditDLQ}); code != http.StatusOK {
		t.Fatalf("fixture CreateQueue(dlq) = %d: %s", code, body)
	}
	rp := `{"deadLetterTargetArn":"` + auditDLQARN + `","maxReceiveCount":3}`
	if code, body := call(t, ts, "SetQueueAttributes", map[string]any{
		"QueueUrl": auditURL, "Attributes": map[string]any{"RedrivePolicy": rp},
	}); code != http.StatusOK {
		t.Fatalf("fixture SetQueueAttributes = %d: %s", code, body)
	}
}

// baselines are requests the service must accept, one per operation.
func baselines() map[string]map[string]any {
	q := map[string]any{"QueueUrl": auditURL}
	return map[string]map[string]any{
		"CreateQueue":                {"QueueName": "made-by-baseline"},
		"DeleteQueue":                {"QueueUrl": "http://sqs.doze-aws.internal/000000000000/made-by-baseline"},
		"GetQueueUrl":                {"QueueName": auditQueue},
		"PurgeQueue":                 q,
		"GetQueueAttributes":         {"QueueUrl": auditURL, "AttributeNames": []any{"All"}},
		"SetQueueAttributes":         {"QueueUrl": auditURL, "Attributes": map[string]any{"VisibilityTimeout": "30"}},
		"ListQueueTags":              q,
		"TagQueue":                   {"QueueUrl": auditURL, "Tags": map[string]any{"env": "dev"}},
		"UntagQueue":                 {"QueueUrl": auditURL, "TagKeys": []any{"env"}},
		"ListDeadLetterSourceQueues": q,
		"SendMessage":                {"QueueUrl": auditURL, "MessageBody": "hello"},
		"SendMessageBatch": {"QueueUrl": auditURL, "Entries": []any{
			map[string]any{"Id": "1", "MessageBody": "hello"},
		}},
		"ReceiveMessage":          {"QueueUrl": auditURL, "MaxNumberOfMessages": 1, "VisibilityTimeout": 0},
		"DeleteMessage":           {"QueueUrl": auditURL, "ReceiptHandle": receipt},
		"DeleteMessageBatch":      {"QueueUrl": auditURL, "Entries": []any{map[string]any{"Id": "1", "ReceiptHandle": receipt}}},
		"ChangeMessageVisibility": {"QueueUrl": auditURL, "ReceiptHandle": receipt, "VisibilityTimeout": 30},
		"ChangeMessageVisibilityBatch": {"QueueUrl": auditURL, "Entries": []any{
			map[string]any{"Id": "1", "ReceiptHandle": receipt, "VisibilityTimeout": 30},
		}},
		"AddPermission":         {"QueueUrl": auditURL, "Label": "audit", "AWSAccountIds": []any{"000000000000"}, "Actions": []any{"SendMessage"}},
		"RemovePermission":      {"QueueUrl": auditURL, "Label": "audit"},
		"StartMessageMoveTask":  {"SourceArn": auditDLQARN, "DestinationArn": auditARN},
		"ListMessageMoveTasks":  {"SourceArn": auditDLQARN},
		"CancelMessageMoveTask": {"TaskHandle": "audit-task"},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Entries[]":                     []any{map[string]any{"Id": "1", "MessageBody": "hello"}},
		"AttributeNames[]":              []any{"All"},
		"MessageSystemAttributeNames[]": []any{"All"},
		"MessageAttributes{}": map[string]any{
			"kind": map[string]any{"DataType": "String", "StringValue": "order"},
		},
		"MessageSystemAttributes{}": map[string]any{
			"AWSTraceHeader": map[string]any{"DataType": "String", "StringValue": "Root=r;Parent=1"},
		},
		"Entries[].MessageAttributes{}": map[string]any{
			"kind": map[string]any{"DataType": "String", "StringValue": "order"},
		},
		"Entries[].MessageSystemAttributes{}": map[string]any{
			"AWSTraceHeader": map[string]any{"DataType": "String", "StringValue": "Root=r;Parent=1"},
		},
	}
}

// prepare gives the non-idempotent operations their own preconditions, so no
// group depends on another having run — operations execute in alphabetical
// order, which is not the order that would make them work.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	if mutating == "QueueName" || mutating == "QueueUrl" {
		return // never overwrite the member the case is about
	}
	switch op {
	case "CreateQueue":
		body["QueueName"] = fmt.Sprintf("created-%d", n)
	case "DeleteQueue":
		name := fmt.Sprintf("doomed-%d", n)
		call(t, ts, "CreateQueue", map[string]any{"QueueName": name})
		body["QueueUrl"] = "http://sqs.doze-aws.internal/000000000000/" + name
	}
}

// needState are operations the audit cannot give a working baseline without
// reshaping what every other case reads. Skipped WITH A REASON, because a case
// nobody ran is not a case that passed.
var needState = map[string]string{
	"CancelMessageMoveTask": "there is never an active task to cancel — local message " +
		"moves complete synchronously, so the baseline is refused however it is built",
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently, so anything NOT on the list fails
// the moment it appears.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_sqs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs []auditCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 {
		t.Fatal("no cases: the audit would pass vacuously")
	}
	return cs
}

func TestSQSRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	ts := sqsAuditServer(t)
	setUpFixture(t, ts)
	base, ex := baselines(), exemplars()

	n := 0
	seq := func() int { n++; return n }

	byOp := map[string][]auditCase{}
	for _, c := range loadCases(t) {
		byOp[c.Operation] = append(byOp[c.Operation], c)
	}
	ops := make([]string, 0, len(byOp))
	for op := range byOp {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	var total, gaps, unbuildable, skipped int
	for _, op := range ops {
		if why, ok := needState[op]; ok {
			skipped += len(byOp[op])
			t.Logf("skipping %s (%d cases): %s", op, len(byOp[op]), why)
			continue
		}
		b, ok := base[op]
		if !ok {
			t.Errorf("%s has %d model-derived cases and no baseline", op, len(byOp[op]))
			continue
		}
		t.Run(op, func(t *testing.T) {
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, op, "", bl, seq())
			if code, body := call(t, ts, op, bl); code != http.StatusOK {
				t.Fatalf("the baseline request was refused (%d): %s\nevery %s case would be meaningless",
					code, body, op)
			}

			// Every container an exemplar stands in for must leave the baseline
			// acceptable, or the cases under it are refused for the exemplar.
			paths := make([]string, 0, len(byOp[op]))
			for _, c := range byOp[op] {
				paths = append(paths, c.Path)
			}
			for _, prefix := range auditkit.Containers(paths) {
				probe := auditkit.DeepCopy(b).(map[string]any)
				if err := auditkit.Apply(probe, ex, prefix+".probe", nil, false); err != nil {
					t.Errorf("container %s: %v", prefix, err)
					continue
				}
				prepare(t, ts, op, "", probe, seq())
				if code, resp := call(t, ts, op, probe); code != http.StatusOK {
					t.Errorf("the exemplar for %q makes the baseline invalid (%d): %s\n"+
						"  Every case under it would be refused for the exemplar, not the mutation.",
						prefix, code, resp)
				}
			}

			for _, c := range byOp[op] {
				total++
				t.Run(c.Path+"/"+c.Why, func(t *testing.T) {
					body := auditkit.DeepCopy(b).(map[string]any)
					if err := auditkit.Apply(body, ex, c.Path, c.Value, true); err != nil {
						unbuildable++
						t.Fatalf("could not build the case: %v\n"+
							"This is a hole in the harness, not a finding about the service.", err)
					}
					prepare(t, ts, op, c.Path, body, seq())

					key := op + "/" + c.Path + "/" + c.Why
					code, resp := call(t, ts, op, body)
					if code == http.StatusOK {
						gaps++
						if !knownGaps[key] {
							t.Errorf("accepted %s = %v\n  AWS refuses it: %s\n  constraint: %s\n"+
								"  This is a NEW gap. Fix it, or add %q to knownGaps with a reason.",
								c.Path, c.Value, c.Why, c.Constraint, key)
						}
						return
					}
					if code >= 500 {
						t.Fatalf("%s = %d (a refusal should be a 4xx): %s", c.Path, code, resp)
					}
					if knownGaps[key] {
						t.Errorf("%s is enforced now — delete it from knownGaps", key)
					}
				})
			}
		})
	}

	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d operations "+
		"(%d skipped, %d unbuildable)",
		total-gaps-unbuildable, total, len(ops)-len(needState), skipped, unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
