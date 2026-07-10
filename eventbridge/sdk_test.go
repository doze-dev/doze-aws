// SDK contract tests: a real aws-sdk-go-v2 EventBridge client, with real
// cross-service delivery — a rule whose target is an SQS queue, driven through
// a full doze-aws Stack so PutEvents actually lands a message in SQS.
package eventbridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func startStack(t *testing.T) (*awseb.Client, *awssqs.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	eb := awseb.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds},
		func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds},
		func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	return eb, sqs
}

func TestSDKRuleToSQSDelivery(t *testing.T) {
	ctx := context.Background()
	eb, sqs := startStack(t)

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders-events")})
	if err != nil {
		t.Fatal(err)
	}
	arn, _ := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	queueArn := arn.Attributes["QueueArn"]

	// A rule that matches OrderCreated events on the app.orders source.
	pattern := `{"source": ["app.orders"], "detail-type": ["OrderCreated"]}`
	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("order-created"), EventPattern: aws.String(pattern),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}
	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("order-created"),
		Targets: []ebtypes.Target{{Id: aws.String("q"), Arn: aws.String(queueArn)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	// Publish a matching and a non-matching event.
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{Source: aws.String("app.orders"), DetailType: aws.String("OrderCreated"),
				Detail: aws.String(`{"orderId": "42", "total": 100}`)},
			{Source: aws.String("app.orders"), DetailType: aws.String("OrderCancelled"),
				Detail: aws.String(`{"orderId": "43"}`)},
		},
	}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	// Exactly the matching event reaches the queue.
	recv, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: q.QueueUrl, WaitTimeSeconds: 3, MaxNumberOfMessages: 10,
	})
	if err != nil || len(recv.Messages) != 1 {
		t.Fatalf("ReceiveMessage: %v, %d messages", err, len(recv.Messages))
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(recv.Messages[0].Body)), &event); err != nil {
		t.Fatalf("delivered body is not an event: %v", err)
	}
	if event["detail-type"] != "OrderCreated" || event["source"] != "app.orders" {
		t.Fatalf("delivered event = %v", event)
	}
}

func TestSDKInputTransformer(t *testing.T) {
	ctx := context.Background()
	eb, sqs := startStack(t)

	q, _ := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("shaped")})
	arn, _ := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})

	eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("shape"), EventPattern: aws.String(`{"source": ["app"]}`),
	})
	eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("shape"),
		Targets: []ebtypes.Target{{
			Id:  aws.String("q"),
			Arn: aws.String(arn.Attributes["QueueArn"]),
			InputTransformer: &ebtypes.InputTransformer{
				InputPathsMap: map[string]string{"id": "$.detail.orderId"},
				InputTemplate: aws.String(`{"order": "<id>"}`),
			},
		}},
	})
	eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("X"), Detail: aws.String(`{"orderId": "99"}`)},
	}})

	recv, err := sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, WaitTimeSeconds: 3})
	if err != nil || len(recv.Messages) != 1 {
		t.Fatalf("ReceiveMessage: %v, %d", err, len(recv.Messages))
	}
	if body := aws.ToString(recv.Messages[0].Body); body != `{"order": "99"}` {
		t.Fatalf("transformed body = %q", body)
	}
}

func TestSDKRulesBusesAndTestPattern(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)

	// Custom bus.
	if _, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("app-bus")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}
	buses, _ := eb.ListEventBuses(ctx, &awseb.ListEventBusesInput{})
	if len(buses.EventBuses) != 2 { // default + app-bus
		t.Fatalf("buses = %d", len(buses.EventBuses))
	}

	// Rule on the custom bus, disable/enable, list.
	eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("r1"), EventBusName: aws.String("app-bus"),
		EventPattern: aws.String(`{"source": ["x"]}`),
	})
	eb.DisableRule(ctx, &awseb.DisableRuleInput{Name: aws.String("r1"), EventBusName: aws.String("app-bus")})
	desc, _ := eb.DescribeRule(ctx, &awseb.DescribeRuleInput{Name: aws.String("r1"), EventBusName: aws.String("app-bus")})
	if desc.State != ebtypes.RuleStateDisabled {
		t.Fatalf("state = %v", desc.State)
	}
	rules, _ := eb.ListRules(ctx, &awseb.ListRulesInput{EventBusName: aws.String("app-bus")})
	if len(rules.Rules) != 1 {
		t.Fatalf("rules = %d", len(rules.Rules))
	}

	// TestEventPattern.
	res, err := eb.TestEventPattern(ctx, &awseb.TestEventPatternInput{
		EventPattern: aws.String(`{"detail": {"n": [{"numeric": [">", 5]}]}}`),
		Event:        aws.String(`{"source":"a","detail-type":"b","detail":{"n":10}}`),
	})
	if err != nil || !res.Result {
		t.Fatalf("TestEventPattern: %v result=%v", err, res != nil && res.Result)
	}

	// Invalid pattern is rejected with the AWS code.
	_, err = eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("bad"), EventPattern: aws.String(`{"source": {"bad": 1}}`),
	})
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidEventPatternException" {
		t.Fatalf("bad pattern error = %v", err)
	}

	// Schedule rules answer honestly until Phase 8.
	_, err = eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("sched"), ScheduleExpression: aws.String("rate(5 minutes)"),
	})
	if !errors.As(err, &ae) || ae.ErrorCode() != "UnsupportedOperationException" {
		t.Fatalf("schedule rule error = %v", err)
	}
}
