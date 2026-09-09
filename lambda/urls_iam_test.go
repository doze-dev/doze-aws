package lambda_test

// A function URL is its own authorization path, and it had no test.
//
// On AWS a function URL with AuthType NONE is still not open by magic: the
// function's resource policy has to carry a statement granting
// lambda:InvokeFunctionUrl to everyone, which is what the console adds for
// you when you tick "NONE". doze-aws reproduces that under IAM soft and
// enforce, in lambda/urls.go — a separate evaluation from every other guard,
// with an anonymous principal and its own 403 body.
//
// Nothing exercised it. The string "InvokeFunctionUrl" appeared in no test
// file in the repository, so the enforce branch, the soft branch and the
// 403 shape were all unproven. A regression that dropped the check would
// have turned every function URL into an open endpoint under enforce, which
// is the mode whose entire purpose is to stop that.

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
	"github.com/doze-dev/doze-aws/iam"
)

// urlUnderMode builds a stack in the given IAM mode with one python function
// and a NONE-auth function URL, and returns the URL to GET.
func urlUnderMode(t *testing.T, mode iam.Mode) (*awslambda.Client, string, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs interpreters")
	}
	skipWithoutPython(t)
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf, IAMMode: mode})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := newTestServer(t, stack.Handler())
	c := awslambda.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")},
		func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts) })

	handler := `def handler(event, context):
    return {"statusCode": 200, "headers": {"content-type": "text/plain"}, "body": "served"}
`
	if _, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("web"), Runtime: lambdatypes.RuntimePython312, Handler: aws.String("h.handler"),
		Role: aws.String("arn:aws:iam::000000000000:role/x"),
		Code: &lambdatypes.FunctionCode{ZipFile: zipOf(t, map[string]string{"h.py": handler})},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := c.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("web"), AuthType: lambdatypes.FunctionUrlAuthTypeNone})
	if err != nil {
		t.Fatal(err)
	}
	// The reported URL is AWS's public shape; its id names the path form this
	// gateway serves.
	reported := aws.ToString(cfg.FunctionUrl)
	if !strings.Contains(reported, ".lambda-url.us-east-1.on.aws/") {
		t.Fatalf("function url = %q", reported)
	}
	id := strings.TrimPrefix(strings.SplitN(reported, ".", 2)[0], "https://")
	return c, ts + "/_aws/lambda-url/" + id + "/", ctx
}

func getURL(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestFunctionURLUnderEnforceNeedsAPermission: NONE is not "open".
func TestFunctionURLUnderEnforceNeedsAPermission(t *testing.T) {
	c, url, ctx := urlUnderMode(t, iam.ModeEnforce)

	code, body := getURL(t, url)
	if code != 403 {
		t.Fatalf("a function URL with no permission must be 403 under enforce, got %d %s", code, body)
	}
	if !strings.Contains(body, "Forbidden") {
		t.Errorf("AWS answers {\"Message\":\"Forbidden\"}, got %s", body)
	}

	// The statement AWS writes when you choose AuthType NONE.
	if _, err := c.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("web"), StatementId: aws.String("FunctionURLAllowPublicAccess"),
		Action: aws.String("lambda:InvokeFunctionUrl"), Principal: aws.String("*"),
		FunctionUrlAuthType: lambdatypes.FunctionUrlAuthTypeNone,
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}

	code, body = getURL(t, url)
	if code != 200 || !strings.Contains(body, "served") {
		t.Fatalf("with the permission the URL must serve, got %d %s", code, body)
	}
}

// TestFunctionURLPermissionForOneUserIsNotEnough: the caller of a function
// URL is anonymous, so a statement naming a principal cannot admit it. This
// is the arm that would let a too-generous evaluator through.
func TestFunctionURLPermissionForOneUserIsNotEnough(t *testing.T) {
	c, url, ctx := urlUnderMode(t, iam.ModeEnforce)

	if _, err := c.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("web"), StatementId: aws.String("OneAccount"),
		Action: aws.String("lambda:InvokeFunctionUrl"), Principal: aws.String(awsident.AccountID),
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	if code, body := getURL(t, url); code != 403 {
		t.Fatalf("a statement naming an account does not admit an anonymous caller, got %d %s", code, body)
	}

	// And a grant for a different action does not carry over either.
	if _, err := c.AddPermission(ctx, &awslambda.AddPermissionInput{
		FunctionName: aws.String("web"), StatementId: aws.String("WrongAction"),
		Action: aws.String("lambda:InvokeFunction"), Principal: aws.String("*"),
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	if code, body := getURL(t, url); code != 403 {
		t.Fatalf("lambda:InvokeFunction is not lambda:InvokeFunctionUrl, got %d %s", code, body)
	}
}

// TestFunctionURLUnderSoftServesAndLogs: soft never blocks. The whole point
// of the mode is that switching IAM on does not break a running app.
func TestFunctionURLUnderSoftServesAndLogs(t *testing.T) {
	_, url, _ := urlUnderMode(t, iam.ModeSoft)
	if code, body := getURL(t, url); code != 200 || !strings.Contains(body, "served") {
		t.Fatalf("soft mode must serve without a permission, got %d %s", code, body)
	}
}

// TestFunctionURLUnderOffServes: the default mode changes nothing.
func TestFunctionURLUnderOffServes(t *testing.T) {
	_, url, _ := urlUnderMode(t, iam.ModeOff)
	if code, body := getURL(t, url); code != 200 || !strings.Contains(body, "served") {
		t.Fatalf("mode off must serve, got %d %s", code, body)
	}
}
