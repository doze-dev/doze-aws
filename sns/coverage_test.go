package sns_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
)

func TestSDKTopicAndSubscriptionAdmin(t *testing.T) {
	ctx := context.Background()
	snsC, _ := startStack(t)

	top, _ := snsC.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("t")})
	sub, err := snsC.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: top.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String("arn:aws:sqs:us-east-1:000000000000:q"), ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// List + attributes.
	if ls, err := snsC.ListSubscriptions(ctx, &awssns.ListSubscriptionsInput{}); err != nil || len(ls.Subscriptions) != 1 {
		t.Fatalf("ListSubscriptions = %v err=%v", ls, err)
	}
	if _, err := snsC.SetSubscriptionAttributes(ctx, &awssns.SetSubscriptionAttributesInput{
		SubscriptionArn: sub.SubscriptionArn, AttributeName: aws.String("RawMessageDelivery"), AttributeValue: aws.String("true"),
	}); err != nil {
		t.Fatalf("SetSubscriptionAttributes: %v", err)
	}
	if _, err := snsC.GetSubscriptionAttributes(ctx, &awssns.GetSubscriptionAttributesInput{SubscriptionArn: sub.SubscriptionArn}); err != nil {
		t.Fatalf("GetSubscriptionAttributes: %v", err)
	}

	// PublishBatch.
	if _, err := snsC.PublishBatch(ctx, &awssns.PublishBatchInput{
		TopicArn: top.TopicArn,
		PublishBatchRequestEntries: []snstypes.PublishBatchRequestEntry{
			{Id: aws.String("1"), Message: aws.String("a")}, {Id: aws.String("2"), Message: aws.String("b")},
		},
	}); err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}

	// Permissions.
	if _, err := snsC.AddPermission(ctx, &awssns.AddPermissionInput{
		TopicArn: top.TopicArn, Label: aws.String("p"), AWSAccountId: []string{"000000000000"}, ActionName: []string{"Publish"},
	}); err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	if _, err := snsC.RemovePermission(ctx, &awssns.RemovePermissionInput{TopicArn: top.TopicArn, Label: aws.String("p")}); err != nil {
		t.Fatalf("RemovePermission: %v", err)
	}

	// Unsubscribe + DeleteTopic.
	if _, err := snsC.Unsubscribe(ctx, &awssns.UnsubscribeInput{SubscriptionArn: sub.SubscriptionArn}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if _, err := snsC.DeleteTopic(ctx, &awssns.DeleteTopicInput{TopicArn: top.TopicArn}); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
}
