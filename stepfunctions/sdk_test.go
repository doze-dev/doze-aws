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
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

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

// TestSDKStagedOperationsSayNotYet — StartExecution is deliberately absent in
// stage 1, and it must say so rather than accept work it would silently drop.
func TestSDKStagedOperationsSayNotYet(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("staged"),
		Definition: aws.String(helloWorld),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
	})
	if err == nil {
		t.Fatal("StartExecution succeeded, but executions are not implemented yet")
	}
	if !strings.Contains(err.Error(), "not supported by doze-aws yet") {
		t.Errorf("error = %v; it should say the operation is staged, not fail obscurely", err)
	}
}
