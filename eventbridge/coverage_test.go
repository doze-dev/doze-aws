package eventbridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

func TestSDKRuleAndBusAdmin(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)

	bus, _ := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("mybus")})
	_ = bus
	if _, err := eb.DescribeEventBus(ctx, &awseb.DescribeEventBusInput{Name: aws.String("mybus")}); err != nil {
		t.Fatalf("DescribeEventBus: %v", err)
	}

	eb.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String("r"), EventBusName: aws.String("mybus"), EventPattern: aws.String(`{"source":["x"]}`)})
	eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"), EventBusName: aws.String("mybus"),
		Targets: []ebtypes.Target{{Id: aws.String("1"), Arn: aws.String("arn:aws:sqs:us-east-1:000000000000:q")}},
	})

	if _, err := eb.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{Rule: aws.String("r"), EventBusName: aws.String("mybus")}); err != nil {
		t.Fatalf("ListTargetsByRule: %v", err)
	}
	if _, err := eb.ListRuleNamesByTarget(ctx, &awseb.ListRuleNamesByTargetInput{TargetArn: aws.String("arn:aws:sqs:us-east-1:000000000000:q"), EventBusName: aws.String("mybus")}); err != nil {
		t.Fatalf("ListRuleNamesByTarget: %v", err)
	}
	if _, err := eb.DisableRule(ctx, &awseb.DisableRuleInput{Name: aws.String("r"), EventBusName: aws.String("mybus")}); err != nil {
		t.Fatalf("DisableRule: %v", err)
	}
	if _, err := eb.EnableRule(ctx, &awseb.EnableRuleInput{Name: aws.String("r"), EventBusName: aws.String("mybus")}); err != nil {
		t.Fatalf("EnableRule: %v", err)
	}

	// Tags on the rule ARN.
	arn := "arn:aws:events:us-east-1:000000000000:rule/mybus/r"
	if _, err := eb.TagResource(ctx, &awseb.TagResourceInput{ResourceARN: aws.String(arn), Tags: []ebtypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}}}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
	if _, err := eb.ListTagsForResource(ctx, &awseb.ListTagsForResourceInput{ResourceARN: aws.String(arn)}); err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}
	if _, err := eb.UntagResource(ctx, &awseb.UntagResourceInput{ResourceARN: aws.String(arn), TagKeys: []string{"k"}}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	// RemoveTargets + DeleteRule + DeleteEventBus.
	if _, err := eb.RemoveTargets(ctx, &awseb.RemoveTargetsInput{Rule: aws.String("r"), EventBusName: aws.String("mybus"), Ids: []string{"1"}}); err != nil {
		t.Fatalf("RemoveTargets: %v", err)
	}
	if _, err := eb.DeleteRule(ctx, &awseb.DeleteRuleInput{Name: aws.String("r"), EventBusName: aws.String("mybus")}); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	if _, err := eb.DeleteEventBus(ctx, &awseb.DeleteEventBusInput{Name: aws.String("mybus")}); err != nil {
		t.Fatalf("DeleteEventBus: %v", err)
	}
}
