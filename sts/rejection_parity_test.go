package sts

// Rejection parity for STS, driven by the cases dzaudit derives from AWS's own
// service model (`dzaudit cases sts`).
//
// Same rule as the awsJson services: a request refused for the WRONG reason
// looks exactly like a pass, so every case is a mutation of a baseline this
// test first proves the service accepts.
//
// What differs is the wire. STS speaks the Query protocol, so there is no
// target header and no JSON body — the request is a form, and nesting is
// spelled into the key: Tags.member.1.Key rather than {"Tags":[{"Key":...}]}.
// The cases describe the model's paths, so the harness builds the request in
// the shape the paths describe and flattens it on the way out. That keeps
// auditkit — which knows how to place a violating value inside a nested
// structure — usable unchanged, and it is the same translation
// modelcheck.FromQuery performs in the other direction on the service side.

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

	"github.com/doze-dev/doze-aws/internal/auditkit"
)

type auditCase struct {
	Operation  string `json:"operation"`
	Path       string `json:"path"`
	Why        string `json:"why"`
	Value      any    `json:"value"`
	Constraint string `json:"constraint"`
}

func stsServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := New(Options{Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
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
	form := url.Values{"Action": {action}, "Version": {"2011-06-15"}}
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
	roleARN  = "arn:aws:iam::000000000000:role/audit-role"
	samlARN  = "arn:aws:iam::000000000000:saml-provider/audit"
	oidcJWT  = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhdWRpdCJ9.c2ln"
	samlAsrt = "PHNhbWxwOlJlc3BvbnNlIHhtbG5zOnNhbWxwPSJ1cm4ifj48L3NhbWxwOlJlc3BvbnNlPg=="
)

// baselines are requests the service must accept, one per operation.
func baselines() map[string]map[string]any {
	return map[string]map[string]any{
		"AssumeRole": {"RoleArn": roleARN, "RoleSessionName": "audit"},
		"AssumeRoleWithSAML": {
			"RoleArn": roleARN, "PrincipalArn": samlARN, "SAMLAssertion": samlAsrt,
		},
		"AssumeRoleWithWebIdentity": {
			"RoleArn": roleARN, "RoleSessionName": "audit", "WebIdentityToken": oidcJWT,
		},
		"AssumeRoot":                 {"TargetPrincipal": "000000000000", "TaskPolicyArn": map[string]any{"arn": "arn:aws:iam::aws:policy/root-task/IAMAuditRootUserCredentials"}},
		"GetSessionToken":            {},
		"GetFederationToken":         {"Name": "audit"},
		"GetCallerIdentity":          {},
		"GetAccessKeyInfo":           {"AccessKeyId": "AKIAIOSFODNN7EXAMPLE"},
		"DecodeAuthorizationMessage": {"EncodedMessage": "encoded-message-payload"},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tags[]":              []any{map[string]any{"Key": "env", "Value": "dev"}},
		"PolicyArns[]":        []any{map[string]any{"arn": "arn:aws:iam::aws:policy/ReadOnlyAccess"}},
		"TransitiveTagKeys[]": []any{"env"},
		"ProvidedContexts[]": []any{map[string]any{
			"ProviderArn":      "arn:aws:iam::aws:contextProvider/IdentityCenter",
			"ContextAssertion": "assertion",
		}},
		"TaskPolicyArn": map[string]any{"arn": "arn:aws:iam::aws:policy/root-task/IAMAuditRootUserCredentials"},
	}
}

// cannotAudit are operations that refuse every request, valid ones included, so
// replaying a mutation against them proves nothing about validation. Recorded
// with the reason rather than dropped, because a case nobody ran is not a case
// that passed.
var cannotAudit = map[string]string{
	"DecodeAuthorizationMessage": "an honest stub — doze-aws never produces encoded " +
		"authorization messages, so there is nothing to decode and the baseline is refused too",
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_sts.json"))
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

func TestSTSRejectsWhatTheModelForbids(t *testing.T) {
	ts := stsServer(t)
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
