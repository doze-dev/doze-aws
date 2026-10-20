package cloudformation_test

// plan/services/sqs.md section 9, "A queue and its DLQ in one template, through
// CloudFormation": the CreateStack call a deploy tool makes (aws cloudformation
// deploy, sam deploy, cdk deploy all end in one), not the transpile-and-apply
// path the neighbouring tests use (dlq_order_apply_test.go,
// queue_policy_apply_test.go). The queue is declared BEFORE the queue its
// RedrivePolicy names, with a GetAtt, so the stack has to order its resources by
// what refers to what, and a QueuePolicy refers to the queue by Ref and by ARN.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

const queueAndDLQTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  OrdersPolicy:
    Type: AWS::SQS::QueuePolicy
    Properties:
      Queues: [!Ref Orders]
      PolicyDocument:
        Version: "2012-10-17"
        Statement:
          - Sid: FromTopic
            Effect: Allow
            Principal: {Service: sns.amazonaws.com}
            Action: sqs:SendMessage
            Resource: !GetAtt Orders.Arn
  Orders:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: orders
      VisibilityTimeout: 45
      RedrivePolicy:
        deadLetterTargetArn: !GetAtt OrdersDlq.Arn
        maxReceiveCount: 4
  OrdersDlq:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: orders-dlq
      MessageRetentionPeriod: 1209600
Outputs:
  OrdersUrl: {Value: !Ref Orders}
  OrdersArn: {Value: !GetAtt Orders.Arn}
  DlqArn:    {Value: !GetAtt OrdersDlq.Arn}
`

func TestServiceAQueueAndItsDeadLetterQueueInOneStack(t *testing.T) {
	ctx := context.Background()
	c, sqs := cfnStack(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{StackName: aws.String("shop"), TemplateBody: aws.String(queueAndDLQTemplate)}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	desc, err := c.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("shop")})
	if err != nil {
		t.Fatal(err)
	}
	if st := desc.Stacks[0]; st.StackStatus != cfntypes.StackStatusCreateComplete {
		t.Fatalf("status = %s (%s)", st.StackStatus, aws.ToString(st.StackStatusReason))
	}
	outputs := map[string]string{}
	for _, o := range desc.Stacks[0].Outputs {
		outputs[aws.ToString(o.OutputKey)] = aws.ToString(o.OutputValue)
	}

	// The dead-letter queue is created first: its CREATE_COMPLETE is before the
	// queue's CREATE_IN_PROGRESS in the event trail (the call answers newest first).
	events, err := c.DescribeStackEvents(ctx, &awscfn.DescribeStackEventsInput{StackName: aws.String("shop")})
	if err != nil {
		t.Fatal(err)
	}
	at := func(logical string, status cfntypes.ResourceStatus) int {
		for i, e := range events.StackEvents {
			if aws.ToString(e.LogicalResourceId) == logical && e.ResourceStatus == status {
				return len(events.StackEvents) - i // oldest is 1
			}
		}
		return -1
	}
	dlqDone, ordersStart, policyStart := at("OrdersDlq", cfntypes.ResourceStatusCreateComplete), at("Orders", cfntypes.ResourceStatusCreateInProgress), at("OrdersPolicy", cfntypes.ResourceStatusCreateInProgress)
	if dlqDone < 0 || ordersStart < 0 || policyStart < 0 {
		t.Fatalf("the event trail lacks an event (dlq done %d, orders start %d, policy start %d):\n%v", dlqDone, ordersStart, policyStart, events.StackEvents)
	}
	if dlqDone > ordersStart {
		t.Errorf("the queue was created before its dead-letter queue was done (dlq complete at %d, orders in progress at %d)", dlqDone, ordersStart)
	}
	if ordersDone := at("Orders", cfntypes.ResourceStatusCreateComplete); ordersDone > policyStart {
		t.Errorf("the queue policy began (%d) before the queue it is for was created (%d)", policyStart, ordersDone)
	}

	// The redrive policy is set, naming the dead-letter queue the stack made.
	q, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{"RedrivePolicy", "Policy", "VisibilityTimeout", "QueueArn"}})
	if err != nil {
		t.Fatal(err)
	}
	var rp struct {
		Target string `json:"deadLetterTargetArn"`
		Max    any    `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(got.Attributes["RedrivePolicy"]), &rp); err != nil {
		t.Fatalf("RedrivePolicy %q: %v", got.Attributes["RedrivePolicy"], err)
	}
	if !strings.HasSuffix(rp.Target, ":orders-dlq") || (rp.Max != float64(4) && rp.Max != "4") {
		t.Errorf("RedrivePolicy = %q, want orders-dlq and a maxReceiveCount of 4", got.Attributes["RedrivePolicy"])
	}
	if got.Attributes["VisibilityTimeout"] != "45" {
		t.Errorf("VisibilityTimeout = %q, want 45", got.Attributes["VisibilityTimeout"])
	}
	if outputs["DlqArn"] != rp.Target || outputs["OrdersArn"] != got.Attributes["QueueArn"] || !strings.HasSuffix(outputs["OrdersUrl"], "/orders") {
		t.Errorf("outputs %v do not agree with the queues (dead-letter target %s, queue ARN %s)", outputs, rp.Target, got.Attributes["QueueArn"])
	}

	// The queue's policy is the template's, on the queue that was named by Ref.
	if p := got.Attributes["Policy"]; !strings.Contains(p, "FromTopic") || !strings.Contains(p, "sns.amazonaws.com") || !strings.Contains(p, got.Attributes["QueueArn"]) {
		t.Errorf("the queue has no policy from the template: %q", p)
	}

	// And the dead-letter queue knows its source: the redrive is real, not recorded.
	srcs, err := sqs.ListDeadLetterSourceQueues(ctx, &awssqs.ListDeadLetterSourceQueuesInput{QueueUrl: aws.String(strings.Replace(aws.ToString(q.QueueUrl), "/orders", "/orders-dlq", 1))})
	if err != nil || len(srcs.QueueUrls) != 1 || !strings.HasSuffix(srcs.QueueUrls[0], "/orders") {
		t.Errorf("ListDeadLetterSourceQueues(orders-dlq) = %v, %v; want orders", srcs, err)
	}

	// Deleting the stack removes both, the queue before the queue it dead-letters to.
	if _, err := c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("shop")}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orders", "orders-dlq"} {
		if _, err := sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(name)}); err == nil {
			t.Errorf("queue %s survived DeleteStack", name)
		}
	}
}
