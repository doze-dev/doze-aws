package console

import (
	"net/http"
	"strconv"
	"strings"
)

func (c *Console) ddbTables(w http.ResponseWriter, r *http.Request) {
	tables, err := c.be.ListTables(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "ddb_home", map[string]any{"List": tables, "Title": "DynamoDB"})
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
	items, truncated, _ := c.be.ScanItems(r.Context(), t, "", 50)
	tables, _ := c.be.ListTables(r.Context())
	c.render(w, r, "ddb_table", map[string]any{
		"Table": t, "Items": items, "Truncated": truncated, "Mode": "scan",
		"Tab": tabOf(r, "items"), "List": tables, "Title": name + " · DynamoDB",
	})
}

// ddbExplore is the query surface: Scan (optional filter), Query (key
// condition on the base table or a GSI), or PartiQL. All render the same item
// table so the drawer/edit/delete flow is identical regardless of mode.
func (c *Console) ddbExplore(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	t, err := c.be.DescribeTable(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	mode := r.FormValue("mode")
	limit := 50
	if l, e := strconv.Atoi(strings.TrimSpace(r.FormValue("limit"))); e == nil && l > 0 {
		limit = l
	}
	data := map[string]any{"Table": t, "Mode": mode}
	switch mode {
	case "query":
		items, truncated, err := c.be.QueryItems(r.Context(), t, QueryOpts{
			Index: r.FormValue("index"), PKValue: r.FormValue("pk"),
			SKOp: r.FormValue("sk_op"), SKValue: r.FormValue("sk"), SKValue2: r.FormValue("sk2"),
			Filter: r.FormValue("filter"), Limit: limit,
		})
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Items"], data["Truncated"] = items, truncated
	case "partiql":
		items, err := c.be.PartiQL(r.Context(), t, r.FormValue("statement"))
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Items"] = items
	default: // scan
		items, truncated, err := c.be.ScanItems(r.Context(), t, r.FormValue("filter"), limit)
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Items"], data["Truncated"] = items, truncated
	}
	c.partial(w, "ddb_item_table", data)
}

func (c *Console) ddbPutItem(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	if err := c.be.PutItemJSON(r.Context(), name, r.FormValue("item")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Item saved")
	c.ddbItemsScan(w, r)
}

func (c *Console) ddbDeleteItem(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	if err := c.be.DeleteItem(r.Context(), name, r.FormValue("key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Item deleted")
	c.ddbItemsScan(w, r)
}

// ddbItemsScan re-scans and swaps the item table after a mutation.
func (c *Console) ddbItemsScan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	t, err := c.be.DescribeTable(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	items, truncated, _ := c.be.ScanItems(r.Context(), t, "", 50)
	c.partial(w, "ddb_item_table", map[string]any{"Table": t, "Items": items, "Truncated": truncated, "Mode": "scan"})
}
