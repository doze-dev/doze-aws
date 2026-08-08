package console

// IAM console handlers.

import (
	"net/http"
	"strings"
)

// iamNav gathers what the list pane needs on every IAM page.
func (c *Console) iamNav(r *http.Request) (principals []Principal, policies []PolicyRef) {
	principals, _ = c.be.ListPrincipals(r.Context())
	policies, _ = c.be.ListManagedPolicies(r.Context())
	return
}

func (c *Console) iamHome(w http.ResponseWriter, r *http.Request) {
	principals, policies := c.iamNav(r)
	mode, events, err := c.be.AccessLog(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "iam_home", map[string]any{
		"Principals": principals, "Policies": policies,
		"Mode": mode, "Events": events, "Title": "IAM",
	})
}

func (c *Console) iamPrincipal(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	p, err := c.be.Principal(r.Context(), kind, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	principals, policies := c.iamNav(r)
	attached, _ := c.be.AttachedPolicies(r.Context(), kind, name)
	inline, _ := c.be.InlinePolicies(r.Context(), kind, name)
	data := map[string]any{
		"P": p, "Principals": principals, "Policies": policies,
		"Attached": attached, "Inline": inline,
		"Title": name + " · IAM",
	}
	data["StarterInline"] = starterPolicy
	if kind == "user" {
		data["Keys"], _ = c.be.AccessKeys(r.Context(), name)
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
	principals, policies := c.iamNav(r)
	name := arn
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		name = arn[i+1:]
	}
	c.render(w, r, "iam_policy", map[string]any{
		"Name": name, "ARN": arn, "Document": doc,
		"Principals": principals, "Policies": policies,
		"Title": name + " · IAM",
	})
}

// iamSimulate answers "would this be allowed" without having to provoke the
// call and read it out of a denial.
func (c *Console) iamSimulate(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("principal")
	actions := strings.FieldsFunc(r.FormValue("actions"), func(ru rune) bool {
		return ru == ',' || ru == ' ' || ru == '\n' || ru == '\r' || ru == '\t'
	})
	if arn == "" || len(actions) == 0 {
		c.partial(w, "iam_sim_result", map[string]any{
			"Err": "Pick a principal and name at least one action, like s3:GetObject.",
		})
		return
	}
	res, err := c.be.Simulate(r.Context(), arn, actions, r.FormValue("resource"))
	if err != nil {
		c.partial(w, "iam_sim_result", map[string]any{"Err": err.Error()})
		return
	}
	c.partial(w, "iam_sim_result", map[string]any{"Results": res})
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
	principals, policies := c.iamNav(r)
	c.render(w, r, "iam_create", map[string]any{
		"Principals": principals, "Policies": policies,
		"Kind": r.URL.Query().Get("kind"), "Title": "Create · IAM",
		"DefaultTrust": defaultTrust, "StarterPolicy": starterPolicy,
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
	c.redirect(w, r, c.prefix+"/iam/user/"+name, id+" / "+secret+" — the secret is not retrievable again")
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
