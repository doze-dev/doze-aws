package sns

// Rejection parity for SNS, driven by the cases dzaudit derives from AWS's own
// service model (`dzaudit cases sns`).
//
// Same rule as the awsJson services: a request refused for the WRONG reason
// looks exactly like a pass, so every case is a mutation of a baseline this
// test first proves the service accepts.
//
// Like STS this is the Query protocol: a form, with nesting spelled into the
// key. SNS adds the other Query container — a MAP, which travels as a numbered
// list of Name/Value pairs (MessageAttributes.entry.1.Name and
// .entry.1.Value.DataType). The harness flattens those from the map the model
// path describes, which is the exact inverse of what modelcheck.FromQuery does
// on the service side, so the two check each other on every case that reaches
// MessageAttributes.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/auditkit"
)

type auditCase struct {
	Operation  string `json:"operation"`
	Path       string `json:"path"`
	Why        string `json:"why"`
	Value      any    `json:"value"`
	Constraint string `json:"constraint"`
}

func snsServer(t *testing.T) *httptest.Server {
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

func setUpFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	if code, body := call(t, ts, "CreateTopic", map[string]any{"Name": fixtureTopic}); code != http.StatusOK {
		t.Fatalf("fixture CreateTopic = %d: %s", code, body)
	}
	code, body := call(t, ts, "Subscribe", map[string]any{
		"TopicArn": auditTopic, "Protocol": "sqs",
		"Endpoint":              "arn:aws:sqs:us-east-1:000000000000:audit-q",
		"ReturnSubscriptionArn": "true",
	})
	if code != http.StatusOK {
		t.Fatalf("fixture Subscribe = %d: %s", code, body)
	}
	if m := regexp.MustCompile(`<SubscriptionArn>([^<]+)`).FindStringSubmatch(body); m != nil {
		subARN = m[1]
	}
	if subARN == "" || strings.Contains(subARN, "pending") {
		t.Fatalf("fixture Subscribe gave no usable ARN: %s", body)
	}

	// Delivery is synchronous here, so the confirmation POST has landed by the
	// time Subscribe returns.
	var captured string
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = string(b)
	}))
	defer sink.Close()
	if code, body := call(t, ts, "Subscribe", map[string]any{
		"TopicArn": auditTopic, "Protocol": "http", "Endpoint": sink.URL,
	}); code != http.StatusOK {
		t.Fatalf("fixture http Subscribe = %d: %s", code, body)
	}
	if m := regexp.MustCompile(`"Token"\s*:\s*"([^"]+)"`).FindStringSubmatch(captured); m != nil {
		confirmToken = m[1]
	}
	if confirmToken == "" {
		t.Fatalf("the confirmation handshake produced no token: %s", captured)
	}
}

// flatten writes the nested request back into Query-protocol form keys. It is
// the inverse of modelcheck.FromQuery, and the two are tested against each
// other by every case that reaches a nested path.
// mapMembers are the members the model calls maps rather than structures. The
// two are indistinguishable once built — both are map[string]any — but they are
// spelled differently on the wire, so the harness has to be told which is which.
var mapMembers = map[string]bool{"MessageAttributes": true, "Attributes": true}

func flatten(prefix string, v any, out url.Values) {
	switch t := v.(type) {
	case map[string]any:
		// A map member becomes entry.N.Name / entry.N.Value, not parent.child.
		if base := prefix[strings.LastIndex(prefix, ".")+1:]; mapMembers[base] {
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				e := prefix + ".entry." + strconv.Itoa(i+1)
				out.Set(e+".Name", k)
				flatten(e+".Value", t[k], out)
			}
			return
		}
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
	form := url.Values{"Action": {action}, "Version": {"2010-03-31"}}
	flatten("", body, form)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/sts/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

const (
	auditTopic   = "arn:aws:sns:us-east-1:000000000000:audit"
	fixtureTopic = "audit"
)

// subARN is the fixture subscription, filled in at setup: operations that
// address a subscription need one that exists, or they are refused as
// not-found, which is a refusal for the wrong reason.
var subARN string

// confirmToken is a real confirmation token, captured from the handshake. An
// SQS subscription is auto-confirmed and never issues one, so the fixture also
// makes an http subscription against a server that records what SNS posts to
// it. Inventing a token would leave ConfirmSubscription's baseline refused as
// not-found, and every case under it meaningless.
var confirmToken string

// baselines are requests the service must accept, one per operation.
func baselines() map[string]map[string]any {
	topic := map[string]any{"TopicArn": auditTopic}
	sub := map[string]any{"SubscriptionArn": subARN}
	policy := `{"Name":"audit","Description":"d","Version":"2021-06-01","Statement":[]}`
	return map[string]map[string]any{
		"CreateTopic":               {"Name": "made-by-baseline"},
		"DeleteTopic":               {"TopicArn": "arn:aws:sns:us-east-1:000000000000:made-by-baseline"},
		"GetTopicAttributes":        topic,
		"SetTopicAttributes":        {"TopicArn": auditTopic, "AttributeName": "DisplayName", "AttributeValue": "audit"},
		"ListSubscriptionsByTopic":  topic,
		"Subscribe":                 {"TopicArn": auditTopic, "Protocol": "sqs", "Endpoint": "arn:aws:sqs:us-east-1:000000000000:audit-q"},
		"ConfirmSubscription":       {"TopicArn": auditTopic, "Token": confirmToken},
		"Unsubscribe":               sub,
		"GetSubscriptionAttributes": sub,
		"SetSubscriptionAttributes": {"SubscriptionArn": subARN, "AttributeName": "RawMessageDelivery", "AttributeValue": "true"},
		"Publish":                   {"TopicArn": auditTopic, "Message": "hello"},
		"PublishBatch": {"TopicArn": auditTopic, "PublishBatchRequestEntries": []any{
			map[string]any{"Id": "1", "Message": "hello"},
		}},
		"AddPermission":           {"TopicArn": auditTopic, "Label": "audit", "AWSAccountId": []any{"000000000000"}, "ActionName": []any{"Publish"}},
		"RemovePermission":        {"TopicArn": auditTopic, "Label": "audit"},
		"TagResource":             {"ResourceArn": auditTopic, "Tags": []any{map[string]any{"Key": "env", "Value": "dev"}}},
		"UntagResource":           {"ResourceArn": auditTopic, "TagKeys": []any{"env"}},
		"ListTagsForResource":     {"ResourceArn": auditTopic},
		"PutDataProtectionPolicy": {"ResourceArn": auditTopic, "DataProtectionPolicy": policy},
		"GetDataProtectionPolicy": {"ResourceArn": auditTopic},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tags[]":    []any{map[string]any{"Key": "env", "Value": "dev"}},
		"TagKeys[]": []any{"env"},
		"MessageAttributes{}": map[string]any{
			"kind": map[string]any{"DataType": "String", "StringValue": "order"},
		},
		"PublishBatchRequestEntries[]": []any{map[string]any{"Id": "1", "Message": "hello"}},
		// The map lives inside the batch entry, so it needs its own exemplar at
		// the full container path.
		"PublishBatchRequestEntries[].MessageAttributes{}": map[string]any{
			"kind": map[string]any{"DataType": "String", "StringValue": "order"},
		},
	}
}

// cannotAudit are operations that refuse every request, valid ones included, so
// replaying a mutation against them proves nothing about validation. Recorded
// with the reason rather than dropped, because a case nobody ran is not a case
// that passed.
//
// Empty: every SNS operation with cases has a baseline the service accepts.
// (An STS entry once sat here by copy-paste and never matched anything, which
// is why the ledger said "18 of 19".)
var cannotAudit = map[string]string{}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_sns.json"))
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

func TestSNSRejectsWhatTheModelForbids(t *testing.T) {
	ts := snsServer(t)
	setUpFixture(t, ts)
	base, ex := baselines(), exemplars()

	byOp := map[string][]auditCase{}
	for _, c := range loadCases(t) {
		byOp[c.Operation] = append(byOp[c.Operation], c)
	}
	ops := make([]string, 0, len(byOp))
	for op := range byOp {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	var total, gaps, unbuildable, unauditable int
	for _, op := range ops {
		if why, ok := cannotAudit[op]; ok {
			unauditable += len(byOp[op])
			t.Logf("cannot audit %s (%d cases): %s", op, len(byOp[op]), why)
			continue
		}
		b, ok := base[op]
		if !ok {
			t.Errorf("%s has %d model-derived cases and no baseline", op, len(byOp[op]))
			continue
		}
		t.Run(op, func(t *testing.T) {
			if code, body := call(t, ts, op, auditkit.DeepCopy(b).(map[string]any)); code != http.StatusOK {
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
		"(%d un-auditable, %d unbuildable)",
		total-gaps-unbuildable, total, len(ops)-len(cannotAudit), unauditable, unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
