package cloudformation_test

// A CDK-shaped REST API deploys for real: AWS::ApiGateway::Resource and
// ::Method build the routes (which used to be dropped, so a CDK RestApi
// deployed with no routes), the TOKEN authorizer gates the method, and the
// CORS preflight is a MOCK method that answers its headers. A Cognito
// authorizer is refused by name.

import (
	"context"
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

const cdkRestAPITemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Backend:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: orders-backend
      Runtime: python3.12
      Handler: h.handler
      Role: arn:aws:iam::000000000000:role/x
      Code:
        S3Bucket: _local_
        S3Key: BACKEND_DIR
  Gate:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: orders-gate
      Runtime: python3.12
      Handler: h.handler
      Role: arn:aws:iam::000000000000:role/x
      Code:
        S3Bucket: _local_
        S3Key: GATE_DIR
  Api:
    Type: AWS::ApiGateway::RestApi
    Properties:
      Name: orders
  Auth:
    Type: AWS::ApiGateway::Authorizer
    Properties:
      Name: token-gate
      RestApiId: !Ref Api
      Type: TOKEN
      IdentitySource: method.request.header.Authorization
      AuthorizerUri: !Sub arn:aws:apigateway:${AWS::Region}:lambda:path/2015-03-31/functions/${Gate.Arn}/invocations
      AuthorizerResultTtlInSeconds: 0
  Orders:
    Type: AWS::ApiGateway::Resource
    Properties:
      RestApiId: !Ref Api
      ParentId: !GetAtt Api.RootResourceId
      PathPart: orders
  OrderById:
    Type: AWS::ApiGateway::Resource
    Properties:
      RestApiId: !Ref Api
      ParentId: !Ref Orders
      PathPart: "{id}"
  GetOrder:
    Type: AWS::ApiGateway::Method
    Properties:
      RestApiId: !Ref Api
      ResourceId: !Ref OrderById
      HttpMethod: GET
      AuthorizationType: CUSTOM
      AuthorizerId: !Ref Auth
      Integration:
        Type: AWS_PROXY
        IntegrationHttpMethod: POST
        Uri: !Sub arn:aws:apigateway:${AWS::Region}:lambda:path/2015-03-31/functions/${Backend.Arn}/invocations
  Preflight:
    Type: AWS::ApiGateway::Method
    Properties:
      RestApiId: !Ref Api
      ResourceId: !Ref OrderById
      HttpMethod: OPTIONS
      AuthorizationType: NONE
      Integration:
        Type: MOCK
        RequestTemplates:
          application/json: '{ statusCode: 200 }'
        IntegrationResponses:
          - StatusCode: "204"
            ResponseParameters:
              method.response.header.Access-Control-Allow-Origin: "'*'"
              method.response.header.Access-Control-Allow-Methods: "'GET,OPTIONS'"
      MethodResponses:
        - StatusCode: "204"
          ResponseParameters:
            method.response.header.Access-Control-Allow-Origin: true
  Deployment:
    Type: AWS::ApiGateway::Deployment
    Properties:
      RestApiId: !Ref Api
    DependsOn: [GetOrder, Preflight]
  Stage:
    Type: AWS::ApiGateway::Stage
    Properties:
      RestApiId: !Ref Api
      DeploymentId: !Ref Deployment
      StageName: v1
`

const cognitoAuthorizerTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Api:
    Type: AWS::ApiGateway::RestApi
    Properties:
      Name: pooled
  Auth:
    Type: AWS::ApiGateway::Authorizer
    Properties:
      Name: cognito
      RestApiId: !Ref Api
      Type: COGNITO_USER_POOLS
      IdentitySource: method.request.header.Authorization
      ProviderARNs: [arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc]
`

func TestApplyCDKRestAPIWithAuthorizer(t *testing.T) {
	if testing.Short() {
		t.Skip("runs Lambda functions across the stack")
	}
	ctx := context.Background()
	backendDir, gateDir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(backendDir, "h.py"), []byte(
		"import json\ndef handler(event, context):\n    auth = event['requestContext'].get('authorizer') or {}\n"+
			"    return {'statusCode': 200, 'body': json.dumps({'id': event['pathParameters']['id'], 'principal': auth.get('principalId'), 'tier': auth.get('tier')})}\n"), 0o644)
	os.WriteFile(filepath.Join(gateDir, "h.py"), []byte(
		"def handler(event, context):\n    effect = 'Allow' if event['authorizationToken'] == 'allow-me' else 'Deny'\n"+
			"    return {'principalId': 'alice', 'context': {'tier': 'gold'}, 'policyDocument': {'Version': '2012-10-17', 'Statement': [{'Effect': effect, 'Action': 'execute-api:Invoke', 'Resource': event['methodArn']}]}}\n"), 0o644)

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	tmpl, err := cloudformation.Parse([]byte(strings.NewReplacer("BACKEND_DIR", backendDir, "GATE_DIR", gateDir).Replace(cdkRestAPITemplate)))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	api := sf.APIs["orders"]
	if api.Stage != "v1" || len(api.Routes) != 2 || len(api.Authorizers) != 1 {
		t.Fatalf("the API did not map: %+v", api)
	}
	var get, opts *provision.Route
	for i := range api.Routes {
		switch api.Routes[i].Method {
		case "GET":
			get = &api.Routes[i]
		case "OPTIONS":
			opts = &api.Routes[i]
		}
	}
	if get == nil || get.Path != "/orders/{id}" || get.Lambda != "orders-backend" || get.Authorizer != "token-gate" {
		t.Fatalf("GET route: %+v", get)
	}
	if opts == nil || opts.Mock == nil || opts.Mock.Status != 204 || opts.Mock.Headers["Access-Control-Allow-Origin"] != "*" {
		t.Fatalf("OPTIONS route: %+v", opts)
	}
	if a := api.Authorizers["token-gate"]; a.Type != "TOKEN" || a.Lambda != "orders-gate" || a.TTL == nil || *a.TTL != 0 {
		t.Fatalf("authorizer: %+v", a)
	}

	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}

	// Find the API id and call through execute-api.
	var apiID string
	for _, id := range apiIDs(t, ts.URL) {
		apiID = id
	}
	call := func(method, path, token string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, ts.URL+apigateway.ExecutePrefix+apiID+"/v1"+path, nil)
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	if code, body, _ := call("GET", "/orders/42", ""); code != 401 || !strings.Contains(body, "Unauthorized") {
		t.Errorf("no token: %d %s", code, body)
	}
	if code, body, _ := call("GET", "/orders/42", "nope"); code != 403 {
		t.Errorf("denied token: %d %s", code, body)
	}
	if code, body, _ := call("GET", "/orders/42", "allow-me"); code != 200 || !strings.Contains(body, `"principal": "alice"`) || !strings.Contains(body, `"tier": "gold"`) {
		t.Errorf("allowed token: %d %s", code, body)
	}
	if code, _, hdr := call("OPTIONS", "/orders/42", ""); code != 204 || hdr.Get("Access-Control-Allow-Origin") != "*" || hdr.Get("Access-Control-Allow-Methods") != "GET,OPTIONS" {
		t.Errorf("preflight: %d %v", code, hdr)
	}

	// A Cognito authorizer is refused at transpile, by name.
	cog, _ := cloudformation.Parse([]byte(cognitoAuthorizerTemplate))
	if _, _, err := cloudformation.Transpile(cog, cloudformation.TranspileOptions{StackName: "pooled"}); err == nil || !strings.Contains(err.Error(), "Cognito") {
		t.Errorf("a Cognito authorizer should be refused by name, got %v", err)
	}
}

// apiIDs lists the REST API ids the service has.
func apiIDs(t *testing.T, base string) []string {
	t.Helper()
	resp, err := http.Get(base + "/restapis")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out []string
	for _, part := range strings.Split(string(body), `"id":"`)[1:] {
		out = append(out, strings.SplitN(part, `"`, 2)[0])
	}
	return out
}
