// SDK v1 contract tests: the legacy aws-sdk-go (v1) client against Step
// Functions. Same awsJson 1.0 wire as v2, but v1 builds its requests from a
// different generator — a second, independent reading of the protocol.
package stepfunctions

import (
	"net/http/httptest"
	"testing"
	"time"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	sfnv1 "github.com/aws/aws-sdk-go/service/sfn"
)

func sdkV1Client(t *testing.T) *sfnv1.SFN {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion("us-east-1").
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials("test", "test", "")))
	if err != nil {
		t.Fatal(err)
	}
	return sfnv1.New(sess)
}

func TestSDKV1RoundTrip(t *testing.T) {
	c := sdkV1Client(t)

	created, err := c.CreateStateMachine(&sfnv1.CreateStateMachineInput{
		Name: awsv1.String("legacy"),
		Definition: awsv1.String(
			`{"StartAt":"P","States":{"P":{"Type":"Pass","Result":"v1","End":true}}}`),
		RoleArn: awsv1.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}
	started, err := c.StartExecution(&sfnv1.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
		Name:            awsv1.String("v1-run"),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		desc, err := c.DescribeExecution(&sfnv1.DescribeExecutionInput{ExecutionArn: started.ExecutionArn})
		if err != nil {
			t.Fatalf("DescribeExecution: %v", err)
		}
		if awsv1.StringValue(desc.Status) == "SUCCEEDED" {
			if got := awsv1.StringValue(desc.Output); got != `"v1"` {
				t.Errorf("output = %s", got)
			}
			break
		}
		if awsv1.StringValue(desc.Status) != "RUNNING" || time.Now().After(deadline) {
			t.Fatalf("execution settled as %s", awsv1.StringValue(desc.Status))
		}
		time.Sleep(20 * time.Millisecond)
	}
	hist, err := c.GetExecutionHistory(&sfnv1.GetExecutionHistoryInput{ExecutionArn: started.ExecutionArn})
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}
	if len(hist.Events) == 0 || awsv1.StringValue(hist.Events[0].Type) != "ExecutionStarted" {
		t.Errorf("v1 history = %d events, first %v", len(hist.Events), hist.Events)
	}
}
