package kms

// Rejection parity for KMS, driven by the cases dzaudit derives from AWS's own
// service model (`dzaudit cases kms`).
//
// Same rule as everywhere: a request refused for the WRONG reason looks exactly
// like a pass, so every case is a mutation of a baseline this test first proves
// the service accepts.
//
// KMS needs the most fixture state of any service audited so far, and it cannot
// be faked. Decrypt needs ciphertext this key actually produced, Verify needs a
// signature over the message it is given, VerifyMac needs a real MAC — an
// invented blob is refused as invalid, which reads exactly like a pass. So the
// fixture makes three keys (symmetric, asymmetric, HMAC) and then performs the
// operations whose OUTPUT later baselines consume.

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

func kmsServer(t *testing.T) *httptest.Server {
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
	req.Header.Set("X-Amz-Target", "TrentService."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/kms/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// fx is the material later baselines consume. Every field is produced by the
// service rather than invented, because KMS refuses anything it did not make.
type fx struct {
	symKey     string
	asymKey    string
	hmacKey    string
	ciphertext string // produced by Encrypt under symKey
	signature  string // produced by Sign over plaintext under asymKey
	mac        string // produced by GenerateMac over plaintext under hmacKey
	alias      string
}

const plaintextB64 = "YXVkaXQtbWVzc2FnZQ==" // "audit-message"

func setUpFixture(t *testing.T, ts *httptest.Server) fx {
	t.Helper()
	var f fx
	mk := func(spec, usage string) string {
		t.Helper()
		in := map[string]any{"Description": "audit"}
		if spec != "" {
			in["KeySpec"] = spec
			in["KeyUsage"] = usage
		}
		code, body := call(t, ts, "CreateKey", in)
		if code != http.StatusOK {
			t.Fatalf("fixture CreateKey(%s) = %d: %s", spec, code, body)
		}
		var out struct {
			KeyMetadata struct {
				KeyID string `json:"KeyId"`
			} `json:"KeyMetadata"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil || out.KeyMetadata.KeyID == "" {
			t.Fatalf("fixture CreateKey(%s) gave no KeyId: %s", spec, body)
		}
		return out.KeyMetadata.KeyID
	}
	f.symKey = mk("", "")
	f.asymKey = mk("RSA_2048", "SIGN_VERIFY")
	f.hmacKey = mk("HMAC_256", "GENERATE_VERIFY_MAC")

	grab := func(action string, in map[string]any, field string) string {
		t.Helper()
		code, body := call(t, ts, action, in)
		if code != http.StatusOK {
			t.Fatalf("fixture %s = %d: %s", action, code, body)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("fixture %s body: %v", action, err)
		}
		v, _ := m[field].(string)
		if v == "" {
			t.Fatalf("fixture %s gave no %s: %s", action, field, body)
		}
		return v
	}
	f.ciphertext = grab("Encrypt",
		map[string]any{"KeyId": f.symKey, "Plaintext": plaintextB64}, "CiphertextBlob")
	f.signature = grab("Sign", map[string]any{
		"KeyId": f.asymKey, "Message": plaintextB64, "SigningAlgorithm": "RSASSA_PSS_SHA_256",
	}, "Signature")
	f.mac = grab("GenerateMac", map[string]any{
		"KeyId": f.hmacKey, "Message": plaintextB64, "MacAlgorithm": "HMAC_SHA_256",
	}, "Mac")

	f.alias = "alias/audit"
	if code, body := call(t, ts, "CreateAlias",
		map[string]any{"AliasName": f.alias, "TargetKeyId": f.symKey}); code != http.StatusOK {
		t.Fatalf("fixture CreateAlias = %d: %s", code, body)
	}
	return f
}

// baselines are requests the service must accept, one per operation.
func baselines(f fx) map[string]map[string]any {
	sym := map[string]any{"KeyId": f.symKey}
	policy := `{"Version":"2012-10-17","Id":"audit","Statement":[{"Sid":"a","Effect":"Allow","Principal":{"AWS":"*"},"Action":"kms:*","Resource":"*"}]}`
	return map[string]map[string]any{
		"CreateKey":                           {"Description": "audit"},
		"DescribeKey":                         sym,
		"ListKeys":                            {},
		"ListAliases":                         {},
		"CreateAlias":                         {"AliasName": "alias/made-by-baseline", "TargetKeyId": f.symKey},
		"UpdateAlias":                         {"AliasName": f.alias, "TargetKeyId": f.symKey},
		"DeleteAlias":                         {"AliasName": "alias/made-by-baseline"},
		"EnableKey":                           sym,
		"DisableKey":                          sym,
		"GetKeyPolicy":                        {"KeyId": f.symKey, "PolicyName": "default"},
		"PutKeyPolicy":                        {"KeyId": f.symKey, "PolicyName": "default", "Policy": policy},
		"ListKeyPolicies":                     sym,
		"GetKeyRotationStatus":                sym,
		"EnableKeyRotation":                   sym,
		"DisableKeyRotation":                  sym,
		"ListKeyRotations":                    sym,
		"RotateKeyOnDemand":                   sym,
		"UpdateKeyDescription":                {"KeyId": f.symKey, "Description": "audit"},
		"ScheduleKeyDeletion":                 {"KeyId": f.symKey, "PendingWindowInDays": 7},
		"CancelKeyDeletion":                   sym,
		"TagResource":                         {"KeyId": f.symKey, "Tags": []any{map[string]any{"TagKey": "env", "TagValue": "dev"}}},
		"UntagResource":                       {"KeyId": f.symKey, "TagKeys": []any{"env"}},
		"ListResourceTags":                    sym,
		"Encrypt":                             {"KeyId": f.symKey, "Plaintext": plaintextB64},
		"Decrypt":                             {"KeyId": f.symKey, "CiphertextBlob": f.ciphertext},
		"ReEncrypt":                           {"CiphertextBlob": f.ciphertext, "DestinationKeyId": f.symKey},
		"GenerateDataKey":                     {"KeyId": f.symKey, "KeySpec": "AES_256"},
		"GenerateDataKeyWithoutPlaintext":     {"KeyId": f.symKey, "KeySpec": "AES_256"},
		"GenerateDataKeyPair":                 {"KeyId": f.symKey, "KeyPairSpec": "RSA_2048"},
		"GenerateDataKeyPairWithoutPlaintext": {"KeyId": f.symKey, "KeyPairSpec": "RSA_2048"},
		"GenerateRandom":                      {"NumberOfBytes": 32},
		"GetPublicKey":                        {"KeyId": f.asymKey},
		"Sign":                                {"KeyId": f.asymKey, "Message": plaintextB64, "SigningAlgorithm": "RSASSA_PSS_SHA_256"},
		"Verify":                              {"KeyId": f.asymKey, "Message": plaintextB64, "Signature": f.signature, "SigningAlgorithm": "RSASSA_PSS_SHA_256"},
		"GenerateMac":                         {"KeyId": f.hmacKey, "Message": plaintextB64, "MacAlgorithm": "HMAC_SHA_256"},
		"VerifyMac":                           {"KeyId": f.hmacKey, "Message": plaintextB64, "Mac": f.mac, "MacAlgorithm": "HMAC_SHA_256"},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tags[]":            []any{map[string]any{"TagKey": "env", "TagValue": "dev"}},
		"TagKeys[]":         []any{"env"},
		"GrantTokens[]":     []any{"audit-grant-token"},
		"DryRunModifiers[]": []any{"DRY_RUN"},
		"Recipient":         map[string]any{"KeyEncryptionAlgorithm": "RSAES_OAEP_SHA_256"},
	}
}

// prepare gives the non-idempotent operations their own preconditions, so no
// group depends on another having run — operations execute in alphabetical
// order, which is not the order that would make them work.
func prepare(t *testing.T, ts *httptest.Server, f fx, op, mutating string, body map[string]any, n int) {
	t.Helper()
	switch op {
	case "CreateAlias":
		if mutating != "AliasName" {
			body["AliasName"] = fmt.Sprintf("alias/created-%d", n)
		}
	case "DeleteAlias":
		if mutating != "AliasName" {
			name := fmt.Sprintf("alias/doomed-%d", n)
			call(t, ts, "CreateAlias", map[string]any{"AliasName": name, "TargetKeyId": f.symKey})
			body["AliasName"] = name
		}
	case "ScheduleKeyDeletion", "CancelKeyDeletion":
		// Scheduling deletion disables the key every later case relies on, so
		// these get a key of their own. Cancel needs one already scheduled.
		if mutating != "KeyId" {
			code, kb := call(t, ts, "CreateKey", map[string]any{"Description": "throwaway"})
			if code != http.StatusOK {
				return
			}
			var out struct {
				KeyMetadata struct {
					KeyID string `json:"KeyId"`
				} `json:"KeyMetadata"`
			}
			if json.Unmarshal([]byte(kb), &out) != nil || out.KeyMetadata.KeyID == "" {
				return
			}
			body["KeyId"] = out.KeyMetadata.KeyID
			if op == "CancelKeyDeletion" {
				call(t, ts, "ScheduleKeyDeletion", map[string]any{
					"KeyId": out.KeyMetadata.KeyID, "PendingWindowInDays": 7,
				})
			}
		}
	case "DisableKey":
		// Disabling the fixture key would refuse every cryptographic baseline
		// that follows it alphabetically.
		if mutating != "KeyId" {
			code, kb := call(t, ts, "CreateKey", map[string]any{"Description": "throwaway"})
			if code != http.StatusOK {
				return
			}
			var out struct {
				KeyMetadata struct {
					KeyID string `json:"KeyId"`
				} `json:"KeyMetadata"`
			}
			if json.Unmarshal([]byte(kb), &out) == nil && out.KeyMetadata.KeyID != "" {
				body["KeyId"] = out.KeyMetadata.KeyID
			}
		}
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

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_kms.json"))
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

func TestKMSRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store and generates keys")
	}
	ts := kmsServer(t)
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
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, f, op, "", bl, seq())
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
