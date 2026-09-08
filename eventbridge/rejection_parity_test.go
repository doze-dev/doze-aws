package eventbridge

// Rejection parity for EventBridge, driven by the cases dzaudit derives from
// AWS's own service model (`dzaudit cases eventbridge`).
//
// Same rule as everywhere: a request refused for the WRONG reason looks exactly
// like a pass, so every case is a mutation of a baseline this test first proves
// the service accepts.
//
// The state here is a bus, a rule with a target, and an archive — the things
// most operations address. Two of them consume what they name: DeleteRule and
// DeleteArchive make their own, because a group that deletes the fixture leaves
// every later baseline refused as not-found, which proves nothing about
// validation.

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
	"strings"
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

func ebServer(t *testing.T) *httptest.Server {
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
	req.Header.Set("X-Amz-Target", "AWSEvents."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/events/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// fx is the state the baselines address.
type fx struct {
	bus        string
	busARN     string
	rule       string
	ruleARN    string
	archive    string
	archiveARN string
	httpFx
}

func setUpFixture(t *testing.T, ts *httptest.Server) fx {
	t.Helper()
	f := fx{bus: "audit-bus", rule: "audit-rule", archive: "audit-archive"}
	code, body := call(t, ts, "CreateEventBus", map[string]any{"Name": f.bus})
	if code != http.StatusOK {
		t.Fatalf("fixture CreateEventBus = %d: %s", code, body)
	}
	var out struct {
		EventBusArn string `json:"EventBusArn"`
	}
	if json.Unmarshal([]byte(body), &out) != nil || out.EventBusArn == "" {
		t.Fatalf("fixture CreateEventBus gave no ARN: %s", body)
	}
	f.busARN = out.EventBusArn

	code, body = call(t, ts, "PutRule", map[string]any{
		"Name": f.rule, "EventBusName": f.bus, "EventPattern": `{"source":["audit"]}`,
	})
	if code != http.StatusOK {
		t.Fatalf("fixture PutRule = %d: %s", code, body)
	}
	var ro struct {
		RuleArn string `json:"RuleArn"`
	}
	json.Unmarshal([]byte(body), &ro)
	f.ruleARN = ro.RuleArn
	if code, body := call(t, ts, "PutTargets", map[string]any{
		"Rule": f.rule, "EventBusName": f.bus, "Targets": []any{
			map[string]any{"Id": "t1", "Arn": "arn:aws:sqs:us-east-1:000000000000:audit-q"},
		},
	}); code != http.StatusOK {
		t.Fatalf("fixture PutTargets = %d: %s", code, body)
	}
	code, body = call(t, ts, "CreateArchive", map[string]any{
		"ArchiveName": f.archive, "EventSourceArn": f.busARN,
	})
	if code != http.StatusOK {
		t.Fatalf("fixture CreateArchive = %d: %s", code, body)
	}
	var ao struct {
		ArchiveArn string `json:"ArchiveArn"`
	}
	json.Unmarshal([]byte(body), &ao)
	f.archiveARN = ao.ArchiveArn
	f.httpFx = setUpHTTPFixture(t, ts)
	return f
}

// baselines are requests the service must accept, one per operation.
func baselines(f fx) map[string]map[string]any {
	rule := map[string]any{"Name": f.rule, "EventBusName": f.bus}
	arch := map[string]any{"ArchiveName": f.archive}
	sqsARN := "arn:aws:sqs:us-east-1:000000000000:audit-q"
	out := map[string]map[string]any{
		"CreateEventBus":   {"Name": "made-by-baseline"},
		"DescribeEventBus": {"Name": f.bus},
		"ListEventBuses":   {},
		"DeleteEventBus":   {"Name": "made-by-baseline"},
		"PutRule":          {"Name": f.rule, "EventBusName": f.bus, "EventPattern": `{"source":["audit"]}`},
		"DescribeRule":     rule,
		"ListRules":        {"EventBusName": f.bus},
		"EnableRule":       rule,
		"DisableRule":      rule,
		"DeleteRule":       {"Name": "made-by-baseline-rule", "EventBusName": f.bus},
		"PutTargets": {"Rule": f.rule, "EventBusName": f.bus, "Targets": []any{
			map[string]any{"Id": "t1", "Arn": sqsARN},
		}},
		"ListTargetsByRule":     {"Rule": f.rule, "EventBusName": f.bus},
		"RemoveTargets":         {"Rule": f.rule, "EventBusName": f.bus, "Ids": []any{"t1"}},
		"ListRuleNamesByTarget": {"TargetArn": sqsARN, "EventBusName": f.bus},
		"PutEvents": {"Entries": []any{
			map[string]any{"Source": "audit", "DetailType": "t", "Detail": "{}", "EventBusName": f.bus},
		}},
		"TestEventPattern": {"EventPattern": `{"source":["audit"]}`, "Event": `{"source":"audit","detail-type":"t","detail":{}}`},
		"CreateArchive":    {"ArchiveName": "made-by-baseline-archive", "EventSourceArn": f.busARN},
		"DescribeArchive":  arch,
		"ListArchives":     {},
		"UpdateArchive":    arch,
		"DeleteArchive":    {"ArchiveName": "made-by-baseline-archive"},
		// A replay replays an ARCHIVE; the bus ARN is refused as not-found.
		"StartReplay": {"ReplayName": "made-by-baseline-replay", "EventSourceArn": f.archiveARN,
			"EventStartTime": 1, "EventEndTime": 2,
			"Destination": map[string]any{"Arn": f.busARN}},
		"DescribeReplay":      {"ReplayName": "made-by-baseline-replay"},
		"ListReplays":         {},
		"CancelReplay":        {"ReplayName": "made-by-baseline-replay"},
		"TagResource":         {"ResourceARN": f.ruleARN, "Tags": []any{map[string]any{"Key": "env", "Value": "dev"}}},
		"UntagResource":       {"ResourceARN": f.ruleARN, "TagKeys": []any{"env"}},
		"ListTagsForResource": {"ResourceARN": f.ruleARN},
	}
	for op, b := range httpBaselines(f.httpFx) {
		out[op] = b
	}
	return out
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	sqsARN := "arn:aws:sqs:us-east-1:000000000000:audit-q"
	out := map[string]any{
		"Tags[]":                           []any{map[string]any{"Key": "env", "Value": "dev"}},
		"TagKeys[]":                        []any{"env"},
		"Ids[]":                            []any{"t1"},
		"Targets[]":                        []any{map[string]any{"Id": "t1", "Arn": sqsARN}},
		"Targets[].BatchParameters":        map[string]any{"JobDefinition": "jd", "JobName": "jn"},
		"Targets[].AppSyncParameters":      map[string]any{"GraphQLOperation": "query Q { f }"},
		"Targets[].EcsParameters":          map[string]any{"TaskDefinitionArn": "arn:aws:ecs:us-east-1:000000000000:task-definition/t:1"},
		"Targets[].HttpParameters":         map[string]any{},
		"Targets[].InputTransformer":       map[string]any{"InputTemplate": "{}"},
		"Targets[].KinesisParameters":      map[string]any{"PartitionKeyPath": "$.id"},
		"Targets[].RedshiftDataParameters": map[string]any{"Database": "db", "Sql": "select 1"},
		"Targets[].RetryPolicy":            map[string]any{"MaximumRetryAttempts": 1},
		"Targets[].RunCommandParameters": map[string]any{
			"RunCommandTargets": []any{map[string]any{"Key": "tag:env", "Values": []any{"dev"}}},
		},
		"Targets[].SageMakerPipelineParameters": map[string]any{},
		"Targets[].SqsParameters":               map[string]any{"MessageGroupId": "g"},
		"Targets[].DeadLetterConfig":            map[string]any{"Arn": sqsARN},
		"Targets[].RoleArn":                     "arn:aws:iam::000000000000:role/r",
		"DeadLetterConfig":                      map[string]any{"Arn": sqsARN},
		"LogConfig":                             map[string]any{"Level": "OFF"},
		"Destination":                           map[string]any{"Arn": "arn:aws:events:us-east-1:000000000000:event-bus/audit-bus"},
		"Entries[]":                             []any{map[string]any{"Source": "audit", "DetailType": "t", "Detail": "{}"}},
	}
	for k, v := range httpExemplars() {
		out[k] = v
	}
	return out
}

// prepare gives the non-idempotent operations their own preconditions, so no
// group depends on another having run — operations execute in alphabetical
// order, which is not the order that would make them work.
func prepare(t *testing.T, ts *httptest.Server, f fx, op, mutating string, body map[string]any, n int) {
	t.Helper()
	switch op {
	case "CreateEventBus":
		if mutating != "Name" {
			body["Name"] = fmt.Sprintf("created-%d", n)
		}
	case "DeleteEventBus":
		if mutating != "Name" {
			name := fmt.Sprintf("doomed-bus-%d", n)
			call(t, ts, "CreateEventBus", map[string]any{"Name": name})
			body["Name"] = name
		}
	case "DeleteRule":
		if mutating != "Name" {
			name := fmt.Sprintf("doomed-rule-%d", n)
			call(t, ts, "PutRule", map[string]any{"Name": name, "EventBusName": f.bus, "EventPattern": `{"source":["x"]}`})
			body["Name"] = name
		}
	case "CreateArchive":
		if mutating != "ArchiveName" {
			body["ArchiveName"] = fmt.Sprintf("created-archive-%d", n)
		}
	case "DeleteArchive":
		if mutating != "ArchiveName" {
			name := fmt.Sprintf("doomed-archive-%d", n)
			call(t, ts, "CreateArchive", map[string]any{"ArchiveName": name, "EventSourceArn": f.busARN})
			body["ArchiveName"] = name
		}
	case "StartReplay":
		if mutating != "ReplayName" {
			body["ReplayName"] = fmt.Sprintf("replay-%d", n)
		}
	case "DescribeReplay", "CancelReplay":
		if mutating != "ReplayName" {
			name := fmt.Sprintf("known-replay-%d", n)
			call(t, ts, "StartReplay", map[string]any{
				"ReplayName": name, "EventSourceArn": f.archiveARN,
				"EventStartTime": 1, "EventEndTime": 2,
				"Destination": map[string]any{"Arn": f.busARN},
			})
			body["ReplayName"] = name
		}
	default:
		prepareHTTP(t, ts, f.httpFx, op, mutating, body, n)
	}
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

// needState are operations the audit cannot give a working baseline without
// destroying what every other case reads. Skipped WITH A REASON.
var needState = map[string]string{}

// pastValidation are operations whose *valid* request is still refused, for a
// reason that is not validation — so there is no accepted baseline to mutate.
// Rather than skip them, the baseline must be refused with exactly this code,
// which proves it cleared validation, and every case under the operation must
// then be refused with ValidationException specifically. That is stricter than
// the normal path, where any 4xx after a single mutation is attributed to the
// mutation.
var pastValidation = map[string]string{
	// doze-aws replays an archive synchronously inside StartReplay, so a replay
	// is COMPLETED the instant it exists and none is ever cancellable. Real AWS
	// replays run for a while and can be cancelled mid-flight; the difference is
	// in the timing model, not in what CancelReplay validates.
	"CancelReplay": "IllegalStatusException",
}

// errCode reads the __type an awsJson error carries, shorn of any namespace.
func errCode(body string) string {
	var e struct {
		Type string `json:"__type"`
	}
	if json.Unmarshal([]byte(body), &e) != nil {
		return ""
	}
	if i := strings.LastIndex(e.Type, "#"); i >= 0 {
		return e.Type[i+1:]
	}
	return e.Type
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_eventbridge.json"))
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

func TestEventBridgeRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	ts := ebServer(t)
	f := setUpFixture(t, ts)
	base, ex := baselines(f), exemplars()

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
			want, past := pastValidation[op]
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, f, op, "", bl, seq())
			code, body := call(t, ts, op, bl)
			switch {
			case past && errCode(body) != want:
				t.Fatalf("the baseline was expected to clear validation and fail with %s, got %d %s\n"+
					"every %s case would be meaningless", want, code, body, op)
			case !past && code != http.StatusOK:
				t.Fatalf("the baseline request was refused (%d): %s\nevery %s case would be meaningless",
					code, body, op)
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
				prepare(t, ts, f, op, "", probe, seq())
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
					prepare(t, ts, f, op, c.Path, body, seq())

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
					if past && errCode(resp) != "ValidationException" {
						gaps++
						if !knownGaps[key] {
							t.Errorf("%s = %v was refused as %s, not ValidationException\n"+
								"  %s has no accepted baseline, so only the code proves the mutation "+
								"was what got caught.", c.Path, c.Value, errCode(resp), op)
						}
						return
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
