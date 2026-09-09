package cloudformation_test

// A CDK-shaped HTTP API deploys for real through the v2 control plane: the
// Api with CORS, an Integration a Route names by Target, a $default Stage
// that auto-deploys, and a REQUEST authorizer with simple responses gating
// one route. SAM's HttpApi event maps to the same shape.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/apigateway"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const cdkHTTPAPITemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Backend:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: items-backend
      Runtime: python3.12
      Handler: h.handler
      Role: arn:aws:iam::000000000000:role/x
      Code:
        S3Bucket: _local_
        S3Key: BACKEND_DIR
  Gate:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: items-gate
      Runtime: python3.12
      Handler: h.handler
      Role: arn:aws:iam::000000000000:role/x
      Code:
        S3Bucket: _local_
        S3Key: GATE_DIR
  Api:
    Type: AWS::ApiGatewayV2::Api
    Properties:
      Name: items
      ProtocolType: HTTP
      CorsConfiguration:
        AllowOrigins: ["*"]
        AllowMethods: ["GET", "POST"]
        MaxAge: 300
  Integ:
    Type: AWS::ApiGatewayV2::Integration
    Properties:
      ApiId: !Ref Api
      IntegrationType: AWS_PROXY
      IntegrationUri: !GetAtt Backend.Arn
      PayloadFormatVersion: "2.0"
  Auth:
    Type: AWS::ApiGatewayV2::Authorizer
    Properties:
      ApiId: !Ref Api
      Name: gate
      AuthorizerType: REQUEST
      AuthorizerUri: !Sub arn:aws:apigateway:${AWS::Region}:lambda:path/2015-03-31/functions/${Gate.Arn}/invocations
      AuthorizerPayloadFormatVersion: "2.0"
      EnableSimpleResponses: true
      IdentitySource:
        - $request.header.Authorization
      AuthorizerResultTtlInSeconds: 0
  GetItem:
    Type: AWS::ApiGatewayV2::Route
    Properties:
      ApiId: !Ref Api
      RouteKey: GET /items/{id}
      Target: !Join ["/", ["integrations", !Ref Integ]]
  Admin:
    Type: AWS::ApiGatewayV2::Route
    Properties:
      ApiId: !Ref Api
      RouteKey: POST /admin
      AuthorizationType: CUSTOM
      AuthorizerId: !Ref Auth
      Target: !Join ["/", ["integrations", !Ref Integ]]
  Stage:
    Type: AWS::ApiGatewayV2::Stage
    Properties:
      ApiId: !Ref Api
      StageName: $default
      AutoDeploy: true
Outputs:
  Endpoint:
    Value: !GetAtt Api.ApiEndpoint
`

func TestApplyCDKHTTPAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("runs Lambda functions across the stack")
	}
	ctx := context.Background()
	backendDir, gateDir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(backendDir, "h.py"), []byte(
		"import json\ndef handler(event, context):\n    auth = (event['requestContext'].get('authorizer') or {}).get('lambda') or {}\n"+
			"    return {'version': event['version'], 'routeKey': event['routeKey'], 'id': (event.get('pathParameters') or {}).get('id'), 'user': auth.get('user')}\n"), 0o644)
	os.WriteFile(filepath.Join(gateDir, "h.py"), []byte(
		"def handler(event, context):\n    return {'isAuthorized': event['headers'].get('authorization') == 'let-me-in', 'context': {'user': 'alice'}}\n"), 0o644)

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	tmpl, err := cloudformation.Parse([]byte(strings.NewReplacer("BACKEND_DIR", backendDir, "GATE_DIR", gateDir).Replace(cdkHTTPAPITemplate)))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "items"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	api := sf.APIs["items"]
	if api.Protocol != "HTTP" || api.Stage != "$default" || len(api.Routes) != 2 || api.CORS == nil || api.CORS.MaxAge == nil || *api.CORS.MaxAge != 300 {
		t.Fatalf("the API did not map: %+v", api)
	}
	for _, rt := range api.Routes {
		if rt.Lambda != "items-backend" || rt.PayloadFormat != "2.0" {
			t.Fatalf("route: %+v", rt)
		}
		if rt.Path == "/admin" && rt.Authorizer != "gate" {
			t.Fatalf("admin route must name the authorizer: %+v", rt)
		}
	}
	if a := api.Authorizers["gate"]; a.Type != "REQUEST" || a.Lambda != "items-gate" || !a.SimpleResponses || a.IdentitySource != "$request.header.Authorization" {
		t.Fatalf("authorizer: %+v", a)
	}
	if out := rep.Outputs["Endpoint"]; !strings.Contains(out, "/_aws/execute-api/items") {
		t.Fatalf("ApiEndpoint output: %q", out)
	}

	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}

	// Find the API id through the v2 list and call the $default stage.
	resp, err := http.Get(ts.URL + "/v2/apis")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Items []struct {
			ID   string `json:"apiId"`
			Name string `json:"name"`
		} `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&listed)
	resp.Body.Close()
	if len(listed.Items) != 1 || listed.Items[0].Name != "items" {
		t.Fatalf("GetApis after two applies: %+v", listed.Items)
	}
	apiID := listed.Items[0].ID
	base := ts.URL + apigateway.ExecutePrefix + apiID

	get := func(path string, headers map[string]string) (int, string) {
		req, _ := http.NewRequest("GET", base+path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if path == "/admin" {
			req.Method = "POST"
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, body := get("/items/7", nil); code != 200 || !strings.Contains(body, `"id": "7"`) || !strings.Contains(body, `"version": "2.0"`) {
		t.Fatalf("GET /items/7: %d %s", code, body)
	}
	if code, _ := get("/admin", nil); code != 401 {
		t.Fatalf("POST /admin without a token: %d", code)
	}
	if code, _ := get("/admin", map[string]string{"Authorization": "nope"}); code != 403 {
		t.Fatalf("POST /admin with a bad token: %d", code)
	}
	if code, body := get("/admin", map[string]string{"Authorization": "let-me-in"}); code != 200 || !strings.Contains(body, `"user": "alice"`) {
		t.Fatalf("POST /admin allowed: %d %s", code, body)
	}
	// CORS from the template.
	req, _ := http.NewRequest("OPTIONS", base+"/items/1", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pre.Body.Close()
	if pre.StatusCode != 204 || pre.Header.Get("Access-Control-Allow-Origin") != "*" || pre.Header.Get("Access-Control-Max-Age") != "300" {
		t.Fatalf("preflight: %d %v", pre.StatusCode, pre.Header)
	}

	if _, err := provision.Destroy(ctx, stack.Handler(), sf); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	listed.Items = nil
	if resp, _ := http.Get(ts.URL + "/v2/apis"); resp != nil {
		json.NewDecoder(resp.Body).Decode(&listed)
		resp.Body.Close()
	}
	if len(listed.Items) != 0 {
		t.Fatalf("Destroy left the HTTP API: %+v", listed.Items)
	}
}

const samHTTPAPITemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Resources:
  Fn:
    Type: AWS::Serverless::Function
    Properties:
      FunctionName: hello
      Runtime: python3.12
      Handler: h.handler
      CodeUri: .
      Events:
        Any:
          Type: HttpApi
        Get:
          Type: HttpApi
          Properties:
            Path: /hello/{name}
            Method: get
            PayloadFormatVersion: "1.0"
`

func TestTranspileSAMHttpApiEvents(t *testing.T) {
	tmpl, err := cloudformation.Parse([]byte(samHTTPAPITemplate))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "sam"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	api, ok := sf.APIs["ServerlessHttpApi"]
	if !ok || api.Protocol != "HTTP" || len(api.Routes) != 2 {
		t.Fatalf("implicit HttpApi: %+v (%v)", api, sf.APIs)
	}
	var def, get *provision.Route
	for i := range api.Routes {
		if api.Routes[i].Path == "$default" {
			def = &api.Routes[i]
		} else {
			get = &api.Routes[i]
		}
	}
	if def == nil || def.Lambda != "hello" {
		t.Fatalf("$default route: %+v", def)
	}
	if get == nil || get.Method != "GET" || get.Path != "/hello/{name}" || get.PayloadFormat != "1.0" {
		t.Fatalf("GET route: %+v", get)
	}
	// A WebSocket API is refused by name.
	ws, _ := cloudformation.Parse([]byte("Resources:\n  Ws:\n    Type: AWS::ApiGatewayV2::Api\n    Properties:\n      Name: ws\n      ProtocolType: WEBSOCKET\n"))
	if _, _, err := cloudformation.Transpile(ws, cloudformation.TranspileOptions{StackName: "ws"}); err == nil || !strings.Contains(err.Error(), "WebSocket") {
		t.Fatalf("a WebSocket API should be refused by name, got %v", err)
	}
}
