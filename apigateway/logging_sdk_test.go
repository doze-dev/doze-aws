package apigateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapi "github.com/aws/aws-sdk-go-v2/service/apigateway"
	apitypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/apigateway"
	"github.com/doze-dev/doze-aws/awsident"
)

// A stage's logging settings are stored the way UpdateStage patches them
// and honoured: an access-log line per request in the stage's format, the
// execution narrative at INFO with bodies under data trace, and nothing at
// OFF. The account's CloudWatch role reads back what UpdateAccount wrote.

func waitForLogs(t *testing.T, logs *cwl.Client, group string, n int) []string {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(group)})
		if err == nil && len(res.Events) >= n {
			var out []string
			for _, ev := range res.Events {
				out = append(out, aws.ToString(ev.Message))
			}
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held %d events (%v)", group, n, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStageLogsAreWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	agw := awsapi.NewFromConfig(cfg, func(o *awsapi.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	logs := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// A MOCK-backed GET /ping, deployed to prod.
	api, err := agw.CreateRestApi(ctx, &awsapi.CreateRestApiInput{Name: aws.String("logged")})
	if err != nil {
		t.Fatal(err)
	}
	apiID := aws.ToString(api.Id)
	ping, _ := agw.CreateResource(ctx, &awsapi.CreateResourceInput{RestApiId: api.Id, ParentId: api.RootResourceId, PathPart: aws.String("ping")})
	agw.PutMethod(ctx, &awsapi.PutMethodInput{RestApiId: api.Id, ResourceId: ping.Id, HttpMethod: aws.String("GET"), AuthorizationType: aws.String("NONE")})
	agw.PutIntegration(ctx, &awsapi.PutIntegrationInput{RestApiId: api.Id, ResourceId: ping.Id, HttpMethod: aws.String("GET"), Type: apitypes.IntegrationTypeMock})
	agw.PutMethodResponse(ctx, &awsapi.PutMethodResponseInput{RestApiId: api.Id, ResourceId: ping.Id, HttpMethod: aws.String("GET"), StatusCode: aws.String("200")})
	agw.PutIntegrationResponse(ctx, &awsapi.PutIntegrationResponseInput{RestApiId: api.Id, ResourceId: ping.Id, HttpMethod: aws.String("GET"), StatusCode: aws.String("200"),
		ResponseTemplates: map[string]string{"application/json": `{"pong":true}`}})
	if _, err := agw.CreateDeployment(ctx, &awsapi.CreateDeploymentInput{RestApiId: api.Id, StageName: aws.String("prod")}); err != nil {
		t.Fatal(err)
	}
	base := ts.URL + apigateway.ExecutePrefix + apiID + "/prod"

	// Logging off: a request writes nothing.
	if resp, _ := http.Get(base + "/ping"); resp == nil || resp.StatusCode != 200 {
		t.Fatalf("the mock should answer 200")
	}

	// The account role, as the CDK sets it before enabling logs.
	role := "arn:aws:iam::000000000000:role/apigw-cloudwatch"
	if _, err := agw.UpdateAccount(ctx, &awsapi.UpdateAccountInput{PatchOperations: []apitypes.PatchOperation{
		{Op: apitypes.OpReplace, Path: aws.String("/cloudwatchRoleArn"), Value: aws.String(role)}}}); err != nil {
		t.Fatal(err)
	}
	if acct, err := agw.GetAccount(ctx, &awsapi.GetAccountInput{}); err != nil || aws.ToString(acct.CloudwatchRoleArn) != role {
		t.Errorf("GetAccount after UpdateAccount = %v %v, want the role", acct, err)
	}

	// The stage's settings, patched the way CloudFormation patches them.
	accessGroup := "/aws/apigateway/logged-access"
	st, err := agw.UpdateStage(ctx, &awsapi.UpdateStageInput{RestApiId: api.Id, StageName: aws.String("prod"), PatchOperations: []apitypes.PatchOperation{
		{Op: apitypes.OpReplace, Path: aws.String("/accessLogSettings/destinationArn"), Value: aws.String(awsident.ARN("logs", "log-group:"+accessGroup))},
		{Op: apitypes.OpReplace, Path: aws.String("/accessLogSettings/format"), Value: aws.String(`{"requestId":"$context.requestId","method":"$context.httpMethod","resource":"$context.resourcePath","status":$context.status,"ip":"$context.identity.sourceIp","stage":"$context.stage"}`)},
		{Op: apitypes.OpReplace, Path: aws.String("/*/*/logging/loglevel"), Value: aws.String("INFO")},
		{Op: apitypes.OpReplace, Path: aws.String("/*/*/logging/dataTrace"), Value: aws.String("true")},
		{Op: apitypes.OpReplace, Path: aws.String("/*/*/metrics/enabled"), Value: aws.String("true")},
		{Op: apitypes.OpReplace, Path: aws.String("/~1ping/GET/throttling/burstLimit"), Value: aws.String("50")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if st.AccessLogSettings == nil || !strings.HasSuffix(aws.ToString(st.AccessLogSettings.DestinationArn), accessGroup) {
		t.Errorf("access log settings did not land: %+v", st.AccessLogSettings)
	}
	all, ok := st.MethodSettings["*/*"]
	if !ok || aws.ToString(all.LoggingLevel) != "INFO" || !all.DataTraceEnabled || !all.MetricsEnabled || all.ThrottlingBurstLimit != 5000 {
		t.Errorf("stage-wide method setting = %+v", all)
	}
	if per, ok := st.MethodSettings["/ping/GET"]; !ok || per.ThrottlingBurstLimit != 50 || aws.ToString(per.LoggingLevel) != "OFF" {
		t.Errorf("per-method setting = %+v (want burst 50 on a fresh default)", per)
	}
	// An unknown patch path is refused, not silently accepted.
	if _, err := agw.UpdateStage(ctx, &awsapi.UpdateStageInput{RestApiId: api.Id, StageName: aws.String("prod"), PatchOperations: []apitypes.PatchOperation{
		{Op: apitypes.OpReplace, Path: aws.String("/nonsense/path"), Value: aws.String("x")}}}); err == nil {
		t.Errorf("an unknown patch path should be a BadRequestException")
	}

	// A request now writes both logs. The per-method setting on /ping GET
	// (loglevel OFF, since only its burst limit was patched) governs the
	// execution log, as on AWS — so log at the stage level for the request
	// that should produce a narrative: /ping is patched below.
	resp, _ := http.Get(base + "/ping?who=ada")
	if resp == nil || resp.StatusCode != 200 {
		t.Fatalf("the mock should answer 200")
	}
	access := waitForLogs(t, logs, accessGroup, 1)
	line := access[len(access)-1]
	for _, want := range []string{`"method":"GET"`, `"resource":"/ping"`, `"status":200`, `"stage":"prod"`, `"ip":"127.0.0.1"`} {
		if !strings.Contains(line, want) {
			t.Errorf("access line lacks %s: %s", want, line)
		}
	}
	if strings.Contains(line, "$context") {
		t.Errorf("a $context variable was left unfilled: %s", line)
	}
	// The execution log for /ping GET is OFF at the method level; raise it.
	if _, err := agw.UpdateStage(ctx, &awsapi.UpdateStageInput{RestApiId: api.Id, StageName: aws.String("prod"), PatchOperations: []apitypes.PatchOperation{
		{Op: apitypes.OpReplace, Path: aws.String("/~1ping/GET/logging/loglevel"), Value: aws.String("INFO")},
		{Op: apitypes.OpReplace, Path: aws.String("/~1ping/GET/logging/dataTrace"), Value: aws.String("true")},
	}}); err != nil {
		t.Fatal(err)
	}
	http.Get(base + "/ping?who=ada")
	exec := waitForLogs(t, logs, "API-Gateway-Execution-Logs_"+apiID+"/prod", 5)
	joined := strings.Join(exec, "\n")
	for _, want := range []string{"Starting execution for request:", "HTTP Method: GET, Resource Path: /ping", `Method request query string: {"who":"ada"}`, "Method completed with status: 200", "Successfully completed execution"} {
		if !strings.Contains(joined, want) {
			t.Errorf("execution log lacks %q:\n%s", want, joined)
		}
	}
	// Every line is prefixed with the request id, the way AWS writes them.
	for _, l := range exec {
		if !strings.HasPrefix(l, "(") {
			t.Errorf("an execution line lacks its request id: %s", l)
		}
	}
	// A miss is logged too, with its status.
	http.Get(base + "/nothing-here")
	access = waitForLogs(t, logs, accessGroup, 3)
	if last := access[len(access)-1]; !strings.Contains(last, `"status":403`) {
		t.Errorf("the 403 should be in the access log: %s", last)
	}
	// Removing the destination stops the access log.
	if _, err := agw.UpdateStage(ctx, &awsapi.UpdateStageInput{RestApiId: api.Id, StageName: aws.String("prod"), PatchOperations: []apitypes.PatchOperation{
		{Op: apitypes.OpRemove, Path: aws.String("/accessLogSettings")}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := agw.GetStage(ctx, &awsapi.GetStageInput{RestApiId: api.Id, StageName: aws.String("prod")}); got.AccessLogSettings != nil {
		t.Errorf("accessLogSettings should be gone after remove: %+v", got.AccessLogSettings)
	}
}
