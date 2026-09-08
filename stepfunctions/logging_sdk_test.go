package stepfunctions_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// A machine's loggingConfiguration is honoured: history goes to the log
// group it names, in AWS's vended shape, filtered by level; an Express
// machine with logging off still leaves a record in the default group; and
// an update replaces the configuration rather than dropping it.

func sfnAndLogs(t *testing.T) (*awssfn.Client, *cwl.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	return awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) }, noHostPrefix),
		cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

type vended struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	Details         map[string]any `json:"details"`
	PreviousEventID string         `json:"previous_event_id"`
	EventTimestamp  string         `json:"event_timestamp"`
	ExecutionARN    string         `json:"execution_arn"`
}

// vendedRecords polls a group until it holds at least n records, or fails.
func vendedRecords(t *testing.T, logs *cwl.Client, group string, n int) []vended {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(group)})
		if err == nil && len(res.Events) >= n {
			var out []vended
			for _, ev := range res.Events {
				var v vended
				if err := json.Unmarshal([]byte(aws.ToString(ev.Message)), &v); err != nil {
					t.Fatalf("a vended record is not JSON: %s", aws.ToString(ev.Message))
				}
				out = append(out, v)
			}
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held %d records (%v)", group, n, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func loggingTo(group, level string, data bool) *sfntypes.LoggingConfiguration {
	return &sfntypes.LoggingConfiguration{
		Level: sfntypes.LogLevel(level), IncludeExecutionData: data,
		Destinations: []sfntypes.LogDestination{{CloudWatchLogsLogGroup: &sfntypes.CloudWatchLogsLogGroup{
			LogGroupArn: aws.String(awsident.ARN("logs", "log-group:"+group+":*"))}}},
	}
}

func TestHistoryIsVendedToTheLogGroup(t *testing.T) {
	ctx := context.Background()
	sfn, logs := sfnAndLogs(t)
	created, err := sfn.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("logged"), RoleArn: aws.String(role), Definition: aws.String(helloWorld),
		LoggingConfiguration: loggingTo("/aws/vendedlogs/states/logged", "ALL", true),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := sfn.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: created.StateMachineArn, Input: aws.String(`{"n":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	recs := vendedRecords(t, logs, "/aws/vendedlogs/states/logged", 4)
	if recs[0].Type != "ExecutionStarted" || recs[0].ID != "1" || recs[0].PreviousEventID != "0" || recs[0].ExecutionARN != aws.ToString(started.ExecutionArn) {
		t.Errorf("first record = %+v", recs[0])
	}
	if in, _ := recs[0].Details["input"].(string); in != `{"n":1}` {
		t.Errorf("includeExecutionData should carry the input, got %v", recs[0].Details)
	}
	if last := recs[len(recs)-1]; last.Type != "ExecutionSucceeded" || last.EventTimestamp == "" {
		t.Errorf("last record = %+v", last)
	}
	// The record set matches the history the API answers.
	hist, _ := sfn.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{ExecutionArn: started.ExecutionArn})
	if len(hist.Events) != len(recs) {
		t.Errorf("history has %d events, the log group %d", len(hist.Events), len(recs))
	}

	// ERROR level with no data: only the failures, without input.
	if _, err := sfn.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("errs"), RoleArn: aws.String(role),
		Definition:           aws.String(`{"StartAt":"Boom","States":{"Boom":{"Type":"Fail","Error":"Bad","Cause":"on purpose"}}}`),
		LoggingConfiguration: loggingTo("/aws/vendedlogs/states/errs", "ERROR", false),
	}); err != nil {
		t.Fatal(err)
	}
	sfn.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: aws.String(awsident.ARN("states", "stateMachine:errs"))})
	errRecs := vendedRecords(t, logs, "/aws/vendedlogs/states/errs", 1)
	for _, r := range errRecs {
		if !strings.HasSuffix(r.Type, "Failed") {
			t.Errorf("ERROR level should write only failures, got %s", r.Type)
		}
	}
	if errRecs[len(errRecs)-1].Type != "ExecutionFailed" {
		t.Errorf("the execution's failure should be the last record: %+v", errRecs)
	}

	// An update replaces the configuration; describe answers the new one.
	if _, err := sfn.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: created.StateMachineArn, LoggingConfiguration: loggingTo("/aws/vendedlogs/states/moved", "FATAL", false),
	}); err != nil {
		t.Fatal(err)
	}
	desc, _ := sfn.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: created.StateMachineArn})
	if desc.LoggingConfiguration == nil || desc.LoggingConfiguration.Level != sfntypes.LogLevelFatal ||
		!strings.Contains(aws.ToString(desc.LoggingConfiguration.Destinations[0].CloudWatchLogsLogGroup.LogGroupArn), "moved") {
		t.Errorf("update did not replace the logging configuration: %+v", desc.LoggingConfiguration)
	}
	// A successful run at FATAL writes nothing to the new group.
	sfn.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: created.StateMachineArn})
	time.Sleep(500 * time.Millisecond)
	if res, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String("/aws/vendedlogs/states/moved")}); err == nil && len(res.Events) > 0 {
		t.Errorf("FATAL should write nothing for a success, got %d records", len(res.Events))
	}
}

func TestExpressLeavesARecordInTheDefaultGroup(t *testing.T) {
	ctx := context.Background()
	sfn, logs := sfnAndLogs(t)
	created, err := sfn.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("fast"), RoleArn: aws.String(role), Type: sfntypes.StateMachineTypeExpress,
		Definition: aws.String(helloWorld),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fire and forget: nothing can describe it afterwards, so the group is
	// the only record.
	if _, err := sfn.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: created.StateMachineArn, Input: aws.String(`{"k":"v"}`)}); err != nil {
		t.Fatal(err)
	}
	recs := vendedRecords(t, logs, "/aws/vendedlogs/states/fast", 4)
	if recs[0].Type != "ExecutionStarted" || recs[len(recs)-1].Type != "ExecutionSucceeded" {
		t.Errorf("records = %+v", recs)
	}
	if in, _ := recs[0].Details["input"].(string); in != `{"k":"v"}` {
		t.Errorf("the default Express policy includes execution data, got %v", recs[0].Details)
	}
}
