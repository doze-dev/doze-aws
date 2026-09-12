package cloudformation_test

// A SAM API with Auth.ApiKeyRequired and Auth.UsagePlan deploys with a key
// and a plan covering its stage; the route answers 403 without the key and
// 200 with it. The plain CloudFormation spelling (ApiKey, UsagePlan,
// UsagePlanKey) maps to the same model.

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
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const samKeyedTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Resources:
  Api:
    Type: AWS::Serverless::Api
    Properties:
      Name: keyed
      StageName: v1
      Auth:
        ApiKeyRequired: true
        UsagePlan:
          CreateUsagePlan: PER_API
          UsagePlanName: keyed-plan
          Throttle:
            RateLimit: 10
            BurstLimit: 5
  Fn:
    Type: AWS::Serverless::Function
    Properties:
      FunctionName: keyed-fn
      Runtime: python3.12
      Handler: h.handler
      CodeUri: CODE_DIR
      Events:
        Get:
          Type: Api
          Properties:
            RestApiId: !Ref Api
            Path: /things
            Method: get
        Open:
          Type: Api
          Properties:
            RestApiId: !Ref Api
            Path: /open
            Method: get
            Auth:
              ApiKeyRequired: false
`

const cfnKeyedTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Api:
    Type: AWS::ApiGateway::RestApi
    Properties:
      Name: plain
  Key:
    Type: AWS::ApiGateway::ApiKey
    Properties:
      Name: partner
      Enabled: true
      Value: partner-value-0123456789abcdef
  Plan:
    Type: AWS::ApiGateway::UsagePlan
    Properties:
      UsagePlanName: partners
      ApiStages:
        - ApiId: !Ref Api
          Stage: v1
      Quota:
        Limit: 1000
        Period: MONTH
  PlanKey:
    Type: AWS::ApiGateway::UsagePlanKey
    Properties:
      KeyId: !Ref Key
      KeyType: API_KEY
      UsagePlanId: !Ref Plan
`

func TestApplyAPIKeysAndUsagePlans(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a Lambda function across the stack")
	}
	ctx := context.Background()
	codeDir := t.TempDir()
	os.WriteFile(filepath.Join(codeDir, "h.py"), []byte(
		"import json\ndef handler(event, context):\n    ident = event['requestContext']['identity']\n"+
			"    return {'statusCode': 200, 'body': json.dumps({'apiKeyId': ident.get('apiKeyId'), 'path': event['path']})}\n"), 0o644)
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	tmpl, err := cloudformation.Parse([]byte(strings.ReplaceAll(samKeyedTemplate, "CODE_DIR", codeDir)))
	if err != nil {
		t.Fatal(err)
	}
	sf, _, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "keyed"})
	if err != nil {
		t.Fatal(err)
	}
	api := sf.APIs["keyed"]
	if !api.APIKeyRequired || len(api.Routes) != 2 {
		t.Fatalf("API did not map: %+v", api)
	}
	plan, ok := sf.UsagePlans["keyed-plan"]
	if !ok || len(plan.Keys) != 1 || len(plan.Stages) != 1 || plan.Stages[0].API != "keyed" || plan.Throttle == nil || plan.Throttle.Rate != 10 {
		t.Fatalf("SAM usage plan did not map: %+v", plan)
	}
	if _, ok := sf.APIKeys[plan.Keys[0]]; !ok {
		t.Fatalf("SAM key %q did not map", plan.Keys[0])
	}
	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf, awsident.Default()); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}

	// Read the key's value back: GET /apikeys?includeValues=true.
	resp, err := http.Get(ts.URL + "/apikeys?includeValues=true")
	if err != nil {
		t.Fatal(err)
	}
	var keys struct {
		Items []struct{ ID, Name, Value string } `json:"item"`
	}
	json.NewDecoder(resp.Body).Decode(&keys)
	resp.Body.Close()
	if len(keys.Items) != 1 || keys.Items[0].Name != plan.Keys[0] || keys.Items[0].Value == "" {
		t.Fatalf("keys after apply: %+v", keys.Items)
	}
	apiID := apiIDs(t, ts.URL)[0]
	call := func(path, key string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+apigateway.ExecutePrefix+apiID+"/v1"+path, nil)
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := call("/things", ""); code != 403 || !strings.Contains(body, "Forbidden") {
		t.Errorf("no key: %d %s", code, body)
	}
	if code, body := call("/things", "wrong-key-value-00000000000"); code != 403 {
		t.Errorf("wrong key: %d %s", code, body)
	}
	if code, body := call("/things", keys.Items[0].Value); code != 200 || !strings.Contains(body, `"apiKeyId": "`+keys.Items[0].ID+`"`) {
		t.Errorf("right key: %d %s", code, body)
	}
	if code, _ := call("/open", ""); code != 200 {
		t.Errorf("the route that opts out of the key should be open, got %d", code)
	}

	// The plain CloudFormation spelling.
	plain, _ := cloudformation.Parse([]byte(cfnKeyedTemplate))
	psf, _, err := cloudformation.Transpile(plain, cloudformation.TranspileOptions{StackName: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if k := psf.APIKeys["partner"]; k.Value != "partner-value-0123456789abcdef" || k.Enabled == nil || !*k.Enabled {
		t.Errorf("ApiKey did not map: %+v", k)
	}
	if p := psf.UsagePlans["partners"]; len(p.Keys) != 1 || p.Keys[0] != "partner" || p.Quota == nil || p.Quota.Period != "MONTH" || p.Stages[0].API != "plain" || p.Stages[0].Stage != "v1" {
		t.Errorf("UsagePlan did not map: %+v", p)
	}
}
