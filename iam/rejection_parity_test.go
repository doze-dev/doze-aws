package iam

// Rejection parity for IAM, driven by the cases dzaudit derives from AWS's own
// service model (`dzaudit cases iam`).
//
// Same rule as everywhere: a request refused for the WRONG reason looks exactly
// like a pass, so every case is a mutation of a baseline this test first proves
// the service accepts.
//
// IAM is the widest audit here — 89 dispatched operations — and almost all of
// the work is in `prepare`. IAM is a graph of named things that reference each
// other, and most of its operations either create a name that must not already
// exist or consume one that must. A group deleted by DeleteGroup is a group
// DeleteGroup's next case cannot delete; a renamed user is gone under its old
// name; an access key is capped at two per user and a managed policy at five
// versions. Operations also run in alphabetical order, which is nothing like
// the order that would make them work. So rather than a shared fixture that
// erodes as the suite runs, nearly every mutating operation builds and
// addresses its own resource.

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

func iamServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
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
	form := url.Values{"Action": {action}, "Version": {"2010-05-08"}}
	flatten("", body, form)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/iam/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

const (
	user      = "audit-user"
	group     = "audit-group"
	role      = "audit-role"
	profile   = "audit-profile"
	policyDoc = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`
	trustDoc  = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	policyARN = "arn:aws:iam::000000000000:policy/audit-policy"
	userARN   = "arn:aws:iam::000000000000:user/audit-user"
	inline    = "audit-inline"
)

// accessKeyID is the fixture user's key, read back from CreateAccessKey rather
// than invented — GetAccessKeyLastUsed and UpdateAccessKey address a real one.
var accessKeyID string

func setUpFixture(t *testing.T, ts *httptest.Server) {
	t.Helper()
	must := func(action string, body map[string]any) string {
		t.Helper()
		code, resp := call(t, ts, action, body)
		if code != http.StatusOK {
			t.Fatalf("fixture %s = %d: %s", action, code, resp)
		}
		return resp
	}
	must("CreateUser", map[string]any{"UserName": user})
	must("CreateGroup", map[string]any{"GroupName": group})
	must("CreateRole", map[string]any{"RoleName": role, "AssumeRolePolicyDocument": trustDoc})
	must("CreatePolicy", map[string]any{"PolicyName": "audit-policy", "PolicyDocument": policyDoc})
	must("CreateInstanceProfile", map[string]any{"InstanceProfileName": profile})
	must("AddUserToGroup", map[string]any{"GroupName": group, "UserName": user})
	must("AddRoleToInstanceProfile", map[string]any{"InstanceProfileName": profile, "RoleName": role})
	must("AttachUserPolicy", map[string]any{"UserName": user, "PolicyArn": policyARN})
	must("AttachGroupPolicy", map[string]any{"GroupName": group, "PolicyArn": policyARN})
	must("AttachRolePolicy", map[string]any{"RoleName": role, "PolicyArn": policyARN})
	must("PutUserPolicy", map[string]any{"UserName": user, "PolicyName": inline, "PolicyDocument": policyDoc})
	must("PutGroupPolicy", map[string]any{"GroupName": group, "PolicyName": inline, "PolicyDocument": policyDoc})
	must("PutRolePolicy", map[string]any{"RoleName": role, "PolicyName": inline, "PolicyDocument": policyDoc})
	must("CreatePolicyVersion", map[string]any{"PolicyArn": policyARN, "PolicyDocument": policyDoc})

	resp := must("CreateAccessKey", map[string]any{"UserName": user})
	m := regexp.MustCompile(`<AccessKeyId>([^<]+)</AccessKeyId>`).FindStringSubmatch(resp)
	if m == nil {
		t.Fatalf("fixture CreateAccessKey returned no AccessKeyId: %s", resp)
	}
	accessKeyID = m[1]
}

// baselines are requests the service must accept, one per operation. Names that
// read "made-by-baseline" are replaced per case by prepare; they are spelled out
// here so an operation with no prepare entry fails loudly rather than quietly
// addressing the shared fixture.
func baselines() map[string]map[string]any {
	byUser := map[string]any{"UserName": user}
	byGroup := map[string]any{"GroupName": group}
	byRole := map[string]any{"RoleName": role}
	byProfile := map[string]any{"InstanceProfileName": profile}
	byPolicy := map[string]any{"PolicyArn": policyARN}
	tags := []any{map[string]any{"Key": "env", "Value": "dev"}}

	return map[string]map[string]any{
		// Principals.
		"CreateUser":  {"UserName": "made-by-baseline"},
		"GetUser":     byUser,
		"ListUsers":   {},
		"UpdateUser":  {"UserName": "made-by-baseline", "NewUserName": "made-by-baseline-2"},
		"DeleteUser":  {"UserName": "made-by-baseline"},
		"CreateGroup": {"GroupName": "made-by-baseline"},
		"GetGroup":    byGroup,
		"ListGroups":  {},
		"UpdateGroup": {"GroupName": "made-by-baseline", "NewGroupName": "made-by-baseline-2"},
		"DeleteGroup": {"GroupName": "made-by-baseline"},
		"CreateRole": {"RoleName": "made-by-baseline",
			"AssumeRolePolicyDocument": trustDoc},
		"GetRole":               byRole,
		"ListRoles":             {},
		"UpdateRole":            {"RoleName": role, "Description": "audited"},
		"UpdateRoleDescription": {"RoleName": role, "Description": "audited"},
		"UpdateAssumeRolePolicy": {"RoleName": role,
			"PolicyDocument": trustDoc},
		"DeleteRole":              {"RoleName": "made-by-baseline"},
		"CreateServiceLinkedRole": {"AWSServiceName": "made-by-baseline.amazonaws.com"},
		"DeleteServiceLinkedRole": {"RoleName": "made-by-baseline"},

		// Group membership.
		"AddUserToGroup":      {"GroupName": group, "UserName": "made-by-baseline"},
		"RemoveUserFromGroup": {"GroupName": group, "UserName": "made-by-baseline"},
		"ListGroupsForUser":   byUser,

		// Managed policies.
		"CreatePolicy": {"PolicyName": "made-by-baseline",
			"PolicyDocument": policyDoc},
		"GetPolicy":               byPolicy,
		"ListPolicies":            {},
		"DeletePolicy":            {"PolicyArn": "made-by-baseline"},
		"CreatePolicyVersion":     {"PolicyArn": "made-by-baseline", "PolicyDocument": policyDoc},
		"GetPolicyVersion":        {"PolicyArn": policyARN, "VersionId": "v1"},
		"ListPolicyVersions":      byPolicy,
		"DeletePolicyVersion":     {"PolicyArn": "made-by-baseline", "VersionId": "v2"},
		"SetDefaultPolicyVersion": {"PolicyArn": "made-by-baseline", "VersionId": "v2"},
		"ListEntitiesForPolicy":   byPolicy,

		// Attachment.
		"AttachUserPolicy":          {"UserName": "made-by-baseline", "PolicyArn": policyARN},
		"DetachUserPolicy":          {"UserName": "made-by-baseline", "PolicyArn": policyARN},
		"AttachGroupPolicy":         {"GroupName": "made-by-baseline", "PolicyArn": policyARN},
		"DetachGroupPolicy":         {"GroupName": "made-by-baseline", "PolicyArn": policyARN},
		"AttachRolePolicy":          {"RoleName": "made-by-baseline", "PolicyArn": policyARN},
		"DetachRolePolicy":          {"RoleName": "made-by-baseline", "PolicyArn": policyARN},
		"ListAttachedUserPolicies":  byUser,
		"ListAttachedGroupPolicies": byGroup,
		"ListAttachedRolePolicies":  byRole,

		// Inline policies.
		"PutUserPolicy":     {"UserName": user, "PolicyName": "made-by-baseline", "PolicyDocument": policyDoc},
		"GetUserPolicy":     {"UserName": user, "PolicyName": inline},
		"ListUserPolicies":  byUser,
		"DeleteUserPolicy":  {"UserName": user, "PolicyName": "made-by-baseline"},
		"PutGroupPolicy":    {"GroupName": group, "PolicyName": "made-by-baseline", "PolicyDocument": policyDoc},
		"GetGroupPolicy":    {"GroupName": group, "PolicyName": inline},
		"ListGroupPolicies": byGroup,
		"DeleteGroupPolicy": {"GroupName": group, "PolicyName": "made-by-baseline"},
		"PutRolePolicy":     {"RoleName": role, "PolicyName": "made-by-baseline", "PolicyDocument": policyDoc},
		"GetRolePolicy":     {"RoleName": role, "PolicyName": inline},
		"ListRolePolicies":  byRole,
		"DeleteRolePolicy":  {"RoleName": role, "PolicyName": "made-by-baseline"},

		// Permissions boundaries.
		"PutUserPermissionsBoundary":    {"UserName": user, "PermissionsBoundary": policyARN},
		"DeleteUserPermissionsBoundary": {"UserName": "made-by-baseline"},
		"PutRolePermissionsBoundary":    {"RoleName": role, "PermissionsBoundary": policyARN},
		"DeleteRolePermissionsBoundary": {"RoleName": "made-by-baseline"},

		// Instance profiles.
		"CreateInstanceProfile":         {"InstanceProfileName": "made-by-baseline"},
		"GetInstanceProfile":            byProfile,
		"ListInstanceProfiles":          {},
		"ListInstanceProfilesForRole":   byRole,
		"DeleteInstanceProfile":         {"InstanceProfileName": "made-by-baseline"},
		"AddRoleToInstanceProfile":      {"InstanceProfileName": "made-by-baseline", "RoleName": role},
		"RemoveRoleFromInstanceProfile": {"InstanceProfileName": "made-by-baseline", "RoleName": role},

		// Access keys.
		"CreateAccessKey":      {"UserName": "made-by-baseline"},
		"ListAccessKeys":       byUser,
		"UpdateAccessKey":      {"UserName": user, "AccessKeyId": "made-by-baseline", "Status": "Inactive"},
		"DeleteAccessKey":      {"UserName": "made-by-baseline", "AccessKeyId": "made-by-baseline"},
		"GetAccessKeyLastUsed": {"AccessKeyId": "made-by-baseline"},

		// Account aliases.
		"CreateAccountAlias": {"AccountAlias": "made-by-baseline"},
		"DeleteAccountAlias": {"AccountAlias": "made-by-baseline"},
		"ListAccountAliases": {},

		// Tags.
		"TagUser":                 {"UserName": user, "Tags": tags},
		"UntagUser":               {"UserName": user, "TagKeys": []any{"made-by-baseline"}},
		"ListUserTags":            byUser,
		"TagRole":                 {"RoleName": role, "Tags": tags},
		"UntagRole":               {"RoleName": role, "TagKeys": []any{"made-by-baseline"}},
		"ListRoleTags":            byRole,
		"TagPolicy":               {"PolicyArn": policyARN, "Tags": tags},
		"UntagPolicy":             {"PolicyArn": policyARN, "TagKeys": []any{"made-by-baseline"}},
		"ListPolicyTags":          byPolicy,
		"TagInstanceProfile":      {"InstanceProfileName": profile, "Tags": tags},
		"UntagInstanceProfile":    {"InstanceProfileName": profile, "TagKeys": []any{"made-by-baseline"}},
		"ListInstanceProfileTags": byProfile,

		// Reporting and simulation.
		"GetAccountAuthorizationDetails":   {},
		"GetContextKeysForCustomPolicy":    {"PolicyInputList": []any{policyDoc}},
		"GetContextKeysForPrincipalPolicy": {"PolicySourceArn": userARN},
		"SimulateCustomPolicy": {"PolicyInputList": []any{policyDoc},
			"ActionNames": []any{"s3:GetObject"}},
		"SimulatePrincipalPolicy": {"PolicySourceArn": userARN,
			"ActionNames": []any{"s3:GetObject"}},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tags[]":            []any{map[string]any{"Key": "env", "Value": "dev"}},
		"TagKeys[]":         []any{"env"},
		"Filter[]":          []any{"User"},
		"ActionNames[]":     []any{"s3:GetObject"},
		"PolicyInputList[]": []any{policyDoc},
		"ResourceArns[]":    []any{"arn:aws:s3:::audit-bucket/*"},
		"ContextEntries[]": []any{map[string]any{
			"ContextKeyName": "aws:SourceIp", "ContextKeyType": "string",
			"ContextKeyValues": []any{"10.0.0.1"},
		}},
		"ContextEntries[].ContextKeyValues[]": []any{"10.0.0.1"},
		"OrderedOrganizationPolicyInputList[]": []any{map[string]any{
			"OrganizationsPolicyId":         "p-audit",
			"ServiceControlPolicyInputList": []any{policyDoc},
		}},
		"OrderedOrganizationPolicyInputList[].ServiceControlPolicyInputList[]": []any{policyDoc},
		"PermissionsBoundaryPolicyInputList[]":                                 []any{policyDoc},
		"ResourcePolicy":                                                       policyDoc,
	}
}

// prepare gives every mutating operation its own resource to act on. IAM's
// operations create names that must not already exist and consume names that
// must, so a shared fixture would erode as the suite runs: the first
// DeleteGroup case would take the group every later case needed.
//
// `mutating` is the path the case is about. When the case mutates the very
// field prepare would set, prepare leaves it alone — otherwise the harness
// would overwrite the violating value with a valid one and the case would test
// nothing.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	mk := func(action string, b map[string]any) {
		t.Helper()
		if code, resp := call(t, ts, action, b); code != http.StatusOK {
			t.Fatalf("prepare %s for %s = %d: %s", action, op, code, resp)
		}
	}
	// newX builds a throwaway resource and returns how to address it.
	newUser := func() string {
		name := fmt.Sprintf("user-%d", n)
		mk("CreateUser", map[string]any{"UserName": name})
		return name
	}
	newGroup := func() string {
		name := fmt.Sprintf("group-%d", n)
		mk("CreateGroup", map[string]any{"GroupName": name})
		return name
	}
	newRole := func() string {
		name := fmt.Sprintf("role-%d", n)
		mk("CreateRole", map[string]any{"RoleName": name, "AssumeRolePolicyDocument": trustDoc})
		return name
	}
	newProfile := func() string {
		name := fmt.Sprintf("profile-%d", n)
		mk("CreateInstanceProfile", map[string]any{"InstanceProfileName": name})
		return name
	}
	newPolicy := func() string {
		name := fmt.Sprintf("policy-%d", n)
		mk("CreatePolicy", map[string]any{"PolicyName": name, "PolicyDocument": policyDoc})
		return "arn:aws:iam::000000000000:policy/" + name
	}
	set := func(field, value string) {
		if mutating != field {
			body[field] = value
		}
	}

	switch op {
	// --- Create: the name must not already exist.
	case "CreateUser":
		set("UserName", fmt.Sprintf("created-user-%d", n))
	case "CreateGroup":
		set("GroupName", fmt.Sprintf("created-group-%d", n))
	case "CreateRole":
		set("RoleName", fmt.Sprintf("created-role-%d", n))
	case "CreateInstanceProfile":
		set("InstanceProfileName", fmt.Sprintf("created-profile-%d", n))
	case "CreatePolicy":
		set("PolicyName", fmt.Sprintf("created-policy-%d", n))
	case "CreateAccountAlias":
		set("AccountAlias", fmt.Sprintf("created-alias-%d", n))
	case "CreateServiceLinkedRole":
		set("AWSServiceName", fmt.Sprintf("svc%d.amazonaws.com", n))
	case "CreateAccessKey":
		// AWS caps a user at two access keys, so each case needs a fresh user.
		set("UserName", newUser())

	// --- Delete: the name must exist, and will not after this.
	case "DeleteUser":
		set("UserName", newUser())
	case "DeleteGroup":
		set("GroupName", newGroup())
	case "DeleteRole":
		set("RoleName", newRole())
	case "DeleteInstanceProfile":
		set("InstanceProfileName", newProfile())
	case "DeletePolicy":
		set("PolicyArn", newPolicy())
	case "DeleteAccountAlias":
		alias := fmt.Sprintf("doomed-alias-%d", n)
		mk("CreateAccountAlias", map[string]any{"AccountAlias": alias})
		set("AccountAlias", alias)
	case "DeleteServiceLinkedRole":
		// The generated role name is read back rather than re-derived here: a
		// test that recomputes the service's own naming rule cannot catch that
		// rule being wrong.
		svc := fmt.Sprintf("doomedsvc%d.amazonaws.com", n)
		code, resp := call(t, ts, "CreateServiceLinkedRole", map[string]any{"AWSServiceName": svc})
		if code != http.StatusOK {
			t.Fatalf("prepare CreateServiceLinkedRole for %s = %d: %s", op, code, resp)
		}
		m := regexp.MustCompile(`<RoleName>([^<]+)</RoleName>`).FindStringSubmatch(resp)
		if m == nil {
			t.Fatalf("CreateServiceLinkedRole returned no RoleName: %s", resp)
		}
		set("RoleName", m[1])

	// --- Rename: the old name is gone afterwards.
	case "UpdateUser":
		set("UserName", newUser())
		set("NewUserName", fmt.Sprintf("renamed-user-%d", n))
	case "UpdateGroup":
		set("GroupName", newGroup())
		set("NewGroupName", fmt.Sprintf("renamed-group-%d", n))

	// --- Membership and attachment: one edge, added then removed.
	case "AddUserToGroup":
		set("UserName", newUser())
	case "RemoveUserFromGroup":
		u := newUser()
		mk("AddUserToGroup", map[string]any{"GroupName": group, "UserName": u})
		set("UserName", u)
	case "AddRoleToInstanceProfile":
		// An instance profile holds at most one role.
		set("InstanceProfileName", newProfile())
	case "RemoveRoleFromInstanceProfile":
		p := newProfile()
		mk("AddRoleToInstanceProfile", map[string]any{"InstanceProfileName": p, "RoleName": role})
		set("InstanceProfileName", p)
	case "AttachUserPolicy":
		set("UserName", newUser())
	case "DetachUserPolicy":
		u := newUser()
		mk("AttachUserPolicy", map[string]any{"UserName": u, "PolicyArn": policyARN})
		set("UserName", u)
	case "AttachGroupPolicy":
		set("GroupName", newGroup())
	case "DetachGroupPolicy":
		g := newGroup()
		mk("AttachGroupPolicy", map[string]any{"GroupName": g, "PolicyArn": policyARN})
		set("GroupName", g)
	case "AttachRolePolicy":
		set("RoleName", newRole())
	case "DetachRolePolicy":
		r := newRole()
		mk("AttachRolePolicy", map[string]any{"RoleName": r, "PolicyArn": policyARN})
		set("RoleName", r)

	// --- Inline policies: put is idempotent, delete consumes.
	case "PutUserPolicy", "PutGroupPolicy", "PutRolePolicy":
		set("PolicyName", fmt.Sprintf("inline-%d", n))
	case "DeleteUserPolicy":
		name := fmt.Sprintf("doomed-inline-%d", n)
		mk("PutUserPolicy", map[string]any{"UserName": user, "PolicyName": name, "PolicyDocument": policyDoc})
		set("PolicyName", name)
	case "DeleteGroupPolicy":
		name := fmt.Sprintf("doomed-inline-%d", n)
		mk("PutGroupPolicy", map[string]any{"GroupName": group, "PolicyName": name, "PolicyDocument": policyDoc})
		set("PolicyName", name)
	case "DeleteRolePolicy":
		name := fmt.Sprintf("doomed-inline-%d", n)
		mk("PutRolePolicy", map[string]any{"RoleName": role, "PolicyName": name, "PolicyDocument": policyDoc})
		set("PolicyName", name)

	// --- Permissions boundaries: delete needs one set.
	case "DeleteUserPermissionsBoundary":
		u := newUser()
		mk("PutUserPermissionsBoundary", map[string]any{"UserName": u, "PermissionsBoundary": policyARN})
		set("UserName", u)
	case "DeleteRolePermissionsBoundary":
		r := newRole()
		mk("PutRolePermissionsBoundary", map[string]any{"RoleName": r, "PermissionsBoundary": policyARN})
		set("RoleName", r)

	// --- Policy versions: AWS caps a managed policy at five.
	case "CreatePolicyVersion":
		set("PolicyArn", newPolicy())
	case "DeletePolicyVersion", "SetDefaultPolicyVersion":
		arn := newPolicy()
		mk("CreatePolicyVersion", map[string]any{"PolicyArn": arn, "PolicyDocument": policyDoc})
		set("PolicyArn", arn)

	// --- Access keys.
	case "DeleteAccessKey":
		u := newUser()
		code, resp := call(t, ts, "CreateAccessKey", map[string]any{"UserName": u})
		if code != http.StatusOK {
			t.Fatalf("prepare CreateAccessKey for %s = %d: %s", op, code, resp)
		}
		m := regexp.MustCompile(`<AccessKeyId>([^<]+)</AccessKeyId>`).FindStringSubmatch(resp)
		set("UserName", u)
		set("AccessKeyId", m[1])
	case "UpdateAccessKey", "GetAccessKeyLastUsed":
		set("AccessKeyId", accessKeyID)

	// --- Untag needs the tag to be there, and consumes it.
	case "UntagUser", "UntagRole", "UntagPolicy", "UntagInstanceProfile":
		if mutating != "TagKeys" && mutating != "TagKeys[]" {
			key := fmt.Sprintf("doomed-tag-%d", n)
			tag := []any{map[string]any{"Key": key, "Value": "v"}}
			switch op {
			case "UntagUser":
				mk("TagUser", map[string]any{"UserName": user, "Tags": tag})
			case "UntagRole":
				mk("TagRole", map[string]any{"RoleName": role, "Tags": tag})
			case "UntagPolicy":
				mk("TagPolicy", map[string]any{"PolicyArn": policyARN, "Tags": tag})
			case "UntagInstanceProfile":
				mk("TagInstanceProfile", map[string]any{"InstanceProfileName": profile, "Tags": tag})
			}
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
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_iam.json"))
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

func TestIAMRejectsWhatTheModelForbids(t *testing.T) {
	ts := iamServer(t)
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
