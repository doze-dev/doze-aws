package console

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// createPage renders a full-page create form inside the workbench shell, with
// the service's own list pane still visible beside it.
func (c *Console) createPage(svc, tmpl string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"Svc": svc, "Title": "Create"}
		switch svc {
		case "s3":
			data["List"] = c.s3List(r)
		case "sqs":
			data["List"] = c.sqsList(r)
			data["Queues"] = c.sqsList(r) // DLQ picker
		case "ddb":
			data["List"], _ = c.be.ListTables(r.Context())
		case "sns":
			data["List"], _ = c.be.ListTopics(r.Context())
		case "eb":
			data["List"], _ = c.be.ListBuses(r.Context())
		case "kms":
			data["List"], _ = c.be.ListKeys(r.Context())
		case "ssm":
			data["List"], _ = c.be.ListParameters(r.Context())
		case "sm":
			data["List"], _ = c.be.ListSecrets(r.Context())
		}
		c.render(w, r, tmpl, data)
	}
}

// ebRuleCreatePage is scoped to a bus.
func (c *Console) ebRuleCreatePage(w http.ResponseWriter, r *http.Request) {
	buses, _ := c.be.ListBuses(r.Context())
	c.render(w, r, "eb_rule_create", map[string]any{"Bus": r.PathValue("bus"), "Svc": "eb", "List": buses, "Title": "Create rule"})
}

// apiResources feeds the command palette.
func (c *Console) apiResources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type res struct {
		S string `json:"s"`
		N string `json:"n"`
		U string `json:"u"`
	}
	var out []res
	add := func(svc, name, u string) { out = append(out, res{S: svc, N: name, U: c.prefix + u}) }

	if buckets, err := c.be.ListBuckets(ctx); err == nil {
		for _, b := range buckets {
			add("s3", b.Name, "/s3/"+b.Name)
		}
	}
	if queues, err := c.be.ListQueues(ctx); err == nil {
		for _, q := range queues {
			add("sqs", q.Name, "/sqs/"+q.Name)
		}
	}
	if tables, err := c.be.ListTables(ctx); err == nil {
		for _, t := range tables {
			add("ddb", t.Name, "/ddb/"+t.Name)
		}
	}
	if topics, err := c.be.ListTopics(ctx); err == nil {
		for _, t := range topics {
			add("sns", t.Name, "/sns/"+t.Name)
		}
	}
	if buses, err := c.be.ListBuses(ctx); err == nil {
		for _, b := range buses {
			add("eb", b.Name, "/eb/"+b.Name)
			if rules, err := c.be.ListRules(ctx, b.Name); err == nil {
				for _, rl := range rules {
					add("eb", b.Name+" › "+rl.Name, "/eb/"+b.Name+"/rule/"+rl.Name)
				}
			}
		}
	}
	if fns, err := c.be.ListFunctions(ctx); err == nil {
		for _, f := range fns {
			add("lambda", f.Name, "/lambda/"+f.Name)
		}
	}
	if keys, err := c.be.ListKeys(ctx); err == nil {
		for _, k := range keys {
			label := k.Alias
			if label == "" {
				label = k.ID
			}
			add("kms", label, "/kms/"+k.ID)
		}
	}
	if params, err := c.be.ListParameters(ctx); err == nil {
		for _, p := range params {
			add("ssm", p.Name, "/ssm/param?name="+url.QueryEscape(p.Name))
		}
	}
	if secrets, err := c.be.ListSecrets(ctx); err == nil {
		for _, s := range secrets {
			add("sm", s.Name, "/sm/secret?name="+url.QueryEscape(s.Name))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// apiCounts feeds the rail's live per-service resource counts.
func (c *Console) apiCounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := map[string]int{}
	if v, err := c.be.ListBuckets(ctx); err == nil {
		counts["s3"] = len(v)
	}
	if v, err := c.be.ListQueues(ctx); err == nil {
		counts["sqs"] = len(v)
	}
	if v, err := c.be.ListTables(ctx); err == nil {
		counts["ddb"] = len(v)
	}
	if v, err := c.be.ListTopics(ctx); err == nil {
		counts["sns"] = len(v)
	}
	if v, err := c.be.ListBuses(ctx); err == nil {
		counts["eb"] = len(v)
	}
	if v, err := c.be.ListFunctions(ctx); err == nil {
		counts["lambda"] = len(v)
	}
	if v, err := c.be.ListKeys(ctx); err == nil {
		counts["kms"] = len(v)
	}
	if v, err := c.be.ListParameters(ctx); err == nil {
		counts["ssm"] = len(v)
	}
	if v, err := c.be.ListSecrets(ctx); err == nil {
		counts["sm"] = len(v)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(counts)
}
