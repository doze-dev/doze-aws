package cloudformation_test

// The logging settings a SAM template carries reach the services: a state
// machine's Logging block becomes its loggingConfiguration and its history
// lands in the group; an Api's AccessLogSetting and MethodSettings become
// the stage's, and a request through the stage writes the access line.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapi "github.com/aws/aws-sdk-go-v2/service/apigateway"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const loggingTemplate = `
Transform: AWS::Serverless-2016-10-31
Resources:
  ApiLogs:
    Type: AWS::Logs::LogGroup
    Properties:
      LogGroupName: /aws/apigateway/shop-access
  SmLogs:
    Type: AWS::Logs::LogGroup
    Properties:
      LogGroupName: /aws/vendedlogs/states/flow
  Shop:
    Type: AWS::Serverless::Api
    Properties:
      Name: shop
      StageName: live
      AccessLogSetting:
        DestinationArn: !GetAtt ApiLogs.Arn
        Format: '{"rid":"$context.requestId","status":$context.status,"path":"$context.path"}'
      MethodSettings:
        - ResourcePath: "/*"
          HttpMethod: "*"
          LoggingLevel: INFO
          DataTraceEnabled: true
  Handler:
    Type: AWS::Serverless::Function
    Properties:
      Runtime: provided.al2023
      Handler: bootstrap
      CodeUri: CODE_DIR
      Events:
        Ping:
          Type: Api
          Properties:
            RestApiId: !Ref Shop
            Path: /ping
            Method: get
  Flow:
    Type: AWS::Serverless::StateMachine
    Properties:
      Name: flow
      Role: arn:aws:iam::000000000000:role/sfn
      Definition:
        StartAt: Done
        States:
          Done:
            Type: Pass
            End: true
      Logging:
        Level: ALL
        IncludeExecutionData: true
        Destinations:
          - CloudWatchLogsLogGroup:
              LogGroupArn: !GetAtt SmLogs.Arn
`

func TestApplyLoggingSettings(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	codeDir := t.TempDir() // no bootstrap: the function fails to launch, which is still a logged request
	os.WriteFile(codeDir+"/README", []byte("empty on purpose"), 0o644)

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	tmpl, err := cloudformation.Parse([]byte(strings.ReplaceAll(loggingTemplate, "CODE_DIR", codeDir)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sf, _, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "logged"})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if sm := sf.StateMachines["flow"]; !strings.Contains(sm.Logging.JSON, `"level":"ALL"`) || !strings.Contains(sm.Logging.JSON, `"logGroupArn":"`+awsident.ARN("logs", "log-group:/aws/vendedlogs/states/flow:*")+`"`) {
		t.Errorf("the SAM Logging block should map to the API's camelCase loggingConfiguration, got %s", sm.Logging.JSON)
	}
	api := sf.APIs["shop"]
	if api.Stage != "live" || api.AccessLog == nil || !strings.HasSuffix(api.AccessLog.DestinationARN, "/aws/apigateway/shop-access:*") || len(api.MethodSettings) != 1 || api.MethodSettings[0].LoggingLevel != "INFO" || !api.MethodSettings[0].DataTrace {
		t.Fatalf("the API's stage logging did not map: %+v", api)
	}
	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}

	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	sfn := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	agw := awsapi.NewFromConfig(cfg, func(o *awsapi.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	logs := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// The state machine logs to the group the template named.
	desc, err := sfn.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(awsident.ARN("states", "stateMachine:flow"))})
	if err != nil || desc.LoggingConfiguration == nil || string(desc.LoggingConfiguration.Level) != "ALL" {
		t.Fatalf("the deployed machine's logging = %+v (%v)", desc.LoggingConfiguration, err)
	}
	if _, err := sfn.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: desc.StateMachineArn, Input: aws.String(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitEvents(t, logs, "/aws/vendedlogs/states/flow", 4)

	// The stage carries the settings, and a request writes the access line.
	apis, _ := agw.GetRestApis(ctx, &awsapi.GetRestApisInput{})
	var apiID string
	for _, a := range apis.Items {
		if aws.ToString(a.Name) == "shop" {
			apiID = aws.ToString(a.Id)
		}
	}
	st, err := agw.GetStage(ctx, &awsapi.GetStageInput{RestApiId: aws.String(apiID), StageName: aws.String("live")})
	if err != nil || st.AccessLogSettings == nil || aws.ToString(st.MethodSettings["*/*"].LoggingLevel) != "INFO" || !st.MethodSettings["*/*"].DataTraceEnabled {
		t.Fatalf("the deployed stage's logging = %+v / %+v (%v)", st.AccessLogSettings, st.MethodSettings, err)
	}
	resp, err := http.Get(ts.URL + "/_aws/execute-api/" + apiID + "/live/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	access := waitEvents(t, logs, "/aws/apigateway/shop-access", 1)
	if !strings.Contains(access[0], `"path":"/live/ping"`) || !strings.Contains(access[0], `"status":`) {
		t.Errorf("access line = %s", access[0])
	}
	exec := waitEvents(t, logs, "API-Gateway-Execution-Logs_"+apiID+"/live", 3)
	if !strings.Contains(strings.Join(exec, "\n"), "Method completed with status:") {
		t.Errorf("execution log = %v", exec)
	}
}

func waitEvents(t *testing.T, logs *cwl.Client, group string, n int) []string {
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
