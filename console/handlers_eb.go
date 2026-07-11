package console

import "net/http"

func (c *Console) ebBuses(w http.ResponseWriter, r *http.Request) {
	buses, err := c.be.ListBuses(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "eb_buses", map[string]any{"Buses": buses})
}

func (c *Console) ebCreateBus(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.CreateBus(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/eb/"+name, "Event bus “"+name+"” created")
}

func (c *Console) ebDeleteBus(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteBus(r.Context(), r.PathValue("bus")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Event bus deleted")
	buses, _ := c.be.ListBuses(r.Context())
	c.partial(w, "eb_bus_list", map[string]any{"Buses": buses})
}

func (c *Console) ebBus(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	rules, err := c.be.ListRules(r.Context(), bus)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "eb_bus", map[string]any{"Bus": bus, "Rules": rules, "BusARN": busARN(bus)})
}

func (c *Console) ebRulesPartial(w http.ResponseWriter, r *http.Request, bus string) {
	rules, _ := c.be.ListRules(r.Context(), bus)
	c.partial(w, "eb_rule_list", map[string]any{"Bus": bus, "Rules": rules})
}

func (c *Console) ebCreateRule(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	name := r.FormValue("name")
	if err := c.be.PutRule(r.Context(), bus, name, r.FormValue("pattern"), r.FormValue("schedule")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/eb/"+bus+"/rule/"+name, "Rule “"+name+"” created")
}

func (c *Console) ebDeleteRule(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	if err := c.be.DeleteRule(r.Context(), bus, r.PathValue("rule")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Rule deleted")
	w.Header().Set("HX-Redirect", c.prefix+"/eb/"+bus)
}

func (c *Console) ebTestEvent(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	detail := r.FormValue("detail")
	if detail == "" {
		detail = "{}"
	}
	if err := c.be.PutTestEvent(r.Context(), bus, r.FormValue("source"), r.FormValue("detail_type"), detail); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Event published to "+bus)
	c.ebRulesPartial(w, r, bus)
}

func (c *Console) ebRule(w http.ResponseWriter, r *http.Request) {
	bus, name := r.PathValue("bus"), r.PathValue("rule")
	rule, err := c.be.GetRule(r.Context(), bus, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	c.render(w, r, "eb_rule", map[string]any{
		"Bus": bus, "Rule": rule, "Queues": queues, "Functions": fns,
	})
}

func (c *Console) ebTargetsPartial(w http.ResponseWriter, r *http.Request, bus, name string) {
	rule, err := c.be.GetRule(r.Context(), bus, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "eb_targets", map[string]any{
		"Bus": bus, "Rule": rule, "Queues": queues, "Functions": fns,
	})
}

func (c *Console) ebAddTarget(w http.ResponseWriter, r *http.Request) {
	bus, name := r.PathValue("bus"), r.PathValue("rule")
	arn := r.FormValue("arn")
	id := "t" + shortID(arn)
	if err := c.be.AddTarget(r.Context(), bus, name, id, arn); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Target added")
	c.ebTargetsPartial(w, r, bus, name)
}

func (c *Console) ebRemoveTarget(w http.ResponseWriter, r *http.Request) {
	bus, name := r.PathValue("bus"), r.PathValue("rule")
	if err := c.be.RemoveTarget(r.Context(), bus, name, r.FormValue("id")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Target removed")
	c.ebTargetsPartial(w, r, bus, name)
}

func busARN(name string) string {
	return "arn:aws:events:us-east-1:000000000000:event-bus/" + name
}

// shortID derives a small stable id from a string (for target ids).
func shortID(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 6)
	for i := range out {
		out[i] = hex[h&0xf]
		h >>= 4
	}
	return string(out)
}
