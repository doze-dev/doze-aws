package console

// API Gateway console handlers.

import "net/http"

func (c *Console) apigwList(w http.ResponseWriter, r *http.Request) {
	apis, err := c.be.ListRestAPIs(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "apigw_home", map[string]any{"List": apis, "Title": "API Gateway"})
}

func (c *Console) apigwAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	api, err := c.be.RestAPI(r.Context(), id)
	if err != nil {
		c.fail(w, err)
		return
	}
	apis, _ := c.be.ListRestAPIs(r.Context())
	routes, _ := c.be.APIRoutes(r.Context(), id)
	stages, _ := c.be.APIStages(r.Context(), id, endpointHost(r))

	c.render(w, r, "apigw_api", map[string]any{
		"API": api, "List": apis, "Routes": routes, "Stages": stages,
		"Tab": tabOf(r, "routes"), "Title": api.Name + " · API Gateway",
	})
}

// apigwInvoke sends a request through the deployed stage. Whether an
// integration actually answers is the one thing a definition cannot tell you.
func (c *Console) apigwInvoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	res, err := c.be.InvokeAPI(r.Context(), id,
		r.FormValue("stage"), r.FormValue("method"), r.FormValue("path"), r.FormValue("body"))
	if err != nil {
		// A transport failure is the answer here, not a page error: it is what
		// the caller would have seen.
		c.partial(w, "apigw_result", map[string]any{"Err": err.Error()})
		return
	}
	c.partial(w, "apigw_result", map[string]any{"Res": res})
}
