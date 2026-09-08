package cloudformation_test

// A subscription filter in a template lands on its group: a declared group
// with a Kinesis destination, and a Lambda-owned group nobody declared, which
// the transpiler adds so the filter has somewhere to land. The export carries
// both groups and their filters back.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const logsSubscriptionTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  ApiLogs:
    Type: AWS::Logs::LogGroup
    Properties:
      LogGroupName: /app/api
      RetentionInDays: 7
  ToStream:
    Type: AWS::Logs::SubscriptionFilter
    Properties:
      LogGroupName: !Ref ApiLogs
      FilterPattern: "ERROR"
      DestinationArn: arn:aws:kinesis:us-east-1:000000000000:stream/audit
      Distribution: ByLogStream
  Sink:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: sink
      Runtime: nodejs20.x
      Handler: index.handler
      Code:
        ZipFile: "exports.handler = async () => ({})"
  WorkerToSink:
    Type: AWS::Logs::SubscriptionFilter
    Properties:
      LogGroupName: /aws/lambda/worker
      FilterPattern: ""
      DestinationArn: !GetAtt Sink.Arn
`

func TestApplyLogsSubscriptionFilters(t *testing.T) {
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

	tmpl, err := cloudformation.Parse([]byte(logsSubscriptionTemplate))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "subs"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	api := sf.LogGroups["/app/api"]
	if api.RetentionDays != 7 || len(api.Subscriptions) != 1 || api.Subscriptions[0].Kinesis != "audit" || api.Subscriptions[0].Name != "ToStream" || api.Subscriptions[0].Distribution != "ByLogStream" {
		t.Fatalf("declared group did not map: %+v", api)
	}
	worker := sf.LogGroups["/aws/lambda/worker"]
	if len(worker.Subscriptions) != 1 || worker.Subscriptions[0].Lambda != "sink" {
		t.Fatalf("the undeclared Lambda group was not added for its filter: %+v", worker)
	}

	// The Kinesis stream is not stack state; it must exist first.
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	kin := awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	if _, err := kin.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("audit"), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := provision.Apply(ctx, stack.Handler(), sf); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}
	logsc := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	desc, err := logsc.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{LogGroupName: aws.String("/app/api")})
	if err != nil || len(desc.SubscriptionFilters) != 1 || !strings.HasSuffix(aws.ToString(desc.SubscriptionFilters[0].DestinationArn), "stream/audit") {
		t.Fatalf("the Kinesis filter did not land: %+v %v", desc, err)
	}
	desc, err = logsc.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{LogGroupName: aws.String("/aws/lambda/worker")})
	if err != nil || len(desc.SubscriptionFilters) != 1 || !strings.HasSuffix(aws.ToString(desc.SubscriptionFilters[0].DestinationArn), "function:sink") {
		t.Fatalf("the Lambda filter did not land: %+v %v", desc, err)
	}

	exported, err := provision.Export(ctx, stack.Handler())
	if err != nil {
		t.Fatal(err)
	}
	out, err := cloudformation.Emit(exported)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := cloudformation.Parse(out)
	round, _, err := cloudformation.Transpile(again, cloudformation.TranspileOptions{StackName: "subs"})
	if err != nil {
		t.Fatalf("exported template does not transpile: %v\n%s", err, out)
	}
	if r := round.LogGroups["/app/api"]; r.RetentionDays != 7 || len(r.Subscriptions) != 1 || r.Subscriptions[0].Kinesis != "audit" {
		t.Errorf("round trip lost the declared group's filter: %+v\n%s", r, out)
	}
	if r := round.LogGroups["/aws/lambda/worker"]; len(r.Subscriptions) != 1 || r.Subscriptions[0].Lambda != "sink" {
		t.Errorf("round trip lost the Lambda group's filter: %+v\n%s", r, out)
	}
}
