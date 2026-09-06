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
		case "kinesis":
			streams, _ := c.be.ListStreams(r.Context())
			data["List"] = streams
		case "s3":
			data["List"] = c.s3List(r)
		case "sqs":
			queues := c.sqsList(r)
			data["List"] = queues
			data["Queues"] = queues // DLQ picker
		case "ddb":
			data["List"], _ = c.be.ListTables(r.Context())
		case "sns":
			data["List"], _ = c.be.ListTopics(r.Context())
		case "eb":
			data["List"], _ = c.be.ListBuses(r.Context())
		case "sfn":
			data["List"], _ = c.be.ListStateMachines(r.Context())
			// The Task states a definition can call: the pickers in the
			// editor's help are populated from what the stack actually has.
			data["Functions"], _ = c.be.ListFunctions(r.Context())
		case "kms":
			data["List"], _ = c.be.ListKeys(r.Context())
		case "ssm":
			data["List"], _ = c.be.ListParameters(r.Context())
		case "logs":
			data["List"], _ = c.be.ListLogGroups(r.Context())
		case "sm":
			data["List"], _ = c.be.ListSecrets(r.Context())
		case "cfn":
			data["List"], _ = c.be.ListStacks(r.Context())
		case "apigw":
			data["List"], _ = c.be.ListRestAPIs(r.Context())
		case "lambda":
			// Missing from this switch since the lambda create page was added,
			// and it became a 500-in-disguise when lp_head started rendering
			// (len .List) in its heading: len of an untyped nil panics the
			// template mid-render, so the page shipped its <head> and died
			// before the form. The status was still 200 — html/template has
			// already written by the time it fails — which is why a status
			// sweep called this page fine.
			data["List"], _ = c.be.ListFunctions(r.Context())
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
	if sms, err := c.be.ListStateMachines(ctx); err == nil {
		for _, m := range sms {
			add("sfn", m.Name, "/sfn/"+m.Name)
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

	// Kinesis, CloudFormation, API Gateway and IAM were absent, so four of
	// thirteen services had working pages that ⌘K could not reach.
	if streams, err := c.be.ListStreams(ctx); err == nil {
		for _, st := range streams {
			add("kinesis", st.Name, "/kinesis/"+st.Name)
		}
	}
	if stacks, err := c.be.ListStacks(ctx); err == nil {
		for _, st := range stacks {
			add("cfn", st.Name, "/cfn/"+st.Name)
		}
	}
	if apis, err := c.be.ListRestAPIs(ctx); err == nil {
		for _, a := range apis {
			add("apigw", a.Name, "/apigw/"+a.ID)
		}
	}
	if ps, err := c.be.ListPrincipals(ctx); err == nil {
		for _, pr := range ps {
			add("iam", pr.Name, "/iam/"+pr.Kind+"/"+pr.Name)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// apiCounts feeds the rail's live per-service resource counts. It runs every
// few seconds in every open tab, so it must use the count-only probes — the
// full List* helpers fan out N+1 describe calls whose results would all be
// discarded here.
func (c *Console) apiCounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := map[string]int{}
	if v, err := c.be.ListBuckets(ctx); err == nil { // single call already
		counts["s3"] = len(v)
	}
	if n, err := c.be.CountQueues(ctx); err == nil {
		counts["sqs"] = n
	}
	if n, err := c.be.CountTables(ctx); err == nil {
		counts["ddb"] = n
	}
	if n, err := c.be.CountTopics(ctx); err == nil {
		counts["sns"] = n
	}
	if n, err := c.be.CountBuses(ctx); err == nil {
		counts["eb"] = n
	}
	if v, err := c.be.ListFunctions(ctx); err == nil { // single call already
		counts["lambda"] = len(v)
	}
	if n, err := c.be.CountKeys(ctx); err == nil {
		counts["kms"] = n
	}
	if n, err := c.be.CountStreams(ctx); err == nil {
		counts["kinesis"] = n
	}
	if n, err := c.be.CountStacks(ctx); err == nil {
		counts["cfn"] = n
	}
	if n, err := c.be.CountRestAPIs(ctx); err == nil {
		counts["apigw"] = n
	}
	if n, err := c.be.CountPrincipals(ctx); err == nil {
		counts["iam"] = n
	}
	if v, err := c.be.ListParameters(ctx); err == nil { // single call already
		counts["ssm"] = len(v)
	}
	if v, err := c.be.ListSecrets(ctx); err == nil { // single call already
		counts["sm"] = len(v)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(counts)
}

// apiPalette serves the catalogue the command palette navigates by. It used to
// be four hand-maintained arrays in shell.js — NAV, ACTS, KIND and SVCSET —
// which is how four of thirteen services became unreachable from ⌘K and how a
// deleted page stayed in the list. Generated from the same catalogue the rail
// renders, so the two cannot disagree again.
func (c *Console) apiPalette(w http.ResponseWriter, r *http.Request) {
	type item struct {
		S string `json:"s,omitempty"` // service key, for colour
		N string `json:"n"`           // label
		U string `json:"u"`           // url
		K string `json:"k,omitempty"` // kind
	}
	out := struct {
		Nav   []item            `json:"nav"`
		Acts  []item            `json:"acts"`
		Kinds map[string]string `json:"kinds"`
	}{Kinds: map[string]string{}}

	for _, e := range surfaces {
		u := c.prefix + "/"
		if e.Key != "traffic" {
			u = c.prefix + "/" + e.Key
		}
		out.Nav = append(out.Nav, item{N: e.Label, U: u, K: "surface"})
	}
	for _, e := range catalog {
		out.Nav = append(out.Nav, item{S: e.Key, N: e.Label, U: c.prefix + "/" + e.Key, K: "service"})
		out.Kinds[e.Key] = e.Noun
		if e.CreatePath != "" {
			out.Acts = append(out.Acts, item{S: e.Key, N: e.CreateLabel, U: c.prefix + e.CreatePath, K: "create"})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// apiResolve turns an ARN, a queue URL or a bare identifier into a console
// page. The table lives in Go, so pasting an ARN out of a stack trace into ⌘K
// and landing on its page costs one call rather than a fourth copy of the
// parser in JavaScript.
func (c *Console) apiResolve(w http.ResponseWriter, r *http.Request) {
	ref := resourceFromARN(r.URL.Query().Get("id"))
	out := struct {
		S string `json:"s,omitempty"`
		N string `json:"n,omitempty"`
		U string `json:"u,omitempty"`
		K string `json:"k,omitempty"`
	}{}
	if ref.OK() {
		out.S, out.N, out.U, out.K = ref.Svc, ref.Name, c.prefix+ref.Path, nounFor(ref.Svc)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
