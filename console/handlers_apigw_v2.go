package console

// HTTP API (apigatewayv2) console handlers, at /apigw-http/{api}.

import (
	"net/http"
	"strconv"
	"strings"
)

func (c *Console) apigwHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	api, err := c.be.HTTPAPI(r.Context(), id)
	if err != nil {
		c.fail(w, err)
		return
	}
	apis, _ := c.be.ListAllAPIs(r.Context())
	tab := tabOf(r, "routes")
	data := map[string]any{
		"API": api, "List": apis, "Tab": tab, "Title": api.Name + " · HTTP API",
	}
	switch tab {
	case "stages":
		data["Stages"], _ = c.be.HTTPStages(r.Context(), id, endpointHost(r))
	case "invoke":
		data["Stages"], _ = c.be.HTTPStages(r.Context(), id, endpointHost(r))
	case "settings":
		data["Authorizers"], _ = c.be.HTTPAuthorizers(r.Context(), id)
		data["Functions"], _ = c.be.ListFunctions(r.Context())
	case "tags":
	default:
		data["Routes"], _ = c.be.HTTPRoutes(r.Context(), id)
		data["Authorizers"], _ = c.be.HTTPAuthorizers(r.Context(), id)
		data["Functions"], _ = c.be.ListFunctions(r.Context())
		data["Stages"], _ = c.be.HTTPStages(r.Context(), id, endpointHost(r))
	}
	c.render(w, r, "apigw_http", data)
}

func (c *Console) apigwHTTPCreate(w http.ResponseWriter, r *http.Request) {
	id, err := c.be.CreateHTTPAPI(r.Context(), strings.TrimSpace(r.FormValue("name")), r.FormValue("cors") == "on")
	if err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/apigw-http/"+id, "HTTP API created — add a route, and it answers at $default")
}

func (c *Console) apigwHTTPUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	var cors *HTTPCORS
	if origins := splitCSV(r.FormValue("cors_origins")); len(origins) > 0 {
		cors = &HTTPCORS{
			Origins: origins, Methods: splitCSV(r.FormValue("cors_methods")), Headers: splitCSV(r.FormValue("cors_headers")),
			Expose: splitCSV(r.FormValue("cors_expose")), Credentials: r.FormValue("cors_credentials") == "on",
		}
		cors.MaxAge, _ = strconv.Atoi(r.FormValue("cors_max_age"))
	}
	if err := c.be.UpdateHTTPAPI(r.Context(), id, strings.TrimSpace(r.FormValue("name")), r.FormValue("description"), cors); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw-http/"+id+"?tab=settings", "API settings saved")
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c *Console) apigwHTTPDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteHTTPAPI(r.Context(), r.PathValue("api")); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	c.redirect(w, r, c.prefix+"/apigw", "HTTP API deleted")
}

// apigwHTTPAddRoute adds a route: "METHOD /path" or $default, forwarding to
// a function or a URL, optionally behind an authorizer.
func (c *Console) apigwHTTPAddRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		method := strings.ToUpper(strings.TrimSpace(r.FormValue("method")))
		path := strings.TrimSpace(r.FormValue("path"))
		if path == "" || path == "$default" {
			key = "$default"
		} else {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			key = firstOf(method, "ANY") + " " + path
		}
	}
	if err := c.be.AddHTTPRoute(r.Context(), id, key, r.FormValue("lambda"), strings.TrimSpace(r.FormValue("url")),
		r.FormValue("payload"), r.FormValue("authorizer")); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	toast(w, "Route "+key+" added")
	c.apigwHTTPRoutesPartial(w, r, id)
}

func (c *Console) apigwHTTPDeleteRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteHTTPRoute(r.Context(), id, r.FormValue("route")); err != nil {
		c.fail(w, err)
		return
	}
	c.be.bustGraph()
	toast(w, "Route removed")
	c.apigwHTTPRoutesPartial(w, r, id)
}

func (c *Console) apigwHTTPRoutesPartial(w http.ResponseWriter, r *http.Request, id string) {
	api, err := c.be.HTTPAPI(r.Context(), id)
	if err != nil {
		c.fail(w, err)
		return
	}
	routes, _ := c.be.HTTPRoutes(r.Context(), id)
	auths, _ := c.be.HTTPAuthorizers(r.Context(), id)
	fns, _ := c.be.ListFunctions(r.Context())
	stages, _ := c.be.HTTPStages(r.Context(), id, endpointHost(r))
	c.partial(w, "apigw_http_routes", map[string]any{
		"API": api, "Routes": routes, "Authorizers": auths, "Functions": fns, "Stages": stages, "Prefix": c.prefix,
	})
}

func (c *Console) apigwHTTPInvoke(w http.ResponseWriter, r *http.Request) {
	res, err := c.be.InvokeHTTPAPI(r.Context(), r.PathValue("api"),
		r.FormValue("stage"), r.FormValue("method"), r.FormValue("path"), r.FormValue("body"))
	if err != nil {
		c.partial(w, "apigw_result", map[string]any{"Err": err.Error()})
		return
	}
	c.partial(w, "apigw_result", map[string]any{"Res": res})
}

func (c *Console) apigwHTTPCreateStage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "$default"
	}
	if err := c.be.CreateHTTPStage(r.Context(), id, name, r.FormValue("auto_deploy") != "off"); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw-http/"+id+"?tab=stages", "Stage "+name+" created")
}

func (c *Console) apigwHTTPDeleteStage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteHTTPStage(r.Context(), id, r.FormValue("name")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw-http/"+id+"?tab=stages", "Stage deleted")
}

func (c *Console) apigwHTTPDeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeployHTTPStage(r.Context(), id, r.FormValue("stage")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw-http/"+id+"?tab=stages", "Deployed to "+r.FormValue("stage"))
}

func (c *Console) apigwHTTPCreateAuthorizer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	ttl, _ := strconv.Atoi(r.FormValue("ttl"))
	if err := c.be.CreateHTTPAuthorizer(r.Context(), id, strings.TrimSpace(r.FormValue("name")), r.FormValue("lambda"),
		strings.TrimSpace(r.FormValue("header")), ttl); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw-http/"+id+"?tab=settings", "Authorizer created")
}

func (c *Console) apigwHTTPDeleteAuthorizer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteHTTPAuthorizer(r.Context(), id, r.FormValue("id")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/apigw-http/"+id+"?tab=settings", "Authorizer deleted")
}
