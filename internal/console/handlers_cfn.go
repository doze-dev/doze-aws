package console

// CloudFormation console handlers.
//
// The stack pages read; these also write. The write path is deliberately the
// same one `aws cloudformation deploy` takes — template in, parameters
// answered, then either a direct deploy or a change set reviewed first — so
// what the console does and what a deploy tool does are the same wire calls.

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (c *Console) cfnStacks(w http.ResponseWriter, r *http.Request) {
	stacks, err := c.be.ListStacks(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	data := map[string]any{"List": stacks, "Title": "CloudFormation"}
	if len(stacks) > 0 {
		// The service-wide surfaces live on the home pane (the SSM pattern):
		// the export registry with its importers — the blast radius of each
		// value — and the deletion record DescribeStacks no longer shows.
		type exportRow struct {
			StackExport
			Importers []string
		}
		exports, _ := c.be.ListExports(r.Context())
		rows := make([]exportRow, 0, len(exports))
		for _, e := range exports {
			imp, _ := c.be.ListImports(r.Context(), e.Name)
			rows = append(rows, exportRow{e, imp})
		}
		data["Exports"] = rows
	}
	if deleted, err := c.be.DeletedStacks(r.Context()); err == nil && len(deleted) > 0 {
		data["Deleted"] = deleted
	}
	c.render(w, r, "cfn_home", data)
}

func (c *Console) cfnStack(w http.ResponseWriter, r *http.Request) {
	name := param(r, "stack")
	st, err := c.be.StackDetail(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	stacks, _ := c.be.ListStacks(r.Context())
	tab := tabOf(r, "resources")

	data := map[string]any{
		"Stack": st, "List": stacks, "Tab": tab,
		"Title": name + " · CloudFormation",
	}
	// Each tab costs a call, so only the one being shown makes it.
	switch tab {
	case "events":
		data["Events"], _ = c.be.StackEvents(r.Context(), name)
	case "template":
		data["Template"], _ = c.be.StackTemplate(r.Context(), name)
	case "changesets":
		data["ChangeSets"], _ = c.be.ListChangeSets(r.Context(), name)
		if cs := r.URL.Query().Get("cs"); cs != "" {
			detail, err := c.be.ChangeSetDetail(r.Context(), name, cs)
			if err != nil {
				c.fail(w, err)
				return
			}
			data["CS"] = detail
		}
	case "update":
		data["Template"], _ = c.be.StackTemplate(r.Context(), name)
		data["CSDefault"] = fmt.Sprintf("console-%d", time.Now().Unix()%100000)
	default:
		res, _ := c.be.StackResources(r.Context(), name)
		data["Resources"] = res
	}
	c.render(w, r, "cfn_stack", data)
}

func (c *Console) cfnDelete(w http.ResponseWriter, r *http.Request) {
	name := param(r, "stack")
	if err := c.be.DeleteStack(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/cfn", "Deleted "+name)
}

// formParams collects the create/update forms' param:Key fields into the
// parameter map the deploy calls take. Empty values are dropped so a blank
// input means "use the template default" (create) or "keep the current
// value" (update, where the emulator merges with the stack's existing set).
func formParams(r *http.Request) map[string]string {
	r.ParseForm() //nolint:errcheck // best-effort: an unparsable form just yields no params
	params := map[string]string{}
	for k := range r.Form {
		if key, ok := strings.CutPrefix(k, "param:"); ok && r.FormValue(k) != "" {
			params[key] = r.FormValue(k)
		}
	}
	return params
}

// cfnValidate runs ValidateTemplate and renders the verdict either way — a
// rejected template is this surface's product, not an error condition.
func (c *Console) cfnValidate(w http.ResponseWriter, r *http.Request) {
	desc, params, err := c.be.ValidateStackTemplate(r.Context(), r.FormValue("template"))
	data := map[string]any{}
	if err != nil {
		data["Err"] = err.Error()
	} else {
		data["Desc"], data["Params"], data["OK"] = desc, params, true
	}
	c.partial(w, "cfn_validate_result", data)
}

// cfnSummary reads the parameter declarations out of the pasted template and
// renders the typed inputs for them (GetTemplateSummary drives the form).
func (c *Console) cfnSummary(w http.ResponseWriter, r *http.Request) {
	info, err := c.be.TemplateSummary(r.Context(), r.FormValue("template"), "")
	data := map[string]any{}
	if err != nil {
		data["Err"] = err.Error()
	} else {
		data["Info"] = info
	}
	c.partial(w, "cfn_params", data)
}

func (c *Console) cfnCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	body := r.FormValue("template")
	params := formParams(r)
	if r.FormValue("review") != "" {
		cs := strings.TrimSpace(r.FormValue("changeset"))
		if err := c.be.CreateChangeSet(r.Context(), name, cs, body, params); err != nil {
			c.fail(w, err)
			return
		}
		c.redirect(w, r, c.prefix+"/cfn/"+name+"?tab=changesets&cs="+cs,
			"Change set created — review the diff, then execute it")
		return
	}
	if err := c.be.CreateStack(r.Context(), name, body, params); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.stackLanding(r, name), c.stackOutcome(r, name, "Stack created and deployed"))
}

func (c *Console) cfnUpdate(w http.ResponseWriter, r *http.Request) {
	name := param(r, "stack")
	body := r.FormValue("template")
	if r.FormValue("useprev") != "" {
		// UsePreviousTemplate: parameters only, template stays as deployed.
		body = ""
	}
	params := formParams(r)
	if r.FormValue("review") != "" {
		cs := strings.TrimSpace(r.FormValue("changeset"))
		if err := c.be.CreateChangeSet(r.Context(), name, cs, body, params); err != nil {
			c.fail(w, err)
			return
		}
		c.redirect(w, r, c.prefix+"/cfn/"+name+"?tab=changesets&cs="+cs,
			"Change set created — review the diff, then execute it")
		return
	}
	if err := c.be.UpdateStack(r.Context(), name, body, params); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/cfn/"+name+"?tab=events", c.stackOutcome(r, name, "Stack update deployed"))
}

// stackOutcome is what to tell someone after a deploy the API accepted.
//
// "Accepted" is all a 200 means. A resource that fails rolls the stack back,
// as on AWS, and the call still succeeds — so the message has to come from
// the stack, not from the call. Saying "created and deployed" over a stack
// that had just undone itself is the one thing worse than saying nothing.
func (c *Console) stackOutcome(r *http.Request, name, ok string) string {
	st, err := c.be.StackDetail(r.Context(), name)
	if err != nil || st == nil {
		return ok
	}
	switch st.Status {
	case "ROLLBACK_COMPLETE":
		return "A resource failed and the stack was rolled back — nothing it created was kept. The events say which."
	case "UPDATE_ROLLBACK_COMPLETE":
		return "A resource failed and the update was rolled back — the stack is as it was before. The events say which."
	case "CREATE_FAILED", "UPDATE_FAILED":
		return "A resource failed and the stack was left as it fell (rollback is disabled). The events say which."
	case "ROLLBACK_FAILED", "UPDATE_ROLLBACK_FAILED":
		return "A resource failed, and so did rolling it back. The events say what is left."
	}
	return ok
}

// stackLanding is where to send someone after a create: the events, when the
// stack did not come up, because that is the only page that says why.
func (c *Console) stackLanding(r *http.Request, name string) string {
	if st, err := c.be.StackDetail(r.Context(), name); err == nil && st != nil && st.Status != "CREATE_COMPLETE" {
		return c.prefix + "/cfn/" + name + "?tab=events"
	}
	return c.prefix + "/cfn/" + name
}

func (c *Console) cfnExecuteCS(w http.ResponseWriter, r *http.Request) {
	stack, cs := param(r, "stack"), param(r, "cs")
	if err := c.be.ExecuteChangeSet(r.Context(), stack, cs); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/cfn/"+stack+"?tab=events", c.stackOutcome(r, stack, "Change set "+cs+" executed"))
}

func (c *Console) cfnDeleteCS(w http.ResponseWriter, r *http.Request) {
	stack, cs := param(r, "stack"), param(r, "cs")
	if err := c.be.DeleteChangeSet(r.Context(), stack, cs); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/cfn/"+stack+"?tab=changesets", "Change set "+cs+" deleted")
}

// cfnResource is the single-resource drill-down (DescribeStackResource): the
// resources table plus the status reason and timestamp it does not show.
func (c *Console) cfnResource(w http.ResponseWriter, r *http.Request) {
	info, err := c.be.StackResource1(r.Context(), param(r, "stack"), r.FormValue("logical"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "cfn_resource_detail", map[string]any{"R": info})
}

// cfnExportTemplate answers with everything currently running as a
// CloudFormation template — the same bytes `doze-aws export` writes. The create
// page's "Start from what's running" button loads it into the editor, so a
// first template is something you edit rather than something you compose from
// nothing against a dialect you may not know.
func (c *Console) cfnExportTemplate(w http.ResponseWriter, r *http.Request) {
	out, err := c.be.ExportTemplate(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}
