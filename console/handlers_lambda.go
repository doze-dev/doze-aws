package console

import (
	"net/http"
	"strconv"
	"strings"
)

// lambdaRuntimeHash fingerprints the live process state for 204-skip polling.
// SleepAt is an absolute deadline, so it's stable through a countdown (the poll
// keeps 204ing while the client ticks) and only changes when the timer resets
// on a new invoke — exactly when the badge needs to re-sync.
func lambdaRuntimeHash(st LambdaRuntimeState) string {
	warm := "0"
	if st.Warm {
		warm = "1"
	}
	return contentHash(warm, strconv.Itoa(st.Runners), strconv.Itoa(st.IdleSecs), strconv.FormatInt(st.SleepAt, 10))
}

func (c *Console) lambdaFns(w http.ResponseWriter, r *http.Request) {
	fns, err := c.be.ListFunctions(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "lambda_home", map[string]any{"List": fns, "Title": "Lambda"})
}

func (c *Console) lambdaFn(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	f, err := c.be.GetFunction(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	fns, _ := c.be.ListFunctions(r.Context())
	rt := c.be.LambdaRuntime(r.Context(), name)
	conn := c.be.Neighbors(r.Context(), "lambda", name)
	queues, _ := c.be.ListQueues(r.Context())
	c.render(w, r, "lambda_fn", map[string]any{
		"Fn": f, "Tab": tabOf(r, "invoke"), "List": fns, "Title": name + " · Lambda",
		"Conn": conn, "Diag": lambdaDiagram(f, conn),
		"RT": rt, "RTHash": lambdaRuntimeHash(rt),
		"URL": c.be.FunctionURL(r.Context(), name), "Queues": queues,
	})
}

// diagNode is one neighbor card in the function-overview diagram.
type diagNode struct{ Svc, Name, URL, Kind string }

// lambdaDiagram shapes a function's 1-hop wiring into the triggers → function
// → destinations picture: upstream edges become trigger cards, downstream
// edges become destination cards labeled by their role.
func lambdaDiagram(f *Function, conn Neighborhood) map[string][]diagNode {
	kindIn := map[string]string{"esm": "event source", "sub": "subscription", "target": "rule target", "notify": "notification"}
	var trig, dest []diagNode
	for _, n := range conn.Upstream {
		k := kindIn[n.Kind]
		if k == "" {
			k = n.Kind
		}
		trig = append(trig, diagNode{n.Svc, n.Name, n.URL, n.Svc + " · " + k})
	}
	for _, n := range conn.Downstream {
		label := n.Kind
		switch {
		case n.Kind == "dlq":
			label = "dead-letter"
		case arnLeaf(f.OnSuccess) == n.Name:
			label = "on success"
		case arnLeaf(f.OnFailure) == n.Name:
			label = "on failure"
		}
		dest = append(dest, diagNode{n.Svc, n.Name, n.URL, label})
	}
	return map[string][]diagNode{"Trig": trig, "Dest": dest}
}

// lambdaRuntimeBadge is the polled live partial for a function's process state:
// 204 when unchanged, otherwise the morph-swapped badge.
func (c *Console) lambdaRuntimeBadge(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	rt := c.be.LambdaRuntime(r.Context(), name)
	hash := lambdaRuntimeHash(rt)
	if liveUnchanged(w, r, hash) {
		return
	}
	c.partial(w, "lambda_runtime_badge", map[string]any{
		"Prefix": c.prefix, "Name": name, "RT": rt, "Hash": hash,
	})
}

func (c *Console) lambdaInvoke(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	payload := r.FormValue("payload")
	if payload == "" {
		payload = "{}"
	}
	async := r.FormValue("async") == "true"
	res, err := c.be.Invoke(r.Context(), name, payload, async)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "lambda_result", map[string]any{"Res": res, "Fn": name})
}

// parseEnvRows turns the env editor's parallel arrays into a map.
func parseEnvRows(r *http.Request) map[string]string {
	env := map[string]string{}
	keys, vals := r.Form["env_key"], r.Form["env_val"]
	for i := range keys {
		k := strings.TrimSpace(keys[i])
		if k == "" {
			continue
		}
		v := ""
		if i < len(vals) {
			v = vals[i]
		}
		env[k] = v
	}
	return env
}

// lambdaCreate provisions a function from a local code path (_local_ extension).
func (c *Console) lambdaCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if err := c.be.CreateFunction(r.Context(), CreateFunctionOpts{
		Name: name, Runtime: r.FormValue("runtime"), Handler: r.FormValue("handler"),
		Code:    strings.TrimSpace(r.FormValue("code")),
		Timeout: atoi(r.FormValue("timeout")), Memory: atoi(r.FormValue("memory")),
		Env: parseEnvRows(r),
	}); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/lambda/"+name, "Function “"+name+"” created")
}

// lambdaSaveConfig edits env / timeout / memory.
func (c *Console) lambdaSaveConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	if err := c.be.UpdateConfig(r.Context(), name,
		atoi(r.FormValue("timeout")), atoi(r.FormValue("memory")), parseEnvRows(r)); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Configuration saved — the runner restarts on next invoke")
	c.lambdaConfigPartial(w, r, name)
}

func (c *Console) lambdaConfigPartial(w http.ResponseWriter, r *http.Request, name string) {
	f, err := c.be.GetFunction(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "lambda_config", map[string]any{"Fn": f, "URL": c.be.FunctionURL(r.Context(), name)})
}

// lambdaCreateURL / lambdaDeleteURL manage the function URL.
func (c *Console) lambdaCreateURL(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	if _, err := c.be.CreateFunctionURL(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Function URL created")
	c.lambdaConfigPartial(w, r, name)
}

func (c *Console) lambdaDeleteURL(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	if err := c.be.DeleteFunctionURL(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Function URL removed")
	c.lambdaConfigPartial(w, r, name)
}

// lambdaAddMapping wires an SQS event source mapping.
func (c *Console) lambdaAddMapping(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	queue := r.FormValue("queue")
	if queue == "" {
		c.fail(w, &apiErr{status: 400, body: "pick a queue"})
		return
	}
	if err := c.be.CreateMapping(r.Context(), name, queue, atoi(r.FormValue("batch"))); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Trigger added — "+queue+" now feeds this function")
	f, err := c.be.GetFunction(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	c.partial(w, "lambda_triggers", map[string]any{"Fn": f, "Queues": queues})
}

func (c *Console) lambdaDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteFunction(r.Context(), r.PathValue("fn")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Function deleted")
	w.Header().Set("HX-Redirect", c.prefix+"/lambda")
}

func (c *Console) lambdaDeleteMapping(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	if err := c.be.DeleteMapping(r.Context(), r.FormValue("uuid")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Trigger removed")
	f, err := c.be.GetFunction(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	c.partial(w, "lambda_triggers", map[string]any{"Fn": f, "Queues": queues})
}
