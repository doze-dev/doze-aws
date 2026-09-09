package apigateway

// The HTTP API data plane, driven over the wire: routes and their
// precedence, the $default stage at the root, CORS, an HTTP proxy backend,
// a Lambda backend in both payload formats, and a REQUEST authorizer —
// with an in-process fake Lambda standing in for the function.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/peers"
)

// v2API drives the control plane for a test: JSON in, decoded JSON out.
type v2API struct {
	t  *testing.T
	ts *httptest.Server
}

func (a v2API) do(method, path string, body any) (int, map[string]any) {
	a.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, a.ts.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/apigateway/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(out, &m)
	if m == nil {
		m = map[string]any{"_raw": string(out)}
	}
	return resp.StatusCode, m
}

func (a v2API) must(method, path string, body any) map[string]any {
	a.t.Helper()
	code, m := a.do(method, path, body)
	if code < 200 || code > 299 {
		a.t.Fatalf("%s %s = %d: %v", method, path, code, m)
	}
	return m
}

// invoke calls the data plane.
func (a v2API) invoke(method, path string, headers map[string]string, body string) (int, http.Header, string) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.ts.URL+ExecutePrefix+path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(out)
}

// echoBackend is an HTTP_PROXY target that reports what it received.
func echoBackend(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Backend", "echo")
		fmt.Fprintf(w, "%s %s?%s body=%s", r.Method, r.URL.Path, r.URL.RawQuery, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// v2FakeLambda answers Invoke for any function with what fn returns, and
// records the last event it saw.
type v2FakeLambda struct {
	t    *testing.T
	last map[string]any
	fn   func(event map[string]any) any
}

func (f *v2FakeLambda) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev map[string]any
		json.Unmarshal(body, &ev)
		f.last = ev
		out, _ := json.Marshal(f.fn(ev))
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	})
}

func v2Server(t *testing.T, lambda http.Handler) v2API {
	t.Helper()
	dir := peers.InProcess(func(service string) http.Handler {
		if service == "lambda" && lambda != nil {
			return lambda
		}
		return nil
	})
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf, Peers: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return v2API{t: t, ts: ts}
}

const fnARN = "arn:aws:lambda:us-east-1:000000000000:function:handler"

func TestV2HTTPProxyRoutesAndPrecedence(t *testing.T) {
	a := v2Server(t, nil)
	backend := echoBackend(t)

	api := a.must("POST", "/v2/apis", map[string]any{"name": "shop", "protocolType": "HTTP"})
	id := api["apiId"].(string)
	if !strings.HasSuffix(api["apiEndpoint"].(string), ExecutePrefix+id) {
		t.Fatalf("apiEndpoint: %v", api["apiEndpoint"])
	}
	integ := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{
		"integrationType": "HTTP_PROXY", "integrationUri": backend.URL + "/{proxy}", "integrationMethod": "ANY",
	})
	target := "integrations/" + integ["integrationId"].(string)
	items := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{
		"integrationType": "HTTP_PROXY", "integrationUri": backend.URL + "/items", "integrationMethod": "GET",
	})
	me := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{
		"integrationType": "HTTP_PROXY", "integrationUri": backend.URL + "/me", "integrationMethod": "POST",
	})
	for _, rt := range []map[string]any{
		{"routeKey": "ANY /{proxy+}", "target": target},
		{"routeKey": "GET /items/{id}", "target": "integrations/" + items["integrationId"].(string)},
		{"routeKey": "GET /items/me", "target": "integrations/" + me["integrationId"].(string)},
	} {
		a.must("POST", "/v2/apis/"+id+"/routes", rt)
	}
	// No stage yet: nothing answers.
	if code, _, _ := a.invoke("GET", id+"/items/1", nil, ""); code != 404 {
		t.Fatalf("no stage: %d", code)
	}
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "$default", "autoDeploy": true})

	// A literal beats a parameter, a parameter beats the proxy, the method
	// narrows, and the $default stage is at the root.
	code, h, body := a.invoke("GET", id+"/items/me", nil, "")
	if code != 200 || !strings.HasPrefix(body, "POST /me") || h.Get("X-Backend") != "echo" {
		t.Fatalf("literal route: %d %q %v", code, body, h)
	}
	if _, _, body := a.invoke("GET", id+"/items/42?x=1", nil, ""); !strings.HasPrefix(body, "GET /items?x=1") {
		t.Fatalf("param route: %q", body)
	}
	if _, _, body := a.invoke("PUT", id+"/anything/deep", nil, "hi"); !strings.HasPrefix(body, "PUT /anything/deep?") || !strings.Contains(body, "body=hi") {
		t.Fatalf("proxy route with {proxy} expansion: %q", body)
	}
	// The stage spelled out, and a named stage.
	if _, _, body := a.invoke("GET", id+"/$default/items/me", nil, ""); !strings.HasPrefix(body, "POST /me") {
		t.Fatalf("$default spelled out: %q", body)
	}
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "beta", "autoDeploy": true})
	if _, _, body := a.invoke("GET", id+"/beta/items/me", nil, ""); !strings.HasPrefix(body, "POST /me") {
		t.Fatalf("named stage: %q", body)
	}
	// A REST client does not see the HTTP API, and vice versa.
	if code, _ := a.do("GET", "/restapis/"+id, nil); code != 404 {
		t.Fatalf("v1 GetRestApi on an HTTP API: %d", code)
	}
	rest := a.must("POST", "/restapis", map[string]any{"name": "rest"})
	if code, _ := a.do("GET", "/v2/apis/"+rest["id"].(string), nil); code != 404 {
		t.Fatalf("v2 GetApi on a REST API: %d", code)
	}
	list := a.must("GET", "/v2/apis", nil)
	if n := len(list["items"].([]any)); n != 1 {
		t.Fatalf("GetApis lists %d, want the one HTTP API", n)
	}
	// Auto-deploy made a deployment per change; the stage points at the last.
	deps := a.must("GET", "/v2/apis/"+id+"/deployments", nil)
	if len(deps["items"].([]any)) == 0 {
		t.Fatal("autoDeploy made no deployment")
	}
	st := a.must("GET", "/v2/apis/"+id+"/stages/$default", nil)
	if st["deploymentId"] == nil || st["deploymentId"] == "" {
		t.Fatalf("stage has no deployment: %v", st)
	}
}

func TestV2LambdaPayloadFormats(t *testing.T) {
	fl := &v2FakeLambda{t: t}
	fl.fn = func(ev map[string]any) any {
		return map[string]any{"statusCode": 201, "headers": map[string]string{"X-From": "lambda"}, "body": "made"}
	}
	a := v2Server(t, fl.handler())
	api := a.must("POST", "/v2/apis", map[string]any{"name": "fn", "protocolType": "HTTP"})
	id := api["apiId"].(string)
	v2 := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "AWS_PROXY", "integrationUri": fnARN, "payloadFormatVersion": "2.0"})
	v1 := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "AWS_PROXY", "integrationUri": fnARN, "payloadFormatVersion": "1.0"})
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "POST /two/{id}", "target": "integrations/" + v2["integrationId"].(string)})
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "POST /one/{id}", "target": "integrations/" + v1["integrationId"].(string)})
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "$default", "autoDeploy": true, "stageVariables": map[string]string{"env": "test"}})

	code, h, body := a.invoke("POST", id+"/two/7?q=1", map[string]string{"Cookie": "a=1; b=2", "X-Test": "yes"}, `{"n":1}`)
	if code != 201 || body != "made" || h.Get("X-From") != "lambda" {
		t.Fatalf("2.0 response: %d %q %v", code, body, h)
	}
	ev := fl.last
	if ev["version"] != "2.0" || ev["routeKey"] != "POST /two/{id}" || ev["rawPath"] != "/two/7" || ev["rawQueryString"] != "q=1" {
		t.Fatalf("2.0 event: %v", ev)
	}
	if ev["pathParameters"].(map[string]any)["id"] != "7" || ev["stageVariables"].(map[string]any)["env"] != "test" {
		t.Fatalf("2.0 params: %v", ev)
	}
	if cookies := ev["cookies"].([]any); len(cookies) != 2 || cookies[0] != "a=1" {
		t.Fatalf("2.0 cookies: %v", ev["cookies"])
	}
	rc := ev["requestContext"].(map[string]any)
	if rc["stage"] != "$default" || rc["apiId"] != id || rc["http"].(map[string]any)["method"] != "POST" {
		t.Fatalf("2.0 requestContext: %v", rc)
	}
	if ev["body"] != `{"n":1}` || ev["headers"].(map[string]any)["x-test"] != "yes" {
		t.Fatalf("2.0 body/headers: %v", ev)
	}

	// A bare value from the function is a 200 JSON body under 2.0.
	fl.fn = func(map[string]any) any { return map[string]any{"ok": true} }
	if code, h, body := a.invoke("POST", id+"/two/8", nil, ""); code != 200 || body != `{"ok":true}` || h.Get("Content-Type") != "application/json" {
		t.Fatalf("2.0 inferred response: %d %q %v", code, body, h)
	}

	fl.fn = func(ev map[string]any) any {
		return map[string]any{"statusCode": 202, "body": "v1"}
	}
	code, _, body = a.invoke("POST", id+"/one/9", nil, "x")
	if code != 202 || body != "v1" {
		t.Fatalf("1.0 response: %d %q", code, body)
	}
	ev = fl.last
	if ev["version"] != "1.0" || ev["resource"] != "/one/{id}" || ev["path"] != "/one/9" || ev["httpMethod"] != "POST" {
		t.Fatalf("1.0 event: %v", ev)
	}
	if ev["requestContext"].(map[string]any)["routeKey"] != "POST /one/{id}" || ev["pathParameters"].(map[string]any)["id"] != "9" {
		t.Fatalf("1.0 requestContext: %v", ev["requestContext"])
	}
	// Under 1.0 a bare value is malformed, as on a REST API.
	fl.fn = func(map[string]any) any { return "nope" }
	if code, _, _ := a.invoke("POST", id+"/one/9", nil, ""); code != 502 {
		t.Fatalf("1.0 malformed: %d", code)
	}
}

func TestV2CORS(t *testing.T) {
	a := v2Server(t, nil)
	backend := echoBackend(t)
	api := a.must("POST", "/v2/apis", map[string]any{"name": "cors", "protocolType": "HTTP", "corsConfiguration": map[string]any{
		"allowOrigins": []string{"https://app.example"}, "allowMethods": []string{"GET", "POST"},
		"allowHeaders": []string{"content-type"}, "maxAge": 600, "allowCredentials": true,
	}})
	id := api["apiId"].(string)
	if api["corsConfiguration"].(map[string]any)["maxAge"] != float64(600) {
		t.Fatalf("cors round trip: %v", api["corsConfiguration"])
	}
	integ := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "HTTP_PROXY", "integrationUri": backend.URL})
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "$default", "target": "integrations/" + integ["integrationId"].(string)})
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "$default", "autoDeploy": true})

	code, h, _ := a.invoke("OPTIONS", id+"/x", map[string]string{"Origin": "https://app.example", "Access-Control-Request-Method": "POST"}, "")
	if code != 204 || h.Get("Access-Control-Allow-Origin") != "https://app.example" || h.Get("Access-Control-Allow-Methods") != "GET,POST" ||
		h.Get("Access-Control-Max-Age") != "600" || h.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight: %d %v", code, h)
	}
	code, h, _ = a.invoke("OPTIONS", id+"/x", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "POST"}, "")
	// Not admitted by the configuration: routed like any request, which the
	// $default route answers, with no allow headers.
	if code != 200 || h.Get("X-Backend") != "echo" || h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight from another origin must be routed without allow headers: %d %v", code, h)
	}
	_, h, body := a.invoke("GET", id+"/x", map[string]string{"Origin": "https://app.example"}, "")
	if h.Get("Access-Control-Allow-Origin") != "https://app.example" || !strings.HasPrefix(body, "GET /") {
		t.Fatalf("actual request: %v %q", h, body)
	}
	a.must("DELETE", "/v2/apis/"+id+"/cors", nil)
	if _, h, _ := a.invoke("GET", id+"/x", map[string]string{"Origin": "https://app.example"}, ""); h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("DeleteCorsConfiguration must stop the headers")
	}
}

func TestV2RequestAuthorizer(t *testing.T) {
	fl := &v2FakeLambda{t: t}
	var authEvent map[string]any
	fl.fn = func(ev map[string]any) any {
		if ev["type"] == "REQUEST" {
			authEvent = ev
			ok := ev["headers"].(map[string]any)["authorization"] == "secret"
			return map[string]any{"isAuthorized": ok, "context": map[string]any{"user": "u1"}}
		}
		return map[string]any{"statusCode": 200, "body": fmt.Sprint(ev["requestContext"].(map[string]any)["authorizer"])}
	}
	a := v2Server(t, fl.handler())
	api := a.must("POST", "/v2/apis", map[string]any{"name": "auth", "protocolType": "HTTP"})
	id := api["apiId"].(string)
	if code, m := a.do("POST", "/v2/apis/"+id+"/authorizers", map[string]any{"name": "jwt", "authorizerType": "JWT", "identitySource": []string{"$request.header.Authorization"}}); code != 501 {
		t.Fatalf("JWT must be refused by name: %d %v", code, m)
	}
	auth := a.must("POST", "/v2/apis/"+id+"/authorizers", map[string]any{
		"name": "gate", "authorizerType": "REQUEST", "authorizerUri": authorizerURI,
		"identitySource": []string{"$request.header.Authorization"}, "authorizerPayloadFormatVersion": "2.0",
		"enableSimpleResponses": true, "authorizerResultTtlInSeconds": 0,
	})
	integ := a.must("POST", "/v2/apis/"+id+"/integrations", map[string]any{"integrationType": "AWS_PROXY", "integrationUri": fnARN})
	if code, m := a.do("POST", "/v2/apis/"+id+"/routes", map[string]any{"routeKey": "GET /private", "target": "integrations/" + integ["integrationId"].(string), "authorizationType": "CUSTOM"}); code != 400 {
		t.Fatalf("CUSTOM without an authorizer: %d %v", code, m)
	}
	a.must("POST", "/v2/apis/"+id+"/routes", map[string]any{
		"routeKey": "GET /private", "target": "integrations/" + integ["integrationId"].(string),
		"authorizationType": "CUSTOM", "authorizerId": auth["authorizerId"],
	})
	a.must("POST", "/v2/apis/"+id+"/stages", map[string]any{"stageName": "$default", "autoDeploy": true})

	if code, _, body := a.invoke("GET", id+"/private", nil, ""); code != 401 || !strings.Contains(body, "Unauthorized") {
		t.Fatalf("missing identity source: %d %s", code, body)
	}
	if code, _, body := a.invoke("GET", id+"/private", map[string]string{"Authorization": "wrong"}, ""); code != 403 || !strings.Contains(body, "Forbidden") {
		t.Fatalf("denied: %d %s", code, body)
	}
	code, _, body := a.invoke("GET", id+"/private", map[string]string{"Authorization": "secret"}, "")
	if code != 200 || !strings.Contains(body, "user:u1") {
		t.Fatalf("allowed: %d %s", code, body)
	}
	if authEvent["routeArn"] == nil || authEvent["identitySource"].([]any)[0] != "secret" || authEvent["version"] != "2.0" {
		t.Fatalf("authorizer event: %v", authEvent)
	}
	// The authorizer cannot be deleted while a route names it.
	if code, _ := a.do("DELETE", "/v2/apis/"+id+"/authorizers/"+auth["authorizerId"].(string), nil); code != 409 {
		t.Fatalf("delete in use: %d", code)
	}
}
