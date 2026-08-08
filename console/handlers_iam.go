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
