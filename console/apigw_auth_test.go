package console_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The API's Settings tab lists, adds, edits and deletes Lambda authorizers,
// and the add-method dialog offers them for a CUSTOM method.
func TestConsoleAPIGatewayAuthorizers(t *testing.T) {
	h := newConsole(t)
	loc := create(t, h, "/_console/apigw/create", url.Values{"name": {"gated"}})
	m := regexp.MustCompile(`/apigw/([a-z0-9]+)`).FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("create location %q", loc)
	}
	api := m[1]
	settings := req(t, h, "GET", "/_console/apigw/"+api+"?tab=settings", nil).Body.String()
	if !strings.Contains(settings, "No authorizers") {
		t.Fatalf("settings tab:\n%s", truncateBody(settings))
	}
	add := req(t, h, "POST", "/_console/apigw/"+api+"/create-authorizer", url.Values{
		"name": {"gate"}, "type": {"TOKEN"}, "function": {"gatekeeper"}, "source": {"X-Auth"}, "ttl": {"60"},
	})
	if add.Code != 200 || !strings.Contains(add.Body.String(), ">gate<") || !strings.Contains(add.Body.String(), "method.request.header.X-Auth") {
		t.Fatalf("create authorizer: %d\n%s", add.Code, add.Body)
	}
	id := regexp.MustCompile(`authorizer/([a-z0-9]+)"`).FindStringSubmatch(add.Body.String())
	if id == nil {
		t.Fatalf("no authorizer id in the panel:\n%s", add.Body)
	}
	detail := req(t, h, "GET", "/_console/apigw/"+api+"/authorizer/"+id[1], nil)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "gatekeeper") || !strings.Contains(detail.Body.String(), "60 s") {
		t.Fatalf("authorizer detail: %d\n%s", detail.Code, detail.Body)
	}
	upd := req(t, h, "POST", "/_console/apigw/"+api+"/update-authorizer", url.Values{"id": {id[1]}, "ttl": {"0"}})
	if upd.Code != 200 || !strings.Contains(upd.Body.String(), `value="0"`) {
		t.Fatalf("update authorizer: %d\n%s", upd.Code, upd.Body)
	}

	// The routes tab offers it in the add-method dialog; a CUSTOM method names it.
	routes := req(t, h, "GET", "/_console/apigw/"+api, nil).Body.String()
	if !strings.Contains(routes, "gate · TOKEN") {
		t.Fatalf("the add-method dialog does not offer the authorizer:\n%s", truncateBody(routes))
	}
	rootID := regexp.MustCompile(`addM={id:&#34;([a-z0-9]+)&#34;, path:&#34;/&#34;}`).FindStringSubmatch(routes)
	if rootID == nil {
		t.Fatalf("no root resource in the routes table:\n%s", truncateBody(routes))
	}
	put := req(t, h, "POST", "/_console/apigw/"+api+"/put-method", url.Values{
		"resource": {rootID[1]}, "verb": {"GET"}, "auth": {"CUSTOM"}, "authorizer": {id[1]},
	})
	if put.Code != 200 {
		t.Fatalf("put method: %d\n%s", put.Code, put.Body)
	}
	panel := req(t, h, "POST", "/_console/apigw/"+api+"/method", url.Values{"resource": {rootID[1]}, "verb": {"GET"}, "path": {"/"}})
	if !strings.Contains(panel.Body.String(), "CUSTOM") || !strings.Contains(panel.Body.String(), "authorizer "+id[1]) {
		t.Fatalf("method panel does not show the authorizer:\n%s", panel.Body)
	}

	del := req(t, h, "POST", "/_console/apigw/"+api+"/delete-authorizer", url.Values{"id": {id[1]}})
	if del.Code != 200 || !strings.Contains(del.Body.String(), "No authorizers") {
		t.Fatalf("delete authorizer: %d\n%s", del.Code, del.Body)
	}
}
