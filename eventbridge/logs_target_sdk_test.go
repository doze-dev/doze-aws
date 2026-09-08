package eventbridge_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// A rule target that is a CloudWatch Logs log group receives the event as a
// log line, shaped by the target's input transformer when it has one.
func TestSDKRuleToLogGroupTarget(t *testing.T) {
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
	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	eb := awseb.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds}, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	logs := cwl.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds}, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	group := "/aws/events/orders"
	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String("to-logs"), EventPattern: aws.String(`{"source":["app.orders"]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{Rule: aws.String("to-logs"), Targets: []ebtypes.Target{
		{Id: aws.String("whole"), Arn: aws.String(awsident.ARN("logs", "log-group:"+group))},
		{Id: aws.String("shaped"), Arn: aws.String(awsident.ARN("logs", "log-group:"+group+"-shaped:*")),
			InputTransformer: &ebtypes.InputTransformer{InputPathsMap: map[string]string{"id": "$.detail.orderId"}, InputTemplate: aws.String(`"order <id> arrived"`)}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app.orders"), DetailType: aws.String("OrderCreated"), Detail: aws.String(`{"orderId":"o-7"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	wait := func(g string) string {
		deadline := time.Now().Add(5 * time.Second)
		for {
			res, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(g)})
			if err == nil && len(res.Events) == 1 {
				if aws.ToString(res.Events[0].LogStreamName) != "to-logs" {
					t.Errorf("the stream should be named for the rule, got %s", aws.ToString(res.Events[0].LogStreamName))
				}
				return aws.ToString(res.Events[0].Message)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never received the event (%v)", g, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	whole := wait(group)
	if !strings.Contains(whole, `"source":"app.orders"`) || !strings.Contains(whole, `"orderId":"o-7"`) {
		t.Errorf("the whole event should be the log line: %s", whole)
	}
	if shaped := wait(group + "-shaped"); shaped != `"order o-7 arrived"` {
		t.Errorf("the transformed input should be the log line, got %s", shaped)
	}
}
