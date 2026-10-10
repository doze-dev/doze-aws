package console

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Tags are a cross-service surface: one editor that saves the whole set, dispatched
// by the svc field. Each service detail page drops a {{template "tags_panel"}}
// that lazy-loads the editor for its resource.

func (c *Console) tagsView(w http.ResponseWriter, r *http.Request) {
	c.renderTagEditor(w, r, r.URL.Query().Get("svc"), r.URL.Query().Get("id"))
}

func (c *Console) renderTagEditor(w http.ResponseWriter, r *http.Request, svc, id string) {
	c.renderTagEditorWith(w, r, svc, id, nil)
}

// tagRefusal is what a refused save tells the editor: the service's own words,
// and what became of the rest of the draft.
type tagRefusal struct {
	Err  failReason
	Note string
}

// renderTagEditorWith draws the editor from what the service holds NOW. After a
// refused save that is the point: the draft in the page is a guess about the
// resource, the service's answer is the fact, and showing the guess again would
// let a half-written set look like the one the user submitted.
func (c *Console) renderTagEditorWith(w http.ResponseWriter, r *http.Request, svc, id string, refused *tagRefusal) {
	tags, err := c.be.ResourceTags(r.Context(), svc, id)
	if err != nil {
		c.fail(w, err)
		return
	}
	data := map[string]any{"Svc": svc, "ID": id, "Tags": tags, "Prefix": c.prefix}
	if refused != nil {
		data["Refused"] = refused
	}
	c.partial(w, "tag_editor", data)
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

	var added, changed int
	set := map[string]string{}
	for k, v := range want {
		if cur, ok := current[k]; !ok || cur != v {
			set[k] = v
			if ok {
				changed++
			} else {
				added++
			}
		}
	}
	var remove []string
	for k := range current {
		if _, keep := want[k]; !keep {
			remove = append(remove, k)
		}
	}
	sort.Strings(remove)

	// One call each way. Removals go first, because a set that would pass the
	// resource's tag limit only does so once the keys leaving it have gone. If
	// the second call is refused the first has already happened, so the editor
	// is redrawn from the resource and says which half was applied: it is never
	// left holding a draft that looks like what the service now has.
	if len(remove) > 0 {
		if err := c.be.RemoveResourceTags(r.Context(), svc, id, remove); err != nil {
			c.renderTagEditorWith(w, r, svc, id, &tagRefusal{
				Err:  failView(err),
				Note: "Nothing was changed. Refused: removing " + quoteKeys(remove) + ".",
			})
			return
		}
	}
	if len(set) > 0 {
		if err := c.be.SetResourceTags(r.Context(), svc, id, set); err != nil {
			keys := make([]string, 0, len(set))
			for k := range set {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			note := "Nothing was changed. Refused: writing " + quoteKeys(keys) + "."
			if len(remove) > 0 {
				note = "The removal of " + quoteKeys(remove) + " was applied. Refused: writing " + quoteKeys(keys) + "."
			}
			c.renderTagEditorWith(w, r, svc, id, &tagRefusal{Err: failView(err), Note: note})
			return
		}
	}

	toast(w, tagSaveNote(added, changed, len(remove)))
	c.renderTagEditor(w, r, svc, id)
}

// quoteKeys names tag keys for a sentence: "a", "b" and "c".
func quoteKeys(keys []string) string {
	q := make([]string, len(keys))
	for i, k := range keys {
		q[i] = strconv.Quote(k)
	}
	switch len(q) {
	case 0:
		return ""
	case 1:
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
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
