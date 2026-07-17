package console

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
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
	opts := TableCreateOpts{
		Name:    name,
		HashKey: r.FormValue("hash_key"), HashType: r.FormValue("hash_type"),
		RangeKey: r.FormValue("range_key"), RangeType: r.FormValue("range_type"),
		TTLAttr: strings.TrimSpace(r.FormValue("ttl_attr")),
	}
	// GSI rows are posted as parallel arrays (gsi_name[], gsi_hash[], …); the
	// Alpine form adds and removes rows, so indices may be sparse — zip by
	// position and skip any row without a name.
	names := r.Form["gsi_name"]
	hks := r.Form["gsi_hash"]
	hts := r.Form["gsi_hash_type"]
	rks := r.Form["gsi_range"]
	rts := r.Form["gsi_range_type"]
	at := func(s []string, i int) string {
		if i < len(s) {
			return s[i]
		}
		return ""
	}
	for i := range names {
		if strings.TrimSpace(names[i]) == "" {
			continue
		}
		opts.GSIs = append(opts.GSIs, GSICreate{
			Name:    strings.TrimSpace(names[i]),
			HashKey: at(hks, i), HashType: def(at(hts, i), "S"),
			RangeKey: strings.TrimSpace(at(rks, i)), RangeType: def(at(rts, i), "S"),
		})
	}
	if err := c.be.CreateTable(r.Context(), opts); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/ddb/"+name, "Table “"+name+"” created")
}

func def(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func (c *Console) ddbDeleteTable(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteTable(r.Context(), r.PathValue("table")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/ddb", "Table deleted")
}

func (c *Console) ddbTable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	t, err := c.be.DescribeTable(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	items, next, _ := c.be.ScanItems(r.Context(), t, "", nil, nil, 50, "")
	tables, _ := c.be.ListTables(r.Context())
	data := map[string]any{
		"Table": t, "Items": items, "Next": next, "NextVals": scanNextVals(name, "", next, nil), "Mode": "scan",
		"Tab": tabOf(r, "items"), "List": tables, "Title": name + " · DynamoDB",
	}
	addItemCols(data, t, items, nil)
	c.render(w, r, "ddb_table", data)
}

// scanNextVals builds the hx-vals JSON a "Load more" trigger carries so the next
// page re-runs the same scan from the pagination cursor. carry holds extra form
// fields the re-run must keep (filter bindings, the column set).
func scanNextVals(table, filter, cursor string, carry map[string]string) string {
	if cursor == "" {
		return ""
	}
	m := map[string]string{"mode": "scan", "filter": filter, "cursor": cursor}
	for k, v := range carry {
		m[k] = v
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// filterBindings reads the dynamic ":binding" value fields the explorer's
// filter UI posts (fv::name / ft::name pairs) into typed AttributeValues, and
// auto-aliases every "#name" token in the expression (the common reason to use
// # is a reserved word, and #status → status is what people mean locally).
func filterBindings(r *http.Request, filter string) (map[string]any, map[string]string, map[string]string) {
	vals := map[string]any{}
	carry := map[string]string{}
	r.ParseForm() //nolint:errcheck // best-effort: an unparsable form just yields no bindings
	for k := range r.Form {
		if name, ok := strings.CutPrefix(k, "fv:"); ok {
			typ := r.FormValue("ft:" + name)
			if typ == "" {
				typ = "S"
			}
			vals[":"+name] = avTyped(typ, r.FormValue(k))
			carry[k] = r.FormValue(k)
			carry["ft:"+name] = typ
		}
	}
	names := map[string]string{}
	for _, tok := range regexpHashTokens.FindAllString(filter, -1) {
		names[tok] = strings.TrimPrefix(tok, "#")
	}
	return vals, names, carry
}

var regexpHashTokens = regexp.MustCompile(`#\w+`)

// addItemCols promotes the page's attribute keys to real table columns when
// they fit (≤5 distinct non-key attributes); wider items fall back to the
// single preview column. cols, when non-nil, pins the set (pagination appends
// must align with the first page).
func addItemCols(data map[string]any, t *Table, items []Item, cols []string) {
	if cols == nil {
		seen := map[string]bool{}
		for _, it := range items {
			for k := range it.Attrs {
				seen[k] = true
			}
		}
		if len(seen) == 0 || len(seen) > 5 {
			data["Colspan"] = 3 + boolInt(t.RangeKey != "")
			return // fall back to the preview column
		}
		cols = make([]string, 0, len(seen))
		for k := range seen {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}
	data["Cols"] = cols
	data["Colspan"] = 2 + boolInt(t.RangeKey != "") + len(cols)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
	cursor := r.FormValue("cursor")
	// Pagination appends must keep the first page's column set.
	var pinnedCols []string
	if cursor != "" && r.FormValue("cols") != "" {
		pinnedCols = strings.Split(r.FormValue("cols"), ",")
	}
	data := map[string]any{"Table": t, "Mode": mode}
	switch mode {
	case "query":
		fvals, fnames, carry := filterBindings(r, r.FormValue("filter"))
		opts := QueryOpts{
			Index: r.FormValue("index"), PKValue: r.FormValue("pk"),
			SKOp: r.FormValue("sk_op"), SKValue: r.FormValue("sk"), SKValue2: r.FormValue("sk2"),
			Filter: r.FormValue("filter"), FilterVals: fvals, FilterNames: fnames,
			Limit: limit, Cursor: cursor,
		}
		items, next, err := c.be.QueryItems(r.Context(), t, opts)
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Items"], data["Next"] = items, next
		addItemCols(data, t, items, pinnedCols)
		if next != "" {
			m := map[string]string{
				"mode": "query", "index": opts.Index, "pk": opts.PKValue,
				"sk_op": opts.SKOp, "sk": opts.SKValue, "sk2": opts.SKValue2,
				"filter": opts.Filter, "cursor": next,
			}
			for k, v := range carry {
				m[k] = v
			}
			if cols, ok := data["Cols"].([]string); ok {
				m["cols"] = strings.Join(cols, ",")
			}
			raw, _ := json.Marshal(m)
			data["NextVals"] = string(raw)
		}
	case "partiql":
		// PartiQL results are targeted; no cursor paging in the console.
		items, err := c.be.PartiQL(r.Context(), t, r.FormValue("statement"))
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Items"] = items
		addItemCols(data, t, items, nil)
	default: // scan
		filter := r.FormValue("filter")
		fvals, fnames, carry := filterBindings(r, filter)
		items, next, err := c.be.ScanItems(r.Context(), t, filter, fvals, fnames, limit, cursor)
		if err != nil {
			c.fail(w, err)
			return
		}
		data["Items"], data["Next"] = items, next
		addItemCols(data, t, items, pinnedCols)
		if cols, ok := data["Cols"].([]string); ok {
			carry["cols"] = strings.Join(cols, ",")
		}
		data["NextVals"] = scanNextVals(name, filter, next, carry)
	}
	// A cursor means this is a "Load more" — return just the row fragment so it
	// appends; a fresh query returns the whole table.
	if cursor != "" {
		c.partial(w, "ddb_item_rows", data)
		return
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
	items, next, _ := c.be.ScanItems(r.Context(), t, "", nil, nil, 50, "")
	data := map[string]any{
		"Table": t, "Items": items, "Next": next, "NextVals": scanNextVals(name, "", next, nil), "Mode": "scan",
	}
	addItemCols(data, t, items, nil)
	c.partial(w, "ddb_item_table", data)
}

// ddbSetTTL enables or disables TTL on a table.
func (c *Console) ddbSetTTL(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	if err := c.be.SetTTL(r.Context(), name, strings.TrimSpace(r.FormValue("attr"))); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "TTL updated")
	c.ddbDetailPartial(w, r, name)
}

// ddbAddGSI adds a global secondary index post-create.
func (c *Console) ddbAddGSI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	g := GSICreate{
		Name: r.FormValue("gsi_name"), HashKey: r.FormValue("gsi_hash"), HashType: r.FormValue("gsi_hash_type"),
		RangeKey: r.FormValue("gsi_range"), RangeType: r.FormValue("gsi_range_type"),
	}
	if g.HashType == "" {
		g.HashType = "S"
	}
	if g.RangeKey != "" && g.RangeType == "" {
		g.RangeType = "S"
	}
	if err := c.be.AddGSI(r.Context(), name, g); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Index “"+g.Name+"” added")
	c.ddbDetailPartial(w, r, name)
}

// ddbDeleteGSI drops a global secondary index.
func (c *Console) ddbDeleteGSI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	if err := c.be.DeleteGSI(r.Context(), name, r.FormValue("index")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Index removed")
	c.ddbDetailPartial(w, r, name)
}

// ddbDetailPartial re-renders the Details tab (schema + TTL + GSIs).
func (c *Console) ddbDetailPartial(w http.ResponseWriter, r *http.Request, name string) {
	t, err := c.be.DescribeTable(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "ddb_details", map[string]any{"Table": t})
}
