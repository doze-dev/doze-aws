package ssm

// Rejection parity for SSM Parameter Store, driven by the cases dzaudit derives
// from AWS's own service model (`dzaudit cases ssm`).
//
// Same rule as everywhere: a request refused for the WRONG reason looks exactly
// like a pass, so every case is a mutation of a baseline this test first proves
// the service accepts.
//
// SSM's model is the largest of any service here — 1,638 cases across 152
// operations — but only the Parameter Store slice is dispatched locally. The
// committed case file is scoped to those 13 operations; the rest fall on fleet
// management, which answers UnsupportedOperationException by design and has no
// input to validate.

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
	Operation   string           `json:"operation"`
	Target      string           `json:"target"`
	Path        string           `json:"path"`
	Why         string           `json:"why"`
	Value       any              `json:"value"`
	ValueRepeat *auditkit.Repeat `json:"value_repeat,omitempty"`
	Constraint  string           `json:"constraint"`
}

func ssmServer(t *testing.T) *httptest.Server {
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
	req.Header.Set("X-Amz-Target", "AmazonSSM."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/ssm/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// param is the fixture parameter every read baseline addresses. It carries a
// label and a tag, because UnlabelParameterVersion and RemoveTagsFromResource
// need something to remove.
const param = "/audit/param"

func setUpFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	if code, body := call(t, ts, "PutParameter", map[string]any{
		"Name": param, "Value": "v1", "Type": "String",
	}); code != http.StatusOK {
		t.Fatalf("fixture PutParameter = %d: %s", code, body)
	}
	if code, body := call(t, ts, "LabelParameterVersion", map[string]any{
		"Name": param, "Labels": []any{"audit"},
	}); code != http.StatusOK {
		t.Fatalf("fixture LabelParameterVersion = %d: %s", code, body)
	}
	if code, body := call(t, ts, "AddTagsToResource", map[string]any{
		"ResourceId": param, "ResourceType": "Parameter",
		"Tags": []any{map[string]any{"Key": "env", "Value": "dev"}},
	}); code != http.StatusOK {
		t.Fatalf("fixture AddTagsToResource = %d: %s", code, body)
	}
}

// baselines are requests the service must accept, one per operation.
func baselines() map[string]map[string]any {
	tagged := map[string]any{"ResourceId": param, "ResourceType": "Parameter"}
	return map[string]map[string]any{
		"PutParameter":            {"Name": "/audit/put", "Value": "v", "Type": "String", "Overwrite": true},
		"GetParameter":            {"Name": param},
		"GetParameters":           {"Names": []any{param}},
		"GetParametersByPath":     {"Path": "/audit"},
		"GetParameterHistory":     {"Name": param},
		"DescribeParameters":      {},
		"DeleteParameter":         {"Name": "made-by-baseline"},
		"DeleteParameters":        {"Names": []any{"made-by-baseline"}},
		"LabelParameterVersion":   {"Name": param, "Labels": []any{"baseline-label"}},
		"UnlabelParameterVersion": {"Name": param, "ParameterVersion": 1, "Labels": []any{"baseline-label"}},
		"AddTagsToResource": {"ResourceId": param, "ResourceType": "Parameter",
			"Tags": []any{map[string]any{"Key": "team", "Value": "audit"}}},
		"RemoveTagsFromResource": {"ResourceId": param, "ResourceType": "Parameter",
			"TagKeys": []any{"team"}},
		"ListTagsForResource": tagged,
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tags[]":                      []any{map[string]any{"Key": "env", "Value": "dev"}},
		"TagKeys[]":                   []any{"env"},
		"Names[]":                     []any{param},
		"Labels[]":                    []any{"audit"},
		"Filters[]":                   []any{map[string]any{"Key": "Name", "Values": []any{"/audit"}}},
		"Filters[].Values[]":          []any{"/audit"},
		"ParameterFilters[]":          []any{map[string]any{"Key": "Name", "Option": "BeginsWith", "Values": []any{"/audit"}}},
		"ParameterFilters[].Values[]": []any{"/audit"},
	}
}

// prepare gives the operations that consume what they name their own
// preconditions, so no group depends on another having run — operations execute
// in alphabetical order, which is not the order that would make them work.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	switch op {
	case "PutParameter":
		if mutating != "Name" {
			body["Name"] = fmt.Sprintf("/audit/put-%d", n)
		}
	case "DeleteParameter":
		if mutating != "Name" {
			name := fmt.Sprintf("/audit/doomed-%d", n)
			call(t, ts, "PutParameter", map[string]any{"Name": name, "Value": "v", "Type": "String"})
			body["Name"] = name
		}
	case "DeleteParameters":
		if mutating != "Names" && mutating != "Names[]" {
			name := fmt.Sprintf("/audit/doomed-many-%d", n)
			call(t, ts, "PutParameter", map[string]any{"Name": name, "Value": "v", "Type": "String"})
			body["Names"] = []any{name}
		}
	case "LabelParameterVersion":
		// A label may only sit on one version, so each case gets its own.
		if mutating != "Labels" && mutating != "Labels[]" {
			body["Labels"] = []any{fmt.Sprintf("label-%d", n)}
		}
	case "UnlabelParameterVersion":
		// Unlabel needs the label to be there, and consumes it.
		if mutating != "Labels" && mutating != "Labels[]" {
			label := fmt.Sprintf("doomed-label-%d", n)
			call(t, ts, "LabelParameterVersion", map[string]any{
				"Name": param, "Labels": []any{label},
			})
			body["Labels"] = []any{label}
		}
	case "RemoveTagsFromResource":
		if mutating != "TagKeys" && mutating != "TagKeys[]" {
			key := fmt.Sprintf("doomed-tag-%d", n)
			call(t, ts, "AddTagsToResource", map[string]any{
				"ResourceId": param, "ResourceType": "Parameter",
				"Tags": []any{map[string]any{"Key": key, "Value": "v"}},
			})
			body["TagKeys"] = []any{key}
		}
	}
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_ssm.json"))
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

func TestSSMRejectsWhatTheModelForbids(t *testing.T) {
	ts := ssmServer(t)
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
		"(%d unbuildable)", total-gaps-unbuildable, total, len(ops), unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
