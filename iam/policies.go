package iam

// Managed-policy lifecycle, attachment, inline policies and permissions
// boundaries. The attach/detach and inline handlers are written once against
// the shared Principal and specialised by attachTarget, which is why users,
// groups and roles behave identically here — as they do in AWS.

import (
	"net/url"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// ---- managed policies ----

func hCreatePolicy(s *Server, p params) (any, *awshttp.APIError) {
	pol, err := s.store.CreatePolicy(
		p.str("PolicyName"), p.str("Path"),
		decodeDocument(p.str("PolicyDocument")),
		p.str("Description"), p.tags())
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Policy policyView `xml:"Policy"`
	}{viewPolicy(pol, pol.ARN())}, nil
}

func hGetPolicy(s *Server, p params) (any, *awshttp.APIError) {
	arn := p.str("PolicyArn")
	pol, err := s.store.GetPolicy(arn)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Policy policyView `xml:"Policy"`
	}{viewPolicy(pol, arn)}, nil
}

func hDeletePolicy(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeletePolicy(p.str("PolicyArn")))
}

func hListPolicies(s *Server, p params) (any, *awshttp.APIError) {
	pols, err := s.store.ListPolicies(p.str("PathPrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]policyView, 0, len(pols))
	for i := range pols {
		// OnlyAttached mirrors AWS's filter; CloudFormation uses it when
		// reconciling.
		if p.str("OnlyAttached") == "true" && pols[i].AttachCount == 0 {
			continue
		}
		views = append(views, viewPolicy(&pols[i], pols[i].ARN()))
	}
	return struct {
		Policies    []policyView `xml:"Policies>member"`
		IsTruncated bool         `xml:"IsTruncated"`
	}{views, false}, nil
}

func hCreatePolicyVersion(s *Server, p params) (any, *awshttp.APIError) {
	arn := p.str("PolicyArn")
	version, err := s.store.AddPolicyVersion(arn,
		decodeDocument(p.str("PolicyDocument")),
		p.str("SetAsDefault") == "true")
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	pol, err := s.store.GetPolicy(arn)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		PolicyVersion policyVersionView `xml:"PolicyVersion"`
	}{policyVersionView{
		VersionId:        version,
		IsDefaultVersion: pol.DefaultVersion == version,
		CreateDate:       iso(pol.Updated),
	}}, nil
}

func hGetPolicyVersion(s *Server, p params) (any, *awshttp.APIError) {
	pol, err := s.store.GetPolicy(p.str("PolicyArn"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	version := p.str("VersionId")
	doc, ok := pol.Versions[version]
	if !ok {
		return nil, errNoEntity("Policy %s version %s does not exist.", p.str("PolicyArn"), version)
	}
	return struct {
		PolicyVersion policyVersionView `xml:"PolicyVersion"`
	}{policyVersionView{
		Document:         url.QueryEscape(doc),
		VersionId:        version,
		IsDefaultVersion: pol.DefaultVersion == version,
		CreateDate:       iso(pol.Created),
	}}, nil
}

func hDeletePolicyVersion(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(
		s.store.DeletePolicyVersion(p.str("PolicyArn"), p.str("VersionId")))
}

func hListPolicyVersions(s *Server, p params) (any, *awshttp.APIError) {
	pol, err := s.store.GetPolicy(p.str("PolicyArn"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]policyVersionView, 0, len(pol.VersionOrder))
	for _, v := range pol.VersionOrder {
		views = append(views, policyVersionView{
			VersionId: v, IsDefaultVersion: v == pol.DefaultVersion, CreateDate: iso(pol.Created),
		})
	}
	return struct {
		Versions    []policyVersionView `xml:"Versions>member"`
		IsTruncated bool                `xml:"IsTruncated"`
	}{views, false}, nil
}

func hSetDefaultPolicyVersion(s *Server, p params) (any, *awshttp.APIError) {
	version := p.str("VersionId")
	_, err := s.store.UpdatePolicy(p.str("PolicyArn"), func(pol *Policy) error {
		if _, ok := pol.Versions[version]; !ok {
			return errNoEntity("Policy version %s does not exist.", version)
		}
		pol.DefaultVersion = version
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hTagPolicy(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdatePolicy(p.str("PolicyArn"), func(pol *Policy) error {
		pol.Tags = mergeTags(pol.Tags, p.tags())
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hUntagPolicy(s *Server, p params) (any, *awshttp.APIError) {
	_, err := s.store.UpdatePolicy(p.str("PolicyArn"), func(pol *Policy) error {
		for _, k := range p.members("TagKeys") {
			delete(pol.Tags, k)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hListPolicyTags(s *Server, p params) (any, *awshttp.APIError) {
	pol, err := s.store.GetPolicy(p.str("PolicyArn"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Tags        []tagView `xml:"Tags>member"`
		IsTruncated bool      `xml:"IsTruncated"`
	}{tagViews(pol.Tags), false}, nil
}

// ---- attachment ----

// attach and detach are the shared bodies; the per-kind handlers below only
// differ in which name parameter they read.
func attach(s *Server, kind attachTarget, name, arn string) (any, *awshttp.APIError) {
	if name == "" || arn == "" {
		return nil, errValidation("both the entity name and PolicyArn are required")
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.Attach(kind, name, arn))
}

func detach(s *Server, kind attachTarget, name, arn string) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.Detach(kind, name, arn))
}

func hAttachUserPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return attach(s, targetUser, p.str("UserName"), p.str("PolicyArn"))
}
func hDetachUserPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return detach(s, targetUser, p.str("UserName"), p.str("PolicyArn"))
}
func hAttachGroupPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return attach(s, targetGroup, p.str("GroupName"), p.str("PolicyArn"))
}
func hDetachGroupPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return detach(s, targetGroup, p.str("GroupName"), p.str("PolicyArn"))
}
func hAttachRolePolicy(s *Server, p params) (any, *awshttp.APIError) {
	return attach(s, targetRole, p.str("RoleName"), p.str("PolicyArn"))
}
func hDetachRolePolicy(s *Server, p params) (any, *awshttp.APIError) {
	return detach(s, targetRole, p.str("RoleName"), p.str("PolicyArn"))
}

func listAttached(s *Server, kind attachTarget, name string) (any, *awshttp.APIError) {
	pr, err := s.store.principalOf(kind, name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]attachedPolicyView, 0, len(pr.Attached))
	for _, arn := range pr.Attached {
		display := arn
		if pol, err := s.store.GetPolicy(arn); err == nil {
			display = pol.Name
		}
		views = append(views, attachedPolicyView{PolicyName: display, PolicyArn: arn})
	}
	return struct {
		AttachedPolicies []attachedPolicyView `xml:"AttachedPolicies>member"`
		IsTruncated      bool                 `xml:"IsTruncated"`
	}{views, false}, nil
}

func hListAttachedUserPolicies(s *Server, p params) (any, *awshttp.APIError) {
	return listAttached(s, targetUser, p.str("UserName"))
}
func hListAttachedGroupPolicies(s *Server, p params) (any, *awshttp.APIError) {
	return listAttached(s, targetGroup, p.str("GroupName"))
}
func hListAttachedRolePolicies(s *Server, p params) (any, *awshttp.APIError) {
	return listAttached(s, targetRole, p.str("RoleName"))
}

// hListEntitiesForPolicy answers the reverse question — who has this policy?
func hListEntitiesForPolicy(s *Server, p params) (any, *awshttp.APIError) {
	arn := p.str("PolicyArn")
	type named struct {
		Name string `xml:"UserName"`
		ID   string `xml:"UserId,omitempty"`
	}
	type namedGroup struct {
		Name string `xml:"GroupName"`
		ID   string `xml:"GroupId,omitempty"`
	}
	type namedRole struct {
		Name string `xml:"RoleName"`
		ID   string `xml:"RoleId,omitempty"`
	}
	var users []named
	var groups []namedGroup
	var roles []namedRole

	filter := p.str("EntityFilter")
	want := func(kind string) bool { return filter == "" || filter == kind || filter == "Any" }

	if want("User") {
		list, _ := s.store.ListUsers("")
		for i := range list {
			if containsString(list[i].Attached, arn) {
				users = append(users, named{list[i].Name, list[i].ID})
			}
		}
	}
	if want("Group") {
		list, _ := s.store.ListGroups("")
		for i := range list {
			if containsString(list[i].Attached, arn) {
				groups = append(groups, namedGroup{list[i].Name, list[i].ID})
			}
		}
	}
	if want("Role") {
		list, _ := s.store.ListRoles("")
		for i := range list {
			if containsString(list[i].Attached, arn) {
				roles = append(roles, namedRole{list[i].Name, list[i].ID})
			}
		}
	}
	return struct {
		PolicyUsers  []named      `xml:"PolicyUsers>member"`
		PolicyGroups []namedGroup `xml:"PolicyGroups>member"`
		PolicyRoles  []namedRole  `xml:"PolicyRoles>member"`
		IsTruncated  bool         `xml:"IsTruncated"`
	}{users, groups, roles, false}, nil
}

// ---- inline policies ----

func putInline(s *Server, kind attachTarget, name, policyName, document string) (any, *awshttp.APIError) {
	if policyName == "" {
		return nil, errValidation("PolicyName is required")
	}
	doc := decodeDocument(document)
	if _, err := ParsePolicy(doc); err != nil {
		return nil, errMalformedPolicy("PolicyDocument: %v", err)
	}
	err := s.store.updatePrincipal(kind, name, func(pr *Principal) error {
		if pr.Inline == nil {
			pr.Inline = map[string]string{}
		}
		pr.Inline[policyName] = doc
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func getInline(s *Server, kind attachTarget, name, policyName, nameField string) (any, *awshttp.APIError) {
	pr, err := s.store.principalOf(kind, name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	doc, ok := pr.Inline[policyName]
	if !ok {
		return nil, errNoEntity("The %s policy with name %s cannot be found.", nameField, policyName)
	}
	// The element carrying the entity name differs per kind, so each caller
	// wraps this document in its own struct.
	return map[string]string{"PolicyDocument": url.QueryEscape(doc), "PolicyName": policyName}, nil
}

func deleteInline(s *Server, kind attachTarget, name, policyName string) (any, *awshttp.APIError) {
	err := s.store.updatePrincipal(kind, name, func(pr *Principal) error {
		if _, ok := pr.Inline[policyName]; !ok {
			return errNoEntity("The policy with name %s cannot be found.", policyName)
		}
		delete(pr.Inline, policyName)
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func listInline(s *Server, kind attachTarget, name string) (any, *awshttp.APIError) {
	pr, err := s.store.principalOf(kind, name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		PolicyNames []string `xml:"PolicyNames>member"`
		IsTruncated bool     `xml:"IsTruncated"`
	}{sortedKeys(pr.Inline), false}, nil
}

func hPutUserPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return putInline(s, targetUser, p.str("UserName"), p.str("PolicyName"), p.str("PolicyDocument"))
}

func hGetUserPolicy(s *Server, p params) (any, *awshttp.APIError) {
	got, aerr := getInline(s, targetUser, p.str("UserName"), p.str("PolicyName"), "user")
	if aerr != nil {
		return nil, aerr
	}
	m := got.(map[string]string)
	return struct {
		UserName       string `xml:"UserName"`
		PolicyName     string `xml:"PolicyName"`
		PolicyDocument string `xml:"PolicyDocument"`
	}{p.str("UserName"), m["PolicyName"], m["PolicyDocument"]}, nil
}

func hDeleteUserPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return deleteInline(s, targetUser, p.str("UserName"), p.str("PolicyName"))
}

func hListUserPolicies(s *Server, p params) (any, *awshttp.APIError) {
	return listInline(s, targetUser, p.str("UserName"))
}

func hPutGroupPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return putInline(s, targetGroup, p.str("GroupName"), p.str("PolicyName"), p.str("PolicyDocument"))
}

func hGetGroupPolicy(s *Server, p params) (any, *awshttp.APIError) {
	got, aerr := getInline(s, targetGroup, p.str("GroupName"), p.str("PolicyName"), "group")
	if aerr != nil {
		return nil, aerr
	}
	m := got.(map[string]string)
	return struct {
		GroupName      string `xml:"GroupName"`
		PolicyName     string `xml:"PolicyName"`
		PolicyDocument string `xml:"PolicyDocument"`
	}{p.str("GroupName"), m["PolicyName"], m["PolicyDocument"]}, nil
}

func hDeleteGroupPolicy(s *Server, p params) (any, *awshttp.APIError) {
	return deleteInline(s, targetGroup, p.str("GroupName"), p.str("PolicyName"))
}

func hListGroupPolicies(s *Server, p params) (any, *awshttp.APIError) {
	return listInline(s, targetGroup, p.str("GroupName"))
}

func hPutRolePolicy(s *Server, p params) (any, *awshttp.APIError) {
	return putInline(s, targetRole, p.str("RoleName"), p.str("PolicyName"), p.str("PolicyDocument"))
}

func hGetRolePolicy(s *Server, p params) (any, *awshttp.APIError) {
	got, aerr := getInline(s, targetRole, p.str("RoleName"), p.str("PolicyName"), "role")
	if aerr != nil {
		return nil, aerr
	}
	m := got.(map[string]string)
	return struct {
		RoleName       string `xml:"RoleName"`
		PolicyName     string `xml:"PolicyName"`
		PolicyDocument string `xml:"PolicyDocument"`
	}{p.str("RoleName"), m["PolicyName"], m["PolicyDocument"]}, nil
}

func hDeleteRolePolicy(s *Server, p params) (any, *awshttp.APIError) {
	return deleteInline(s, targetRole, p.str("RoleName"), p.str("PolicyName"))
}

func hListRolePolicies(s *Server, p params) (any, *awshttp.APIError) {
	return listInline(s, targetRole, p.str("RoleName"))
}

// ---- permissions boundaries ----
//
// The boundary is stored and returned, and Evaluate treats it as a ceiling
// when one is set — an action must be allowed by both the identity policies
// and the boundary.

func setBoundary(s *Server, kind attachTarget, name, arn string) (any, *awshttp.APIError) {
	if _, err := s.store.GetPolicy(arn); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	err := s.store.updatePrincipal(kind, name, func(pr *Principal) error {
		pr.PermissionsBoundary = arn
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func clearBoundary(s *Server, kind attachTarget, name string) (any, *awshttp.APIError) {
	err := s.store.updatePrincipal(kind, name, func(pr *Principal) error {
		pr.PermissionsBoundary = ""
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hPutUserBoundary(s *Server, p params) (any, *awshttp.APIError) {
	return setBoundary(s, targetUser, p.str("UserName"), p.str("PermissionsBoundary"))
}
func hDeleteUserBoundary(s *Server, p params) (any, *awshttp.APIError) {
	return clearBoundary(s, targetUser, p.str("UserName"))
}
func hPutRoleBoundary(s *Server, p params) (any, *awshttp.APIError) {
	return setBoundary(s, targetRole, p.str("RoleName"), p.str("PermissionsBoundary"))
}
func hDeleteRoleBoundary(s *Server, p params) (any, *awshttp.APIError) {
	return clearBoundary(s, targetRole, p.str("RoleName"))
}

// ---- account ----

func hCreateAccountAlias(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.SetAccountAlias(p.str("AccountAlias")))
}

func hDeleteAccountAlias(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.SetAccountAlias(""))
}

func hListAccountAliases(s *Server, _ params) (any, *awshttp.APIError) {
	var aliases []string
	if a := s.store.AccountAlias(); a != "" {
		aliases = append(aliases, a)
	}
	return struct {
		AccountAliases []string `xml:"AccountAliases>member"`
		IsTruncated    bool     `xml:"IsTruncated"`
	}{aliases, false}, nil
}

func hGetAccountSummary(s *Server, _ params) (any, *awshttp.APIError) {
	users, _ := s.store.ListUsers("")
	groups, _ := s.store.ListGroups("")
	roles, _ := s.store.ListRoles("")
	policies, _ := s.store.ListPolicies("")

	type entry struct {
		Key   string `xml:"key"`
		Value int    `xml:"value"`
	}
	return struct {
		SummaryMap []entry `xml:"SummaryMap>entry"`
	}{[]entry{
		{"Users", len(users)},
		{"Groups", len(groups)},
		{"Roles", len(roles)},
		{"Policies", len(policies)},
		{"AccountMFAEnabled", 0},
	}}, nil
}

// hGetAccountPasswordPolicy answers NoSuchEntity, which is what AWS returns
// for an account that has never set one — and is the honest local answer,
// since doze-aws has no console login to apply a password policy to.
func hGetAccountPasswordPolicy(_ *Server, _ params) (any, *awshttp.APIError) {
	return nil, errNoEntity("The Password Policy with domain %s cannot be found.", awsident.AccountID)
}

// hGetAccountAuthorizationDetails dumps every principal with its policies —
// the call `aws iam get-account-authorization-details` makes, and the one
// policy-analysis tooling reads.
func hGetAccountAuthorizationDetails(s *Server, _ params) (any, *awshttp.APIError) {
	type inlineView struct {
		PolicyName     string `xml:"PolicyName"`
		PolicyDocument string `xml:"PolicyDocument"`
	}
	type userDetail struct {
		Path                    string               `xml:"Path"`
		UserName                string               `xml:"UserName"`
		UserId                  string               `xml:"UserId"`
		Arn                     string               `xml:"Arn"`
		CreateDate              string               `xml:"CreateDate"`
		UserPolicyList          []inlineView         `xml:"UserPolicyList>member,omitempty"`
		AttachedManagedPolicies []attachedPolicyView `xml:"AttachedManagedPolicies>member,omitempty"`
		GroupList               []string             `xml:"GroupList>member,omitempty"`
	}
	type roleDetail struct {
		Path                     string               `xml:"Path"`
		RoleName                 string               `xml:"RoleName"`
		RoleId                   string               `xml:"RoleId"`
		Arn                      string               `xml:"Arn"`
		CreateDate               string               `xml:"CreateDate"`
		AssumeRolePolicyDocument string               `xml:"AssumeRolePolicyDocument,omitempty"`
		RolePolicyList           []inlineView         `xml:"RolePolicyList>member,omitempty"`
		AttachedManagedPolicies  []attachedPolicyView `xml:"AttachedManagedPolicies>member,omitempty"`
	}

	inlineOf := func(pr *Principal) []inlineView {
		var out []inlineView
		for _, name := range sortedKeys(pr.Inline) {
			out = append(out, inlineView{name, url.QueryEscape(pr.Inline[name])})
		}
		return out
	}
	attachedOf := func(pr *Principal) []attachedPolicyView {
		var out []attachedPolicyView
		for _, arn := range pr.Attached {
			display := arn
			if pol, err := s.store.GetPolicy(arn); err == nil {
				display = pol.Name
			}
			out = append(out, attachedPolicyView{display, arn})
		}
		return out
	}

	users, _ := s.store.ListUsers("")
	var userDetails []userDetail
	for i := range users {
		u := &users[i]
		userDetails = append(userDetails, userDetail{
			Path: u.Path, UserName: u.Name, UserId: u.ID,
			Arn:            awsident.GlobalARN("iam", "user"+u.Path+u.Name),
			CreateDate:     iso(u.Created),
			UserPolicyList: inlineOf(&u.Principal), AttachedManagedPolicies: attachedOf(&u.Principal),
			GroupList: u.Groups,
		})
	}
	roles, _ := s.store.ListRoles("")
	var roleDetails []roleDetail
	for i := range roles {
		r := &roles[i]
		roleDetails = append(roleDetails, roleDetail{
			Path: r.Path, RoleName: r.Name, RoleId: r.ID,
			Arn:                      awsident.GlobalARN("iam", "role"+r.Path+r.Name),
			CreateDate:               iso(r.Created),
			AssumeRolePolicyDocument: url.QueryEscape(r.AssumeRolePolicy),
			RolePolicyList:           inlineOf(&r.Principal),
			AttachedManagedPolicies:  attachedOf(&r.Principal),
		})
	}
	return struct {
		UserDetailList []userDetail `xml:"UserDetailList>member,omitempty"`
		RoleDetailList []roleDetail `xml:"RoleDetailList>member,omitempty"`
		IsTruncated    bool         `xml:"IsTruncated"`
	}{userDetails, roleDetails, false}, nil
}
