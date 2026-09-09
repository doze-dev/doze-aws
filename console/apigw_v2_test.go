package console_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// An HTTP API from the create page: it appears in the sidebar with its
// badge, its routes tab adds and removes a route, the stages tab creates
// $default, invoke reaches the route, and settings edits CORS and adds an
// authorizer.
func TestConsoleHTTPAPI(t *testing.T) {
	h := newConsole(t)
	loc := create(t, h, "/_console/apigw/create", url.Values{"name": {"shop-http"}, "protocol": {"HTTP"}, "cors": {"on"}})
	m := regexp.MustCompile(`/apigw-http/([a-z0-9]+)`).FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("create location %q", loc)
	}
	api := m[1]

	page := req(t, h, "GET", "/_console/apigw-http/"+api, nil)
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, "shop-http") || !strings.Contains(body, "CORS on") || !strings.Contains(body, "No routes") {
		t.Fatalf("http api page: %d\n%s", page.Code, truncateBody(body))
	}
	if !strings.Contains(body, `title="HTTP API (apigatewayv2)">HTTP</span>`) {
		t.Fatalf("the sidebar does not badge the HTTP API:\n%s", truncateBody(body))
	}

	// A URL route, then a Lambda route.
	add := req(t, h, "POST", "/_console/apigw-http/"+api+"/add-route", url.Values{
		"method": {"GET"}, "path": {"/items/{id}"}, "url": {"http://127.0.0.1:1/{proxy}"},
	})
	if add.Code != 200 || !strings.Contains(add.Body.String(), "/items/{id}") || !strings.Contains(add.Body.String(), "HTTP_PROXY") {
		t.Fatalf("add URL route: %d\n%s", add.Code, add.Body)
	}
	add = req(t, h, "POST", "/_console/apigw-http/"+api+"/add-route", url.Values{"lambda": {"handler"}, "payload": {"1.0"}})
	if add.Code != 200 || !strings.Contains(add.Body.String(), "$default") || !strings.Contains(add.Body.String(), ">handler<") {
		t.Fatalf("add $default Lambda route: %d\n%s", add.Code, add.Body)
	}
	routeID := regexp.MustCompile(`hx-vals='\{"route":"([a-z0-9]+)"\}'`).FindStringSubmatch(add.Body.String())
	if routeID == nil {
		t.Fatalf("no route id in the table:\n%s", add.Body)
	}
	if bad := req(t, h, "POST", "/_console/apigw-http/"+api+"/add-route", url.Values{"method": {"GET"}, "path": {"/items/{id}"}, "lambda": {"handler"}}); bad.Code == 200 && !strings.Contains(bad.Body.String(), "already exists") {
		t.Fatalf("a duplicate route key must be refused: %d\n%s", bad.Code, truncateBody(bad.Body.String()))
	}

	// Stages: create $default, then invoke the URL route (the backend is
	// unreachable, which proves the route was taken: a 502, not a 404).
	stage := req(t, h, "POST", "/_console/apigw-http/"+api+"/create-stage", url.Values{"name": {""}, "auto_deploy": {"on"}})
	if stage.Code != 303 && stage.Code != 200 {
		t.Fatalf("create stage: %d\n%s", stage.Code, stage.Body)
	}
	stages := req(t, h, "GET", "/_console/apigw-http/"+api+"?tab=stages", nil).Body.String()
	if !strings.Contains(stages, "$default") || !strings.Contains(stages, "/_aws/execute-api/"+api) {
		t.Fatalf("stages tab:\n%s", truncateBody(stages))
	}
	res := req(t, h, "POST", "/_console/apigw-http/"+api+"/invoke", url.Values{"stage": {"$default"}, "method": {"GET"}, "path": {"/items/7"}})
	if res.Code != 200 || !strings.Contains(res.Body.String(), "502") {
		t.Fatalf("invoke through the console: %d\n%s", res.Code, truncateBody(res.Body.String()))
	}

	// Settings: CORS round-trips, an authorizer is added and offered.
	set := req(t, h, "POST", "/_console/apigw-http/"+api+"/update", url.Values{
		"name": {"shop-http"}, "cors_origins": {"https://app.example"}, "cors_methods": {"GET"}, "cors_max_age": {"120"},
	})
	if set.Code != 303 && set.Code != 200 {
		t.Fatalf("update: %d\n%s", set.Code, set.Body)
	}
	settings := req(t, h, "GET", "/_console/apigw-http/"+api+"?tab=settings", nil).Body.String()
	if !strings.Contains(settings, `value="https://app.example"`) || !strings.Contains(settings, `value="120"`) {
		t.Fatalf("settings tab after CORS update:\n%s", truncateBody(settings))
	}
	auth := req(t, h, "POST", "/_console/apigw-http/"+api+"/create-authorizer", url.Values{"name": {"gate"}, "lambda": {"gatekeeper"}, "header": {"X-Auth"}, "ttl": {"30"}})
	if auth.Code != 303 && auth.Code != 200 {
		t.Fatalf("create authorizer: %d\n%s", auth.Code, auth.Body)
	}
	settings = req(t, h, "GET", "/_console/apigw-http/"+api+"?tab=settings", nil).Body.String()
	if !strings.Contains(settings, ">gate<") || !strings.Contains(settings, "$request.header.X-Auth") || !strings.Contains(settings, "30s") {
		t.Fatalf("settings tab after adding an authorizer:\n%s", truncateBody(settings))
	}
	routes := req(t, h, "GET", "/_console/apigw-http/"+api, nil).Body.String()
	if !strings.Contains(routes, `<option value="`) || !strings.Contains(routes, ">gate<") {
		t.Fatalf("the add-route form does not offer the authorizer:\n%s", truncateBody(routes))
	}

	// Delete the route; its integration goes with it.
	del := req(t, h, "POST", "/_console/apigw-http/"+api+"/delete-route", url.Values{"route": {routeID[1]}})
	if del.Code != 200 || strings.Contains(del.Body.String(), ">handler<") {
		t.Fatalf("delete route: %d\n%s", del.Code, truncateBody(del.Body.String()))
	}
	gone := req(t, h, "POST", "/_console/apigw-http/"+api+"/delete", nil)
	if gone.Code != 303 && gone.Code != 200 {
		t.Fatalf("delete api: %d", gone.Code)
	}
	if list := req(t, h, "GET", "/_console/apigw", nil).Body.String(); strings.Contains(list, "shop-http") {
		t.Fatal("the deleted HTTP API is still listed")
	}
}
