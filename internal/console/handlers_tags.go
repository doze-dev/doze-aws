package console

import (
	"net/http"
	"strconv"
	"strings"
)

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
	c.partial(w, "tag_editor", map[string]any{"Svc": svc, "ID": id, "Tags": tags, "Prefix": c.prefix})
}

// tagsSave replaces the whole tag set in one submit, which is what an explicit
// Save means.
//
// The old editor applied every keystroke's worth of intent immediately: the ×
// on a row called UntagResource there and then, and the add-row called
// TagResource on submit. That is fine for a text field and wrong for infra —
// there was no draft to reconsider and no way to undo a mis-click, because the
// change had already reached the service. AWS makes you press Save for the same
// reason, and its Cancel is a real affordance rather than a courtesy.
//
// The diff is computed here rather than in the page because the page does not
// know what is actually on the resource — only what it was shown when it
// loaded. Someone else's change between load and save should not be silently
// reverted by a stale form, so removals are limited to keys the resource still
// has, and writes are limited to keys that actually differ.
func (c *Console) tagsSave(w http.ResponseWriter, r *http.Request) {
	svc, id := r.FormValue("svc"), r.FormValue("id")
	want := map[string]string{}
	for i, k := range r.Form["tag_key"] {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		v := ""
		if i < len(r.Form["tag_val"]) {
			v = r.Form["tag_val"][i]
		}
		want[k] = v
	}

	have, err := c.be.ResourceTags(r.Context(), svc, id)
	if err != nil {
		c.fail(w, err)
		return
	}
	current := make(map[string]string, len(have))
	for _, kv := range have {
		current[kv.K] = kv.V
	}

	var added, changed, removed int
	for k, v := range want {
		if cur, ok := current[k]; !ok || cur != v {
			if err := c.be.SetResourceTag(r.Context(), svc, id, k, v); err != nil {
				c.fail(w, err)
				return
			}
			if ok {
				changed++
			} else {
				added++
			}
		}
	}
	for k := range current {
		if _, keep := want[k]; !keep {
			if err := c.be.RemoveResourceTag(r.Context(), svc, id, k); err != nil {
				c.fail(w, err)
				return
			}
			removed++
		}
	}

	toast(w, tagSaveNote(added, changed, removed))
	c.renderTagEditor(w, r, svc, id)
}

// tagSaveNote says what the save actually did. "Tags saved" after a submit that
// changed nothing is a small lie, and the one case where you most want to know
// is when you expected a change and got none.
func tagSaveNote(added, changed, removed int) string {
	var parts []string
	if added > 0 {
		parts = append(parts, strconv.Itoa(added)+" added")
	}
	if changed > 0 {
		parts = append(parts, strconv.Itoa(changed)+" updated")
	}
	if removed > 0 {
		parts = append(parts, strconv.Itoa(removed)+" removed")
	}
	if len(parts) == 0 {
		return "No tag changes to save"
	}
	return "Tags saved — " + strings.Join(parts, ", ")
}
