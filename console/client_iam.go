package console

// IAM console client (Query protocol, XML responses).
//
// Locally IAM is usually off, and when it is on the question is never "what
// does this policy say" but "why was that request denied" — or, before
// deploying, "what permissions does this thing actually need". So the page is
// built around the principal and its effective permissions, with the soft-mode
// access log and the policy it generates as the centre of gravity rather than
// a footnote.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// Principal is a user or a role.
type Principal struct {
	Kind    string // "user" | "role"
	Name    string
	ARN     string
	Path    string
	Created string
	// Trust is a role's assume-role policy, formatted.
	Trust string
}

// PolicyRef is a managed policy attached to a principal, or a standalone one.
type PolicyRef struct {
	Name       string
	ARN        string
	AWSManaged bool
	Attached   int
}

// InlinePolicy is a policy embedded directly in a principal.
type InlinePolicy struct {
	Name     string
	Document string
}

// AccessKey is one credential belonging to a user.
type AccessKey struct {
	ID      string
	Status  string
	Created string
}

// AccessEvent is one recorded authorization question and its verdict.
type AccessEvent struct {
	Principal string
	Action    string
	Resource  string
	Decision  string
	Known     bool
	// MatchedBy names the statement that decided. The permissions-boundary case
	// arrives here as the literal string "permissions boundary", which is a
	// distinction the AWS console does not draw.
	MatchedBy string
	Count     int
	Last      string
	Denied    bool
}

// SimResult is one decision from the policy simulator.
type SimResult struct {
	Action   string
	Resource string
	Decision string
	Allowed  bool
	// MatchedBy names the statement that decided — the Sid, or PolicyID[i], or
	// statement[i]. iam/simulate.go calls it "the part that makes a simulation
	// actionable rather than just a verdict", and it was being discarded here.
	// Empty means nothing matched, which is a DIFFERENT answer from an explicit
	// Deny and has to read differently.
	MatchedBy string
}

func (b *backend) iam(ctx context.Context, action string, extra url.Values) ([]byte, error) {
	v := url.Values{"Action": {action}, "Version": {"2010-05-08"}}
	for k, vals := range extra {
		v[k] = vals
	}
	// Signed with the SigV4-shaped credential scope: the gateway's Query
	// action table stops short of IAM's long tail (the context-keys pair,
	// instance-profile tags), and a real SDK's signature names the service
	// anyway — so the console's does too.
	req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20260101/"+awsident.Region+"/iam/aws4_request")
	return b.do(req)
}

// prettyPolicy reformats a policy document for display. IAM returns documents
// URL-encoded, which prettyJSON alone would pass straight through as one
// unreadable line.
func prettyPolicy(s string) string {
	if s == "" {
		return ""
	}
	if dec, err := url.QueryUnescape(s); err == nil {
		s = dec
	}
	return prettyJSON(s)
}

func (b *backend) ListPrincipals(ctx context.Context) ([]Principal, error) {
	var out []Principal

	body, err := b.iam(ctx, "ListUsers", nil)
	if err != nil {
		return nil, err
	}
	var users struct {
		Members []struct {
			UserName   string `xml:"UserName"`
			Arn        string `xml:"Arn"`
			Path       string `xml:"Path"`
			CreateDate string `xml:"CreateDate"`
		} `xml:"ListUsersResult>Users>member"`
	}
	if err := xml.Unmarshal(body, &users); err != nil {
		return nil, err
	}
	for _, u := range users.Members {
		out = append(out, Principal{
			Kind: "user", Name: u.UserName, ARN: u.Arn, Path: u.Path,
			Created: shortTime(u.CreateDate),
		})
	}

	body, err = b.iam(ctx, "ListRoles", nil)
	if err != nil {
		return nil, err
	}
	var roles struct {
		Members []struct {
			RoleName   string `xml:"RoleName"`
			Arn        string `xml:"Arn"`
			Path       string `xml:"Path"`
			CreateDate string `xml:"CreateDate"`
			Trust      string `xml:"AssumeRolePolicyDocument"`
		} `xml:"ListRolesResult>Roles>member"`
	}
	if err := xml.Unmarshal(body, &roles); err != nil {
		return nil, err
	}
	for _, r := range roles.Members {
		out = append(out, Principal{
			Kind: "role", Name: r.RoleName, ARN: r.Arn, Path: r.Path,
			Created: shortTime(r.CreateDate), Trust: prettyPolicy(r.Trust),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// CountPrincipals is the cheap probe for the nav badge.
func (b *backend) CountPrincipals(ctx context.Context) (int, error) {
	ps, err := b.ListPrincipals(ctx)
	return len(ps), err
}

func (b *backend) Principal(ctx context.Context, kind, name string) (*Principal, error) {
	ps, err := b.ListPrincipals(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ps {
		if ps[i].Kind == kind && ps[i].Name == name {
			return &ps[i], nil
		}
	}
	return nil, fmt.Errorf("%s %s does not exist", kind, name)
}

// AttachedPolicies lists the managed policies on a principal.
func (b *backend) AttachedPolicies(ctx context.Context, kind, name string) ([]PolicyRef, error) {
	action, key := "ListAttachedUserPolicies", "UserName"
	switch kind {
	case "role":
		action, key = "ListAttachedRolePolicies", "RoleName"
	case "group":
		action, key = "ListAttachedGroupPolicies", "GroupName"
	}
	body, err := b.iam(ctx, action, url.Values{key: {name}})
	if err != nil {
		return nil, err
	}
	// The Query protocol wraps every payload in an action-specific Result
	// element, so the path differs between the user and role forms even though
	// the shape inside is identical.
	type member struct {
		PolicyName string `xml:"PolicyName"`
		PolicyArn  string `xml:"PolicyArn"`
	}
	// One branch per kind. The group arm was missing: the call went out as
	// ListAttachedGroupPolicies and the answer was parsed as the User form,
	// so the path never matched and a group's attached policies were ALWAYS
	// empty. The page said "No managed policies attached" however many you
	// attached, and the e2e test that should have caught it asserted
	// toContainText(''), which is true of any content at all.
	var members []member
	switch kind {
	case "role":
		var out struct {
			M []member `xml:"ListAttachedRolePoliciesResult>AttachedPolicies>member"`
		}
		if err := xml.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		members = out.M
	case "group":
		var out struct {
			M []member `xml:"ListAttachedGroupPoliciesResult>AttachedPolicies>member"`
		}
		if err := xml.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		members = out.M
	default:
		var out struct {
			M []member `xml:"ListAttachedUserPoliciesResult>AttachedPolicies>member"`
		}
		if err := xml.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		members = out.M
	}
	refs := make([]PolicyRef, 0, len(members))
	for _, p := range members {
		refs = append(refs, PolicyRef{
			Name: p.PolicyName, ARN: p.PolicyArn,
			AWSManaged: strings.HasPrefix(p.PolicyArn, "arn:aws:iam::aws:"),
		})
	}
	return refs, nil
}

// InlinePolicies lists and resolves the policies embedded in a principal.
func (b *backend) InlinePolicies(ctx context.Context, kind, name string) ([]InlinePolicy, error) {
	listAction, getAction, key := "ListUserPolicies", "GetUserPolicy", "UserName"
	if kind == "role" {
		listAction, getAction, key = "ListRolePolicies", "GetRolePolicy", "RoleName"
	}
	body, err := b.iam(ctx, listAction, url.Values{key: {name}})
	if err != nil {
		return nil, err
	}
	var policyNames []string
	if kind == "role" {
		var n struct {
			Names []string `xml:"ListRolePoliciesResult>PolicyNames>member"`
		}
		if err := xml.Unmarshal(body, &n); err != nil {
			return nil, err
		}
		policyNames = n.Names
	} else {
		var n struct {
			Names []string `xml:"ListUserPoliciesResult>PolicyNames>member"`
		}
		if err := xml.Unmarshal(body, &n); err != nil {
			return nil, err
		}
		policyNames = n.Names
	}
	out := make([]InlinePolicy, 0, len(policyNames))
	for _, pn := range policyNames {
		doc, err := b.iam(ctx, getAction, url.Values{key: {name}, "PolicyName": {pn}})
		if err != nil {
			continue
		}
		var document string
		if kind == "role" {
			var d struct {
				Doc string `xml:"GetRolePolicyResult>PolicyDocument"`
			}
			if xml.Unmarshal(doc, &d) == nil {
				document = d.Doc
			}
		} else {
			var d struct {
				Doc string `xml:"GetUserPolicyResult>PolicyDocument"`
			}
			if xml.Unmarshal(doc, &d) == nil {
				document = d.Doc
			}
		}
		if document != "" {
			out = append(out, InlinePolicy{Name: pn, Document: prettyPolicy(document)})
		}
	}
	return out, nil
}

func (b *backend) AccessKeys(ctx context.Context, user string) ([]AccessKey, error) {
	body, err := b.iam(ctx, "ListAccessKeys", url.Values{"UserName": {user}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []struct {
			ID         string `xml:"AccessKeyId"`
			Status     string `xml:"Status"`
			CreateDate string `xml:"CreateDate"`
		} `xml:"ListAccessKeysResult>AccessKeyMetadata>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	keys := make([]AccessKey, 0, len(out.Members))
	for _, k := range out.Members {
		keys = append(keys, AccessKey{ID: k.ID, Status: k.Status, Created: shortTime(k.CreateDate)})
	}
	return keys, nil
}

// ListManagedPolicies lists customer-managed policies. AWS-managed ones are
// synthesized on demand and there are hundreds, so listing them would bury the
// handful someone actually wrote.
func (b *backend) ListManagedPolicies(ctx context.Context) ([]PolicyRef, error) {
	body, err := b.iam(ctx, "ListPolicies", url.Values{"Scope": {"Local"}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Members []struct {
			PolicyName string `xml:"PolicyName"`
			Arn        string `xml:"Arn"`
			Count      int    `xml:"AttachmentCount"`
		} `xml:"ListPoliciesResult>Policies>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	refs := make([]PolicyRef, 0, len(out.Members))
	for _, p := range out.Members {
		refs = append(refs, PolicyRef{Name: p.PolicyName, ARN: p.Arn, Attached: p.Count})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, nil
}

// PolicyDocument resolves a managed policy's default version.
func (b *backend) PolicyDocument(ctx context.Context, arn string) (string, error) {
	body, err := b.iam(ctx, "GetPolicy", url.Values{"PolicyArn": {arn}})
	if err != nil {
		return "", err
	}
	var meta struct {
		Version string `xml:"GetPolicyResult>Policy>DefaultVersionId"`
	}
	if err := xml.Unmarshal(body, &meta); err != nil {
		return "", err
	}
	if meta.Version == "" {
		meta.Version = "v1"
	}
	body, err = b.iam(ctx, "GetPolicyVersion", url.Values{
		"PolicyArn": {arn}, "VersionId": {meta.Version},
	})
	if err != nil {
		return "", err
	}
	var ver struct {
		Document string `xml:"GetPolicyVersionResult>PolicyVersion>Document"`
	}
	if err := xml.Unmarshal(body, &ver); err != nil {
		return "", err
	}
	return prettyPolicy(ver.Document), nil
}

// AccessLog returns the enforcement mode and everything IAM was asked to
// decide. In soft mode this is the record of what a run actually needed.
func (b *backend) AccessLog(ctx context.Context) (string, []AccessEvent, error) {
	body, err := b.iam(ctx, "DozeAccessLog", nil)
	if err != nil {
		return "", nil, err
	}
	var out struct {
		Mode    string `xml:"DozeAccessLogResult>Mode"`
		Entries []struct {
			Principal string `xml:"Principal"`
			Action    string `xml:"Action"`
			Resource  string `xml:"Resource"`
			Decision  string `xml:"Decision"`
			Known     bool   `xml:"ResourceKnown"`
			MatchedBy string `xml:"MatchedBy"`
			Count     int    `xml:"Count"`
			LastUsed  string `xml:"LastUsed"`
		} `xml:"DozeAccessLogResult>Entries>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", nil, err
	}
	evs := make([]AccessEvent, 0, len(out.Entries))
	for _, e := range out.Entries {
		evs = append(evs, AccessEvent{
			Principal: e.Principal, Action: e.Action, Resource: e.Resource,
			Decision: e.Decision, Known: e.Known, MatchedBy: e.MatchedBy, Count: e.Count,
			Last: shortTime(e.LastUsed), Denied: !strings.EqualFold(e.Decision, "allowed"),
		})
	}
	sort.Slice(evs, func(i, j int) bool {
		// Denials first: they are the reason anyone opens this page.
		if evs[i].Denied != evs[j].Denied {
			return evs[i].Denied
		}
		return evs[i].Action < evs[j].Action
	})
	return out.Mode, evs, nil
}

// GeneratedPolicy is the least-privilege document covering exactly what the
// recorded principals did — the payoff of a soft-mode run.
func (b *backend) GeneratedPolicy(ctx context.Context, principal string) (string, error) {
	v := url.Values{}
	if principal != "" {
		v.Set("Principal", principal)
	}
	body, err := b.iam(ctx, "DozeGeneratePolicy", v)
	if err != nil {
		// The service explains this one properly ("no access has been
		// recorded; run with iam mode 'soft' first"). Showing the raw error
		// envelope instead would bury the one sentence that helps.
		return "", awsMessage(err)
	}
	var out struct {
		Document string `xml:"DozeGeneratePolicyResult>PolicyDocument"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return prettyPolicy(out.Document), nil
}

// Simulate asks IAM how it would decide, without having to provoke the call.
func (b *backend) Simulate(ctx context.Context, principalARN string, actions []string, resource string) ([]SimResult, error) {
	v := url.Values{"PolicySourceArn": {principalARN}}
	for i, a := range actions {
		v.Set(fmt.Sprintf("ActionNames.member.%d", i+1), a)
	}
	if resource != "" {
		v.Set("ResourceArns.member.1", resource)
	}
	body, err := b.iam(ctx, "SimulatePrincipalPolicy", v)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct {
			Action   string `xml:"EvalActionName"`
			Resource string `xml:"EvalResourceName"`
			Decision string `xml:"EvalDecision"`
			Matched  []struct {
				SourcePolicyId string `xml:"SourcePolicyId"`
			} `xml:"MatchedStatements>member"`
		} `xml:"SimulatePrincipalPolicyResult>EvaluationResults>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	res := make([]SimResult, 0, len(out.Results))
	for _, r := range out.Results {
		sr := SimResult{
			Action: r.Action, Resource: r.Resource, Decision: r.Decision,
			Allowed: strings.EqualFold(r.Decision, "allowed"),
		}
		if len(r.Matched) > 0 {
			sr.MatchedBy = r.Matched[0].SourcePolicyId
		}
		res = append(res, sr)
	}
	return res, nil
}

// awsMessage reduces an AWS error envelope to the message it carries. The
// services answer with a usable sentence; the XML around it is noise.
// awsMessage used to flatten an *apiErr into a bare message, which read better
// in a toast and cost the error code — and every IAM mutation goes through here,
// so IAM was the one service whose failures could never name what refused them.
//
// It now returns the error unchanged. parseRefusal already pulls both the code
// and the message out of this exact XML shape, and c.fail runs it, so keeping
// the *apiErr intact is what lets an IAM failure render like every other one.
// Kept as a named function rather than deleted at thirteen call sites so the
// reason survives next to them.
func awsMessage(err error) error { return err }

// ---- mutations ----

// CreateUser makes a user. Locally a user matters mostly as something to hang
// permissions on and to simulate against.
func (b *backend) CreateUser(ctx context.Context, name string) error {
	_, err := b.iam(ctx, "CreateUser", url.Values{"UserName": {name}})
	return awsMessage(err)
}

// CreateRole needs a trust policy: a role nothing may assume is not a role.
func (b *backend) CreateRole(ctx context.Context, name, trust string) error {
	_, err := b.iam(ctx, "CreateRole", url.Values{
		"RoleName": {name}, "AssumeRolePolicyDocument": {trust},
	})
	return awsMessage(err)
}

func (b *backend) CreatePolicy(ctx context.Context, name, document string) error {
	_, err := b.iam(ctx, "CreatePolicy", url.Values{
		"PolicyName": {name}, "PolicyDocument": {document},
	})
	return awsMessage(err)
}

// AttachPolicy and DetachPolicy are the same operation under two names, split
// by whether the principal is a user or a role.
func (b *backend) AttachPolicy(ctx context.Context, kind, name, policyARN string) error {
	action, key := "AttachUserPolicy", "UserName"
	switch kind {
	case "role":
		action, key = "AttachRolePolicy", "RoleName"
	case "group":
		action, key = "AttachGroupPolicy", "GroupName"
	}
	_, err := b.iam(ctx, action, url.Values{key: {name}, "PolicyArn": {policyARN}})
	return awsMessage(err)
}

func (b *backend) DetachPolicy(ctx context.Context, kind, name, policyARN string) error {
	action, key := "DetachUserPolicy", "UserName"
	switch kind {
	case "role":
		action, key = "DetachRolePolicy", "RoleName"
	case "group":
		action, key = "DetachGroupPolicy", "GroupName"
	}
	_, err := b.iam(ctx, action, url.Values{key: {name}, "PolicyArn": {policyARN}})
	return awsMessage(err)
}

func (b *backend) DeletePrincipal(ctx context.Context, kind, name string) error {
	action, key := "DeleteUser", "UserName"
	if kind == "role" {
		action, key = "DeleteRole", "RoleName"
	}
	_, err := b.iam(ctx, action, url.Values{key: {name}})
	return awsMessage(err)
}

func (b *backend) DeleteManagedPolicy(ctx context.Context, arn string) error {
	_, err := b.iam(ctx, "DeletePolicy", url.Values{"PolicyArn": {arn}})
	return awsMessage(err)
}

// NewAccessKey returns the created pair. The secret is shown once, the way AWS
// does it, because it is not retrievable afterwards.
func (b *backend) NewAccessKey(ctx context.Context, user string) (id, secret string, err error) {
	body, err := b.iam(ctx, "CreateAccessKey", url.Values{"UserName": {user}})
	if err != nil {
		return "", "", awsMessage(err)
	}
	var out struct {
		ID     string `xml:"CreateAccessKeyResult>AccessKey>AccessKeyId"`
		Secret string `xml:"CreateAccessKeyResult>AccessKey>SecretAccessKey"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", "", err
	}
	return out.ID, out.Secret, nil
}

func (b *backend) DeleteAccessKey(ctx context.Context, user, keyID string) error {
	_, err := b.iam(ctx, "DeleteAccessKey", url.Values{
		"UserName": {user}, "AccessKeyId": {keyID},
	})
	return awsMessage(err)
}

// PutInlinePolicy creates or replaces a policy embedded in a principal. The
// service parses the document and refuses a malformed one, so a bad edit comes
// back as a message rather than being stored and failing later.
func (b *backend) PutInlinePolicy(ctx context.Context, kind, name, policyName, document string) error {
	action, key := "PutUserPolicy", "UserName"
	if kind == "role" {
		action, key = "PutRolePolicy", "RoleName"
	}
	_, err := b.iam(ctx, action, url.Values{
		key: {name}, "PolicyName": {policyName}, "PolicyDocument": {document},
	})
	return awsMessage(err)
}

func (b *backend) DeleteInlinePolicy(ctx context.Context, kind, name, policyName string) error {
	action, key := "DeleteUserPolicy", "UserName"
	if kind == "role" {
		action, key = "DeleteRolePolicy", "RoleName"
	}
	_, err := b.iam(ctx, action, url.Values{key: {name}, "PolicyName": {policyName}})
	return awsMessage(err)
}

// ---- groups, instance profiles, entity updates: wave one of the burn-down ----

// IAMGroup is one group, with its member count resolved for the listing.
type IAMGroup struct {
	Name, ARN, Created string
	Members            []string
}

// ListIAMGroups lists every group (ListGroups), members unresolved.
func (b *backend) ListIAMGroups(ctx context.Context) ([]IAMGroup, error) {
	body, err := b.iam(ctx, "ListGroups", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Groups []struct {
			GroupName  string `xml:"GroupName"`
			Arn        string `xml:"Arn"`
			CreateDate string `xml:"CreateDate"`
		} `xml:"ListGroupsResult>Groups>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	groups := make([]IAMGroup, 0, len(out.Groups))
	for _, g := range out.Groups {
		groups = append(groups, IAMGroup{Name: g.GroupName, ARN: g.Arn, Created: shortTime(g.CreateDate)})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

// GetIAMGroup reads one group with its members (GetGroup returns both).
func (b *backend) GetIAMGroup(ctx context.Context, name string) (*IAMGroup, error) {
	body, err := b.iam(ctx, "GetGroup", url.Values{"GroupName": {name}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Group struct {
			GroupName  string `xml:"GroupName"`
			Arn        string `xml:"Arn"`
			CreateDate string `xml:"CreateDate"`
		} `xml:"GetGroupResult>Group"`
		Users []struct {
			UserName string `xml:"UserName"`
		} `xml:"GetGroupResult>Users>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	g := &IAMGroup{Name: out.Group.GroupName, ARN: out.Group.Arn, Created: shortTime(out.Group.CreateDate)}
	for _, u := range out.Users {
		g.Members = append(g.Members, u.UserName)
	}
	sort.Strings(g.Members)
	return g, nil
}

func (b *backend) CreateIAMGroup(ctx context.Context, name string) error {
	_, err := b.iam(ctx, "CreateGroup", url.Values{"GroupName": {name}})
	return err
}

func (b *backend) DeleteIAMGroup(ctx context.Context, name string) error {
	_, err := b.iam(ctx, "DeleteGroup", url.Values{"GroupName": {name}})
	return err
}

// RenameIAMGroup is UpdateGroup's useful half locally.
func (b *backend) RenameIAMGroup(ctx context.Context, name, newName string) error {
	_, err := b.iam(ctx, "UpdateGroup", url.Values{"GroupName": {name}, "NewGroupName": {newName}})
	return err
}

func (b *backend) AddUserToGroup(ctx context.Context, group, user string) error {
	_, err := b.iam(ctx, "AddUserToGroup", url.Values{"GroupName": {group}, "UserName": {user}})
	return err
}

func (b *backend) RemoveUserFromGroup(ctx context.Context, group, user string) error {
	_, err := b.iam(ctx, "RemoveUserFromGroup", url.Values{"GroupName": {group}, "UserName": {user}})
	return err
}

// GroupsForUser answers the membership question from the user's side.
func (b *backend) GroupsForUser(ctx context.Context, user string) ([]string, error) {
	body, err := b.iam(ctx, "ListGroupsForUser", url.Values{"UserName": {user}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Groups []struct {
			GroupName string `xml:"GroupName"`
		} `xml:"ListGroupsForUserResult>Groups>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	var names []string
	for _, g := range out.Groups {
		names = append(names, g.GroupName)
	}
	sort.Strings(names)
	return names, nil
}

// InstanceProfile is one instance profile with the roles it carries.
type InstanceProfile struct {
	Name, ARN, Created string
	Roles              []string
}

func decodeProfiles(body []byte, path string) ([]InstanceProfile, error) {
	// Both listing shapes share the member element; only the wrapper differs,
	// so the caller names the result path and this decodes the members.
	type profWire struct {
		InstanceProfileName string `xml:"InstanceProfileName"`
		Arn                 string `xml:"Arn"`
		CreateDate          string `xml:"CreateDate"`
		Roles               []struct {
			RoleName string `xml:"RoleName"`
		} `xml:"Roles>member"`
	}
	var la struct {
		A []profWire `xml:"ListInstanceProfilesResult>InstanceProfiles>member"`
		B []profWire `xml:"ListInstanceProfilesForRoleResult>InstanceProfiles>member"`
	}
	if err := xml.Unmarshal(body, &la); err != nil {
		return nil, err
	}
	wires := la.A
	if path == "role" {
		wires = la.B
	}
	profiles := make([]InstanceProfile, 0, len(wires))
	for _, p := range wires {
		ip := InstanceProfile{Name: p.InstanceProfileName, ARN: p.Arn, Created: shortTime(p.CreateDate)}
		for _, r := range p.Roles {
			ip.Roles = append(ip.Roles, r.RoleName)
		}
		profiles = append(profiles, ip)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

func (b *backend) ListInstanceProfiles(ctx context.Context) ([]InstanceProfile, error) {
	body, err := b.iam(ctx, "ListInstanceProfiles", nil)
	if err != nil {
		return nil, err
	}
	return decodeProfiles(body, "all")
}

func (b *backend) ProfilesForRole(ctx context.Context, role string) ([]InstanceProfile, error) {
	body, err := b.iam(ctx, "ListInstanceProfilesForRole", url.Values{"RoleName": {role}})
	if err != nil {
		return nil, err
	}
	return decodeProfiles(body, "role")
}

func (b *backend) CreateInstanceProfile(ctx context.Context, name string) error {
	_, err := b.iam(ctx, "CreateInstanceProfile", url.Values{"InstanceProfileName": {name}})
	return err
}

func (b *backend) DeleteInstanceProfile(ctx context.Context, name string) error {
	_, err := b.iam(ctx, "DeleteInstanceProfile", url.Values{"InstanceProfileName": {name}})
	return err
}

// GetInstanceProfile reads one profile — the existence check the add-role
// form's error path leans on.
func (b *backend) GetInstanceProfile(ctx context.Context, name string) (*InstanceProfile, error) {
	body, err := b.iam(ctx, "GetInstanceProfile", url.Values{"InstanceProfileName": {name}})
	if err != nil {
		return nil, err
	}
	var out struct {
		P struct {
			InstanceProfileName string `xml:"InstanceProfileName"`
			Arn                 string `xml:"Arn"`
			CreateDate          string `xml:"CreateDate"`
			Roles               []struct {
				RoleName string `xml:"RoleName"`
			} `xml:"Roles>member"`
		} `xml:"GetInstanceProfileResult>InstanceProfile"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	ip := &InstanceProfile{Name: out.P.InstanceProfileName, ARN: out.P.Arn, Created: shortTime(out.P.CreateDate)}
	for _, r := range out.P.Roles {
		ip.Roles = append(ip.Roles, r.RoleName)
	}
	return ip, nil
}

func (b *backend) AddRoleToProfile(ctx context.Context, profile, role string) error {
	_, err := b.iam(ctx, "AddRoleToInstanceProfile", url.Values{"InstanceProfileName": {profile}, "RoleName": {role}})
	return err
}

func (b *backend) RemoveRoleFromProfile(ctx context.Context, profile, role string) error {
	_, err := b.iam(ctx, "RemoveRoleFromInstanceProfile", url.Values{"InstanceProfileName": {profile}, "RoleName": {role}})
	return err
}

// ---- entity updates the create forms never offered ----

// RenamePrincipal renames a user or group-of-one-name; roles cannot be
// renamed in IAM, matching AWS (UpdateUser carries NewUserName).
func (b *backend) RenameUser(ctx context.Context, name, newName string) error {
	_, err := b.iam(ctx, "UpdateUser", url.Values{"UserName": {name}, "NewUserName": {newName}})
	return err
}

// UpdateRoleMeta writes a role's description and/or session duration
// (UpdateRole); UpdateRoleDescriptionOnly uses the older single-field op the
// SDK still ships.
func (b *backend) UpdateRoleMeta(ctx context.Context, name, description string, maxSession int) error {
	v := url.Values{"RoleName": {name}, "Description": {description}}
	if maxSession > 0 {
		v.Set("MaxSessionDuration", strconv.Itoa(maxSession))
	}
	_, err := b.iam(ctx, "UpdateRole", v)
	return err
}

func (b *backend) UpdateRoleDescriptionOnly(ctx context.Context, name, description string) error {
	_, err := b.iam(ctx, "UpdateRoleDescription", url.Values{"RoleName": {name}, "Description": {description}})
	return err
}

// UpdateTrustPolicy replaces a role's assume-role document
// (UpdateAssumeRolePolicy) — the study-3 leftover: the trust was set at
// create and frozen ever after.
func (b *backend) UpdateTrustPolicy(ctx context.Context, role, document string) error {
	_, err := b.iam(ctx, "UpdateAssumeRolePolicy", url.Values{"RoleName": {role}, "PolicyDocument": {document}})
	return err
}

// SetAccessKeyActive flips a key between Active and Inactive (UpdateAccessKey)
// — the revoke-without-deleting step every rotation runbook has.
func (b *backend) SetAccessKeyActive(ctx context.Context, user, keyID string, active bool) error {
	status := "Inactive"
	if active {
		status = "Active"
	}
	_, err := b.iam(ctx, "UpdateAccessKey", url.Values{"UserName": {user}, "AccessKeyId": {keyID}, "Status": {status}})
	return err
}

// AccessKeyLastUsed reads when a key last authenticated something
// (GetAccessKeyLastUsed) — "" means never.
func (b *backend) AccessKeyLastUsed(ctx context.Context, keyID string) string {
	body, err := b.iam(ctx, "GetAccessKeyLastUsed", url.Values{"AccessKeyId": {keyID}})
	if err != nil {
		return ""
	}
	var out struct {
		LastUsedDate string `xml:"GetAccessKeyLastUsedResult>AccessKeyLastUsed>LastUsedDate"`
	}
	if xml.Unmarshal(body, &out) != nil || out.LastUsedDate == "" {
		return ""
	}
	return shortTime(out.LastUsedDate)
}

// GetIAMUser / GetIAMRole are the singular reads (GetUser / GetRole); the
// principal page had been filtering the list.
func (b *backend) GetIAMUser(ctx context.Context, name string) error {
	_, err := b.iam(ctx, "GetUser", url.Values{"UserName": {name}})
	return err
}

func (b *backend) GetIAMRole(ctx context.Context, name string) (description string, maxSession int, err error) {
	body, err := b.iam(ctx, "GetRole", url.Values{"RoleName": {name}})
	if err != nil {
		return "", 0, err
	}
	var out struct {
		Description        string `xml:"GetRoleResult>Role>Description"`
		MaxSessionDuration int    `xml:"GetRoleResult>Role>MaxSessionDuration"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", 0, err
	}
	return out.Description, out.MaxSessionDuration, nil
}

// CreateServiceLinkedRole / DeleteServiceLinkedRole: the role variant a
// service owns; the console offers it beside the ordinary kinds.
func (b *backend) CreateServiceLinkedRole(ctx context.Context, service string) error {
	_, err := b.iam(ctx, "CreateServiceLinkedRole", url.Values{"AWSServiceName": {service}})
	return err
}

func (b *backend) DeleteServiceLinkedRole(ctx context.Context, role string) error {
	_, err := b.iam(ctx, "DeleteServiceLinkedRole", url.Values{"RoleName": {role}})
	return err
}

// ---- policy versions, entity tags, custom simulation: wave two ----

// PolicyVersion is one version of a managed policy.
type PolicyVersion struct {
	ID, Created string
	Default     bool
}

func (b *backend) PolicyVersions(ctx context.Context, arn string) ([]PolicyVersion, error) {
	body, err := b.iam(ctx, "ListPolicyVersions", url.Values{"PolicyArn": {arn}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Versions []struct {
			VersionId        string `xml:"VersionId"`
			IsDefaultVersion bool   `xml:"IsDefaultVersion"`
			CreateDate       string `xml:"CreateDate"`
		} `xml:"ListPolicyVersionsResult>Versions>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	versions := make([]PolicyVersion, 0, len(out.Versions))
	for _, v := range out.Versions {
		versions = append(versions, PolicyVersion{ID: v.VersionId, Default: v.IsDefaultVersion, Created: shortTime(v.CreateDate)})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].ID > versions[j].ID })
	return versions, nil
}

// CreatePolicyVersion publishes an edited document as the next version,
// optionally making it the default in the same call.
func (b *backend) CreatePolicyVersion(ctx context.Context, arn, document string, setDefault bool) error {
	v := url.Values{"PolicyArn": {arn}, "PolicyDocument": {document}}
	if setDefault {
		v.Set("SetAsDefault", "true")
	}
	_, err := b.iam(ctx, "CreatePolicyVersion", v)
	return err
}

func (b *backend) DeletePolicyVersion(ctx context.Context, arn, versionID string) error {
	_, err := b.iam(ctx, "DeletePolicyVersion", url.Values{"PolicyArn": {arn}, "VersionId": {versionID}})
	return err
}

// SetDefaultPolicyVersion is the rollback: an old version becomes what every
// attachment evaluates from the next request on.
func (b *backend) SetDefaultPolicyVersion(ctx context.Context, arn, versionID string) error {
	_, err := b.iam(ctx, "SetDefaultPolicyVersion", url.Values{"PolicyArn": {arn}, "VersionId": {versionID}})
	return err
}

// PolicyEntities names everything a managed policy is attached to
// (ListEntitiesForPolicy) — the blast radius before an edit or delete.
type PolicyEntities struct {
	Users, Roles, Groups []string
}

func (b *backend) PolicyEntities(ctx context.Context, arn string) (*PolicyEntities, error) {
	body, err := b.iam(ctx, "ListEntitiesForPolicy", url.Values{"PolicyArn": {arn}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Users []struct {
			UserName string `xml:"UserName"`
		} `xml:"ListEntitiesForPolicyResult>PolicyUsers>member"`
		Roles []struct {
			RoleName string `xml:"RoleName"`
		} `xml:"ListEntitiesForPolicyResult>PolicyRoles>member"`
		Groups []struct {
			GroupName string `xml:"GroupName"`
		} `xml:"ListEntitiesForPolicyResult>PolicyGroups>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	e := &PolicyEntities{}
	for _, u := range out.Users {
		e.Users = append(e.Users, u.UserName)
	}
	for _, r := range out.Roles {
		e.Roles = append(e.Roles, r.RoleName)
	}
	for _, g := range out.Groups {
		e.Groups = append(e.Groups, g.GroupName)
	}
	return e, nil
}

// iamTagOps maps a console tag-kind onto its op family, each name spelled
// out in full — composed names would call the right actions and be invisible
// to the coverage ratchet's grep.
var iamTagOps = map[string]struct{ list, tag, untag, field string }{
	"user":    {"ListUserTags", "TagUser", "UntagUser", "UserName"},
	"role":    {"ListRoleTags", "TagRole", "UntagRole", "RoleName"},
	"policy":  {"ListPolicyTags", "TagPolicy", "UntagPolicy", "PolicyArn"},
	"profile": {"ListInstanceProfileTags", "TagInstanceProfile", "UntagInstanceProfile", "InstanceProfileName"},
}

// IAMTags / SetIAMTag / RemoveIAMTag are ListXxxTags / TagXxx / UntagXxx for
// users, roles, managed policies and instance profiles.
func (b *backend) IAMTags(ctx context.Context, kind, id string) ([]KV, error) {
	p, ok := iamTagOps[kind]
	if !ok {
		return nil, fmt.Errorf("unknown iam tag kind %q", kind)
	}
	body, err := b.iam(ctx, p.list, url.Values{p.field: {id}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tags []struct {
			Key   string `xml:"Key"`
			Value string `xml:"Value"`
		} `xml:"Tags>member"`
	}
	// The wrapper element name varies by op; a lax decode over the whole
	// document keys off the unique Tags>member path instead.
	type anyWrap struct {
		Tags []struct {
			Key   string `xml:"Key"`
			Value string `xml:"Value"`
		} `xml:"Tags>member"`
	}
	var lu struct {
		U anyWrap `xml:"ListUserTagsResult"`
		R anyWrap `xml:"ListRoleTagsResult"`
		P anyWrap `xml:"ListPolicyTagsResult"`
		I anyWrap `xml:"ListInstanceProfileTagsResult"`
	}
	if err := xml.Unmarshal(body, &lu); err != nil {
		return nil, err
	}
	out.Tags = append(out.Tags, lu.U.Tags...)
	out.Tags = append(out.Tags, lu.R.Tags...)
	out.Tags = append(out.Tags, lu.P.Tags...)
	out.Tags = append(out.Tags, lu.I.Tags...)
	var tags []KV
	for _, t := range out.Tags {
		tags = append(tags, KV{K: t.Key, V: t.Value})
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].K < tags[j].K })
	return tags, nil
}

func (b *backend) SetIAMTag(ctx context.Context, kind, id, key, value string) error {
	p, ok := iamTagOps[kind]
	if !ok {
		return fmt.Errorf("unknown iam tag kind %q", kind)
	}
	v := url.Values{p.field: {id}}
	v.Set("Tags.member.1.Key", key)
	v.Set("Tags.member.1.Value", value)
	_, err := b.iam(ctx, p.tag, v)
	return err
}

func (b *backend) RemoveIAMTag(ctx context.Context, kind, id, key string) error {
	p, ok := iamTagOps[kind]
	if !ok {
		return fmt.Errorf("unknown iam tag kind %q", kind)
	}
	v := url.Values{p.field: {id}}
	v.Set("TagKeys.member.1", key)
	_, err := b.iam(ctx, p.untag, v)
	return err
}

// SimulateCustom evaluates a DRAFT policy document — one that exists nowhere
// yet — against actions and a resource (SimulateCustomPolicy). Checking a
// policy before creating it is the one thing the principal simulator cannot
// do.
func (b *backend) SimulateCustom(ctx context.Context, document string, actions []string, resource string) ([]SimResult, error) {
	v := url.Values{"PolicyInputList.member.1": {document}}
	for i, a := range actions {
		v.Set(fmt.Sprintf("ActionNames.member.%d", i+1), a)
	}
	if resource != "" {
		v.Set("ResourceArns.member.1", resource)
	}
	body, err := b.iam(ctx, "SimulateCustomPolicy", v)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct {
			Action   string `xml:"EvalActionName"`
			Resource string `xml:"EvalResourceName"`
			Decision string `xml:"EvalDecision"`
		} `xml:"SimulateCustomPolicyResult>EvaluationResults>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	res := make([]SimResult, 0, len(out.Results))
	for _, r := range out.Results {
		res = append(res, SimResult{
			Action: r.Action, Resource: r.Resource, Decision: r.Decision,
			Allowed: strings.EqualFold(r.Decision, "allowed"),
		})
	}
	return res, nil
}

// ContextKeysForCustomPolicy names the condition keys a draft document
// references (GetContextKeysForCustomPolicy) — what the simulation would need
// values for.
func (b *backend) ContextKeysForCustomPolicy(ctx context.Context, document string) ([]string, error) {
	body, err := b.iam(ctx, "GetContextKeysForCustomPolicy",
		url.Values{"PolicyInputList.member.1": {document}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Keys []string `xml:"GetContextKeysForCustomPolicyResult>ContextKeyNames>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	sort.Strings(out.Keys)
	return out.Keys, nil
}

// ContextKeysForPrincipal is the same question asked of everything already
// attached to a principal (GetContextKeysForPrincipalPolicy).
func (b *backend) ContextKeysForPrincipal(ctx context.Context, principalARN string) ([]string, error) {
	body, err := b.iam(ctx, "GetContextKeysForPrincipalPolicy",
		url.Values{"PolicySourceArn": {principalARN}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Keys []string `xml:"GetContextKeysForPrincipalPolicyResult>ContextKeyNames>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	sort.Strings(out.Keys)
	return out.Keys, nil
}

// ---- the account itself: wave three ----

// IAMSummary is GetAccountSummary's map, reduced to the rows worth showing.
type IAMSummary struct {
	Users, Roles, Groups, Policies int
}

func (b *backend) IAMAccountSummary(ctx context.Context) (*IAMSummary, error) {
	body, err := b.iam(ctx, "GetAccountSummary", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Entries []struct {
			Key   string `xml:"key"`
			Value int    `xml:"value"`
		} `xml:"GetAccountSummaryResult>SummaryMap>entry"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	s := &IAMSummary{}
	for _, e := range out.Entries {
		switch e.Key {
		case "Users":
			s.Users = e.Value
		case "Roles":
			s.Roles = e.Value
		case "Groups":
			s.Groups = e.Value
		case "Policies":
			s.Policies = e.Value
		}
	}
	return s, nil
}

// AccountAliases lists the account's aliases (at most one, as in AWS).
func (b *backend) AccountAliases(ctx context.Context) ([]string, error) {
	body, err := b.iam(ctx, "ListAccountAliases", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Aliases []string `xml:"ListAccountAliasesResult>AccountAliases>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Aliases, nil
}

func (b *backend) CreateAccountAlias(ctx context.Context, alias string) error {
	_, err := b.iam(ctx, "CreateAccountAlias", url.Values{"AccountAlias": {alias}})
	return err
}

func (b *backend) DeleteAccountAlias(ctx context.Context, alias string) error {
	_, err := b.iam(ctx, "DeleteAccountAlias", url.Values{"AccountAlias": {alias}})
	return err
}

// PasswordPolicy is GetAccountPasswordPolicy, rendered read-only — nothing
// local logs in with a password, so the policy is a fact, not a control.
func (b *backend) PasswordPolicy(ctx context.Context) (string, error) {
	body, err := b.iam(ctx, "GetAccountPasswordPolicy", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		MinimumPasswordLength int  `xml:"GetAccountPasswordPolicyResult>PasswordPolicy>MinimumPasswordLength"`
		RequireSymbols        bool `xml:"GetAccountPasswordPolicyResult>PasswordPolicy>RequireSymbols"`
		RequireNumbers        bool `xml:"GetAccountPasswordPolicyResult>PasswordPolicy>RequireNumbers"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", err
	}
	desc := fmt.Sprintf("minimum length %d", out.MinimumPasswordLength)
	if out.RequireSymbols {
		desc += ", symbols required"
	}
	if out.RequireNumbers {
		desc += ", numbers required"
	}
	return desc, nil
}

// AuthorizationDetails is the whole IAM database in one read
// (GetAccountAuthorizationDetails) — the audit export, pretty-printed.
func (b *backend) AuthorizationDetails(ctx context.Context) (string, error) {
	body, err := b.iam(ctx, "GetAccountAuthorizationDetails", nil)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
