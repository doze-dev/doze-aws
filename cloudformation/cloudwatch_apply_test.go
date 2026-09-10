package cloudformation_test

// A template with an alarm, a dashboard and a metric filter deploys, the
// alarm fires, and the whole thing survives an export/emit round trip.
//
// This is the shape a CDK app takes: `new Alarm(...)` with an SNS action, and
// a `MetricFilter` on a log group. Both were in ignoredTypes with the reason
// "there is no CloudWatch locally", which meant a stack like this transpiled
// and deployed and the alarm simply never existed.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/provision"
)

const cloudwatchTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Alerts:
    Type: AWS::SNS::Topic
    Properties:
      TopicName: alerts
  AppLogs:
    Type: AWS::Logs::LogGroup
    Properties:
      LogGroupName: /app/api
  ErrorCount:
    Type: AWS::Logs::MetricFilter
    Properties:
      LogGroupName: !Ref AppLogs
      FilterName: errors
      FilterPattern: "ERROR"
      MetricTransformations:
        - MetricNamespace: Shop
          MetricName: Errors
          MetricValue: "1"
          Unit: Count
  ErrorsAlarm:
    Type: AWS::CloudWatch::Alarm
    Properties:
      AlarmName: too-many-errors
      AlarmDescription: the API is erroring
      Namespace: Shop
      MetricName: Errors
      Statistic: Sum
      Period: 60
      EvaluationPeriods: 1
      Threshold: 2
      ComparisonOperator: GreaterThanOrEqualToThreshold
      TreatMissingData: notBreaching
      Dimensions:
        - Name: Stage
          Value: prod
      AlarmActions:
        - !Ref Alerts
  Overview:
    Type: AWS::CloudWatch::Dashboard
    Properties:
      DashboardName: overview
      DashboardBody: '{"widgets":[{"type":"metric"}]}'
`

func TestApplyCloudWatchAlarmsAndFilters(t *testing.T) {
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

	tmpl, err := cloudformation.Parse([]byte(cloudwatchTemplate))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	// Nothing may be reported as ignored: that was the old behaviour, and it
	// is what made a template like this deploy without an alarm.
	for _, e := range rep.Entries {
		if e.Kind == cloudformation.Ignored {
			t.Errorf("%s (%s) is still ignored: %s", e.LogicalID, e.Type, e.Reason)
		}
	}

	a, ok := sf.Alarms["too-many-errors"]
	if !ok {
		t.Fatalf("the alarm did not map: %+v", sf.Alarms)
	}
	if a.Namespace != "Shop" || a.MetricName != "Errors" || a.Statistic != "Sum" ||
		a.Period != 60 || a.EvaluationPeriods != 1 || a.Threshold != 2 ||
		a.ComparisonOperator != "GreaterThanOrEqualToThreshold" ||
		a.TreatMissingData != "notBreaching" || a.Dimensions["Stage"] != "prod" {
		t.Fatalf("the alarm mapped wrongly: %+v", a)
	}
	// !Ref on a topic resolves to its ARN, which is what an alarm action is.
	if len(a.AlarmActions) != 1 || a.AlarmActions[0] != awsident.ARN("sns", "alerts") {
		t.Fatalf("the alarm action did not resolve: %+v", a.AlarmActions)
	}
	group := sf.LogGroups["/app/api"]
	if len(group.MetricFilters) != 1 || group.MetricFilters[0].Name != "errors" ||
		len(group.MetricFilters[0].Transformations) != 1 {
		t.Fatalf("the metric filter did not land on its group: %+v", group)
	}

	// Twice: apply is convergent, and PutMetricAlarm is an upsert.
	for i := range 2 {
		if _, err := provision.Apply(ctx, stack.Handler(), sf); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}

	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	cw := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	logsc := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	alarms, err := cw.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{
		AlarmNames: []string{"too-many-errors"}})
	if err != nil || len(alarms.MetricAlarms) != 1 {
		t.Fatalf("the alarm was not deployed: %+v %v", alarms, err)
	}
	if got := aws.ToString(alarms.MetricAlarms[0].AlarmDescription); got != "the API is erroring" {
		t.Errorf("description = %q", got)
	}
	dash, err := cw.GetDashboard(ctx, &awscw.GetDashboardInput{DashboardName: aws.String("overview")})
	if err != nil {
		t.Fatalf("the dashboard was not deployed: %v", err)
	}
	// Verbatim, not re-marshalled: a caller diffing what it declared against
	// what it reads back must see no change it did not make.
	if got := aws.ToString(dash.DashboardBody); got != `{"widgets":[{"type":"metric"}]}` {
		t.Errorf("the dashboard body was rewritten: %q", got)
	}
	filters, err := logsc.DescribeMetricFilters(ctx, &cwl.DescribeMetricFiltersInput{
		LogGroupName: aws.String("/app/api")})
	if err != nil || len(filters.MetricFilters) != 1 {
		t.Fatalf("the metric filter was not deployed: %+v %v", filters, err)
	}

	// The whole point of the alarm: three ERROR lines cross the threshold of
	// two, and the alarm the template declared flips to ALARM.
	now := time.Now().UnixMilli()
	if _, err := logsc.CreateLogStream(ctx, &cwl.CreateLogStreamInput{
		LogGroupName: aws.String("/app/api"), LogStreamName: aws.String("main")}); err != nil {
		t.Fatal(err)
	}
	if _, err := logsc.PutLogEvents(ctx, &cwl.PutLogEventsInput{
		LogGroupName: aws.String("/app/api"), LogStreamName: aws.String("main"),
		LogEvents: []logstypes.InputLogEvent{
			{Timestamp: aws.Int64(now), Message: aws.String("ERROR one")},
			{Timestamp: aws.Int64(now + 1), Message: aws.String("ERROR two")},
			{Timestamp: aws.Int64(now + 2), Message: aws.String("ERROR three")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// The filter publishes undimensioned, and the alarm watches Stage=prod,
	// so the alarm's own metric is published directly — the filter's job here
	// is to prove the template's filter reached the service.
	//
	// The timestamps matter, and getting them wrong is not a flaky test, it
	// is a test that quietly checks nothing.
	//
	// The evaluator lags by one period on purpose — the period containing
	// "now" is still filling, and judging a partial period makes an alarm
	// flap — so its window is the last COMPLETE minute. A datum stamped now
	// falls outside it and the alarm reads notBreaching and goes OK.
	//
	// Stamping one datum into the previous minute is not enough either: this
	// test polls for tens of seconds, and if the minute boundary passes while
	// it waits, the window advances to a minute with nothing in it. That does
	// not read as "no data" — TreatMissingData is notBreaching, so the empty
	// period resolves to a datapoint that did NOT breach and the alarm settles
	// on OK. So the breaching value goes into every minute the window can
	// reach while the test waits: the two before this one, and this one, whose
	// period completes partway through the wait.
	minute := time.Now().Truncate(time.Minute)
	var data []cwtypes.MetricDatum
	for _, offset := range []time.Duration{
		30 * time.Second, -30 * time.Second, -90 * time.Second,
	} {
		data = append(data, cwtypes.MetricDatum{
			MetricName: aws.String("Errors"), Value: aws.Float64(3),
			Timestamp:  aws.Time(minute.Add(offset)),
			Dimensions: []cwtypes.Dimension{{Name: aws.String("Stage"), Value: aws.String("prod")}},
		})
	}
	if _, err := cw.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"), MetricData: data,
	}); err != nil {
		t.Fatal(err)
	}
	waitForAlarmState(t, cw, ctx, "too-many-errors", "ALARM")

	// Round trip: export what is live, emit a template, transpile it again,
	// and the alarm must come back the same.
	exported, err := provision.Export(ctx, stack.Handler())
	if err != nil {
		t.Fatal(err)
	}
	out, err := cloudformation.Emit(exported)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cloudformation.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	round, _, err := cloudformation.Transpile(again, cloudformation.TranspileOptions{StackName: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	back, ok := round.Alarms["too-many-errors"]
	if !ok {
		t.Fatalf("the alarm did not survive the round trip: %+v", round.Alarms)
	}
	if back.Namespace != a.Namespace || back.MetricName != a.MetricName ||
		back.Statistic != a.Statistic || back.Period != a.Period ||
		back.Threshold != a.Threshold || back.ComparisonOperator != a.ComparisonOperator ||
		back.TreatMissingData != a.TreatMissingData ||
		back.Dimensions["Stage"] != a.Dimensions["Stage"] {
		t.Errorf("the alarm changed across the round trip:\n before %+v\n after  %+v", a, back)
	}
	if round.Dashboards["overview"].Body != `{"widgets":[{"type":"metric"}]}` {
		t.Errorf("the dashboard body changed across the round trip: %q",
			round.Dashboards["overview"].Body)
	}
	if len(round.LogGroups["/app/api"].MetricFilters) != 1 {
		t.Errorf("the metric filter did not survive the round trip: %+v",
			round.LogGroups["/app/api"])
	}
}

// waitForAlarmState polls until the alarm reaches want; the evaluator runs on
// its own ticker, so the transition is not synchronous with the publish.
func waitForAlarmState(t *testing.T, c *awscw.Client, ctx context.Context, name, want string) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	last, reason := "", ""
	for time.Now().Before(deadline) {
		out, err := c.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{AlarmNames: []string{name}})
		if err == nil && len(out.MetricAlarms) == 1 {
			last = string(out.MetricAlarms[0].StateValue)
			reason = aws.ToString(out.MetricAlarms[0].StateReason)
			if last == want {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("alarm %s is %s, want %s (reason: %s)", name, last, want, reason)
}
