package secretsmanager

// Rejection parity for Secrets Manager, driven by the cases dzaudit derives
// from AWS's own service model (`dzaudit cases secrets-manager`).
//
// Same method as DynamoDB's and Kinesis's, and the same rule underneath: a
// request refused for the WRONG reason looks exactly like a pass, so every case
// is a mutation of a baseline this test first proves the service accepts.
//
// The state here is simpler than Kinesis's — one secret satisfies almost every
// operation — but two of them consume it. DeleteSecret and RestoreSecret make
// their own, because a group that deletes the fixture leaves every later
// baseline refused with ResourceNotFoundException, which proves nothing about
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

func smServer(t *testing.T) *httptest.Server {
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
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/secretsmanager/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

const fixtureSecret = "audit/secret"

// fixtureVersion is the real version id CreateSecret allocated. Baselines that
// address a version need it: an invented UUID is refused as not-found, which is
// a refusal for the wrong reason.
var fixtureVersion string

func setUpFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	code, body := call(t, ts, "secretsmanager.CreateSecret", map[string]any{
		"Name": fixtureSecret, "SecretString": `{"user":"a","password":"b"}`,
	})
	if code != http.StatusOK {
		t.Fatalf("fixture CreateSecret = %d: %s", code, body)
	}
	var out struct {
		VersionID string `json:"VersionId"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.VersionID == "" {
		t.Fatalf("fixture CreateSecret gave no VersionId: %s", body)
	}
	fixtureVersion = out.VersionID
}

// baselines are requests the service must accept, one per operation.
func baselines() map[string]map[string]any {
	id := map[string]any{"SecretId": fixtureSecret}
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"secretsmanager:GetSecretValue","Resource":"*"}]}`
	return map[string]map[string]any{
		"CreateSecret":         {"Name": "made-by-baseline", "SecretString": "s"},
		"DeleteSecret":         {"SecretId": "made-by-baseline"},
		"RestoreSecret":        {"SecretId": "made-by-baseline"},
		"DescribeSecret":       id,
		"GetSecretValue":       id,
		"ListSecrets":          {},
		"ListSecretVersionIds": id,
		"UpdateSecret":         {"SecretId": fixtureSecret, "SecretString": "s2"},
		"PutSecretValue":       {"SecretId": fixtureSecret, "SecretString": "s3"},
		// One of MoveToVersionId/RemoveFromVersionId is required by the service,
		// and the version has to exist — a custom stage on the real version is
		// the least disruptive request that satisfies both.
		"UpdateSecretVersionStage": {"SecretId": fixtureSecret, "VersionStage": "AUDIT",
			"MoveToVersionId": fixtureVersion},
		"CancelRotateSecret": id,
		// Rotation needs a Lambda to rotate with; the service refuses without one,
		// which is a semantic refusal rather than a model-constraint one.
		"RotateSecret": {"SecretId": fixtureSecret, "RotateImmediately": false,
			"RotationLambdaARN": "arn:aws:lambda:us-east-1:000000000000:function:rotator"},
		"TagResource":            {"SecretId": fixtureSecret, "Tags": []any{map[string]any{"Key": "env", "Value": "dev"}}},
		"UntagResource":          {"SecretId": fixtureSecret, "TagKeys": []any{"env"}},
		"PutResourcePolicy":      {"SecretId": fixtureSecret, "ResourcePolicy": policy},
		"GetResourcePolicy":      id,
		"DeleteResourcePolicy":   id,
		"ValidateResourcePolicy": {"ResourcePolicy": policy},
		"GetRandomPassword":      {},
		"BatchGetSecretValue":    {"SecretIdList": []any{fixtureSecret}},
	}
}

// exemplars stand in for containers a baseline does not already carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tags[]":                           []any{map[string]any{"Key": "k", "Value": "v"}},
		"Filters[]":                        []any{map[string]any{"Key": "name", "Values": []any{"audit"}}},
		"AddReplicaRegions[]":              []any{map[string]any{"Region": "us-west-2"}},
		"RotationRules":                    map[string]any{"AutomaticallyAfterDays": 30},
		"ExternalSecretRotationMetadata[]": []any{map[string]any{"Key": "k", "Value": "v"}},
	}
}

// prepare gives the non-idempotent operations their own preconditions, so no
// group depends on another having run — operations execute in alphabetical
// order, which is not the order that would make them work.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	if mutating == "Name" || mutating == "SecretId" {
		return // never overwrite the member the case is about
	}
	switch op {
	case "CreateSecret":
		body["Name"] = fmt.Sprintf("created-%d", n)
	case "DeleteSecret":
		name := fmt.Sprintf("doomed-%d", n)
		call(t, ts, "secretsmanager.CreateSecret", map[string]any{"Name": name, "SecretString": "s"})
		body["SecretId"] = name
	case "RestoreSecret":
		// Restore needs something deleted-but-recoverable, which means making
		// it and scheduling its deletion first.
		name := fmt.Sprintf("restorable-%d", n)
		call(t, ts, "secretsmanager.CreateSecret", map[string]any{"Name": name, "SecretString": "s"})
		call(t, ts, "secretsmanager.DeleteSecret", map[string]any{"SecretId": name})
		body["SecretId"] = name
	case "GetResourcePolicy", "DeleteResourcePolicy":
		// DeleteResourcePolicy sorts first and removes what Get would read.
		if id, ok := body["SecretId"].(string); ok {
			call(t, ts, "secretsmanager.PutResourcePolicy", map[string]any{
				"SecretId": id, "ResourcePolicy": `{"Version":"2012-10-17","Statement":[]}`,
			})
		}
	}
}

// needPeer are operations whose baseline cannot succeed in an isolated service
// test. They are skipped WITH A REASON rather than dropped, because a case
// nobody ran is not a case that passed.
var needPeer = map[string]string{
	"RotateSecret": "rotation invokes a Lambda, and this test boots secretsmanager alone",
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently, so anything NOT on the list fails
// the moment it appears.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_secretsmanager.json"))
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

func TestSecretsManagerRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	ts := smServer(t)
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
		if why, ok := needPeer[op]; ok {
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
			if code, body := call(t, ts, byOp[op][0].Target, bl); code != http.StatusOK {
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
						t.Errorf("%s is enforced now — delete it from knownGaps", key)
					}
				})
			}
		})
	}

	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d operations "+
		"(%d skipped for a peer, %d unbuildable)",
		total-gaps-unbuildable, total, len(ops)-len(needPeer), skipped, unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
