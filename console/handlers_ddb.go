package console

import "net/http"

func (c *Console) ddbTables(w http.ResponseWriter, r *http.Request) {
	tables, err := c.be.ListTables(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "ddb_tables", map[string]any{"Tables": tables})
}

func (c *Console) ddbCreateTable(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.CreateTable(r.Context(), name,
		r.FormValue("hash_key"), r.FormValue("hash_type"),
		r.FormValue("range_key"), r.FormValue("range_type")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/ddb/"+name, "Table “"+name+"” created")
}

func (c *Console) ddbDeleteTable(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteTable(r.Context(), r.PathValue("table")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Table deleted")
	tables, _ := c.be.ListTables(r.Context())
	c.partial(w, "ddb_table_list", map[string]any{"Tables": tables})
}

func (c *Console) ddbTable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	t, err := c.be.DescribeTable(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	items, truncated, _ := c.be.ScanItems(r.Context(), t, 50)
	c.render(w, r, "ddb_table", map[string]any{
		"Table": t, "Items": items, "Truncated": truncated,
		"Tab": tabOf(r, "items"),
	})
}

// ddbItems is the HTMX partial refreshing the item table after a mutation.
func (c *Console) ddbItems(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	t, err := c.be.DescribeTable(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	items, truncated, _ := c.be.ScanItems(r.Context(), t, 50)
	c.partial(w, "ddb_item_table", map[string]any{"Table": t, "Items": items, "Truncated": truncated})
}

func (c *Console) ddbPutItem(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	if err := c.be.PutItemJSON(r.Context(), name, r.FormValue("item")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Item saved")
	c.ddbItems(w, r)
}

func (c *Console) ddbDeleteItem(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	if err := c.be.DeleteItem(r.Context(), name, r.FormValue("key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Item deleted")
	c.ddbItems(w, r)
}

// tabOf returns the active tab from ?tab=, defaulting sensibly.
func tabOf(r *http.Request, def string) string {
	if t := r.URL.Query().Get("tab"); t != "" {
		return t
	}
	return def
}
