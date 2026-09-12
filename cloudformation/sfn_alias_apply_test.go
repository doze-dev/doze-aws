package cloudformation_test

// The CDK's version-and-alias shape: a StateMachineVersion Refs the machine,
// an alias's RoutingConfiguration Refs the version, and an activity ARN is
// wired into a Task by GetAtt. Deploying it twice must converge, the alias
// ARN must be a real one an SDK can start an execution against, and the
// exported template must carry all three resources back out.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const sfnAliasTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Worker:
    Type: AWS::StepFunctions::Activity
    Properties:
      Name: cdk-worker
  Machine:
    Type: AWS::StepFunctions::StateMachine
    Properties:
      StateMachineName: aliased
      RoleArn: arn:aws:iam::000000000000:role/StepFunctions
      DefinitionString:
        Fn::Join:
          - ""
          - - '{"StartAt":"Work","States":{"Work":{"Type":"Task","Resource":"'
            - Fn::GetAtt: [Worker, Arn]
            - '","End":true}}}'
  MachineVersion:
    Type: AWS::StepFunctions::StateMachineVersion
    Properties:
      StateMachineArn:
        Ref: Machine
  Live:
    Type: AWS::StepFunctions::StateMachineAlias
    Properties:
      Name: live
      Description: what production runs
      RoutingConfiguration:
        - StateMachineVersionArn:
            Ref: MachineVersion
          Weight: 100
Outputs:
  AliasArn:
    Value:
      Ref: Live
  ActivityArn:
    Value:
      Fn::GetAtt: [Worker, Arn]
`

func TestApplyStateMachineAliasTemplate(t *testing.T) {
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

	tmpl, err := cloudformation.Parse([]byte(sfnAliasTemplate))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "sfn"})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	sm, ok := sf.StateMachines["aliased"]
	if !ok {
		t.Fatalf("no state machine mapped: %+v", rep.Entries)
	}
	if !sm.Publish {
		t.Error("a StateMachineVersion in the template should mark the machine to publish")
	}
	if got := sm.Aliases["live"].Description; got != "what production runs" {
		t.Errorf("alias live = %+v, want the template's description", sm.Aliases)
	}
	if _, ok := sf.Activities["cdk-worker"]; !ok {
		t.Errorf("the activity was not mapped: %+v", sf.Activities)
	}
	if !strings.Contains(sm.Definition, awsident.ARN("states", "activity:cdk-worker")) {
		t.Errorf("GetAtt Worker.Arn did not resolve into the definition:\n%s", sm.Definition)
	}
	aliasARN := awsident.ARN("states", "stateMachine:aliased:live")
	if got := rep.Outputs["AliasArn"]; got != aliasARN {
		t.Errorf("Ref Live = %q, want %q", got, aliasARN)
	}
	if got := rep.Outputs["ActivityArn"]; got != awsident.ARN("states", "activity:cdk-worker") {
		t.Errorf("GetAtt Worker.Arn = %q", got)
	}

	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf, awsident.Default()); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}

	cfg := aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	sfn := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	alias, err := sfn.DescribeStateMachineAlias(ctx, &awssfn.DescribeStateMachineAliasInput{StateMachineAliasArn: aws.String(aliasARN)})
	if err != nil {
		t.Fatalf("the deployed alias does not exist: %v", err)
	}
	if n := len(alias.RoutingConfiguration); n != 1 || !strings.HasSuffix(aws.ToString(alias.RoutingConfiguration[0].StateMachineVersionArn), ":1") {
		t.Errorf("alias routes to %+v, want version 1 alone", alias.RoutingConfiguration)
	}
	versions, err := sfn.ListStateMachineVersions(ctx, &awssfn.ListStateMachineVersionsInput{
		StateMachineArn: aws.String(awsident.ARN("states", "stateMachine:aliased")),
	})
	if err != nil || len(versions.StateMachineVersions) != 1 {
		t.Fatalf("two applies of an unchanged definition should leave one version, got %d (%v)", len(versions.StateMachineVersions), err)
	}
	if _, err := sfn.DescribeActivity(ctx, &awssfn.DescribeActivityInput{ActivityArn: aws.String(awsident.ARN("states", "activity:cdk-worker"))}); err != nil {
		t.Fatalf("the deployed activity does not exist: %v", err)
	}
	// An execution started on the alias runs the version it routes to.
	started, err := sfn.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: aws.String(aliasARN), Input: aws.String(`{}`)})
	if err != nil {
		t.Fatalf("StartExecution on the alias: %v", err)
	}
	desc, err := sfn.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: started.ExecutionArn})
	if err != nil || aws.ToString(desc.StateMachineAliasArn) != aliasARN {
		t.Errorf("execution on the alias describes alias %q (%v)", aws.ToString(desc.StateMachineAliasArn), err)
	}

	// Export carries the three resources back out as a template that parses
	// and transpiles to the same stack.
	exported, err := provision.Export(ctx, stack.Handler(), awsident.Default())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	out, err := cloudformation.Emit(exported)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	for _, want := range []string{"AWS::StepFunctions::StateMachineVersion", "AWS::StepFunctions::StateMachineAlias", "AWS::StepFunctions::Activity"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("exported template lacks %s:\n%s", want, out)
		}
	}
	again, err := cloudformation.Parse(out)
	if err != nil {
		t.Fatalf("exported template does not parse: %v", err)
	}
	round, _, err := cloudformation.Transpile(again, cloudformation.TranspileOptions{StackName: "sfn"})
	if err != nil {
		t.Fatalf("exported template does not transpile: %v", err)
	}
	if r := round.StateMachines["aliased"]; !r.Publish || r.Aliases["live"].Description != "what production runs" {
		t.Errorf("round trip lost the version or alias: %+v", r)
	}
}
