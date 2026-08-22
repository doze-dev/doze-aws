package cloudformation_test

// Rejection parity for CloudFormation, driven by the cases dzaudit derives from
// AWS's own service model (`dzaudit cases cloudformation`).
//
// Same rule as everywhere: a request refused for the WRONG reason looks exactly
// like a pass, so every case is a mutation of a baseline this test first proves
// the service accepts.
//
// Two things differ from the awsJson services. The wire is the Query protocol,
// so the request is a form and nesting is spelled into the key — the harness
// builds the shape the model's paths describe and flattens it on the way out,
// which is the same translation modelcheck.FromQuery performs on the service
// side. And CloudFormation provisions for real: a stack here creates an actual
// SQS queue in the sibling service, so the audit stands up a whole doze-aws
// stack rather than the one handler.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/auditkit"
)

type auditCase struct {
	Operation  string `json:"operation"`
	Path       string `json:"path"`
	Why        string `json:"why"`
	Value      any    `json:"value"`
	Constraint string `json:"constraint"`
}

// auditTemplate is deliberately the smallest thing CloudFormation will accept
// and provision — every case creates or updates a stack, and a heavier template
// would multiply that by 182.
// changedTemplate adds a resource the deployed stack does not have, with a new
// name on every call. A change set with nothing to do lands in FAILED and
// cannot be executed — which would refuse ExecuteChangeSet's baseline for a
// reason that has nothing to do with its input — and executing one moves the
// stack, so a fixed "changed" template stops being a change after the first
// case.
//
// It has to *add* a resource rather than edit one: doze-aws diffs change sets
// by resource identity (added, removed, renamed physical id), so a property-only
// edit registers as no change at all.
func changedTemplate(n int) string {
	return fmt.Sprintf(`
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: audit-queue
  Extra%d:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: audit-extra-%d
Outputs:
  QueueUrl:
    Value: !Ref Q
    Export:
      Name: !Sub "${AWS::StackName}-queue"
`, n, n)
}

const auditTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: audit-queue
Outputs:
  QueueUrl:
    Value: !Ref Q
    Export:
      Name: !Sub "${AWS::StackName}-queue"
`

func cfnServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(st.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// flatten writes the nested request back into Query-protocol form keys. It is
// the inverse of modelcheck.FromQuery, and the two are tested against each
// other by every case that reaches a nested path.
func flatten(prefix string, v any, out url.Values) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, t[k], out)
		}
	case []any:
		for i, el := range t {
			flatten(prefix+".member."+strconv.Itoa(i+1), el, out)
		}
	case nil:
		// absent
	default:
		out.Set(prefix, fmt.Sprint(t))
	}
}

func call(t *testing.T, ts *httptest.Server, action string, body map[string]any) (int, string) {
	t.Helper()
	form := url.Values{"Action": {action}, "Version": {"2010-05-15"}}
	flatten("", body, form)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/cloudformation/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

const (
	stack     = "audit-stack"
	changeSet = "audit-change-set"
	policy    = `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}`
)

func setUpFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	if code, body := call(t, ts, "CreateStack", map[string]any{
		"StackName": stack, "TemplateBody": auditTemplate,
	}); code != http.StatusOK {
		t.Fatalf("fixture CreateStack = %d: %s", code, body)
	}
	if code, body := call(t, ts, "CreateChangeSet", map[string]any{
		"StackName": stack, "ChangeSetName": changeSet, "TemplateBody": auditTemplate,
	}); code != http.StatusOK {
		t.Fatalf("fixture CreateChangeSet = %d: %s", code, body)
	}
}

// baselines are requests the service must accept, one per operation.
func baselines() map[string]map[string]any {
	named := map[string]any{"StackName": stack}
	return map[string]map[string]any{
		"CreateStack":                 {"StackName": "made-by-baseline", "TemplateBody": auditTemplate},
		"UpdateStack":                 {"StackName": stack, "TemplateBody": auditTemplate},
		"DeleteStack":                 {"StackName": "made-by-baseline"},
		"DescribeStacks":              {},
		"DescribeStackEvents":         named,
		"DescribeStackResource":       {"StackName": stack, "LogicalResourceId": "Q"},
		"ListStackResources":          named,
		"ListStacks":                  {},
		"ListExports":                 {},
		"ListImports":                 {"ExportName": stack + "-queue"},
		"GetTemplate":                 named,
		"GetTemplateSummary":          {"TemplateBody": auditTemplate},
		"ValidateTemplate":            {"TemplateBody": auditTemplate},
		"GetStackPolicy":              named,
		"SetStackPolicy":              {"StackName": stack, "StackPolicyBody": policy},
		"CreateChangeSet":             {"StackName": stack, "ChangeSetName": "made-by-baseline-cs", "TemplateBody": auditTemplate},
		"DescribeChangeSet":           {"StackName": stack, "ChangeSetName": changeSet},
		"ListChangeSets":              named,
		"ExecuteChangeSet":            {"StackName": stack, "ChangeSetName": "made-by-baseline-cs"},
		"DeleteChangeSet":             {"StackName": stack, "ChangeSetName": "made-by-baseline-cs"},
		"CancelUpdateStack":           named,
		"UpdateTerminationProtection": {"StackName": stack, "EnableTerminationProtection": false},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tags[]":                []any{map[string]any{"Key": "env", "Value": "dev"}},
		"Capabilities[]":        []any{"CAPABILITY_IAM"},
		"ResourceTypes[]":       []any{"AWS::SQS::Queue"},
		"StackStatusFilter[]":   []any{"CREATE_COMPLETE"},
		"RollbackConfiguration": map[string]any{"MonitoringTimeInMinutes": 0},
		"RollbackConfiguration.RollbackTriggers[]": []any{map[string]any{
			"Arn":  "arn:aws:cloudwatch:us-east-1:000000000000:alarm:audit",
			"Type": "AWS::CloudWatch::Alarm",
		}},
		"DeploymentConfig": map[string]any{},
		"ResourcesToImport[]": []any{map[string]any{
			"ResourceType": "AWS::SQS::Queue", "LogicalResourceId": "Imported",
			"ResourceIdentifier": map[string]any{"QueueName": "audit-imported"},
		}},
		"ResourcesToImport[].ResourceIdentifier{}": map[string]any{"QueueName": "audit-imported"},
	}
}

// prepare gives the operations that consume what they name their own
// preconditions, so no group depends on another having run — operations execute
// in alphabetical order, which is not the order that would make them work.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	switch op {
	case "CreateStack":
		if mutating != "StackName" {
			body["StackName"] = fmt.Sprintf("created-%d", n)
		}
	case "DeleteStack":
		if mutating != "StackName" {
			name := fmt.Sprintf("doomed-stack-%d", n)
			call(t, ts, "CreateStack", map[string]any{"StackName": name, "TemplateBody": auditTemplate})
			body["StackName"] = name
		}
	case "CreateChangeSet":
		if mutating != "ChangeSetName" {
			body["ChangeSetName"] = fmt.Sprintf("created-cs-%d", n)
		}
	case "DeleteChangeSet", "ExecuteChangeSet":
		if mutating != "ChangeSetName" {
			name := fmt.Sprintf("doomed-cs-%d", n)
			call(t, ts, "CreateChangeSet", map[string]any{
				"StackName": stack, "ChangeSetName": name, "TemplateBody": changedTemplate(n),
			})
			body["ChangeSetName"] = name
		}
	}
}

// pastValidation are operations whose *valid* request is still refused, for a
// reason that is not validation — so there is no accepted baseline to mutate.
// Rather than skip them, the baseline must be refused with exactly this code,
// which proves it cleared validation, and every case under the operation must
// then be refused with ValidationError specifically.
var pastValidation = map[string]string{
	// doze-aws applies a stack synchronously inside CreateStack/UpdateStack, so
	// no stack is ever mid-update and CancelUpdateStack refuses every request.
	// Real CloudFormation runs the update over minutes and can be interrupted;
	// the difference is in the timing model, not in what the operation
	// validates.
	"CancelUpdateStack": "Stack [" + stack + "] has no update in progress",
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

// isValidationRefusal reports whether the body is modelcheck's refusal rather
// than some other 4xx. The Query protocol spells every refusal ValidationError,
// state errors included, so on the operations with no acceptable baseline the
// code alone proves nothing and the message has to carry it.
func isValidationRefusal(body string) bool {
	return strings.Contains(body, "validation error detected:")
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_cloudformation.json"))
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

func TestCloudFormationRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack and provisions for real")
	}
	ts := cfnServer(t)
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
			wantMsg, past := pastValidation[op]
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, op, "", bl, seq())
			code, body := call(t, ts, op, bl)
			switch {
			case past && !strings.Contains(body, wantMsg):
				t.Fatalf("the baseline was expected to clear validation and fail with %q, got %d %s\n"+
					"every %s case would be meaningless", wantMsg, code, body, op)
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
				prepare(t, ts, op, "", probe, seq())
				if code, resp := call(t, ts, op, probe); code != http.StatusOK && !past {
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
					if past && !isValidationRefusal(resp) {
						gaps++
						if !knownGaps[key] {
							t.Errorf("%s = %v was refused, but not as a validation error: %s\n"+
								"  %s has no accepted baseline, so only the message proves the "+
								"mutation was what got caught.", c.Path, c.Value, resp, op)
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
		"(%d unbuildable)", total-gaps-unbuildable, total, len(ops), unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
