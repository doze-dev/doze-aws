// End-to-end for the HTTP API: build it with the real aws-sdk-go-v2
// apigatewayv2 client through the full stack, back it with a Lambda, and
// call the $default stage.
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
	awsv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	v2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/apigateway"
	"github.com/doze-dev/doze-aws/awsident"
)

// echo2Handler answers a payload-2.0 event with an object the test reads.
const echo2Handler = `package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
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
		rc, _ := ev["requestContext"].(map[string]any)
		out, _ := json.Marshal(map[string]any{
			"version": ev["version"], "routeKey": ev["routeKey"], "rawPath": ev["rawPath"],
			"pathParameters": ev["pathParameters"], "stage": rc["stage"], "body": ev["body"],
		})
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+id+"/response",
			"application/json", strings.NewReader(string(out)))
	}
}
`

func buildEcho2(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(echo2Handler), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module echo2\n\ngo 1.26\n"), 0o644)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build echo2 handler: %v\n%s", err, out)
	}
	return dir
}

func TestHTTPAPIServesLambda(t *testing.T) {
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
	v2 := awsv2.NewFromConfig(cfg, func(o *awsv2.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	fn, err := lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("echo2"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role: aws.String("arn:aws:iam::000000000000:role/r"),
		Code: &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildEcho2(t))},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	api, err := v2.CreateApi(ctx, &awsv2.CreateApiInput{
		Name: aws.String("shop"), ProtocolType: v2types.ProtocolTypeHttp,
		CorsConfiguration: &v2types.Cors{AllowOrigins: []string{"*"}, AllowMethods: []string{"*"}},
		Tags:              map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateApi: %v", err)
	}
	apiID := aws.ToString(api.ApiId)
	if !strings.HasSuffix(aws.ToString(api.ApiEndpoint), apigateway.ExecutePrefix+apiID) {
		t.Fatalf("ApiEndpoint: %v", api.ApiEndpoint)
	}
	integ, err := v2.CreateIntegration(ctx, &awsv2.CreateIntegrationInput{
		ApiId: api.ApiId, IntegrationType: v2types.IntegrationTypeAwsProxy,
		IntegrationUri: fn.FunctionArn, PayloadFormatVersion: aws.String("2.0"),
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if _, err := v2.CreateRoute(ctx, &awsv2.CreateRouteInput{
		ApiId: api.ApiId, RouteKey: aws.String("GET /orders/{id}"), Target: aws.String("integrations/" + aws.ToString(integ.IntegrationId)),
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	if _, err := v2.CreateStage(ctx, &awsv2.CreateStageInput{ApiId: api.ApiId, StageName: aws.String("$default"), AutoDeploy: aws.Bool(true)}); err != nil {
		t.Fatalf("CreateStage: %v", err)
	}

	resp, err := http.Get(ts.URL + apigateway.ExecutePrefix + apiID + "/orders/42")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("invoke: %d %s", resp.StatusCode, body)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["version"] != "2.0" || got["routeKey"] != "GET /orders/{id}" || got["rawPath"] != "/orders/42" || got["stage"] != "$default" {
		t.Fatalf("event: %s", body)
	}
	if got["pathParameters"].(map[string]any)["id"] != "42" {
		t.Fatalf("pathParameters: %s", body)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("a bare object is a 200 JSON body, got %v", resp.Header)
	}

	// The control plane reads back what it wrote, with the v2 shapes.
	routes, err := v2.GetRoutes(ctx, &awsv2.GetRoutesInput{ApiId: api.ApiId})
	if err != nil || len(routes.Items) != 1 || aws.ToString(routes.Items[0].RouteKey) != "GET /orders/{id}" {
		t.Fatalf("GetRoutes: %v %v", err, routes)
	}
	st, err := v2.GetStage(ctx, &awsv2.GetStageInput{ApiId: api.ApiId, StageName: aws.String("$default")})
	if err != nil || !aws.ToBool(st.AutoDeploy) || aws.ToString(st.DeploymentId) == "" || st.CreatedDate == nil {
		t.Fatalf("GetStage: %v %+v", err, st)
	}
	tags, err := v2.GetTags(ctx, &awsv2.GetTagsInput{ResourceArn: aws.String((&apigateway.Server{}).V2APIARN(apiID))})
	if err != nil || tags.Tags["env"] != "test" {
		t.Fatalf("GetTags: %v %v", err, tags)
	}
	upd, err := v2.UpdateApi(ctx, &awsv2.UpdateApiInput{ApiId: api.ApiId, Description: aws.String("updated")})
	if err != nil || aws.ToString(upd.Description) != "updated" || aws.ToString(upd.Name) != "shop" {
		t.Fatalf("UpdateApi: %v %v", err, upd)
	}
	if _, err := v2.CreateDomainName(ctx, &awsv2.CreateDomainNameInput{DomainName: aws.String("api.example.com")}); err == nil || !strings.Contains(err.Error(), "NotImplemented") {
		t.Fatalf("domain names must be refused by name: %v", err)
	}
	if _, err := v2.DeleteApi(ctx, &awsv2.DeleteApiInput{ApiId: api.ApiId}); err != nil {
		t.Fatalf("DeleteApi: %v", err)
	}
	if _, err := v2.GetApi(ctx, &awsv2.GetApiInput{ApiId: api.ApiId}); err == nil {
		t.Fatal("GetApi after delete must fail")
	}
}
