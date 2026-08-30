package console

import (
	"fmt"
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
	data := map[string]any{"List": fns, "Title": "Lambda"}
	if len(fns) > 0 {
		// The service-wide surfaces live on the home pane (the SSM pattern):
		// account usage against the nominal limits, and the layers registry —
		// a whole subsystem the emulator implements that had no UI at all.
		data["Acct"], _ = c.be.LambdaAccount(r.Context())
	}
	layers, _ := c.be.ListLambdaLayers(r.Context())
	data["Layers"] = layers
	c.render(w, r, "lambda_home", data)
}

// lambdaLayerVersions renders one layer's version list (ListLayerVersions).
func (c *Console) lambdaLayerVersions(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("layer")
	versions, err := c.be.LayerVersions(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "lambda_layer_versions", map[string]any{"Name": name, "Versions": versions})
}

// lambdaLayerVersion renders one version in full — content and permissions
// (GetLayerVersion + GetLayerVersionPolicy).
func (c *Console) lambdaLayerVersion(w http.ResponseWriter, r *http.Request) {
	v, err := strconv.ParseInt(r.FormValue("version"), 10, 64)
	if err != nil {
		c.fail(w, fmt.Errorf("version must be a number"))
		return
	}
	info, err := c.be.LayerVersion(r.Context(), r.FormValue("layer"), v)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "lambda_layer_detail", map[string]any{"L": info})
}

// lambdaLayerFind resolves a pasted layer-version ARN (GetLayerVersionByArn).
func (c *Console) lambdaLayerFind(w http.ResponseWriter, r *http.Request) {
	info, err := c.be.LayerVersionByARN(r.Context(), strings.TrimSpace(r.FormValue("arn")))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "lambda_layer_detail", map[string]any{"L": info})
}

// lambdaLayerPublish publishes a new layer version (PublishLayerVersion) and
// re-renders the registry.
func (c *Console) lambdaLayerPublish(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	var runtimes []string
	for _, rt := range strings.Split(r.FormValue("runtimes"), ",") {
		if rt = strings.TrimSpace(rt); rt != "" {
			runtimes = append(runtimes, rt)
		}
	}
	err := c.be.PublishLayer(r.Context(), name, strings.TrimSpace(r.FormValue("description")),
		strings.TrimSpace(r.FormValue("path")), runtimes)
	if err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Layer version published")
	c.lambdaLayersPartial(w, r)
}

// lambdaLayerDelete deletes one version (DeleteLayerVersion); functions
// already configured with it keep running, as in AWS.
func (c *Console) lambdaLayerDelete(w http.ResponseWriter, r *http.Request) {
	v, _ := strconv.ParseInt(r.FormValue("version"), 10, 64)
	if err := c.be.DeleteLayerVersion(r.Context(), r.FormValue("layer"), v); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Layer version deleted")
	c.lambdaLayersPartial(w, r)
}

// lambdaLayerGrant / lambdaLayerRevoke edit a version's resource policy
// (AddLayerVersionPermission / RemoveLayerVersionPermission), re-rendering the
// version detail so the policy block reflects the change.
func (c *Console) lambdaLayerGrant(w http.ResponseWriter, r *http.Request) {
	v, _ := strconv.ParseInt(r.FormValue("version"), 10, 64)
	if err := c.be.AddLayerPermission(r.Context(), r.FormValue("layer"), v,
		strings.TrimSpace(r.FormValue("sid")), strings.TrimSpace(r.FormValue("principal"))); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Permission added")
	c.lambdaLayerVersion(w, r)
}

func (c *Console) lambdaLayerRevoke(w http.ResponseWriter, r *http.Request) {
	v, _ := strconv.ParseInt(r.FormValue("version"), 10, 64)
	if err := c.be.RemoveLayerPermission(r.Context(), r.FormValue("layer"), v, r.FormValue("sid")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Permission removed")
	c.lambdaLayerVersion(w, r)
}

// lambdaLayersPartial re-renders the layers registry after a mutation.
func (c *Console) lambdaLayersPartial(w http.ResponseWriter, r *http.Request) {
	layers, err := c.be.ListLambdaLayers(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "lambda_layers", map[string]any{"Layers": layers})
}

// lambdaUpdateCode points the function at new code (UpdateFunctionCode).
func (c *Console) lambdaUpdateCode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	path := strings.TrimSpace(r.FormValue("path"))
	if path == "" {
		c.fail(w, fmt.Errorf("a code path is required"))
		return
	}
	if err := c.be.UpdateCode(r.Context(), name, path); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Code updated — the runner restarts on next invoke")
	c.lambdaConfigPartial(w, r, name)
}

// lambdaResetAsync discards the async invoke policy back to defaults
// (DeleteFunctionEventInvokeConfig).
func (c *Console) lambdaResetAsync(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	if err := c.be.ResetEventInvokeConfig(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Async invoke policy reset to defaults")
	c.lambdaConfigPartial(w, r, name)
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
		atoi(r.FormValue("timeout")), atoi(r.FormValue("memory")),
		strings.TrimSpace(r.FormValue("runtime")), strings.TrimSpace(r.FormValue("handler")),
		parseEnvRows(r)); err != nil {
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
