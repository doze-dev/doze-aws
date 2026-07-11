package console

import (
	"net/http"
	"strconv"
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
	c.render(w, r, "lambda_fn", map[string]any{
		"Fn": f, "Tab": tabOf(r, "invoke"), "List": fns, "Title": name + " · Lambda",
		"Conn": c.be.Neighbors(r.Context(), "lambda", name),
		"RT":   rt, "RTHash": lambdaRuntimeHash(rt),
	})
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
	res, err := c.be.Invoke(r.Context(), name, payload)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "lambda_result", map[string]any{"Res": res, "Fn": name})
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
	c.partial(w, "lambda_triggers", map[string]any{"Fn": f})
}
