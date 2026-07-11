package console

import "net/http"

// Tags are a cross-service surface: one editor, one pair of routes, dispatched
// by the svc field. Each service detail page drops a {{template "tags_panel"}}
// that lazy-loads the editor for its resource.

func (c *Console) tagsView(w http.ResponseWriter, r *http.Request) {
	c.renderTagEditor(w, r, r.URL.Query().Get("svc"), r.URL.Query().Get("id"))
}

func (c *Console) tagsSet(w http.ResponseWriter, r *http.Request) {
	svc, id := r.FormValue("svc"), r.FormValue("id")
	if err := c.be.SetResourceTag(r.Context(), svc, id, r.FormValue("key"), r.FormValue("value")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Tag saved")
	c.renderTagEditor(w, r, svc, id)
}

func (c *Console) tagsRemove(w http.ResponseWriter, r *http.Request) {
	svc, id := r.FormValue("svc"), r.FormValue("id")
	if err := c.be.RemoveResourceTag(r.Context(), svc, id, r.FormValue("key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Tag removed")
	c.renderTagEditor(w, r, svc, id)
}

func (c *Console) renderTagEditor(w http.ResponseWriter, r *http.Request, svc, id string) {
	tags, err := c.be.ResourceTags(r.Context(), svc, id)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "tag_editor", map[string]any{"Svc": svc, "ID": id, "Tags": tags})
}
