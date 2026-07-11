package console

import "net/http"

func (c *Console) lambdaFns(w http.ResponseWriter, r *http.Request) {
	fns, err := c.be.ListFunctions(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "lambda_fns", map[string]any{"Functions": fns})
}

func (c *Console) lambdaFn(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	f, err := c.be.GetFunction(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "lambda_fn", map[string]any{"Fn": f, "Tab": tabOf(r, "invoke")})
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
