package stepfunctions

// Rejection parity for Step Functions, driven by the cases dzaudit derives
// from AWS's own service model (`dzaudit cases sfn`, filtered to the
// operations this build dispatches — a mutation replayed against an operation
// that refuses everything proves nothing).
//
// Same method as Kinesis's: every case is a mutation of a baseline this test
// first proves the service accepts, because a request refused for the WRONG
// reason looks exactly like a pass.

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

// fixture is the state the baselines address: a machine whose executions
// never finish on their own (a very long Wait), one execution that stays
// RUNNING, one that exists to be stopped, and an activity.
type fixture struct {
	machineARN  string
	execARN     string
	stopExecARN string
	activityARN string
}

const auditDef = `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":30000,"Next":"S"},"S":{"Type":"Succeed"}}}`

func sfnServer(t *testing.T) *httptest.Server {
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

// call posts one awsJson1.0 request: the target header plus a JSON body.
func call(t *testing.T, ts *httptest.Server, target string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader(raw))
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/states/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func setUpFixture(t *testing.T, ts *httptest.Server) fixture {
	t.Helper()
	f := fixture{}
	code, body := call(t, ts, "AWSStepFunctions.CreateStateMachine", map[string]any{
		"name": "audit", "definition": auditDef,
		"roleArn": "arn:aws:iam::000000000000:role/StepFunctions",
	})
	if code != http.StatusOK {
		t.Fatalf("fixture CreateStateMachine = %d: %s", code, body)
	}
	var m struct {
		ARN string `json:"stateMachineArn"`
	}
	json.Unmarshal([]byte(body), &m)
	f.machineARN = m.ARN

	for _, e := range []struct{ name string }{{"parked"}, {"stoppable"}} {
		code, body = call(t, ts, "AWSStepFunctions.StartExecution", map[string]any{
			"stateMachineArn": f.machineARN, "name": e.name, "input": "{}",
		})
		if code != http.StatusOK {
			t.Fatalf("fixture StartExecution %s = %d: %s", e.name, code, body)
		}
	}
	f.execARN = execARN("audit", "parked")
	f.stopExecARN = execARN("audit", "stoppable")

	code, body = call(t, ts, "AWSStepFunctions.CreateActivity", map[string]any{"name": "auditact"})
	if code != http.StatusOK {
		t.Fatalf("fixture CreateActivity = %d: %s", code, body)
	}
	f.activityARN = activityARN("auditact")
	return f
}

// baselines are requests the service must accept, one per operation. The
// creates and StartExecution are idempotent against identical repeats, which
// is what lets one baseline serve a whole group of cases.
func baselines(f fixture) map[string]map[string]any {
	mach := map[string]any{"stateMachineArn": f.machineARN}
	exec := map[string]any{"executionArn": f.execARN}
	tags := []any{map[string]any{"key": "env", "value": "dev"}}
	return map[string]map[string]any{
		"CreateStateMachine": {"name": "audit", "definition": auditDef,
			"roleArn": "arn:aws:iam::000000000000:role/StepFunctions"},
		"DescribeStateMachine": mach,
		"UpdateStateMachine":   {"stateMachineArn": f.machineARN, "definition": auditDef},
		// Deletion is idempotent, so a machine that never existed is a valid,
		// harmless target — deleting the fixture machine would break every
		// baseline after this one alphabetically.
		"DeleteStateMachine":             {"stateMachineArn": machineARN("never-created")},
		"ListStateMachines":              {},
		"ValidateStateMachineDefinition": {"definition": auditDef},

		"CreateActivity":   {"name": "auditact"},
		"DescribeActivity": {"activityArn": f.activityARN},
		"DeleteActivity":   {"activityArn": activityARN("never-created")},
		"ListActivities":   {},

		"TagResource":         {"resourceArn": f.machineARN, "tags": tags},
		"UntagResource":       {"resourceArn": f.machineARN, "tagKeys": []any{"env"}},
		"ListTagsForResource": {"resourceArn": f.machineARN},

		"StartExecution":                   {"stateMachineArn": f.machineARN, "name": "parked", "input": "{}"},
		"DescribeExecution":                exec,
		"DescribeStateMachineForExecution": exec,
		"GetExecutionHistory":              exec,
		"ListExecutions":                   mach,
		"StopExecution":                    {"executionArn": f.stopExecARN},
	}
}

// exemplars stand in for containers a baseline does not already carry.
func exemplars(f fixture) map[string]any {
	return map[string]any{
		"encryptionConfiguration": map[string]any{"type": "AWS_OWNED_KEY"},
		"loggingConfiguration":    map[string]any{"level": "OFF"},
		"tracingConfiguration":    map[string]any{"enabled": false},
		"tags[]":                  []any{map[string]any{"key": "probe", "value": "v"}},
	}
}

// prepare adjusts a request just before it is sent. Step Functions' creates
// and StartExecution are idempotent against identical bodies, so nothing here
// needs sequencing — the hook exists to match the harness shape and for the
// day an operation stops being idempotent.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	_ = fmt.Sprint(n)
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the
// last run. Listed rather than tolerated silently.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

// needState are operations whose baseline cannot be constructed without live
// state the harness would consume.
var needState = map[string]string{
	"SendTaskSuccess":   "needs a live task token, and redeeming it consumes it",
	"SendTaskFailure":   "needs a live task token, and redeeming it consumes it",
	"SendTaskHeartbeat": "needs a live task token from a parked task",
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_sfn.json"))
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

func TestStepFunctionsRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	ts := sfnServer(t)
	f := setUpFixture(t, ts)
	base := baselines(f)
	ex := exemplars(f)

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

	var total, gaps, skipped, unbuildable int
	for _, op := range ops {
		if why, ok := needState[op]; ok {
			skipped += len(byOp[op])
			t.Logf("skipping %s (%d cases): %s", op, len(byOp[op]), why)
			continue
		}
		b, ok := base[op]
		if !ok {
			t.Errorf("%s has %d model-derived cases and no baseline — add one, or "+
				"record it in needState with a reason", op, len(byOp[op]))
			continue
		}

		t.Run(op, func(t *testing.T) {
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, op, "", bl, seq())
			if code, body := call(t, ts, byOp[op][0].Target, bl); code != http.StatusOK {
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
				prepare(t, ts, op, "", probe, seq())
				if code, resp := call(t, ts, byOp[op][0].Target, probe); code != http.StatusOK {
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
					code, resp := call(t, ts, c.Target, body)
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
						t.Errorf("%s is enforced now — delete it from knownGaps, or the list "+
							"stops describing what is actually broken", key)
					}
				})
			}
		})
	}

	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d operations "+
		"(%d skipped for state, %d unbuildable)",
		total-gaps-unbuildable, total, len(ops)-len(needState), skipped, unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — the harness is missing an exemplar "+
			"or a baseline, and those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
