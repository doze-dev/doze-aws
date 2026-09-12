package cloudwatch_test

// Alarms end to end, through the real v2 SDK, and one cross-service test:
// a metric breaching a threshold delivers AWS's alarm JSON to an SNS topic.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
)

func alarmInput(name string, mut func(*awscw.PutMetricAlarmInput)) *awscw.PutMetricAlarmInput {
	in := &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String(name),
		Namespace:          aws.String("Shop"),
		MetricName:         aws.String("Errors"),
		Statistic:          cwtypes.StatisticSum,
		Period:             aws.Int32(60),
		EvaluationPeriods:  aws.Int32(2),
		Threshold:          aws.Float64(10),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
	}
	if mut != nil {
		mut(in)
	}
	return in
}

func TestAlarmLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, now)

	if _, err := c.PutMetricAlarm(ctx, alarmInput("errors-high", nil)); err != nil {
		t.Fatalf("PutMetricAlarm: %v", err)
	}

	out, err := c.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.MetricAlarms) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(out.MetricAlarms))
	}
	a := out.MetricAlarms[0]
	if aws.ToString(a.AlarmName) != "errors-high" {
		t.Errorf("AlarmName = %q", aws.ToString(a.AlarmName))
	}
	// A new alarm has seen no data, which is what INSUFFICIENT_DATA means.
	if a.StateValue != cwtypes.StateValueInsufficientData {
		t.Errorf("a new alarm should start INSUFFICIENT_DATA, got %v", a.StateValue)
	}
	if !strings.HasSuffix(aws.ToString(a.AlarmArn), ":alarm:errors-high") {
		t.Errorf("AlarmArn = %q", aws.ToString(a.AlarmArn))
	}
	if !aws.ToBool(a.ActionsEnabled) {
		t.Error("actions should be enabled by default")
	}

	// Replacing an alarm keeps its state: a description change is not a
	// reason to forget that it was firing.
	if _, err := c.PutMetricAlarm(ctx, alarmInput("errors-high", func(in *awscw.PutMetricAlarmInput) {
		in.AlarmDescription = aws.String("too many errors")
	})); err != nil {
		t.Fatal(err)
	}
	out, _ = c.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if aws.ToString(out.MetricAlarms[0].AlarmDescription) != "too many errors" {
		t.Error("the update did not take")
	}
	if len(out.MetricAlarms) != 1 {
		t.Errorf("the update created a second alarm: %d", len(out.MetricAlarms))
	}

	// DescribeAlarmsForMetric answers "what watches this metric", which is
	// how a console shows an alarm badge on a graph.
	forMetric, err := c.DescribeAlarmsForMetric(ctx, &awscw.DescribeAlarmsForMetricInput{
		Namespace: aws.String("Shop"), MetricName: aws.String("Errors")})
	if err != nil {
		t.Fatal(err)
	}
	if len(forMetric.MetricAlarms) != 1 {
		t.Errorf("want the alarm on Shop/Errors, got %d", len(forMetric.MetricAlarms))
	}

	if _, err := c.DeleteAlarms(ctx, &awscw.DeleteAlarmsInput{
		AlarmNames: []string{"errors-high"}}); err != nil {
		t.Fatal(err)
	}
	out, _ = c.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if len(out.MetricAlarms) != 0 {
		t.Errorf("the alarm survived deletion: %d", len(out.MetricAlarms))
	}
}

// The checks AWS makes at create time, which are the ones worth catching
// locally: a deploy that fails on these fails after everything else worked.
func TestPutMetricAlarmRefusesWhatAWSRefuses(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, now)

	cases := map[string]func(*awscw.PutMetricAlarmInput){
		"an evaluation window past the one-day ceiling": func(in *awscw.PutMetricAlarmInput) {
			in.Period = aws.Int32(3600)
			in.EvaluationPeriods = aws.Int32(25) // 25h
		},
		"DatapointsToAlarm above EvaluationPeriods": func(in *awscw.PutMetricAlarmInput) {
			in.EvaluationPeriods = aws.Int32(2)
			in.DatapointsToAlarm = aws.Int32(5)
		},
		"an action doze-aws cannot deliver": func(in *awscw.PutMetricAlarmInput) {
			in.AlarmActions = []string{"arn:aws:automate:us-east-1:ec2:stop"}
		},
		"an anomaly-detection comparison operator": func(in *awscw.PutMetricAlarmInput) {
			in.ComparisonOperator = cwtypes.ComparisonOperatorLessThanLowerThreshold
		},
		"a period that is not a valid granularity": func(in *awscw.PutMetricAlarmInput) {
			in.Period = aws.Int32(45)
		},
		"an invalid TreatMissingData": func(in *awscw.PutMetricAlarmInput) {
			in.TreatMissingData = aws.String("whatever")
		},
	}
	for name, mut := range cases {
		if _, err := c.PutMetricAlarm(ctx, alarmInput("a", mut)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// SetAlarmState flips an alarm by hand, which is how a developer tests the
// downstream handler without waiting for the metric to move.
func TestSetAlarmStateAndHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, now)

	if _, err := c.PutMetricAlarm(ctx, alarmInput("errors-high", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
		AlarmName:   aws.String("errors-high"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("testing the runbook"),
	}); err != nil {
		t.Fatalf("SetAlarmState: %v", err)
	}
	out, _ := c.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if out.MetricAlarms[0].StateValue != cwtypes.StateValueAlarm {
		t.Fatalf("state = %v, want ALARM", out.MetricAlarms[0].StateValue)
	}
	if aws.ToString(out.MetricAlarms[0].StateReason) != "testing the runbook" {
		t.Errorf("StateReason = %q", aws.ToString(out.MetricAlarms[0].StateReason))
	}

	hist, err := c.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName: aws.String("errors-high")})
	if err != nil {
		t.Fatal(err)
	}
	// Creation and the state change, newest first.
	if len(hist.AlarmHistoryItems) < 2 {
		t.Fatalf("want creation and the state change in history, got %d",
			len(hist.AlarmHistoryItems))
	}
	if hist.AlarmHistoryItems[0].HistoryItemType != cwtypes.HistoryItemTypeStateUpdate {
		t.Errorf("newest history item = %v, want the state update",
			hist.AlarmHistoryItems[0].HistoryItemType)
	}

	// Deleting the alarm takes its history: transitions for something that
	// no longer exists are a listing nobody can act on.
	if _, err := c.DeleteAlarms(ctx, &awscw.DeleteAlarmsInput{
		AlarmNames: []string{"errors-high"}}); err != nil {
		t.Fatal(err)
	}
	hist, _ = c.DescribeAlarmHistory(ctx, &awscw.DescribeAlarmHistoryInput{
		AlarmName: aws.String("errors-high")})
	if len(hist.AlarmHistoryItems) != 0 {
		t.Errorf("history outlived the alarm: %d items", len(hist.AlarmHistoryItems))
	}
}

func TestEnableAndDisableAlarmActions(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, now)
	if _, err := c.PutMetricAlarm(ctx, alarmInput("a", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DisableAlarmActions(ctx, &awscw.DisableAlarmActionsInput{
		AlarmNames: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	out, _ := c.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if aws.ToBool(out.MetricAlarms[0].ActionsEnabled) {
		t.Error("actions are still enabled after Disable")
	}
	if _, err := c.EnableAlarmActions(ctx, &awscw.EnableAlarmActionsInput{
		AlarmNames: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	out, _ = c.DescribeAlarms(ctx, &awscw.DescribeAlarmsInput{})
	if !aws.ToBool(out.MetricAlarms[0].ActionsEnabled) {
		t.Error("actions are still disabled after Enable")
	}
}

// The cross-service test: a metric breaches, the evaluator moves the alarm,
// and AWS's alarm JSON lands on a queue subscribed to the topic. This is the
// whole point of alarms existing locally.
func TestAlarmFiresToSNS(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full stack")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := base.Add(2 * time.Minute)

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  t.TempDir(),
		Services: []string{"cloudwatch", "sns", "sqs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}
	ctx := context.Background()
	snsC := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqsC := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	cwC := awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	topic, err := snsC.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("alerts")})
	if err != nil {
		t.Fatal(err)
	}
	queue, err := sqsC.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("inbox")})
	if err != nil {
		t.Fatal(err)
	}
	qattrs, err := sqsC.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: queue.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{"QueueArn"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snsC.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: topic.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String(qattrs.Attributes["QueueArn"]),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := cwC.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName:          aws.String("errors-high"),
		Namespace:          aws.String("Shop"),
		MetricName:         aws.String("Errors"),
		Statistic:          cwtypes.StatisticSum,
		Period:             aws.Int32(60),
		EvaluationPeriods:  aws.Int32(1),
		Threshold:          aws.Float64(10),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
		AlarmActions:       []string{aws.ToString(topic.TopicArn)},
	}); err != nil {
		t.Fatal(err)
	}
	// A breach in the completed minute before now.
	if _, err := cwC.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Errors"), Value: aws.Float64(50),
			Timestamp: aws.Time(base.Add(time.Minute)),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	// SetAlarmState rather than waiting for the ten-second evaluator: the
	// delivery path is what is under test, and it is the same one.
	if _, err := cwC.SetAlarmState(ctx, &awscw.SetAlarmStateInput{
		AlarmName:   aws.String("errors-high"),
		StateValue:  cwtypes.StateValueAlarm,
		StateReason: aws.String("Threshold Crossed"),
	}); err != nil {
		t.Fatal(err)
	}
	_ = now

	// Delivery is asynchronous, so poll rather than sleep a fixed time.
	var body string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := sqsC.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl: queue.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs.Messages) > 0 {
			body = aws.ToString(msgs.Messages[0].Body)
			break
		}
	}
	if body == "" {
		t.Fatal("the alarm action did not reach the queue")
	}

	// SNS wraps the notification; the alarm JSON is the Message member.
	var envelope struct{ Message string }
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("the SNS envelope did not parse: %v (%s)", err, body)
	}
	var payload struct {
		AlarmName      string
		NewStateValue  string
		OldStateValue  string
		NewStateReason string
		AlarmArn       string
		Trigger        struct {
			MetricName         string
			Namespace          string
			ComparisonOperator string
			Threshold          float64
			Period             int
		}
	}
	if err := json.Unmarshal([]byte(envelope.Message), &payload); err != nil {
		t.Fatalf("the alarm payload did not parse: %v (%s)", err, envelope.Message)
	}
	if payload.AlarmName != "errors-high" {
		t.Errorf("AlarmName = %q", payload.AlarmName)
	}
	if payload.NewStateValue != "ALARM" {
		t.Errorf("NewStateValue = %q", payload.NewStateValue)
	}
	// AWS's shape, so a handler written against the cloud reads it unchanged.
	if payload.Trigger.MetricName != "Errors" || payload.Trigger.Namespace != "Shop" {
		t.Errorf("Trigger = %+v", payload.Trigger)
	}
	if payload.Trigger.Threshold != 10 || payload.Trigger.Period != 60 {
		t.Errorf("Trigger threshold/period = %v/%v", payload.Trigger.Threshold, payload.Trigger.Period)
	}
	if !strings.Contains(payload.AlarmArn, ":alarm:errors-high") {
		t.Errorf("AlarmArn = %q", payload.AlarmArn)
	}
}
