package logs

// Rejection parity for CloudWatch Logs, driven by the cases dzaudit derives
// from AWS's own service model (`dzaudit cases cloudwatch-logs`, scoped to
// the 18 operations this build dispatches — a mutation replayed against an
// operation that refuses everything proves nothing).
//
// Same rule as everywhere: a request refused for the WRONG reason looks
// exactly like a pass, so every case is a mutation of a baseline this test
// first proves the service accepts.

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

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/auditkit"
)

type auditCase struct {
	Operation   string           `json:"operation"`
	Target      string           `json:"target"`
	Path        string           `json:"path"`
	Why         string           `json:"why"`
	Value       any              `json:"value"`
	ValueRepeat *auditkit.Repeat `json:"value_repeat,omitempty"`
	Constraint  string           `json:"constraint"`
}

func logsServer(t *testing.T) *httptest.Server {
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

func call(t *testing.T, ts *httptest.Server, action string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader(raw))
	req.Header.Set("X-Amz-Target", "Logs_20140328."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/logs/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// The fixture: a group with a stream and a tag, so every read and every
// removal has something to address.
const (
	auditGroup  = "/audit/group"
	auditStream = "audit-stream"
)

func setUpFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	for _, step := range []struct {
		op   string
		body map[string]any
	}{
		{"CreateLogGroup", map[string]any{"logGroupName": auditGroup, "tags": map[string]any{"env": "dev"}}},
		{"CreateLogStream", map[string]any{"logGroupName": auditGroup, "logStreamName": auditStream}},
		{"PutRetentionPolicy", map[string]any{"logGroupName": auditGroup, "retentionInDays": 7}},
	} {
		if code, body := call(t, ts, step.op, step.body); code != http.StatusOK {
			t.Fatalf("fixture %s = %d: %s", step.op, code, body)
		}
	}
}

func baselines() map[string]map[string]any {
	group := map[string]any{"logGroupName": auditGroup}
	// The tag operations take the ARN without the :* suffix DescribeLogGroups
	// reports — the model's pattern for resourceArn has no *.
	arn := map[string]any{"resourceArn": awsident.ARN("logs", "log-group:"+auditGroup)}
	return map[string]map[string]any{
		"CreateLogGroup":        {"logGroupName": "/audit/created"},
		"DeleteLogGroup":        {"logGroupName": "/audit/doomed"},
		"DescribeLogGroups":     {},
		"ListLogGroups":         {},
		"PutRetentionPolicy":    {"logGroupName": auditGroup, "retentionInDays": 7},
		"DeleteRetentionPolicy": group,
		"CreateLogStream":       {"logGroupName": auditGroup, "logStreamName": "created"},
		"DeleteLogStream":       {"logGroupName": auditGroup, "logStreamName": "doomed"},
		"DescribeLogStreams":    group,
		"PutLogEvents": {"logGroupName": auditGroup, "logStreamName": auditStream,
			"logEvents": []any{map[string]any{"timestamp": 1000, "message": "hello"}}},
		"GetLogEvents":        {"logGroupName": auditGroup, "logStreamName": auditStream},
		"FilterLogEvents":     group,
		"TagResource":         {"resourceArn": arn["resourceArn"], "tags": map[string]any{"team": "audit"}},
		"UntagResource":       {"resourceArn": arn["resourceArn"], "tagKeys": []any{"team"}},
		"ListTagsForResource": arn,
		"TagLogGroup":         {"logGroupName": auditGroup, "tags": map[string]any{"team": "audit"}},
		"UntagLogGroup":       {"logGroupName": auditGroup, "tags": []any{"team"}},
		"ListTagsLogGroup":    group,
		"PutSubscriptionFilter": {"logGroupName": auditGroup, "filterName": "audit-sub", "filterPattern": "ERROR",
			"destinationArn": awsident.ARN("lambda", "function:audit-sink")},
		"DeleteSubscriptionFilter":    {"logGroupName": auditGroup, "filterName": "doomed-sub"},
		"DescribeSubscriptionFilters": group,
	}
}

func exemplars() map[string]any {
	return map[string]any{
		"tags{}":                  map[string]any{"env": "dev"},
		"tags[]":                  []any{"env"},
		"tagKeys[]":               []any{"env"},
		"accountIdentifiers[]":    []any{awsident.AccountID},
		"logGroupIdentifiers[]":   []any{auditGroup},
		"logStreamNames[]":        []any{auditStream},
		"fieldIndexNames[]":       []any{"level"},
		"logGroupTags[]":          []any{map[string]any{"key": "env", "values": []any{"dev"}}},
		"logGroupTags[].values[]": []any{"dev"},
		"dataSources[]":           []any{map[string]any{"name": "x"}},
		"logEvents[]":             []any{map[string]any{"timestamp": 1000, "message": "hello"}},
		"entity":                  map[string]any{"keyAttributes": map[string]any{"Type": "Service"}},
		"entity.attributes{}":     map[string]any{"k": "v"},
		"entity.keyAttributes{}":  map[string]any{"Type": "Service"},
	}
}

// prepare gives the operations that consume what they name their own
// preconditions: a fresh name per create, a victim per delete.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	switch op {
	case "CreateLogGroup":
		if mutating != "logGroupName" {
			body["logGroupName"] = fmt.Sprintf("/audit/created-%d", n)
		}
	case "DeleteLogGroup":
		if mutating != "logGroupName" {
			name := fmt.Sprintf("/audit/doomed-%d", n)
			call(t, ts, "CreateLogGroup", map[string]any{"logGroupName": name})
			body["logGroupName"] = name
		}
	case "CreateLogStream":
		if mutating != "logStreamName" {
			body["logStreamName"] = fmt.Sprintf("created-%d", n)
		}
	case "DeleteLogStream":
		if mutating != "logStreamName" {
			name := fmt.Sprintf("doomed-%d", n)
			call(t, ts, "CreateLogStream", map[string]any{"logGroupName": auditGroup, "logStreamName": name})
			body["logStreamName"] = name
		}
	case "DeleteSubscriptionFilter":
		// A group holds two filters at most, so each delete gets its own group.
		if mutating != "filterName" && mutating != "logGroupName" {
			group := fmt.Sprintf("/audit/sub-%d", n)
			call(t, ts, "CreateLogGroup", map[string]any{"logGroupName": group})
			call(t, ts, "PutSubscriptionFilter", map[string]any{"logGroupName": group, "filterName": "doomed-sub",
				"filterPattern": "", "destinationArn": awsident.ARN("lambda", "function:audit-sink")})
			body["logGroupName"] = group
		}
	}
}

var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_logs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs []auditCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	// A max-length case stores the shape of its padding rather than the run
	// itself: written out, those runs were 35 MB of the 37.5 MB of fixtures.
	for i := range cs {
		cs[i].Value = auditkit.Materialize(cs[i].Value, cs[i].ValueRepeat)
	}
	if len(cs) == 0 {
		t.Fatal("no cases: the audit would pass vacuously")
	}
	return cs
}

func TestLogsRejectsWhatTheModelForbids(t *testing.T) {
	ts := logsServer(t)
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

	var total, gaps, unbuildable int
	for _, op := range ops {
		b, ok := base[op]
		if !ok {
			t.Errorf("%s has %d model-derived cases and no baseline", op, len(byOp[op]))
			continue
		}
		t.Run(op, func(t *testing.T) {
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, op, "", bl, seq())
			if code, body := call(t, ts, op, bl); code != http.StatusOK {
				t.Fatalf("the baseline request was refused (%d): %s\nevery %s case would be meaningless", code, body, op)
			}
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
					t.Errorf("the exemplar for %q makes the baseline invalid (%d): %s", prefix, code, resp)
				}
			}
			for _, c := range byOp[op] {
				total++
				t.Run(c.Path+"/"+c.Why, func(t *testing.T) {
					body := auditkit.DeepCopy(b).(map[string]any)
					if err := auditkit.Apply(body, ex, c.Path, c.Value, true); err != nil {
						unbuildable++
						t.Fatalf("could not build the case: %v", err)
					}
					prepare(t, ts, op, c.Path, body, seq())
					key := op + "/" + c.Path + "/" + c.Why
					code, resp := call(t, ts, op, body)
					if code == http.StatusOK {
						gaps++
						if !knownGaps[key] {
							t.Errorf("accepted %s = %v\n  AWS refuses it: %s\n  constraint: %s\n  NEW gap: fix it, or add %q to knownGaps with a reason.",
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
	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d operations (%d unbuildable)",
		total-gaps-unbuildable, total, len(ops), unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
