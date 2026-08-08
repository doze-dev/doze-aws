package console

// CloudFormation console handlers.

import "net/http"

func (c *Console) cfnStacks(w http.ResponseWriter, r *http.Request) {
	stacks, err := c.be.ListStacks(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "cfn_home", map[string]any{"List": stacks, "Title": "CloudFormation"})
}

func (c *Console) cfnStack(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("stack")
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
	default:
		res, _ := c.be.StackResources(r.Context(), name)
		data["Resources"] = res
	}
	c.render(w, r, "cfn_stack", data)
}

func (c *Console) cfnDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("stack")
	if err := c.be.DeleteStack(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/cfn", "Deleted "+name)
}
