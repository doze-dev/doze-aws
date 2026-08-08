package iam

// The action dispatch table and the CRUD surface: users, groups, roles,
// managed and inline policies, access keys, instance profiles and the account.
//
// Query/XML result shapes are declared as small view structs rather than
// assembled ad hoc, because the SDKs are strict about element names and a typo
// surfaces as a silently empty field rather than an error.

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsquery"
)

// params wraps the decoded Query form.
type params struct{ url.Values }

func (p params) str(key string) string { return p.Get(key) }

func (p params) int(key string) int {
	n, _ := strconv.Atoi(p.Get(key))
	return n
}

// tags reads a Tags.member.N.Key/Value list.
func (p params) tags() map[string]string {
	out := awsquery.PairMap(p.Values, "Tags.member", "Key", "Value")
	if len(out) == 0 {
		return nil
	}
	return out
}

// members reads a Foo.member.N list.
func (p params) members(prefix string) []string {
	return awsquery.Members(p.Values, prefix, false)
}

var handlers = map[string]handler{
	// Users.
	"CreateUser":   hCreateUser,
	"GetUser":      hGetUser,
	"UpdateUser":   hUpdateUser,
	"DeleteUser":   hDeleteUser,
	"ListUsers":    hListUsers,
	"TagUser":      hTagUser,
	"UntagUser":    hUntagUser,
	"ListUserTags": hListUserTags,

	// Groups.
	"CreateGroup":         hCreateGroup,
	"GetGroup":            hGetGroup,
	"UpdateGroup":         hUpdateGroup,
	"DeleteGroup":         hDeleteGroup,
	"ListGroups":          hListGroups,
	"AddUserToGroup":      hAddUserToGroup,
	"RemoveUserFromGroup": hRemoveUserFromGroup,
	"ListGroupsForUser":   hListGroupsForUser,

	// Roles.
	"CreateRole":              hCreateRole,
	"GetRole":                 hGetRole,
	"UpdateRole":              hUpdateRole,
	"UpdateRoleDescription":   hUpdateRoleDescription,
	"DeleteRole":              hDeleteRole,
	"ListRoles":               hListRoles,
	"UpdateAssumeRolePolicy":  hUpdateAssumeRolePolicy,
	"TagRole":                 hTagRole,
	"UntagRole":               hUntagRole,
	"ListRoleTags":            hListRoleTags,
	"CreateServiceLinkedRole": hCreateServiceLinkedRole,
	"DeleteServiceLinkedRole": hDeleteServiceLinkedRole,

	// Managed policies.
	"CreatePolicy":            hCreatePolicy,
	"GetPolicy":               hGetPolicy,
	"DeletePolicy":            hDeletePolicy,
	"ListPolicies":            hListPolicies,
	"CreatePolicyVersion":     hCreatePolicyVersion,
	"GetPolicyVersion":        hGetPolicyVersion,
	"DeletePolicyVersion":     hDeletePolicyVersion,
	"ListPolicyVersions":      hListPolicyVersions,
	"SetDefaultPolicyVersion": hSetDefaultPolicyVersion,
	"TagPolicy":               hTagPolicy,
	"UntagPolicy":             hUntagPolicy,
	"ListPolicyTags":          hListPolicyTags,

	// Attachment.
	"AttachUserPolicy":          hAttachUserPolicy,
	"DetachUserPolicy":          hDetachUserPolicy,
	"AttachGroupPolicy":         hAttachGroupPolicy,
	"DetachGroupPolicy":         hDetachGroupPolicy,
	"AttachRolePolicy":          hAttachRolePolicy,
	"DetachRolePolicy":          hDetachRolePolicy,
	"ListAttachedUserPolicies":  hListAttachedUserPolicies,
	"ListAttachedGroupPolicies": hListAttachedGroupPolicies,
	"ListAttachedRolePolicies":  hListAttachedRolePolicies,
	"ListEntitiesForPolicy":     hListEntitiesForPolicy,

	// Inline policies.
	"PutUserPolicy":     hPutUserPolicy,
	"GetUserPolicy":     hGetUserPolicy,
	"DeleteUserPolicy":  hDeleteUserPolicy,
	"ListUserPolicies":  hListUserPolicies,
	"PutGroupPolicy":    hPutGroupPolicy,
	"GetGroupPolicy":    hGetGroupPolicy,
	"DeleteGroupPolicy": hDeleteGroupPolicy,
	"ListGroupPolicies": hListGroupPolicies,
	"PutRolePolicy":     hPutRolePolicy,
	"GetRolePolicy":     hGetRolePolicy,
	"DeleteRolePolicy":  hDeleteRolePolicy,
	"ListRolePolicies":  hListRolePolicies,

	// Permissions boundaries.
	"PutUserPermissionsBoundary":    hPutUserBoundary,
	"DeleteUserPermissionsBoundary": hDeleteUserBoundary,
	"PutRolePermissionsBoundary":    hPutRoleBoundary,
	"DeleteRolePermissionsBoundary": hDeleteRoleBoundary,

	// Access keys.
	"CreateAccessKey":      hCreateAccessKey,
	"ListAccessKeys":       hListAccessKeys,
	"UpdateAccessKey":      hUpdateAccessKey,
	"DeleteAccessKey":      hDeleteAccessKey,
	"GetAccessKeyLastUsed": hGetAccessKeyLastUsed,

	// Instance profiles.
	"CreateInstanceProfile":         hCreateInstanceProfile,
	"GetInstanceProfile":            hGetInstanceProfile,
	"DeleteInstanceProfile":         hDeleteInstanceProfile,
	"ListInstanceProfiles":          hListInstanceProfiles,
	"ListInstanceProfilesForRole":   hListInstanceProfilesForRole,
	"AddRoleToInstanceProfile":      hAddRoleToInstanceProfile,
	"RemoveRoleFromInstanceProfile": hRemoveRoleFromInstanceProfile,

	// Account.
	"CreateAccountAlias":             hCreateAccountAlias,
	"DeleteAccountAlias":             hDeleteAccountAlias,
	"ListAccountAliases":             hListAccountAliases,
	"GetAccountSummary":              hGetAccountSummary,
	"GetAccountPasswordPolicy":       hGetAccountPasswordPolicy,
	"GetAccountAuthorizationDetails": hGetAccountAuthorizationDetails,

	// Instance-profile tags.
	"TagInstanceProfile":      hTagInstanceProfile,
	"UntagInstanceProfile":    hUntagInstanceProfile,
	"ListInstanceProfileTags": hListInstanceProfileTags,

	// Simulation and policy analysis (simulate.go).
	"SimulateCustomPolicy":             hSimulateCustomPolicy,
	"SimulatePrincipalPolicy":          hSimulatePrincipalPolicy,
	"GetContextKeysForCustomPolicy":    hGetContextKeysForCustomPolicy,
	"GetContextKeysForPrincipalPolicy": hGetContextKeysForPrincipalPolicy,

	// doze extensions (authorize.go).
	"DozeGeneratePolicy": hDozeGeneratePolicy,
	"DozeAccessLog":      hDozeAccessLog,
}

// ---- view structs ----

type tagView struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func tagViews(tags map[string]string) []tagView {
	keys := sortedKeys(tags)
	out := make([]tagView, 0, len(keys))
	for _, k := range keys {
		out = append(out, tagView{Key: k, Value: tags[k]})
	}
	return out
}

type userView struct {
	Path       string    `xml:"Path"`
	UserName   string    `xml:"UserName"`
	UserId     string    `xml:"UserId"`
	Arn        string    `xml:"Arn"`
	CreateDate string    `xml:"CreateDate"`
	Tags       []tagView `xml:"Tags>member,omitempty"`
}

func viewUser(u *User) userView {
	return userView{
		Path: u.Path, UserName: u.Name, UserId: u.ID,
		Arn:        awsident.GlobalARN("iam", "user"+u.Path+u.Name),
		CreateDate: iso(u.Created), Tags: tagViews(u.Tags),
	}
}

type groupView struct {
	Path       string `xml:"Path"`
	GroupName  string `xml:"GroupName"`
	GroupId    string `xml:"GroupId"`
	Arn        string `xml:"Arn"`
	CreateDate string `xml:"CreateDate"`
}

func viewGroup(g *Group) groupView {
	return groupView{
		Path: g.Path, GroupName: g.Name, GroupId: g.ID,
		Arn:        awsident.GlobalARN("iam", "group"+g.Path+g.Name),
		CreateDate: iso(g.Created),
	}
}

type roleView struct {
	Path                     string    `xml:"Path"`
	RoleName                 string    `xml:"RoleName"`
	RoleId                   string    `xml:"RoleId"`
	Arn                      string    `xml:"Arn"`
	CreateDate               string    `xml:"CreateDate"`
	AssumeRolePolicyDocument string    `xml:"AssumeRolePolicyDocument,omitempty"`
	Description              string    `xml:"Description,omitempty"`
	MaxSessionDuration       int       `xml:"MaxSessionDuration,omitempty"`
	Tags                     []tagView `xml:"Tags>member,omitempty"`
}

func viewRole(r *Role) roleView {
	return roleView{
		Path: r.Path, RoleName: r.Name, RoleId: r.ID,
		Arn:        awsident.GlobalARN("iam", "role"+r.Path+r.Name),
		CreateDate: iso(r.Created),
		// AWS URL-encodes policy documents in Query responses; the SDKs decode
		// them, and skipping it produces XML the parsers choke on.
		AssumeRolePolicyDocument: url.QueryEscape(r.AssumeRolePolicy),
		Description:              r.Description,
		MaxSessionDuration:       r.MaxSessionDuration,
		Tags:                     tagViews(r.Tags),
	}
}

type policyView struct {
	PolicyName       string    `xml:"PolicyName"`
	PolicyId         string    `xml:"PolicyId"`
	Arn              string    `xml:"Arn"`
	Path             string    `xml:"Path"`
	DefaultVersionId string    `xml:"DefaultVersionId"`
	AttachmentCount  int       `xml:"AttachmentCount"`
	IsAttachable     bool      `xml:"IsAttachable"`
	Description      string    `xml:"Description,omitempty"`
	CreateDate       string    `xml:"CreateDate"`
	UpdateDate       string    `xml:"UpdateDate"`
	Tags             []tagView `xml:"Tags>member,omitempty"`
}

func viewPolicy(p *Policy, arn string) policyView {
	return policyView{
		PolicyName: p.Name, PolicyId: p.ID, Arn: arn, Path: p.Path,
		DefaultVersionId: p.DefaultVersion, AttachmentCount: p.AttachCount,
		IsAttachable: true, Description: p.Description,
		CreateDate: iso(p.Created), UpdateDate: iso(p.Updated),
		Tags: tagViews(p.Tags),
	}
}

type policyVersionView struct {
	Document         string `xml:"Document,omitempty"`
	VersionId        string `xml:"VersionId"`
	IsDefaultVersion bool   `xml:"IsDefaultVersion"`
	CreateDate       string `xml:"CreateDate"`
}

// attachedPolicyView is the shape ListAttached*Policies returns.
type attachedPolicyView struct {
	PolicyName string `xml:"PolicyName"`
	PolicyArn  string `xml:"PolicyArn"`
}

type accessKeyView struct {
	UserName        string `xml:"UserName"`
	AccessKeyId     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey,omitempty"`
	Status          string `xml:"Status"`
	CreateDate      string `xml:"CreateDate"`
}

type instanceProfileView struct {
	Path                string     `xml:"Path"`
	InstanceProfileName string     `xml:"InstanceProfileName"`
	InstanceProfileId   string     `xml:"InstanceProfileId"`
	Arn                 string     `xml:"Arn"`
	CreateDate          string     `xml:"CreateDate"`
	Roles               []roleView `xml:"Roles>member,omitempty"`
}

// ---- users ----

func hCreateUser(s *Server, p params) (any, *awshttp.APIError) {
	u, err := s.store.CreateUser(p.str("UserName"), p.str("Path"), p.tags())
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		User userView `xml:"User"`
	}{viewUser(u)}, nil
}

func hGetUser(s *Server, p params) (any, *awshttp.APIError) {
	name := p.str("UserName")
	if name == "" {
		// GetUser with no UserName describes the caller. Locally that is the
		// fixed root identity.
		return struct {
			User userView `xml:"User"`
		}{userView{
			Path: "/", UserName: "root", UserId: "AIDADOZEROOTACCOUNT00",
			Arn: awsident.GlobalARN("iam", "root"), CreateDate: iso(0),
		}}, nil
	}
	u, err := s.store.GetUser(name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		User userView `xml:"User"`
	}{viewUser(u)}, nil
}

func hUpdateUser(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdateUser(p.str("UserName"), func(u *User) error {
		if v := p.str("NewUserName"); v != "" {
			u.Name = v
		}
		if v := p.str("NewPath"); v != "" {
			u.Path = normPath(v)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hDeleteUser(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteUser(p.str("UserName")))
}

func hListUsers(s *Server, p params) (any, *awshttp.APIError) {
	users, err := s.store.ListUsers(p.str("PathPrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]userView, 0, len(users))
	for i := range users {
		views = append(views, viewUser(&users[i]))
	}
	return struct {
		Users       []userView `xml:"Users>member"`
		IsTruncated bool       `xml:"IsTruncated"`
	}{views, false}, nil
}

func hTagUser(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdateUser(p.str("UserName"), func(u *User) error {
		u.Tags = mergeTags(u.Tags, p.tags())
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hUntagUser(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdateUser(p.str("UserName"), func(u *User) error {
		for _, k := range p.members("TagKeys") {
			delete(u.Tags, k)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hListUserTags(s *Server, p params) (any, *awshttp.APIError) {
	u, err := s.store.GetUser(p.str("UserName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Tags        []tagView `xml:"Tags>member"`
		IsTruncated bool      `xml:"IsTruncated"`
	}{tagViews(u.Tags), false}, nil
}

// ---- groups ----

func hCreateGroup(s *Server, p params) (any, *awshttp.APIError) {
	g, err := s.store.CreateGroup(p.str("GroupName"), p.str("Path"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Group groupView `xml:"Group"`
	}{viewGroup(g)}, nil
}

func hGetGroup(s *Server, p params) (any, *awshttp.APIError) {
	g, err := s.store.GetGroup(p.str("GroupName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// GetGroup also returns the group's members.
	users, _ := s.store.ListUsers("")
	var members []userView
	for i := range users {
		if containsString(users[i].Groups, g.Name) {
			members = append(members, viewUser(&users[i]))
		}
	}
	return struct {
		Group       groupView  `xml:"Group"`
		Users       []userView `xml:"Users>member"`
		IsTruncated bool       `xml:"IsTruncated"`
	}{viewGroup(g), members, false}, nil
}

func hUpdateGroup(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdateGroup(p.str("GroupName"), func(g *Group) error {
		if v := p.str("NewGroupName"); v != "" {
			g.Name = v
		}
		if v := p.str("NewPath"); v != "" {
			g.Path = normPath(v)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hDeleteGroup(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteGroup(p.str("GroupName")))
}

func hListGroups(s *Server, p params) (any, *awshttp.APIError) {
	groups, err := s.store.ListGroups(p.str("PathPrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]groupView, 0, len(groups))
	for i := range groups {
		views = append(views, viewGroup(&groups[i]))
	}
	return struct {
		Groups      []groupView `xml:"Groups>member"`
		IsTruncated bool        `xml:"IsTruncated"`
	}{views, false}, nil
}

func hAddUserToGroup(s *Server, p params) (any, *awshttp.APIError) {
	group := p.str("GroupName")
	if _, err := s.store.GetGroup(group); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	_, err := s.store.UpdateUser(p.str("UserName"), func(u *User) error {
		if !containsString(u.Groups, group) {
			u.Groups = append(u.Groups, group)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hRemoveUserFromGroup(s *Server, p params) (any, *awshttp.APIError) {
	group := p.str("GroupName")
	_, err := s.store.UpdateUser(p.str("UserName"), func(u *User) error {
		u.Groups = removeString(u.Groups, group)
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hListGroupsForUser(s *Server, p params) (any, *awshttp.APIError) {
	u, err := s.store.GetUser(p.str("UserName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	var views []groupView
	for _, name := range u.Groups {
		if g, err := s.store.GetGroup(name); err == nil {
			views = append(views, viewGroup(g))
		}
	}
	return struct {
		Groups      []groupView `xml:"Groups>member"`
		IsTruncated bool        `xml:"IsTruncated"`
	}{views, false}, nil
}

// ---- roles ----

func hCreateRole(s *Server, p params) (any, *awshttp.APIError) {
	// The trust policy arrives URL-encoded from most SDKs.
	trust := decodeDocument(p.str("AssumeRolePolicyDocument"))
	r, err := s.store.CreateRole(p.str("RoleName"), p.str("Path"), trust,
		p.str("Description"), p.int("MaxSessionDuration"), p.tags())
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Role roleView `xml:"Role"`
	}{viewRole(r)}, nil
}

func hGetRole(s *Server, p params) (any, *awshttp.APIError) {
	r, err := s.store.GetRole(p.str("RoleName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Role roleView `xml:"Role"`
	}{viewRole(r)}, nil
}

func hUpdateRole(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdateRole(p.str("RoleName"), func(r *Role) error {
		if v := p.str("Description"); v != "" {
			r.Description = v
		}
		if v := p.int("MaxSessionDuration"); v != 0 {
			r.MaxSessionDuration = v
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hUpdateRoleDescription(s *Server, p params) (any, *awshttp.APIError) {
	r, err := s.store.UpdateRole(p.str("RoleName"), func(r *Role) error {
		r.Description = p.str("Description")
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Role roleView `xml:"Role"`
	}{viewRole(r)}, nil
}

func hDeleteRole(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteRole(p.str("RoleName")))
}

func hListRoles(s *Server, p params) (any, *awshttp.APIError) {
	roles, err := s.store.ListRoles(p.str("PathPrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]roleView, 0, len(roles))
	for i := range roles {
		views = append(views, viewRole(&roles[i]))
	}
	return struct {
		Roles       []roleView `xml:"Roles>member"`
		IsTruncated bool       `xml:"IsTruncated"`
	}{views, false}, nil
}

func hUpdateAssumeRolePolicy(s *Server, p params) (any, *awshttp.APIError) {
	doc := decodeDocument(p.str("PolicyDocument"))
	if _, err := ParsePolicy(doc); err != nil {
		return nil, errMalformedPolicy("PolicyDocument: %v", err)
	}
	_, err := s.store.UpdateRole(p.str("RoleName"), func(r *Role) error {
		r.AssumeRolePolicy = doc
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hTagRole(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdateRole(p.str("RoleName"), func(r *Role) error {
		r.Tags = mergeTags(r.Tags, p.tags())
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hUntagRole(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdateRole(p.str("RoleName"), func(r *Role) error {
		for _, k := range p.members("TagKeys") {
			delete(r.Tags, k)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hListRoleTags(s *Server, p params) (any, *awshttp.APIError) {
	r, err := s.store.GetRole(p.str("RoleName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Tags        []tagView `xml:"Tags>member"`
		IsTruncated bool      `xml:"IsTruncated"`
	}{tagViews(r.Tags), false}, nil
}

// hCreateServiceLinkedRole synthesizes the role AWS would create on a service's
// behalf. CloudFormation and several SDKs call it opportunistically.
func hCreateServiceLinkedRole(s *Server, p params) (any, *awshttp.APIError) {
	svc := p.str("AWSServiceName")
	if svc == "" {
		return nil, errValidation("AWSServiceName is required")
	}
	short := svc
	if i := strings.Index(short, "."); i > 0 {
		short = short[:i]
	}
	name := "AWSServiceRoleFor" + strings.ToUpper(short[:1]) + short[1:]
	if suffix := p.str("CustomSuffix"); suffix != "" {
		name += "_" + suffix
	}
	trust := jsonDoc("Allow", "Action", []string{"sts:AssumeRole"}, []string{"*"})
	r, err := s.store.CreateRole(name, "/aws-service-role/"+svc+"/", trust, p.str("Description"), 3600, nil)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Role roleView `xml:"Role"`
	}{viewRole(r)}, nil
}

func hDeleteServiceLinkedRole(s *Server, p params) (any, *awshttp.APIError) {
	if err := s.store.DeleteRole(p.str("RoleName")); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// AWS deletes service-linked roles asynchronously and hands back a task id.
	return struct {
		DeletionTaskId string `xml:"DeletionTaskId"`
	}{"task/" + p.str("RoleName") + "/00000000"}, nil
}

// ---- helpers ----

func mergeTags(existing, add map[string]string) map[string]string {
	if len(add) == 0 {
		return existing
	}
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range add {
		existing[k] = v
	}
	return existing
}

// decodeDocument accepts a policy document either raw or URL-encoded, which is
// how the two SDK generations differ.
func decodeDocument(v string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(v), "{") {
		return v
	}
	if decoded, err := url.QueryUnescape(v); err == nil {
		return decoded
	}
	return v
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func iso(unix int64) string {
	return awshttp.ISO8601(timeUnix(unix))
}
