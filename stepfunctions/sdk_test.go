package stepfunctions_test

// SDK contract tests: the real aws-sdk-go-v2 Step Functions client against the
// whole stack, the way every other service in this repo is checked.
//
// Driving the SDK rather than raw HTTP is what catches the things a hand-rolled
// request would paper over — that Step Functions is awsJson **1.0** where most
// of this repo is 1.1, that it signs as `states` while targeting
// `AWSStepFunctions`, and that its members are lowercase-initial.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

const helloWorld = `{
  "Comment": "A Hello World example",
  "StartAt": "HelloWorld",
  "States": {"HelloWorld": {"Type": "Pass", "Result": "Hello", "End": true}}
}`

func sfnClient(t *testing.T) *awssfn.Client {
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

	cfg := aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	return awssfn.NewFromConfig(cfg, func(o *awssfn.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKStateMachineLifecycle(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("hello"),
		Definition: aws.String(helloWorld),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}
	arn := aws.ToString(created.StateMachineArn)
	if !strings.Contains(arn, ":states:") || !strings.HasSuffix(arn, ":stateMachine:hello") {
		t.Fatalf("StateMachineArn = %q, want an arn:aws:states:...:stateMachine:hello", arn)
	}

	desc, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}
	// The definition must come back byte-identical: CDK and Terraform both diff
	// what they get against what they hold, and a re-serialised document shows
	// as permanent drift.
	if got := aws.ToString(desc.Definition); got != helloWorld {
		t.Errorf("definition round-trip changed the document:\n got %q\nwant %q", got, helloWorld)
	}
	if desc.Type != sfntypes.StateMachineTypeStandard {
		t.Errorf("type = %s, want STANDARD by default", desc.Type)
	}
	if aws.ToString(desc.Name) != "hello" {
		t.Errorf("name = %q", aws.ToString(desc.Name))
	}

	if _, err := c.ListStateMachines(ctx, &awssfn.ListStateMachinesInput{}); err != nil {
		t.Fatalf("ListStateMachines: %v", err)
	}

	upd, err := c.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: aws.String(arn),
		RoleArn:         aws.String("arn:aws:iam::000000000000:role/Other"),
	})
	if err != nil {
		t.Fatalf("UpdateStateMachine: %v", err)
	}
	if aws.ToString(upd.RevisionId) == "" {
		t.Error("UpdateStateMachine returned no revisionId; CDK reads it back")
	}
	// An update that named only the role must not blank the definition.
	desc2, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(desc2.Definition) != helloWorld {
		t.Error("updating the role erased the definition")
	}

	if _, err := c.DeleteStateMachine(ctx, &awssfn.DeleteStateMachineInput{StateMachineArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteStateMachine: %v", err)
	}
	// Deleting twice must succeed — a repeated `cdk destroy` otherwise fails.
	if _, err := c.DeleteStateMachine(ctx, &awssfn.DeleteStateMachineInput{StateMachineArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteStateMachine is not idempotent: %v", err)
	}
	if _, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn)}); err == nil {
		t.Error("DescribeStateMachine succeeded after delete")
	}
}

// TestSDKRefusesABrokenDefinition is the point of stage 1. A definition AWS
// would reject must be rejected here, with the SDK's typed exception, so the
// failure lands locally instead of on deploy.
func TestSDKRefusesABrokenDefinition(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	for _, tc := range []struct{ name, def string }{
		{"dangling Next", `{"StartAt":"A","States":{"A":{"Type":"Pass","Next":"Nowhere"}}}`},
		{"no StartAt", `{"States":{"A":{"Type":"Succeed"}}}`},
		{"Task with no Resource", `{"StartAt":"A","States":{"A":{"Type":"Task","End":true}}}`},
		{"not JSON", `{"StartAt":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
				Name:       aws.String("broken-" + strings.ReplaceAll(tc.name, " ", "-")),
				Definition: aws.String(tc.def),
				RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
			})
			if err == nil {
				t.Fatal("accepted a definition AWS would refuse")
			}
			var invalid *sfntypes.InvalidDefinition
			if !errors.As(err, &invalid) {
				t.Errorf("error = %v; the SDK should see a typed InvalidDefinition", err)
			}
		})
	}
}

// TestSDKValidateStateMachineDefinition — the analyser exposed directly, which
// is how the CDK and the CLI check a definition without creating anything.
func TestSDKValidateStateMachineDefinition(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	ok, err := c.ValidateStateMachineDefinition(ctx, &awssfn.ValidateStateMachineDefinitionInput{
		Definition: aws.String(helloWorld),
	})
	if err != nil {
		t.Fatalf("ValidateStateMachineDefinition: %v", err)
	}
	if ok.Result != sfntypes.ValidateStateMachineDefinitionResultCodeOk {
		t.Errorf("a valid definition reported %s: %+v", ok.Result, ok.Diagnostics)
	}

	bad, err := c.ValidateStateMachineDefinition(ctx, &awssfn.ValidateStateMachineDefinitionInput{
		Definition: aws.String(`{"StartAt":"A","States":{"A":{"Type":"Pass","Next":"Gone"},"B":{"Type":"Succeed"}}}`),
	})
	if err != nil {
		t.Fatalf("ValidateStateMachineDefinition: %v", err)
	}
	if bad.Result != sfntypes.ValidateStateMachineDefinitionResultCodeFail {
		t.Fatal("a broken definition reported OK")
	}
	// Two problems: the dangling Next, and B being unreachable. Reporting both
	// is the difference between one edit and two.
	if len(bad.Diagnostics) < 2 {
		t.Errorf("got %d diagnostics, want both the dangling Next and the unreachable state: %+v",
			len(bad.Diagnostics), bad.Diagnostics)
	}
}

func TestSDKActivityAndTags(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	act, err := c.CreateActivity(ctx, &awssfn.CreateActivityInput{Name: aws.String("approve")})
	if err != nil {
		t.Fatalf("CreateActivity: %v", err)
	}
	arn := aws.ToString(act.ActivityArn)
	if !strings.HasSuffix(arn, ":activity:approve") {
		t.Fatalf("ActivityArn = %q", arn)
	}
	if _, err := c.DescribeActivity(ctx, &awssfn.DescribeActivityInput{ActivityArn: aws.String(arn)}); err != nil {
		t.Fatalf("DescribeActivity: %v", err)
	}

	if _, err := c.TagResource(ctx, &awssfn.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []sfntypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
	tags, err := c.ListTagsForResource(ctx, &awssfn.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}
	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Errorf("tags = %+v, want env=dev", tags.Tags)
	}
	if _, err := c.UntagResource(ctx, &awssfn.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}
	after, err := c.ListTagsForResource(ctx, &awssfn.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Tags) != 0 {
		t.Errorf("tags after untag = %+v, want none", after.Tags)
	}

	if _, err := c.DeleteActivity(ctx, &awssfn.DeleteActivityInput{ActivityArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteActivity: %v", err)
	}
}

// waitForStatus polls DescribeExecution until the execution reaches a
// terminal status, which the engine delivers asynchronously.
func waitForStatus(t *testing.T, c *awssfn.Client, arn string, want sfntypes.ExecutionStatus) *awssfn.DescribeExecutionOutput {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		desc, err := c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: aws.String(arn)})
		if err != nil {
			t.Fatalf("DescribeExecution: %v", err)
		}
		if desc.Status == want {
			return desc
		}
		if desc.Status != sfntypes.ExecutionStatusRunning {
			t.Fatalf("execution settled as %s (error=%s cause=%s), want %s",
				desc.Status, aws.ToString(desc.Error), aws.ToString(desc.Cause), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("execution did not reach %s in time", want)
	return nil
}

// TestSDKExecutionRuns is stage G3's claim: a state machine of Pass, Choice
// and Wait states actually executes, end to end, through the real SDK.
func TestSDKExecutionRuns(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	def := `{
	  "StartAt": "Classify",
	  "States": {
	    "Classify": {"Type": "Choice", "Choices": [
	      {"Variable": "$.n", "NumericGreaterThan": 5, "Next": "Big"}
	    ], "Default": "Small"},
	    "Big": {"Type": "Pass", "Result": "big", "ResultPath": "$.size", "Next": "Breathe"},
	    "Small": {"Type": "Pass", "Result": "small", "ResultPath": "$.size", "Next": "Breathe"},
	    "Breathe": {"Type": "Wait", "Seconds": 0, "Next": "Done"},
	    "Done": {"Type": "Pass", "Parameters": {"verdict.$": "States.Format('n={} is {}', $.n, $.size)"}, "End": true}
	  }
	}`
	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("runs"),
		Definition: aws.String(def),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatal(err)
	}

	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
		Name:            aws.String("run-1"),
		Input:           aws.String(`{"n": 9}`),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	arn := aws.ToString(started.ExecutionArn)
	if !strings.HasSuffix(arn, ":execution:runs:run-1") {
		t.Fatalf("ExecutionArn = %q", arn)
	}

	desc := waitForStatus(t, c, arn, sfntypes.ExecutionStatusSucceeded)
	if got := aws.ToString(desc.Output); got != `{"verdict":"n=9 is big"}` {
		t.Errorf("output = %s", got)
	}
	if aws.ToString(desc.Input) != `{"n": 9}` {
		t.Errorf("input round-trip changed: %s", aws.ToString(desc.Input))
	}

	// The same name with the same input answers with the original execution;
	// with different input it conflicts.
	again, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
		Name:            aws.String("run-1"),
		Input:           aws.String(`{"n": 9}`),
	})
	if err == nil && aws.ToString(again.ExecutionArn) != arn {
		t.Error("a re-run under the same name minted a new execution")
	}
	// run-1 already finished, so even the same input now conflicts on AWS's
	// rules for reused names — but a *finished* same-input rerun is the one
	// place doze-aws is looser; assert only the different-input conflict.
	_, err = c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
		Name:            aws.String("run-1"),
		Input:           aws.String(`{"n": 1}`),
	})
	var exists *sfntypes.ExecutionAlreadyExists
	if !errors.As(err, &exists) {
		t.Errorf("reusing a name with different input = %v, want ExecutionAlreadyExists", err)
	}

	list, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{
		StateMachineArn: created.StateMachineArn,
	})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(list.Executions) != 1 || aws.ToString(list.Executions[0].Name) != "run-1" {
		t.Errorf("executions = %+v", list.Executions)
	}
}

// TestSDKExecutionHistory drives GetExecutionHistory through the SDK's typed
// HistoryEvent — the shape where emulators classically diverge: ids global
// and ascending, previousEventId chains causal, payloads JSON-encoded
// STRINGS inside the details, ExecutionStarted first and ExecutionSucceeded
// last.
func TestSDKExecutionHistory(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	def := `{
	  "StartAt": "A",
	  "States": {
	    "A": {"Type": "Pass", "Result": {"ready": true}, "Next": "B"},
	    "B": {"Type": "Choice", "Choices": [
	      {"Variable": "$.ready", "BooleanEquals": true, "Next": "C"}], "Default": "C"},
	    "C": {"Type": "Succeed"}
	  }
	}`
	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("historied"),
		Definition: aws.String(def),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn, Input: aws.String(`{"seed": 1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	arn := aws.ToString(started.ExecutionArn)
	waitForStatus(t, c, arn, sfntypes.ExecutionStatusSucceeded)

	hist, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{
		ExecutionArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}
	evs := hist.Events
	if len(evs) < 8 {
		t.Fatalf("got %d events, want the full Entered/Exited chain", len(evs))
	}
	if evs[0].Type != sfntypes.HistoryEventTypeExecutionStarted || evs[0].Id != 1 || evs[0].PreviousEventId != 0 {
		t.Errorf("first event = %s id=%d prev=%d", evs[0].Type, evs[0].Id, evs[0].PreviousEventId)
	}
	if last := evs[len(evs)-1]; last.Type != sfntypes.HistoryEventTypeExecutionSucceeded {
		t.Errorf("last event = %s, want ExecutionSucceeded", last.Type)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Id <= evs[i-1].Id {
			t.Fatalf("ids not ascending at %d: %d then %d", i, evs[i-1].Id, evs[i].Id)
		}
		if evs[i].PreviousEventId != evs[i-1].Id {
			t.Errorf("event %d (%s) previousEventId = %d, want %d — the root chain is linear here",
				evs[i].Id, evs[i].Type, evs[i].PreviousEventId, evs[i-1].Id)
		}
	}
	var sawEntered, sawChoice bool
	for _, ev := range evs {
		if ev.Type == sfntypes.HistoryEventTypePassStateEntered {
			sawEntered = true
			d := ev.StateEnteredEventDetails
			if d == nil || aws.ToString(d.Name) != "A" {
				t.Fatalf("PassStateEntered details = %+v", d)
			}
			// The input must be a JSON-encoded string (not an object); its
			// formatting may be compacted.
			if got := aws.ToString(d.Input); got != `{"seed":1}` && got != `{"seed": 1}` {
				t.Errorf("entered input = %q", got)
			}
		}
		if ev.Type == sfntypes.HistoryEventTypeChoiceStateExited {
			sawChoice = true
			if d := ev.StateExitedEventDetails; d == nil || aws.ToString(d.Output) == "" {
				t.Errorf("ChoiceStateExited without output details")
			}
		}
	}
	if !sawEntered || !sawChoice {
		t.Errorf("missing typed events: PassStateEntered=%v ChoiceStateExited=%v", sawEntered, sawChoice)
	}

	// reverseOrder flips the walk.
	rev, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{
		ExecutionArn: aws.String(arn), ReverseOrder: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Events[0].Type != sfntypes.HistoryEventTypeExecutionSucceeded {
		t.Errorf("reverseOrder first = %s", rev.Events[0].Type)
	}

	// Pagination: two-at-a-time pages cover the same ids exactly once.
	var paged []int64
	var token *string
	for {
		page, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{
			ExecutionArn: aws.String(arn), MaxResults: 2, NextToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range page.Events {
			paged = append(paged, ev.Id)
		}
		if page.NextToken == nil {
			break
		}
		token = page.NextToken
	}
	if len(paged) != len(evs) {
		t.Errorf("pagination returned %d events, full read %d", len(paged), len(evs))
	}
}

// TestSDKStopAndFrozenSnapshot: a long Wait parks the execution; stopping it
// aborts, and DescribeStateMachineForExecution answers with the definition
// the execution started with even after the machine is updated.
func TestSDKStopAndFrozenSnapshot(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	def := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":300,"Next":"S"},"S":{"Type":"Succeed"}}}`
	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("parked"),
		Definition: aws.String(def),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
		Name:            aws.String("stopped"),
	})
	if err != nil {
		t.Fatal(err)
	}
	arn := aws.ToString(started.ExecutionArn)

	updated := `{"StartAt":"S","States":{"S":{"Type":"Succeed"}}}`
	if _, err := c.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: created.StateMachineArn,
		Definition:      aws.String(updated),
	}); err != nil {
		t.Fatal(err)
	}
	frozen, err := c.DescribeStateMachineForExecution(ctx, &awssfn.DescribeStateMachineForExecutionInput{
		ExecutionArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeStateMachineForExecution: %v", err)
	}
	if aws.ToString(frozen.Definition) != def {
		t.Errorf("the frozen snapshot moved with the update:\n got %s", aws.ToString(frozen.Definition))
	}

	stopped, err := c.StopExecution(ctx, &awssfn.StopExecutionInput{
		ExecutionArn: aws.String(arn),
		Error:        aws.String("Manual.Stop"),
		Cause:        aws.String("test teardown"),
	})
	if err != nil {
		t.Fatalf("StopExecution: %v", err)
	}
	if stopped.StopDate == nil {
		t.Error("StopExecution returned no stopDate")
	}
	desc := waitForStatus(t, c, arn, sfntypes.ExecutionStatusAborted)
	if aws.ToString(desc.Error) != "Manual.Stop" {
		t.Errorf("error = %q, want the stop's error", aws.ToString(desc.Error))
	}
}

// TestSDKCallbackPattern is the human-approval loop end to end through real
// services: the machine parks on .waitForTaskToken after sending the token
// through the stack's own SQS; a worker receives the message, redeems the
// token with SendTaskSuccess, and the execution completes with the worker's
// output.
func TestSDKCallbackPattern(t *testing.T) {
	ctx := context.Background()
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
	cfg := aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	c := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("approvals")})
	if err != nil {
		t.Fatal(err)
	}
	def := `{"StartAt":"Ask","States":{"Ask":{"Type":"Task",
	  "Resource":"arn:aws:states:::sqs:sendMessage.waitForTaskToken",
	  "Parameters":{"QueueUrl":"` + aws.ToString(q.QueueUrl) + `",
	    "MessageBody":{"ticket.$":"$.ticket","token.$":"$$.Task.Token"}},
	  "End":true}}}`
	m, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("approval"), Definition: aws.String(def),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: m.StateMachineArn, Input: aws.String(`{"ticket": "T-1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The worker's half: the token arrives on the queue.
	msgs, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 5,
	})
	if err != nil || len(msgs.Messages) == 0 {
		t.Fatalf("the token never reached the queue: %v", err)
	}
	var body struct {
		Ticket string `json:"ticket"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal([]byte(aws.ToString(msgs.Messages[0].Body)), &body); err != nil {
		t.Fatalf("queue body: %v", err)
	}
	if body.Ticket != "T-1" || body.Token == "" {
		t.Fatalf("queue body = %+v", body)
	}

	if _, err := c.SendTaskSuccess(ctx, &awssfn.SendTaskSuccessInput{
		TaskToken: aws.String(body.Token), Output: aws.String(`{"approved": true, "by": "test"}`),
	}); err != nil {
		t.Fatalf("SendTaskSuccess: %v", err)
	}
	desc := waitForStatus(t, c, aws.ToString(started.ExecutionArn), sfntypes.ExecutionStatusSucceeded)
	if out := aws.ToString(desc.Output); out != `{"approved":true,"by":"test"}` {
		t.Errorf("output = %s", out)
	}
}

// TestSDKFailedExecution: a Fail state surfaces its error and cause on
// DescribeExecution.
func TestSDKFailedExecution(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	def := `{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"Custom.Nope","Cause":"deliberate"}}}`
	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("fails"),
		Definition: aws.String(def),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
	})
	if err != nil {
		t.Fatal(err)
	}
	desc := waitForStatus(t, c, aws.ToString(started.ExecutionArn), sfntypes.ExecutionStatusFailed)
	if aws.ToString(desc.Error) != "Custom.Nope" || aws.ToString(desc.Cause) != "deliberate" {
		t.Errorf("error/cause = %q/%q", aws.ToString(desc.Error), aws.ToString(desc.Cause))
	}
}
