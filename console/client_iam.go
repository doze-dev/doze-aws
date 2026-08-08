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
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
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
}

func (b *backend) iam(ctx context.Context, action string, extra url.Values) ([]byte, error) {
	v := url.Values{"Action": {action}, "Version": {"2010-05-08"}}
	for k, vals := range extra {
		v[k] = vals
	}
	return b.queryXML(ctx, v)
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
	if kind == "role" {
		action, key = "ListAttachedRolePolicies", "RoleName"
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
	var members []member
	if kind == "role" {
		var out struct {
			M []member `xml:"ListAttachedRolePoliciesResult>AttachedPolicies>member"`
		}
		if err := xml.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		members = out.M
	} else {
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
			Decision: e.Decision, Known: e.Known, Count: e.Count,
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
		} `xml:"SimulatePrincipalPolicyResult>EvaluationResults>member"`
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

// awsMessage reduces an AWS error envelope to the message it carries. The
// services answer with a usable sentence; the XML around it is noise.
func awsMessage(err error) error {
	var e *apiErr
	if !errors.As(err, &e) {
		return err
	}
	var out struct {
		Message string `xml:"Error>Message"`
	}
	if xml.Unmarshal([]byte(e.body), &out) == nil && out.Message != "" {
		return fmt.Errorf("%s", out.Message)
	}
	return err
}

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
	if kind == "role" {
		action, key = "AttachRolePolicy", "RoleName"
	}
	_, err := b.iam(ctx, action, url.Values{key: {name}, "PolicyArn": {policyARN}})
	return awsMessage(err)
}

func (b *backend) DetachPolicy(ctx context.Context, kind, name, policyARN string) error {
	action, key := "DetachUserPolicy", "UserName"
	if kind == "role" {
		action, key = "DetachRolePolicy", "RoleName"
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
