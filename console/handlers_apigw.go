package console

// API Gateway console handlers.

import (
	"net/http"
	"strings"
)

func (c *Console) apigwList(w http.ResponseWriter, r *http.Request) {
	apis, err := c.be.ListAllAPIs(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	if len(apis) > 0 {
		r.SetPathValue("api", apis[0].ID)
		if apis[0].Protocol == "HTTP" {
			c.apigwHTTP(w, r)
			return
		}
		c.apigwAPI(w, r)
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
	apis, _ := c.be.ListAllAPIs(r.Context())
	tab := tabOf(r, "routes")
	data := map[string]any{
		"API": api, "List": apis, "Tab": tab, "Title": api.Name + " · API Gateway",
	}
	// Each tab costs its own calls, so only the one being shown makes them.
	switch tab {
	case "stages":
		data["Stages"], _ = c.be.APIStages(r.Context(), id, endpointHost(r))
		data["Deployments"], _ = c.be.APIDeployments(r.Context(), id)
	case "invoke":
		data["Stages"], _ = c.be.APIStages(r.Context(), id, endpointHost(r))
	case "settings":
		data["Authorizers"], _ = c.be.APIAuthorizers(r.Context(), id)
		data["Functions"], _ = c.be.ListFunctions(r.Context())
	case "tags":
	default:
		data["Routes"], _ = c.be.APIRoutes(r.Context(), id)
		data["Authorizers"], _ = c.be.APIAuthorizers(r.Context(), id)
	}
	c.render(w, r, "apigw_api", data)
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

// ---- the build-an-API path: resources, methods, integrations, deploys ----

// apigwRoutesPartial re-renders the editable route tree after a mutation.
func (c *Console) apigwRoutesPartial(w http.ResponseWriter, r *http.Request, apiID string) {
	routes, err := c.be.APIRoutes(r.Context(), apiID)
	if err != nil {
		c.fail(w, err)
		return
	}
	api, _ := c.be.RestAPI(r.Context(), apiID)
	c.partial(w, "apigw_routes", map[string]any{"Routes": routes, "API": api})
}

func (c *Console) apigwCreate(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("protocol") == "HTTP" {
		c.apigwHTTPCreate(w, r)
		return
	}
	id, err := c.be.CreateRestAPI(r.Context(), strings.TrimSpace(r.FormValue("name")), r.FormValue("description"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/apigw/"+id, "API created — add resources and methods, then deploy")
}

func (c *Console) apigwUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	ops := map[string]string{}
	if v := strings.TrimSpace(r.FormValue("name")); v != "" {
		ops["/name"] = v
	}
	ops["/description"] = r.FormValue("description")
	if err := c.be.UpdateRestAPI(r.Context(), id, ops); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw/"+id+"?tab=settings", "API settings saved")
}

func (c *Console) apigwDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteRestAPI(r.Context(), id); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/apigw", "API deleted")
}

func (c *Console) apigwAddResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	part := strings.Trim(strings.TrimSpace(r.FormValue("part")), "/")
	if err := c.be.CreateAPIResource(r.Context(), id, r.FormValue("parent"), part); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Resource /"+part+" added")
	c.apigwRoutesPartial(w, r, id)
}

func (c *Console) apigwDeleteResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteAPIResource(r.Context(), id, r.FormValue("resource")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Resource deleted")
	c.apigwRoutesPartial(w, r, id)
}

func (c *Console) apigwRenameResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	part := strings.Trim(strings.TrimSpace(r.FormValue("part")), "/")
	if err := c.be.RenameAPIResource(r.Context(), id, r.FormValue("resource"), part); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Resource renamed")
	c.apigwRoutesPartial(w, r, id)
}

func (c *Console) apigwPutMethod(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	verb := r.FormValue("verb")
	if err := c.be.PutAPIMethod(r.Context(), id, r.FormValue("resource"), verb,
		r.FormValue("auth"), r.FormValue("authorizer"), r.FormValue("apikey") != ""); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, verb+" added — wire an integration next")
	c.apigwRoutesPartial(w, r, id)
}

func (c *Console) apigwDeleteMethod(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteAPIMethod(r.Context(), id, r.FormValue("resource"), r.FormValue("verb")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Method deleted")
	c.apigwRoutesPartial(w, r, id)
}

// apigwMethodPanel renders one method in full — integration, both response
// halves — with the forms to edit each.
func (c *Console) apigwMethodPanel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	d, err := c.be.APIMethodDetail(r.Context(), id, r.FormValue("resource"), r.FormValue("verb"))
	if err != nil {
		c.fail(w, err)
		return
	}
	d.Path = r.FormValue("path")
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "apigw_method", map[string]any{"API": map[string]any{"ID": id}, "M": d, "Functions": fns})
}

func (c *Console) apigwPutIntegration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.PutAPIIntegration(r.Context(), id, r.FormValue("resource"), r.FormValue("verb"),
		r.FormValue("type"), strings.TrimSpace(r.FormValue("target"))); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	toast(w, "Integration saved")
	c.apigwMethodPanel(w, r)
}

func (c *Console) apigwDeleteIntegration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteAPIIntegration(r.Context(), id, r.FormValue("resource"), r.FormValue("verb")); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	toast(w, "Integration removed — the method now has no backend")
	c.apigwMethodPanel(w, r)
}

// apigwPutResponse and apigwDeleteResponse cover both halves of the response
// contract; the "half" form field picks method vs integration.
func (c *Console) apigwPutResponse(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	res, verb, status := r.FormValue("resource"), r.FormValue("verb"), strings.TrimSpace(r.FormValue("status"))
	var err error
	if r.FormValue("half") == "integration" {
		err = c.be.PutAPIIntegrationResponse(r.Context(), id, res, verb, status, r.FormValue("template"))
	} else {
		err = c.be.PutAPIMethodResponse(r.Context(), id, res, verb, status)
	}
	if err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Response "+status+" declared")
	c.apigwMethodPanel(w, r)
}

func (c *Console) apigwDeleteResponse(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	res, verb, status := r.FormValue("resource"), r.FormValue("verb"), r.FormValue("status")
	var err error
	if r.FormValue("half") == "integration" {
		err = c.be.DeleteAPIIntegrationResponse(r.Context(), id, res, verb, status)
	} else {
		err = c.be.DeleteAPIMethodResponse(r.Context(), id, res, verb, status)
	}
	if err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Response "+status+" removed")
	c.apigwMethodPanel(w, r)
}

func (c *Console) apigwDeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	stage := strings.TrimSpace(r.FormValue("stage"))
	if err := c.be.CreateAPIDeployment(r.Context(), id, stage, r.FormValue("description")); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/apigw/"+id+"?tab=stages", "Deployed to "+stage+" — the routes are live")
}

func (c *Console) apigwDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteAPIDeployment(r.Context(), id, r.FormValue("deployment")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw/"+id+"?tab=stages", "Deployment deleted")
}

func (c *Console) apigwCreateStage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	name := strings.TrimSpace(r.FormValue("name"))
	if err := c.be.CreateAPIStage(r.Context(), id, name, r.FormValue("deployment"), r.FormValue("description")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw/"+id+"?tab=stages", "Stage "+name+" created")
}

func (c *Console) apigwUpdateStage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	name := r.FormValue("name")
	if err := c.be.UpdateAPIStage(r.Context(), id, name,
		map[string]string{"/deploymentId": r.FormValue("deployment")}); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw/"+id+"?tab=stages", "Stage "+name+" repointed")
}

func (c *Console) apigwDeleteStage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	name := r.FormValue("name")
	if err := c.be.DeleteAPIStage(r.Context(), id, name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw/"+id+"?tab=stages", "Stage "+name+" deleted")
}
