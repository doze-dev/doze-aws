package lambda_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// A function URL is served: a plain HTTP request through the gateway becomes
// the payload-format-2.0 event, and the function's answer is decoded by
// AWS's rule — statusCode object as a response, anything else as a 200 body.

func TestFunctionURLIsServed(t *testing.T) {
	if testing.Short() {
		t.Skip("runs interpreters")
	}
	skipWithoutPython(t)
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := newTestServer(t, stack.Handler())
	c := awslambda.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")},
		func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts) })

	handler := `import json
def handler(event, context):
    if event["rawPath"] == "/raw":
        return {"path": event["rawPath"], "method": event["requestContext"]["http"]["method"], "q": event.get("queryStringParameters"), "cookies": event.get("cookies"), "body": event.get("body"), "version": event["version"]}
    return {"statusCode": 201, "headers": {"content-type": "text/plain", "x-made-by": "fn"}, "cookies": ["a=1", "b=2"], "body": "created " + event["rawPath"]}
`
	if _, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("web"), Runtime: lambdatypes.RuntimePython312, Handler: aws.String("h.handler"),
		Role: aws.String("arn:aws:iam::000000000000:role/x"),
		Code: &lambdatypes.FunctionCode{ZipFile: zipOf(t, map[string]string{"h.py": handler})},
	}); err != nil {
		t.Fatal(err)
	}
	// Nothing is served before a URL config exists.
	if resp, _ := http.Get(ts + "/_aws/lambda-url/nonesuch/"); resp == nil || resp.StatusCode != 404 {
		t.Errorf("an unknown URL id should be 404")
	}
	cfg, err := c.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{FunctionName: aws.String("web"), AuthType: lambdatypes.FunctionUrlAuthTypeNone})
	if err != nil {
		t.Fatal(err)
	}
	// With no endpoint configured the URL is AWS's own shape; its id names
	// the path form the gateway also serves.
	reported := aws.ToString(cfg.FunctionUrl)
	if !strings.HasPrefix(reported, "https://") || !strings.Contains(reported, ".lambda-url.us-east-1.on.aws/") {
		t.Fatalf("FunctionUrl = %s", reported)
	}
	id := strings.TrimPrefix(strings.SplitN(reported, ".", 2)[0], "https://")
	url := ts + "/_aws/lambda-url/" + id + "/"
	if got, _ := c.GetFunctionUrlConfig(ctx, &awslambda.GetFunctionUrlConfigInput{FunctionName: aws.String("web")}); aws.ToString(got.FunctionUrl) != reported {
		t.Errorf("GetFunctionUrlConfig = %s, want %s", aws.ToString(got.FunctionUrl), reported)
	}

	// A response object: status, headers, cookies, body.
	req, _ := http.NewRequest(http.MethodPost, url+"orders/7?x=1&y=2", strings.NewReader(`{"n":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "s=abc; t=def")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 || string(body) != "created /orders/7" || resp.Header.Get("X-Made-By") != "fn" || len(resp.Header.Values("Set-Cookie")) != 2 {
		t.Errorf("response = %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp.Header.Get("X-Amzn-RequestId") == "" {
		t.Errorf("the response should carry the request id")
	}
	// A bare value: a 200 JSON body, and the event carried what was sent.
	req, _ = http.NewRequest(http.MethodPost, url+"raw?x=1", strings.NewReader("hello"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Cookie", "s=abc")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		t.Errorf("bare value = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{`"path": "/raw"`, `"method": "POST"`, `"x": "1"`, `"s=abc"`, `"body": "hello"`, `"version": "2.0"`} {
		if !strings.Contains(got, want) {
			t.Errorf("event lacks %s:\n%s", want, got)
		}
	}
	// The virtual-host form routes too.
	req, _ = http.NewRequest(http.MethodGet, ts+"/raw", nil)
	req.Host = id + ".lambda-url.us-east-1.on.aws"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"method": "GET"`) {
		t.Errorf("host-routed = %d %s", resp.StatusCode, body)
	}
	// Deleting the config stops serving.
	if _, err := c.DeleteFunctionUrlConfig(ctx, &awslambda.DeleteFunctionUrlConfigInput{FunctionName: aws.String("web")}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := http.Get(url); resp == nil || resp.StatusCode != 404 {
		t.Errorf("a deleted URL should be 404")
	}
}
