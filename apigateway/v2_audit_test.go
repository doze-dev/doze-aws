package apigateway

// Regressions from the post-batch audit of the HTTP API surface: each test
// here reproduces a bug the review found and pins the fix.

import (
	"net/url"
	"strings"
	"testing"
)

// The target quick create (aws apigatewayv2 create-api --target) panicked
// on the record's omitted maps.
func TestV2QuickCreateMakesRouteStageAndAnswers(t *testing.T) {
	fl := &v2FakeLambda{t: t}
	fl.fn = func(ev map[string]any) any { return map[string]any{"got": ev["rawPath"]} }
	a := v2Server(t, fl.handler())
	api := a.must("POST", "/v2/apis", map[string]any{"name": "quick", "protocolType": "HTTP", "target": fnARN, "routeKey": "get /hello"})
	id := api["apiId"].(string)
	routes := a.must("GET", "/v2/apis/"+id+"/routes", nil)["items"].([]any)
	if len(routes) != 1 || routes[0].(map[string]any)["routeKey"] != "GET /hello" {
		t.Fatalf("quick create routes: %v", routes)
	}
	st := a.must("GET", "/v2/apis/"+id+"/stages/$default", nil)
	// viewV2Stage omits deploymentId when it is empty, so it comes back nil
	// rather than "" — the old `st["deploymentId"] == ""` could never fire and
	// a quick create that produced no deployment would have passed.
	if st["autoDeploy"] != true || st["deploymentId"] == nil || st["deploymentId"] == "" {
		t.Fatalf("quick create stage: %v", st)
	}
	if code, _, body := a.invoke("GET", id+"/hello", nil, ""); code != 200 || !strings.Contains(body, "/hello") {
		t.Fatalf("quick-created API does not answer: %d %s", code, body)
	}
	// A refused CreateApi creates nothing.
	if code, _ := a.do("POST", "/v2/apis", map[string]any{"name": "bad", "protocolType": "HTTP", "corsConfiguration": map[string]any{"allowOrigins": []string{"*"}, "allowCredentials": true}}); code != 400 {
		t.Fatalf("* with credentials must be refused: %d", code)
	}
	if n := len(a.must("GET", "/v2/apis", nil)["items"].([]any)); n != 1 {
		t.Fatalf("a refused CreateApi left an API behind: %d listed", n)
	}
}

// DeleteRouteSettings takes a percent-encoded key with a slash in it, which
// the decoded path split apart.
func TestV2DeleteRouteSettingsByEncodedKey(t *testing.T) {
	a := v2Server(t, nil)
	id := a.must("POST", "/v2/apis", map[string]any{"name": "rs", "protocolType": "HTTP"})["apiId"].(string)
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "dev",
		"routeSettings": map[string]any{"GET /items/{id}": map[string]any{"throttlingBurstLimit": 5}}})
	if code, m := a.do("DELETE", "/v2/apis/"+id+"/stages/dev/routesettings/"+url.PathEscape("POST /nothing"), nil); code != 404 {
		t.Fatalf("an unknown key must be 404: %d %v", code, m)
	}
	if code, m := a.do("DELETE", "/v2/apis/"+id+"/stages/dev/routesettings/"+url.PathEscape("GET /items/{id}"), nil); code != 204 {
		t.Fatalf("delete: %d %v", code, m)
	}
	st := a.must("GET", "/v2/apis/"+id+"/stages/dev", nil)
	if rs := st["routeSettings"].(map[string]any); len(rs) != 0 {
		t.Fatalf("the setting survived: %v", rs)
	}
}

// Two proxy routes at the same depth picked whichever the map yielded first;
// a literal segment must beat a parameter, and a key is one route in any
// spelling.
func TestV2ProxyTieBreakAndKeySpelling(t *testing.T) {
	a := v2Server(t, nil)
	backend := echoBackend(t)
	id := a.must("POST", "/v2/apis", map[string]any{"name": "tie", "protocolType": "HTTP"})["apiId"].(string)
	mk := func(uri string) string {
		return "integrations/" + a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "HTTP_PROXY", "integrationUri": uri})["integrationId"].(string)
	}
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "GET /{x}/{proxy+}", "target": mk(backend.URL + "/param")})
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "get /a/{proxy+}", "target": mk(backend.URL + "/literal")})
	if code, m := a.do("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "GET /a/{proxy+}", "target": mk(backend.URL + "/dup")}); code != 409 {
		t.Fatalf("the same key in another spelling must conflict: %d %v", code, m)
	}
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "$default", "autoDeploy": true})
	for i := 0; i < 30; i++ {
		if _, _, body := a.invoke("GET", id+"/a/b/c", nil, ""); !strings.HasPrefix(body, "GET /literal") {
			t.Fatalf("try %d: the literal proxy route must win: %q", i, body)
		}
	}
}

// The event carries the stage in the path for a named stage and not for
// $default, in both payload formats.
func TestV2EventPathsCarryTheStage(t *testing.T) {
	fl := &v2FakeLambda{t: t}
	fl.fn = func(map[string]any) any { return map[string]any{"statusCode": 200} }
	a := v2Server(t, fl.handler())
	id := a.must("POST", "/v2/apis", map[string]any{"name": "paths", "protocolType": "HTTP"})["apiId"].(string)
	v2 := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "AWS_PROXY", "integrationUri": fnARN, "payloadFormatVersion": "2.0"})
	v1 := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "AWS_PROXY", "integrationUri": fnARN, "payloadFormatVersion": "1.0"})
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "GET /two", "target": "integrations/" + v2["integrationId"].(string)})
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "GET /one", "target": "integrations/" + v1["integrationId"].(string)})
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "$default", "autoDeploy": true})
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "dev", "autoDeploy": true})

	a.invoke("GET", id+"/two", nil, "")
	if fl.last["rawPath"] != "/two" || fl.last["requestContext"].(map[string]any)["http"].(map[string]any)["path"] != "/two" {
		t.Fatalf("$default 2.0 paths: %v", fl.last)
	}
	a.invoke("GET", id+"/dev/two", nil, "")
	if fl.last["rawPath"] != "/dev/two" || fl.last["requestContext"].(map[string]any)["http"].(map[string]any)["path"] != "/dev/two" {
		t.Fatalf("named stage 2.0 paths: %v", fl.last)
	}
	a.invoke("GET", id+"/one", nil, "")
	if fl.last["path"] != "/one" || fl.last["requestContext"].(map[string]any)["path"] != "/one" {
		t.Fatalf("$default 1.0 paths: %v", fl.last)
	}
	a.invoke("GET", id+"/dev/one", nil, "")
	if fl.last["path"] != "/one" || fl.last["requestContext"].(map[string]any)["path"] != "/dev/one" {
		t.Fatalf("named stage 1.0 paths: %v", fl.last)
	}
}

// CORS: "*" is answered literally, a preflight the configuration does not
// admit is routed, and a stage nothing was deployed to answers 404.
func TestV2CORSStarAndUnmatchedPreflightAndUndeployedStage(t *testing.T) {
	fl := &v2FakeLambda{t: t}
	fl.fn = func(ev map[string]any) any {
		return map[string]any{"statusCode": 200, "body": "from route " + ev["routeKey"].(string)}
	}
	a := v2Server(t, fl.handler())
	id := a.must("POST", "/v2/apis", map[string]any{"name": "cors2", "protocolType": "HTTP", "corsConfiguration": map[string]any{
		"allowOrigins": []string{"*"}, "allowMethods": []string{"GET"},
	}})["apiId"].(string)
	integ := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "AWS_PROXY", "integrationUri": fnARN})
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "OPTIONS /opt", "target": "integrations/" + integ["integrationId"].(string)})
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "manual"})
	if code, _, _ := a.invoke("GET", id+"/manual/opt", nil, ""); code != 404 {
		t.Fatalf("a stage with no deployment must answer 404: %d", code)
	}
	a.must("POST", "/v2/apis/"+id+"/deployments", map[string]any{"stageName": "manual"})

	_, h, _ := a.invoke("OPTIONS", id+"/manual/opt", map[string]string{"Origin": "https://x.example", "Access-Control-Request-Method": "GET"}, "")
	if h.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("a * configuration answers * literally, got %q", h.Get("Access-Control-Allow-Origin"))
	}
	// The method is not allowed by the configuration: the OPTIONS route
	// answers instead of a bare 204.
	code, _, body := a.invoke("OPTIONS", id+"/manual/opt", map[string]string{"Origin": "https://x.example", "Access-Control-Request-Method": "POST"}, "")
	if code != 200 || body != "from route OPTIONS /opt" {
		t.Fatalf("an unmatched preflight must be routed: %d %q", code, body)
	}
	// Updating to a credentialed * is refused, and CORS is unchanged.
	if code, _ := a.do("PATCH", "/v2/apis/"+id, map[string]any{"corsConfiguration": map[string]any{"allowOrigins": []string{"*"}, "allowCredentials": true}}); code != 400 {
		t.Fatalf("* with credentials on update: %d", code)
	}
}

// Every auto-deploy makes a deployment; the record keeps a bounded history.
func TestV2AutoDeploymentsArePruned(t *testing.T) {
	a := v2Server(t, nil)
	id := a.must("POST", "/v2/apis", map[string]any{"name": "many", "protocolType": "HTTP"})["apiId"].(string)
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "$default", "autoDeploy": true})
	for i := 0; i < 30; i++ {
		a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "HTTP_PROXY", "integrationUri": "http://127.0.0.1:1/"})
	}
	deps := a.must("GET", "/v2/apis/"+id+"/deployments", nil)["items"].([]any)
	if len(deps) > keptAutoDeployments+1 {
		t.Fatalf("%d deployments kept, want at most %d plus the stage's", len(deps), keptAutoDeployments)
	}
	st := a.must("GET", "/v2/apis/"+id+"/stages/$default", nil)
	if code, _ := a.do("GET", "/v2/apis/"+id+"/deployments/"+st["deploymentId"].(string), nil); code != 200 {
		t.Fatalf("the stage's deployment was pruned: %d", code)
	}
}

// A v1 tag ARN for an HTTP API, or a v2 one for a REST API, names nothing.
func TestV2TagARNsDoNotCross(t *testing.T) {
	a := v2Server(t, nil)
	id := a.must("POST", "/v2/apis", map[string]any{"name": "tagged", "protocolType": "HTTP", "tags": map[string]string{"k": "v"}})["apiId"].(string)
	if code, _ := a.do("GET", "/tags/"+url.PathEscape(APIARN(id)), nil); code != 404 {
		t.Fatalf("v1 ARN on an HTTP API: %d", code)
	}
	if m := a.must("GET", "/v2/tags/"+url.PathEscape(V2APIARN(id)), nil); m["tags"].(map[string]any)["k"] != "v" {
		t.Fatalf("v2 tags: %v", m)
	}
	rest := a.must("POST", "/restapis", map[string]any{"name": "rest"})["id"].(string)
	if code, _ := a.do("GET", "/v2/tags/"+url.PathEscape(V2APIARN(rest)), nil); code != 404 {
		t.Fatalf("v2 ARN on a REST API: %d", code)
	}
}
