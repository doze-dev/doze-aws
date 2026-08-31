package console

// IAM console handlers.

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// iamNav gathers what the list pane needs on every IAM page.
func (c *Console) iamNav(r *http.Request) (principals []Principal, policies []PolicyRef, groups []IAMGroup, profiles []InstanceProfile) {
	principals, _ = c.be.ListPrincipals(r.Context())
	policies, _ = c.be.ListManagedPolicies(r.Context())
	groups, _ = c.be.ListIAMGroups(r.Context())
	profiles, _ = c.be.ListInstanceProfiles(r.Context())
	return
}

func (c *Console) iamHome(w http.ResponseWriter, r *http.Request) {
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	mode, events, err := c.be.AccessLog(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "iam_home", map[string]any{
		"Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles,
		"Mode": mode, "Events": events, "Title": "IAM",
	})
}

func (c *Console) iamPrincipal(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	// Groups and instance profiles share the /iam/{kind}/{name} shape but
	// are not principals; they get their own pages.
	switch kind {
	case "group":
		c.iamGroupPage(w, r, name)
		return
	case "profile":
		c.iamProfilePage(w, r, name)
		return
	}
	p, err := c.be.Principal(r.Context(), kind, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	attached, _ := c.be.AttachedPolicies(r.Context(), kind, name)
	inline, _ := c.be.InlinePolicies(r.Context(), kind, name)
	data := map[string]any{
		"P": p, "Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles,
		"Attached": attached, "Inline": inline,
		"Title": name + " · IAM",
	}
	data["StarterInline"] = starterPolicy
	data["SeenActions"] = c.seenActions(r)
	if kind == "user" {
		// The singular read plus the facts the listing cannot carry: group
		// membership and when each key last authenticated something.
		c.be.GetIAMUser(r.Context(), name) //nolint:errcheck // the page already has the user
		keys, _ := c.be.AccessKeys(r.Context(), name)
		type keyView struct {
			AccessKey
			LastUsed string
		}
		kviews := make([]keyView, 0, len(keys))
		for _, k := range keys {
			kviews = append(kviews, keyView{k, c.be.AccessKeyLastUsed(r.Context(), k.ID)})
		}
		data["Keys"] = kviews
		data["Groups"], _ = c.be.GroupsForUser(r.Context(), name)
		allGroups, _ := c.be.ListIAMGroups(r.Context())
		data["AllGroups"] = allGroups
	}
	if kind == "role" {
		desc, session, err := c.be.GetIAMRole(r.Context(), name)
		if err == nil {
			data["Description"], data["MaxSession"] = desc, session
		}
		data["RoleProfiles"], _ = c.be.ProfilesForRole(r.Context(), name)
	}
	c.render(w, r, "iam_principal", data)
}

func (c *Console) iamPolicy(w http.ResponseWriter, r *http.Request) {
	arn := r.URL.Query().Get("arn")
	doc, err := c.be.PolicyDocument(r.Context(), arn)
	if err != nil {
		c.fail(w, err)
		return
	}
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	name := arn
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		name = arn[i+1:]
	}
	data := map[string]any{
		"Name": name, "ARN": arn, "Document": doc,
		"Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles,
		"Title": name + " · IAM",
	}
	data["SeenActions"] = c.seenActions(r)
	data["Versions"], _ = c.be.PolicyVersions(r.Context(), arn)
	data["Entities"], _ = c.be.PolicyEntities(r.Context(), arn)
	c.render(w, r, "iam_policy", data)
}

// iamSimulate answers "would this be allowed" without having to provoke the
// call and read it out of a denial.
func (c *Console) iamSimulate(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("principal")
	actions := strings.FieldsFunc(r.FormValue("actions"), func(ru rune) bool {
		return ru == ',' || ru == ' ' || ru == '\n' || ru == '\r' || ru == '\t'
	})
	// Draft mode: a pasted document simulates as SimulateCustomPolicy — the
	// check-before-create the principal simulator cannot do. The context-keys
	// read rides along either way: which condition keys would need values.
	doc := strings.TrimSpace(r.FormValue("document"))
	if doc == "" && arn == "" || len(actions) == 0 {
		c.partial(w, "iam_sim_result", map[string]any{
			"Err": "Pick a principal (or paste a draft policy) and name at least one action, like s3:GetObject.",
		})
		return
	}
	var res []SimResult
	var err error
	var ctxKeys []string
	if doc != "" {
		res, err = c.be.SimulateCustom(r.Context(), doc, actions, r.FormValue("resource"))
		ctxKeys, _ = c.be.ContextKeysForCustomPolicy(r.Context(), doc)
	} else {
		res, err = c.be.Simulate(r.Context(), arn, actions, r.FormValue("resource"))
		ctxKeys, _ = c.be.ContextKeysForPrincipal(r.Context(), arn)
	}
	if err != nil {
		c.partial(w, "iam_sim_result", map[string]any{"Err": err.Error()})
		return
	}
	c.partial(w, "iam_sim_result", map[string]any{"Results": res, "CtxKeys": ctxKeys, "Draft": doc != ""})
}

// iamGenerate turns a soft-mode run into the policy it actually needed.
func (c *Console) iamGenerate(w http.ResponseWriter, r *http.Request) {
	doc, err := c.be.GeneratedPolicy(r.Context(), r.FormValue("principal"))
	if err != nil {
		c.partial(w, "iam_generated", map[string]any{"Err": err.Error()})
		return
	}
	c.partial(w, "iam_generated", map[string]any{"Document": doc})
}

// ---- mutations ----

const defaultTrust = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "lambda.amazonaws.com" },
      "Action": "sts:AssumeRole"
    }
  ]
}`

const starterPolicy = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject"],
      "Resource": "*"
    }
  ]
}`

func (c *Console) iamCreatePage(w http.ResponseWriter, r *http.Request) {
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	c.render(w, r, "iam_create", map[string]any{
		"Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles,
		"Kind": r.URL.Query().Get("kind"), "Title": "Create · IAM",
		"DefaultTrust": defaultTrust, "StarterPolicy": starterPolicy,
		"SeenActions": c.seenActions(r),
	})
}

func (c *Console) iamCreate(w http.ResponseWriter, r *http.Request) {
	kind := r.FormValue("kind")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		c.redirect(w, r, c.prefix+"/iam/create?kind="+kind, "A name is required")
		return
	}
	var err error
	var to string
	switch kind {
	case "role":
		trust := r.FormValue("trust")
		if strings.TrimSpace(trust) == "" {
			trust = defaultTrust
		}
		err, to = c.be.CreateRole(r.Context(), name, trust), "/iam/role/"+name
	case "policy":
		err, to = c.be.CreatePolicy(r.Context(), name, r.FormValue("document")), "/iam"
	case "group":
		err, to = c.be.CreateIAMGroup(r.Context(), name), "/iam/group/"+name
	case "profile":
		err, to = c.be.CreateInstanceProfile(r.Context(), name), "/iam/profile/"+name
	case "slr":
		// name carries the AWS service (CreateServiceLinkedRole names the
		// role itself from it).
		err, to = c.be.CreateServiceLinkedRole(r.Context(), name), "/iam"
	default:
		err, to = c.be.CreateUser(r.Context(), name), "/iam/user/"+name
	}
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+to, "Created "+name)
}

func (c *Console) iamAttach(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	arn := r.FormValue("arn")
	if arn == "" {
		c.redirect(w, r, c.prefix+"/iam/"+kind+"/"+name, "Pick a policy to attach")
		return
	}
	if err := c.be.AttachPolicy(r.Context(), kind, name, arn); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/"+kind+"/"+name, "Attached")
}

func (c *Console) iamDetach(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	if err := c.be.DetachPolicy(r.Context(), kind, name, r.FormValue("arn")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/"+kind+"/"+name, "Detached")
}

func (c *Console) iamDeletePrincipal(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	switch {
	case kind == "group":
		if err := c.be.DeleteIAMGroup(r.Context(), name); err != nil {
			c.fail(w, err)
			return
		}
		c.redirect(w, r, c.prefix+"/iam", "Group "+name+" deleted")
		return
	case kind == "profile":
		if err := c.be.DeleteInstanceProfile(r.Context(), name); err != nil {
			c.fail(w, err)
			return
		}
		c.redirect(w, r, c.prefix+"/iam", "Instance profile "+name+" deleted")
		return
	case kind == "role" && r.FormValue("slr") != "":
		if err := c.be.DeleteServiceLinkedRole(r.Context(), name); err != nil {
			c.fail(w, err)
			return
		}
		c.redirect(w, r, c.prefix+"/iam", "Service-linked role "+name+" deleted")
		return
	}
	if err := c.be.DeletePrincipal(r.Context(), kind, name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam", "Deleted "+name)
}

func (c *Console) iamDeletePolicy(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteManagedPolicy(r.Context(), r.FormValue("arn")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam", "Policy deleted")
}

// iamNewKey creates a credential pair. The secret is shown once and then never
// again, exactly as AWS does it, so it is carried in the flash rather than
// stored anywhere the page could re-read.
func (c *Console) iamNewKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id, secret, err := c.be.NewAccessKey(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	// The one flash that must survive being read: AWS never shows this again.
	c.redirectSticky(w, r, c.prefix+"/iam/user/"+name, id+" / "+secret+" — the secret is not retrievable again")
}

func (c *Console) iamDeleteKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := c.be.DeleteAccessKey(r.Context(), name, r.FormValue("id")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/user/"+name, "Key deleted")
}

// iamPutInline saves an inline policy. Inline policies are how permissions are
// most often granted locally — one document on one principal, with nothing to
// attach — so editing one in place is worth more than a separate screen.
func (c *Console) iamPutInline(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	policyName := strings.TrimSpace(r.FormValue("policy"))
	if policyName == "" {
		c.redirect(w, r, c.prefix+"/iam/"+kind+"/"+name, "An inline policy needs a name")
		return
	}
	if err := c.be.PutInlinePolicy(r.Context(), kind, name, policyName, r.FormValue("document")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/"+kind+"/"+name, "Saved "+policyName)
}

func (c *Console) iamDeleteInline(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	if err := c.be.DeleteInlinePolicy(r.Context(), kind, name, r.FormValue("policy")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/"+kind+"/"+name, "Removed inline policy")
}

// ---- groups, instance profiles, account: the burn-down surfaces ----

// iamGroupPage renders one group: members, attached policies, tags.
func (c *Console) iamGroupPage(w http.ResponseWriter, r *http.Request, name string) {
	g, err := c.be.GetIAMGroup(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	attached, _ := c.be.AttachedPolicies(r.Context(), "group", name)
	c.render(w, r, "iam_group", map[string]any{
		"G": g, "Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles, "Attached": attached,
		"Title": name + " · IAM",
	})
}

// iamProfilePage renders one instance profile: the roles it carries.
func (c *Console) iamProfilePage(w http.ResponseWriter, r *http.Request, name string) {
	p, err := c.be.GetInstanceProfile(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	c.render(w, r, "iam_profile", map[string]any{
		"IP": p, "Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles,
		"Title": name + " · IAM",
	})
}

func (c *Console) iamGroupMember(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("name")
	user := strings.TrimSpace(r.FormValue("user"))
	var err error
	if r.FormValue("remove") != "" {
		err = c.be.RemoveUserFromGroup(r.Context(), group, user)
	} else {
		err = c.be.AddUserToGroup(r.Context(), group, user)
	}
	if err != nil {
		c.fail(w, err)
		return
	}
	verb := "added to"
	if r.FormValue("remove") != "" {
		verb = "removed from"
	}
	c.redirect(w, r, c.prefix+"/iam/group/"+group, user+" "+verb+" "+group)
}

func (c *Console) iamGroupRename(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	newName := strings.TrimSpace(r.FormValue("new"))
	if err := c.be.RenameIAMGroup(r.Context(), name, newName); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/group/"+newName, "Group renamed to "+newName)
}

func (c *Console) iamProfileRole(w http.ResponseWriter, r *http.Request) {
	profile := r.PathValue("name")
	role := strings.TrimSpace(r.FormValue("role"))
	var err error
	if r.FormValue("remove") != "" {
		err = c.be.RemoveRoleFromProfile(r.Context(), profile, role)
	} else {
		err = c.be.AddRoleToProfile(r.Context(), profile, role)
	}
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/profile/"+profile, "Profile updated")
}

// iamAccount is the account page: summary, aliases, password policy, and the
// full authorization export on demand.
func (c *Console) iamAccount(w http.ResponseWriter, r *http.Request) {
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	data := map[string]any{
		"Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles, "Title": "Account · IAM",
	}
	data["Summary"], _ = c.be.IAMAccountSummary(r.Context())
	data["Aliases"], _ = c.be.AccountAliases(r.Context())
	if pw, err := c.be.PasswordPolicy(r.Context()); err == nil {
		data["PasswordPolicy"] = pw
	}
	c.render(w, r, "iam_account", data)
}

func (c *Console) iamAlias(w http.ResponseWriter, r *http.Request) {
	if old := r.FormValue("delete"); old != "" {
		if err := c.be.DeleteAccountAlias(r.Context(), old); err != nil {
			c.fail(w, err)
			return
		}
		c.redirect(w, r, c.prefix+"/iam/account", "Alias removed")
		return
	}
	alias := strings.TrimSpace(r.FormValue("alias"))
	if err := c.be.CreateAccountAlias(r.Context(), alias); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/account", "Account alias set to "+alias)
}

// iamAuthDetails renders the whole-account export (the audit read) into a
// code panel — on demand, because it is the biggest response IAM has.
func (c *Console) iamAuthDetails(w http.ResponseWriter, r *http.Request) {
	raw, err := c.be.AuthorizationDetails(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "iam_auth_details", map[string]any{"Raw": raw})
}

// iamKeyToggle flips an access key Active/Inactive — the
// revoke-without-deleting step of a rotation.
func (c *Console) iamKeyToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	active := r.FormValue("active") != ""
	if err := c.be.SetAccessKeyActive(r.Context(), name, r.FormValue("id"), active); err != nil {
		c.fail(w, err)
		return
	}
	if active {
		toast(w, "Key activated")
	} else {
		toast(w, "Key deactivated — requests signed with it are refused until it is re-activated")
	}
	c.redirect(w, r, c.prefix+"/iam/user/"+name, "")
}

// iamTrust replaces a role's assume-role policy (the study-3 leftover).
func (c *Console) iamTrust(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := c.be.UpdateTrustPolicy(r.Context(), name, r.FormValue("document")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/role/"+name, "Trust policy updated — who may assume this role just changed")
}

// iamRoleMeta writes description and session duration.
func (c *Console) iamRoleMeta(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := c.be.UpdateRoleMeta(r.Context(), name,
		r.FormValue("description"), atoi(r.FormValue("session"))); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/role/"+name, "Role settings saved")
}

func (c *Console) iamRenameUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	newName := strings.TrimSpace(r.FormValue("new"))
	if err := c.be.RenameUser(r.Context(), name, newName); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/user/"+newName, "User renamed to "+newName)
}

// ---- managed-policy versions ----

func (c *Console) iamNewPolicyVersion(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("arn")
	if err := c.be.CreatePolicyVersion(r.Context(), arn, r.FormValue("document"),
		r.FormValue("default") != ""); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/policy?arn="+url.QueryEscape(arn), "New version published")
}

func (c *Console) iamSetDefaultVersion(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("arn")
	v := r.FormValue("version")
	if err := c.be.SetDefaultPolicyVersion(r.Context(), arn, v); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/policy?arn="+url.QueryEscape(arn),
		v+" is now the default — every attachment evaluates it from here on")
}

func (c *Console) iamDeleteVersion(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("arn")
	if err := c.be.DeletePolicyVersion(r.Context(), arn, r.FormValue("version")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/policy?arn="+url.QueryEscape(arn), "Version deleted")
}

// iamJoinGroup is AddUserToGroup from the user's side of the relationship.
func (c *Console) iamJoinGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	group := r.FormValue("group")
	if err := c.be.AddUserToGroup(r.Context(), group, name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/iam/user/"+name, name+" added to "+group)
}

// ---- STS: minting credentials against the local account ----

// iamSTS is the credentials page: every way STS hands out a key set.
func (c *Console) iamSTS(w http.ResponseWriter, r *http.Request) {
	principals, policies, navGroups, navProfiles := c.iamNav(r)
	c.render(w, r, "iam_sts", map[string]any{
		"Principals": principals, "Policies": policies, "NavGroups": navGroups, "NavProfiles": navProfiles,
		"Title": "STS · IAM",
	})
}

// iamSTSMint runs the chosen mint and renders the credential set.
func (c *Console) iamSTSMint(w http.ResponseWriter, r *http.Request) {
	mode := r.FormValue("mode")
	v := url.Values{}
	set := func(param, field string) {
		if s := strings.TrimSpace(r.FormValue(field)); s != "" {
			v.Set(param, s)
		}
	}
	set("DurationSeconds", "duration")
	switch mode {
	case "assume-role", "web-identity", "saml":
		if role := strings.TrimSpace(r.FormValue("role")); role != "" {
			v.Set("RoleArn", "arn:aws:iam::"+awsident.AccountID+":role/"+role)
		}
		set("RoleSessionName", "session")
	}
	switch mode {
	case "federation":
		set("Name", "name")
	case "root":
		set("TargetPrincipal", "target")
		set("TaskPolicyArn.arn", "task")
	case "web-identity":
		set("WebIdentityToken", "token")
	case "saml":
		set("SAMLAssertion", "assertion")
		set("PrincipalArn", "provider")
	}
	creds, err := c.be.MintCredentials(r.Context(), mode, v)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "iam_sts_creds", map[string]any{"C": creds})
}

// iamSTSKeyInfo answers which account a pasted key id belongs to.
func (c *Console) iamSTSKeyInfo(w http.ResponseWriter, r *http.Request) {
	account, err := c.be.AccessKeyAccount(r.Context(), strings.TrimSpace(r.FormValue("id")))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "iam_sts_keyinfo", map[string]any{"Account": account})
}

// seenActions feeds the builder's action autocomplete: the actions the access
// log has actually recorded, plus each service's wildcard. The recorded
// traffic IS the catalog locally — more useful than AWS's static list, since
// every entry is something this stack has genuinely been asked for.
func (c *Console) seenActions(r *http.Request) []string {
	set := map[string]bool{}
	for _, p := range []string{"s3", "sqs", "sns", "sts", "dynamodb", "kms", "ssm",
		"secretsmanager", "events", "lambda", "kinesis", "iam", "cloudformation", "apigateway"} {
		set[p+":*"] = true
	}
	if _, events, err := c.be.AccessLog(r.Context()); err == nil {
		for _, e := range events {
			if e.Action != "" && strings.Contains(e.Action, ":") {
				set[e.Action] = true
			}
		}
	}
	actions := make([]string, 0, len(set))
	for a := range set {
		actions = append(actions, a)
	}
	sort.Strings(actions)
	return actions
}

// iamSimInline evaluates the builder's draft against one or more actions and
// renders an id-free result block (the home simulator owns #iam-sim).
func (c *Console) iamSimInline(w http.ResponseWriter, r *http.Request) {
	doc := strings.TrimSpace(r.FormValue("document"))
	actions := strings.FieldsFunc(r.FormValue("actions"), func(ru rune) bool {
		return ru == ',' || ru == ' ' || ru == '\n' || ru == '\r' || ru == '\t'
	})
	if doc == "" || len(actions) == 0 {
		c.partial(w, "iam_sim_inline", map[string]any{"Err": "Name at least one action, like s3:GetObject."})
		return
	}
	res, err := c.be.SimulateCustom(r.Context(), doc, actions, r.FormValue("resource"))
	if err != nil {
		c.partial(w, "iam_sim_inline", map[string]any{"Err": err.Error()})
		return
	}
	// A draft's conditions evaluate against an EMPTY context here, so a deny
	// on a conditioned statement needs its reason spelled out — the condition
	// keys with no values are the reason.
	keys, _ := c.be.ContextKeysForCustomPolicy(r.Context(), doc)
	c.partial(w, "iam_sim_inline", map[string]any{"Results": res, "CtxKeys": keys})
}
