// End-to-end: a template declaring a state machine deploys against a live
// stack, and the deployed machine actually runs — with
// DefinitionSubstitutions resolved from other resources, which is the CDK's
// standard shape and the one that fails invisibly when mishandled.
package cloudformation_test

import (
	"context"
	"encoding/json"
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
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const sfnTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  WorkQueue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: sfn-work
  Pipeline:
    Type: AWS::StepFunctions::StateMachine
    Properties:
      StateMachineName: pipeline
      RoleArn: arn:aws:iam::000000000000:role/StepFunctions
      DefinitionString: |
        {
          "StartAt": "Send",
          "States": {
            "Send": {
              "Type": "Task",
              "Resource": "arn:aws:states:::sqs:sendMessage",
              "Parameters": {"QueueUrl": "${QueueUrl}", "MessageBody.$": "$.msg"},
              "End": true
            }
          }
        }
      DefinitionSubstitutions:
        QueueUrl: !Ref WorkQueue
Outputs:
  MachineArn:
    Value: !Ref Pipeline
`

func TestApplyStateMachineTemplate(t *testing.T) {
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

	tmpl, err := cloudformation.Parse([]byte(sfnTemplate))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "sfn"})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	sm, ok := sf.StateMachines["pipeline"]
	if !ok {
		t.Fatalf("no state machine mapped: %+v", rep.Entries)
	}
	// The substitution must be gone from the definition, replaced by the
	// queue's Ref (its URL).
	if strings.Contains(sm.Definition, "${QueueUrl}") {
		t.Fatalf("DefinitionSubstitutions were not applied:\n%s", sm.Definition)
	}
	if !strings.Contains(sm.Definition, "/sfn-work") {
		t.Fatalf("the substituted QueueUrl is not the queue's URL:\n%s", sm.Definition)
	}
	// Ref on the machine is its ARN.
	if arn := rep.Outputs["MachineArn"]; !strings.Contains(arn, ":stateMachine:pipeline") {
		t.Fatalf("output MachineArn = %q", arn)
	}

	if _, err := provision.Apply(ctx, stack.Handler(), sf, awsident.Default()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Applying twice converges rather than conflicting.
	if _, err := provision.Apply(ctx, stack.Handler(), sf, awsident.Default()); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	sfn := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	started, err := sfn.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(awsident.ARN("states", "stateMachine:pipeline")),
		Input:           aws.String(`{"msg": "deployed-and-running"}`),
	})
	if err != nil {
		t.Fatalf("StartExecution on the deployed machine: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		desc, err := sfn.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: started.ExecutionArn})
		if err != nil {
			t.Fatal(err)
		}
		if desc.Status == sfntypes.ExecutionStatusSucceeded {
			break
		}
		if desc.Status != sfntypes.ExecutionStatusRunning || time.Now().After(deadline) {
			t.Fatalf("deployed machine settled as %s (%s: %s)", desc.Status,
				aws.ToString(desc.Error), aws.ToString(desc.Cause))
		}
		time.Sleep(50 * time.Millisecond)
	}
	q, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("sfn-work")})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 5,
	})
	if err != nil || len(msgs.Messages) == 0 {
		t.Fatalf("the deployed machine never delivered to the queue: %v", err)
	}
	if body := aws.ToString(msgs.Messages[0].Body); body != "deployed-and-running" {
		t.Errorf("queue body = %q", body)
	}
}

// TestTranspileSAMStateMachine: the SAM spelling normalises onto the plain
// resource — Name, Role and an inline Definition object.
func TestTranspileSAMStateMachine(t *testing.T) {
	tmpl, err := cloudformation.Parse([]byte(`
Transform: AWS::Serverless-2016-10-31
Resources:
  Approval:
    Type: AWS::Serverless::StateMachine
    Properties:
      Name: approval
      Role: arn:aws:iam::000000000000:role/SFN
      Definition:
        StartAt: Done
        States:
          Done:
            Type: Succeed
`))
	if err != nil {
		t.Fatal(err)
	}
	sf, _, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "sam"})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	sm, ok := sf.StateMachines["approval"]
	if !ok {
		t.Fatalf("SAM state machine not mapped: %+v", sf.StateMachines)
	}
	if sm.RoleARN != "arn:aws:iam::000000000000:role/SFN" {
		t.Errorf("role = %q", sm.RoleARN)
	}
	var probe struct {
		StartAt string
	}
	if err := json.Unmarshal([]byte(sm.Definition), &probe); err != nil || probe.StartAt != "Done" {
		t.Errorf("definition did not marshal from the object form: %s", sm.Definition)
	}
}

// TestTranspileStateMachineRefusals: the failure modes fail at transpile with
// messages naming the fix, not at runtime.
func TestTranspileStateMachineRefusals(t *testing.T) {
	for _, tc := range []struct{ name, tmpl, want string }{
		{"DefinitionUri", `
Resources:
  M:
    Type: AWS::StepFunctions::StateMachine
    Properties:
      RoleArn: arn:aws:iam::000000000000:role/r
      DefinitionUri: s3://bucket/def.json
`, "DefinitionUri"},
		{"no definition", `
Resources:
  M:
    Type: AWS::StepFunctions::StateMachine
    Properties:
      RoleArn: arn:aws:iam::000000000000:role/r
`, "DefinitionString"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := cloudformation.Parse([]byte(tc.tmpl))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "x"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %s", err, tc.want)
			}
		})
	}
}
