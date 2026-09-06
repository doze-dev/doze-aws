package stepfunctions_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

	"github.com/doze-dev/doze-aws/awsident"
)

// Activities over the real SDK: the worker loop every activity example
// ships — GetActivityTask, do the work, SendTaskSuccess — and the history
// the console reads back afterwards.

// activityWorker polls once on its own goroutine and reports what it got.
// The context is cancelled at cleanup so no long-poll outlives the test
// server (httptest's Close waits for in-flight requests).
func activityWorker(t *testing.T, c *awssfn.Client, arn, worker string) <-chan *awssfn.GetActivityTaskOutput {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := make(chan *awssfn.GetActivityTaskOutput, 1)
	go func() {
		res, err := c.GetActivityTask(ctx, &awssfn.GetActivityTaskInput{
			ActivityArn: aws.String(arn), WorkerName: aws.String(worker),
		})
		if err != nil {
			t.Errorf("GetActivityTask: %v", err)
			close(out)
			return
		}
		out <- res
	}()
	return out
}

func TestSDKActivityWorkerCompletesExecution(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	act, err := c.CreateActivity(ctx, &awssfn.CreateActivityInput{Name: aws.String("resize")})
	if err != nil {
		t.Fatalf("CreateActivity: %v", err)
	}
	arn := aws.ToString(act.ActivityArn)
	m, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("resizer"),
		Definition: aws.String(`{"StartAt":"Resize","States":{"Resize":{"Type":"Task",
		  "Resource":"` + arn + `","HeartbeatSeconds":60,"TimeoutSeconds":300,
		  "ResultSelector":{"px.$":"$.width"},"ResultPath":"$.out","End":true}}}`),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	got := activityWorker(t, c, arn, "worker-1")
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: m.StateMachineArn, Name: aws.String("img"), Input: aws.String(`{"file":"a.png"}`),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	task := <-got
	if task == nil || task.TaskToken == nil {
		t.Fatal("the worker never received the task")
	}
	if aws.ToString(task.Input) != `{"file":"a.png"}` {
		t.Errorf("input = %q", aws.ToString(task.Input))
	}
	if _, err := c.SendTaskHeartbeat(ctx, &awssfn.SendTaskHeartbeatInput{TaskToken: task.TaskToken}); err != nil {
		t.Fatalf("SendTaskHeartbeat: %v", err)
	}
	if _, err := c.SendTaskSuccess(ctx, &awssfn.SendTaskSuccessInput{
		TaskToken: task.TaskToken, Output: aws.String(`{"width":640,"height":480}`),
	}); err != nil {
		t.Fatalf("SendTaskSuccess: %v", err)
	}
	desc := waitForStatus(t, c, aws.ToString(started.ExecutionArn), sfntypes.ExecutionStatusSucceeded)
	if out := aws.ToString(desc.Output); out != `{"file":"a.png","out":{"px":640}}` {
		t.Errorf("output = %s", out)
	}

	hist, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{ExecutionArn: started.ExecutionArn})
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}
	var types []string
	for _, ev := range hist.Events {
		types = append(types, string(ev.Type))
		switch ev.Type {
		case sfntypes.HistoryEventTypeActivityScheduled:
			d := ev.ActivityScheduledEventDetails
			if d == nil || aws.ToString(d.Resource) != arn || aws.ToString(d.Input) != `{"file":"a.png"}` ||
				aws.ToInt64(d.HeartbeatInSeconds) != 60 || aws.ToInt64(d.TimeoutInSeconds) != 300 {
				t.Errorf("ActivityScheduled details = %+v", d)
			}
		case sfntypes.HistoryEventTypeActivityStarted:
			if ev.ActivityStartedEventDetails == nil || aws.ToString(ev.ActivityStartedEventDetails.WorkerName) != "worker-1" {
				t.Errorf("ActivityStarted details = %+v", ev.ActivityStartedEventDetails)
			}
		case sfntypes.HistoryEventTypeActivitySucceeded:
			if ev.ActivitySucceededEventDetails == nil || aws.ToString(ev.ActivitySucceededEventDetails.Output) != `{"width":640,"height":480}` {
				t.Errorf("ActivitySucceeded details = %+v", ev.ActivitySucceededEventDetails)
			}
		}
	}
	want := "ExecutionStarted TaskStateEntered ActivityScheduled ActivityStarted ActivitySucceeded TaskStateExited ExecutionSucceeded"
	if got := strings.Join(types, " "); got != want {
		t.Errorf("history = %s\n     want = %s", got, want)
	}
}

func TestSDKActivityFailureAndCatch(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	act, err := c.CreateActivity(ctx, &awssfn.CreateActivityInput{Name: aws.String("check")})
	if err != nil {
		t.Fatal(err)
	}
	arn := aws.ToString(act.ActivityArn)
	m, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("checker"),
		Definition: aws.String(`{"StartAt":"Check","States":{
		  "Check":{"Type":"Task","Resource":"` + arn + `",
		    "Catch":[{"ErrorEquals":["Check.Failed"],"ResultPath":"$.err","Next":"Report"}],"End":true},
		  "Report":{"Type":"Pass","Parameters":{"why.$":"$.err.Cause"},"End":true}}}`),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: m.StateMachineArn, Input: aws.String(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	task := <-activityWorker(t, c, arn, "w")
	if task == nil || task.TaskToken == nil {
		t.Fatal("no task")
	}
	if _, err := c.SendTaskFailure(ctx, &awssfn.SendTaskFailureInput{
		TaskToken: task.TaskToken, Error: aws.String("Check.Failed"), Cause: aws.String("bad checksum"),
	}); err != nil {
		t.Fatalf("SendTaskFailure: %v", err)
	}
	desc := waitForStatus(t, c, aws.ToString(started.ExecutionArn), sfntypes.ExecutionStatusSucceeded)
	if out := aws.ToString(desc.Output); out != `{"why":"bad checksum"}` {
		t.Errorf("output = %s", out)
	}
	hist, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{ExecutionArn: started.ExecutionArn})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range hist.Events {
		if ev.Type == sfntypes.HistoryEventTypeActivityFailed {
			found = true
			if d := ev.ActivityFailedEventDetails; d == nil || aws.ToString(d.Error) != "Check.Failed" {
				t.Errorf("ActivityFailed details = %+v", d)
			}
		}
	}
	if !found {
		t.Error("no ActivityFailed event in history")
	}
	// A spent token is TaskDoesNotExist, as for any callback token.
	_, err = c.SendTaskSuccess(ctx, &awssfn.SendTaskSuccessInput{TaskToken: task.TaskToken, Output: aws.String("{}")})
	var gone *sfntypes.TaskDoesNotExist
	if !errors.As(err, &gone) {
		t.Errorf("redeeming a spent activity token = %v, want TaskDoesNotExist", err)
	}
}

// TestSDKGetActivityTaskRefusals: the model's own errors, over the wire.
func TestSDKGetActivityTaskRefusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := sfnClient(t)

	_, err := c.GetActivityTask(ctx, &awssfn.GetActivityTaskInput{
		ActivityArn: aws.String(awsident.ARN("states", "activity:nobody")),
	})
	var missing *sfntypes.ActivityDoesNotExist
	if !errors.As(err, &missing) {
		t.Errorf("unknown activity = %v, want ActivityDoesNotExist", err)
	}
	_, err = c.GetActivityTask(ctx, &awssfn.GetActivityTaskInput{ActivityArn: aws.String("arn:aws:states:us-east-1:000000000000:stateMachine:x")})
	var bad *sfntypes.InvalidArn
	if !errors.As(err, &bad) {
		t.Errorf("a state machine ARN = %v, want InvalidArn", err)
	}
}
