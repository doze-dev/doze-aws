package dynamodb_test

// Rejection parity for DynamoDB, driven by the cases dzaudit derives from
// AWS's own service model (`dzaudit cases --op CreateTable dynamodb`).
//
// The cases are COMMITTED rather than generated at test time. Generating would
// mean fetching a model over the network in CI, and would let the expectations
// change without anyone reviewing the diff — the point of an audit is that
// what it asserts is visible.
//
// The rule every case obeys, and the reason the baseline is asserted first: a
// request refused for the WRONG reason looks exactly like a pass. A mutation of
// a request that was already invalid tells you nothing about the mutation. So
// each case starts from a baseline this test proves the service accepts, and a
// case whose baseline fails is reported unusable rather than counted.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/doze-dev/doze-aws/dynamodb"
)

type auditCase struct {
	Operation       string   `json:"operation"`
	Target          string   `json:"target"`
	Path            string   `json:"path"`
	Why             string   `json:"why"`
	Value           any      `json:"value"`
	Constraint      string   `json:"constraint"`
	RequiredMembers []string `json:"required_members"`
}

// baselineCreateTable is a request the service must accept. Everything the
// cases mutate is mutated from this.
func baselineCreateTable(name string) map[string]any {
	return map[string]any{
		"TableName":            name,
		"AttributeDefinitions": []any{map[string]any{"AttributeName": "pk", "AttributeType": "S"}},
		"KeySchema":            []any{map[string]any{"AttributeName": "pk", "KeyType": "HASH"}},
		"BillingMode":          "PAY_PER_REQUEST",
	}
}

func newDDB(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := dynamodb.New(dynamodb.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

// send posts an awsJson request: the target header plus a JSON body, which is
// the whole of the protocol.
func send(t *testing.T, ts *httptest.Server, target string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader(raw))
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/dynamodb/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := make([]byte, 400)
	n, _ := resp.Body.Read(out)
	return resp.StatusCode, string(out[:n])
}

func loadCases(t *testing.T, file string) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", file))
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

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. They are listed rather than silently tolerated so the set is reviewable,
// and so anything NOT on the list fails the build the moment it appears.
//
// Removing an entry is the point: fix the validation, delete the line. A gap
// that starts being enforced also fails, with a note to delete it, so the list
// cannot rot into a lie about what is broken.
var knownGaps = map[string]bool{
	// Each of these accepts a value AWS refuses. Found by the first run of
	// this suite; none is a regression, all are unwritten validation.
	"BillingMode/not one of PAY_PER_REQUEST, PROVISIONED":                                     true,
	"GlobalTableSettingsReplicationMode/not one of ENABLED, DISABLED, ENABLED_WITH_OVERRIDES": true,
	"GlobalTableSourceArn/longer than the maximum length of 1024":                             true,
	"GlobalTableSourceArn/shorter than the minimum length of 1":                               true,
	"TableClass/not one of STANDARD_INFREQUENT_ACCESS, STANDARD":                              true,
	"TableName/longer than the maximum length of 1024":                                        true,
}

func TestCreateTableRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	ts := newDDB(t)
	cases := loadCases(t, "cases_createtable.json")

	// The baseline has to be accepted, or every case below is a mutation of
	// something already invalid and proves nothing.
	if code, body := send(t, ts, "DynamoDB_20120810.CreateTable", baselineCreateTable("baseline-ok")); code != http.StatusOK {
		t.Fatalf("the baseline request was refused (%d): %s\nevery case would be meaningless", code, body)
	}

	var gaps []string
	for i, c := range cases {
		t.Run(c.Path+"/"+c.Why, func(t *testing.T) {
			body := baselineCreateTable(fmt.Sprintf("case-%d", i))
			if c.Value == nil {
				delete(body, c.Path) // a @required case is the member's absence
			} else {
				body[c.Path] = c.Value
			}

			key := c.Path + "/" + c.Why
			code, resp := send(t, ts, c.Target, body)

			if code == http.StatusOK {
				// Accepted. AWS refuses this, so doze-aws is more permissive
				// than production — the direction that hides bugs until deploy.
				gaps = append(gaps, fmt.Sprintf("%s (%s)", key, c.Constraint))
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

	t.Logf("%d/%d model-derived CreateTable constraints enforced", len(cases)-len(gaps), len(cases))
	for _, g := range gaps {
		t.Logf("  gap: %s", g)
	}
	if len(gaps) > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", len(gaps), len(knownGaps))
	}
}
