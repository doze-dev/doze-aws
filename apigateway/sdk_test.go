// End-to-end: build a REST API with the real aws-sdk-go-v2 client, deploy it,
// then call the deployed endpoint and check a Lambda actually answered.
package apigateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapi "github.com/aws/aws-sdk-go-v2/service/apigateway"
	apitypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/apigateway"
	"github.com/doze-dev/doze-aws/awsident"
)

// echoHandler is a proxy-integration Lambda: it returns the API Gateway
// response shape, echoing what it was given so the test can assert the event
// was built correctly.
const echoHandler = `package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		event, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var ev map[string]any
		json.Unmarshal(event, &ev)
		body, _ := json.Marshal(map[string]any{
			"path":       ev["path"],
			"httpMethod": ev["httpMethod"],
			"pathParams": ev["pathParameters"],
			"query":      ev["queryStringParameters"],
			"gotBody":    ev["body"],
		})
		out, _ := json.Marshal(map[string]any{
			"statusCode": 201,
			"headers":    map[string]string{"X-Echo": "yes", "Content-Type": "application/json"},
			"body":       string(body),
		})
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+id+"/response",
			"application/json", strings.NewReader(string(out)))
	}
}
`

func buildEcho(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := strings.Replace(echoHandler, `"os"`, "\"os\"\n\t\"strings\"", 1)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module echo\n\ngo 1.26\n"), 0o644)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build echo handler: %v\n%s", err, out)
	}
	return dir
}

// TestDeployedAPIInvokesLambda is the test that matters: the whole path from
// CreateRestApi to an HTTP request landing in a function.
func TestDeployedAPIInvokesLambda(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs a lambda across the stack")
	}
	ctx := context.Background()

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	agw := awsapi.NewFromConfig(cfg, func(o *awsapi.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// The backing function.
	if _, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("echo"), Runtime: lamtypes.RuntimeProvidedal2,
		Handler: aws.String("bootstrap"),
		Role:    aws.String("arn:aws:iam::000000000000:role/r"),
		Code:    &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildEcho(t))},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// The API.
	api, err := agw.CreateRestApi(ctx, &awsapi.CreateRestApiInput{Name: aws.String("shop")})
	if err != nil {
		t.Fatalf("CreateRestApi: %v", err)
	}
	apiID := aws.ToString(api.Id)
	if aws.ToString(api.RootResourceId) == "" {
		t.Fatal("CreateRestApi must report a rootResourceId")
	}

	// /orders/{id}
	orders, err := agw.CreateResource(ctx, &awsapi.CreateResourceInput{
		RestApiId: api.Id, ParentId: api.RootResourceId, PathPart: aws.String("orders"),
	})
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}
	byID, err := agw.CreateResource(ctx, &awsapi.CreateResourceInput{
		RestApiId: api.Id, ParentId: orders.Id, PathPart: aws.String("{id}"),
	})
	if err != nil {
		t.Fatalf("CreateResource({id}): %v", err)
	}
	if got := aws.ToString(byID.Path); got != "/orders/{id}" {
		t.Fatalf("resource path = %q", got)
	}

	if _, err := agw.PutMethod(ctx, &awsapi.PutMethodInput{
		RestApiId: api.Id, ResourceId: byID.Id, HttpMethod: aws.String("ANY"),
		AuthorizationType: aws.String("NONE"),
	}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}
	uri := "arn:aws:apigateway:" + awsident.Region + ":lambda:path/2015-03-31/functions/" +
		awsident.ARN("lambda", "function:echo") + "/invocations"
	if _, err := agw.PutIntegration(ctx, &awsapi.PutIntegrationInput{
		RestApiId: api.Id, ResourceId: byID.Id, HttpMethod: aws.String("ANY"),
		Type: apitypes.IntegrationTypeAwsProxy, IntegrationHttpMethod: aws.String("POST"),
		Uri: aws.String(uri),
	}); err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}

	if _, err := agw.CreateDeployment(ctx, &awsapi.CreateDeploymentInput{
		RestApiId: api.Id, StageName: aws.String("prod"),
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Call the deployed API for real.
	base := ts.URL + apigateway.ExecutePrefix + apiID + "/prod"
	resp, err := http.Post(base+"/orders/42?debug=1", "application/json",
		strings.NewReader(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("calling the deployed API: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 201 {
		t.Fatalf("status = %d (want the function's 201), body: %s", resp.StatusCode, raw)
	}
	if resp.Header.Get("X-Echo") != "yes" {
		t.Errorf("the function's headers did not reach the response: %v", resp.Header)
	}

	var echoed struct {
		Path       string            `json:"path"`
		HTTPMethod string            `json:"httpMethod"`
		PathParams map[string]string `json:"pathParams"`
		Query      map[string]string `json:"query"`
		GotBody    string            `json:"gotBody"`
	}
	if err := json.Unmarshal(raw, &echoed); err != nil {
		t.Fatalf("function body was not the echo shape: %v (%s)", err, raw)
	}
	if echoed.Path != "/orders/42" {
		t.Errorf("event path = %q", echoed.Path)
	}
	if echoed.HTTPMethod != "POST" {
		t.Errorf("event httpMethod = %q", echoed.HTTPMethod)
	}
	if echoed.PathParams["id"] != "42" {
		t.Errorf("path parameters = %v, want id=42", echoed.PathParams)
	}
	if echoed.Query["debug"] != "1" {
		t.Errorf("query parameters = %v", echoed.Query)
	}
	if echoed.GotBody != `{"hello":"world"}` {
		t.Errorf("event body = %q", echoed.GotBody)
	}

	// An unknown path on a deployed API is a 403 with the AWS wording, not a
	// 404 that looks like the emulator is broken.
	miss, err := http.Get(base + "/nothing-here")
	if err != nil {
		t.Fatal(err)
	}
	defer miss.Body.Close()
	if miss.StatusCode != 403 {
		t.Errorf("unknown path = %d, want 403", miss.StatusCode)
	}

	// An unknown stage is a 404.
	badStage, err := http.Get(ts.URL + apigateway.ExecutePrefix + apiID + "/nope/orders/1")
	if err != nil {
		t.Fatal(err)
	}
	defer badStage.Body.Close()
	if badStage.StatusCode != 404 {
		t.Errorf("unknown stage = %d, want 404", badStage.StatusCode)
	}
}

// TestControlPlaneCRUD exercises the control plane on its own.
func TestControlPlaneCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: t.Logf, Services: []string{"apigateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	c := awsapi.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awsapi.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	api, err := c.CreateRestApi(ctx, &awsapi.CreateRestApiInput{
		Name: aws.String("crud"), Description: aws.String("a test api"),
		Tags: map[string]string{"team": "shop"},
	})
	if err != nil {
		t.Fatalf("CreateRestApi: %v", err)
	}

	got, err := c.GetRestApi(ctx, &awsapi.GetRestApiInput{RestApiId: api.Id})
	if err != nil || aws.ToString(got.Name) != "crud" {
		t.Fatalf("GetRestApi = %v, %v", got, err)
	}
	if got.Tags["team"] != "shop" {
		t.Errorf("tags = %v", got.Tags)
	}

	list, err := c.GetRestApis(ctx, &awsapi.GetRestApisInput{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("GetRestApis = %d items, %v", len(list.Items), err)
	}

	// Resources: create, list, delete.
	res, err := c.CreateResource(ctx, &awsapi.CreateResourceInput{
		RestApiId: api.Id, ParentId: api.RootResourceId, PathPart: aws.String("things"),
	})
	if err != nil {
		t.Fatalf("CreateResource: %v", err)
	}
	// A duplicate path part under the same parent conflicts.
	if _, err := c.CreateResource(ctx, &awsapi.CreateResourceInput{
		RestApiId: api.Id, ParentId: api.RootResourceId, PathPart: aws.String("things"),
	}); err == nil {
		t.Error("a duplicate path part should conflict")
	}

	resources, err := c.GetResources(ctx, &awsapi.GetResourcesInput{RestApiId: api.Id})
	if err != nil || len(resources.Items) != 2 {
		t.Fatalf("GetResources = %d, %v", len(resources.Items), err)
	}

	// Method + integration + responses.
	if _, err := c.PutMethod(ctx, &awsapi.PutMethodInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		AuthorizationType: aws.String("NONE"),
	}); err != nil {
		t.Fatalf("PutMethod: %v", err)
	}
	if _, err := c.PutIntegration(ctx, &awsapi.PutIntegrationInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		Type:             apitypes.IntegrationTypeMock,
		RequestTemplates: map[string]string{"application/json": `{"statusCode":200}`},
	}); err != nil {
		t.Fatalf("PutIntegration: %v", err)
	}
	if _, err := c.PutMethodResponse(ctx, &awsapi.PutMethodResponseInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		StatusCode: aws.String("200"),
	}); err != nil {
		t.Fatalf("PutMethodResponse: %v", err)
	}
	if _, err := c.PutIntegrationResponse(ctx, &awsapi.PutIntegrationResponseInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		StatusCode:        aws.String("200"),
		ResponseTemplates: map[string]string{"application/json": `{"ok":true}`},
	}); err != nil {
		t.Fatalf("PutIntegrationResponse: %v", err)
	}

	// A re-put of the method must not unwire the integration.
	if _, err := c.PutMethod(ctx, &awsapi.PutMethodInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
		AuthorizationType: aws.String("NONE"), OperationName: aws.String("getThings"),
	}); err != nil {
		t.Fatalf("PutMethod (re-put): %v", err)
	}
	if _, err := c.GetIntegration(ctx, &awsapi.GetIntegrationInput{
		RestApiId: api.Id, ResourceId: res.Id, HttpMethod: aws.String("GET"),
	}); err != nil {
		t.Fatalf("re-putting a method unwired its integration: %v", err)
	}

	// Deploy and read the stage back.
	dep, err := c.CreateDeployment(ctx, &awsapi.CreateDeploymentInput{
		RestApiId: api.Id, StageName: aws.String("dev"),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	stage, err := c.GetStage(ctx, &awsapi.GetStageInput{
		RestApiId: api.Id, StageName: aws.String("dev"),
	})
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}
	if aws.ToString(stage.DeploymentId) != aws.ToString(dep.Id) {
		t.Errorf("stage deployment = %q, want %q",
			aws.ToString(stage.DeploymentId), aws.ToString(dep.Id))
	}

	if _, err := c.DeleteResource(ctx, &awsapi.DeleteResourceInput{
		RestApiId: api.Id, ResourceId: res.Id,
	}); err != nil {
		t.Fatalf("DeleteResource: %v", err)
	}
	if _, err := c.DeleteRestApi(ctx, &awsapi.DeleteRestApiInput{RestApiId: api.Id}); err != nil {
		t.Fatalf("DeleteRestApi: %v", err)
	}
	if _, err := c.GetRestApi(ctx, &awsapi.GetRestApiInput{RestApiId: api.Id}); err == nil {
		t.Error("the API survived deletion")
	}
}
