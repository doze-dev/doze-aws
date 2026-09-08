package sns_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// A topic's delivery status attributes switch on one record per delivery
// attempt: successes to sns/<region>/<account>/<topic>, failures to its
// /Failure group, and nothing for a topic that has not asked.
func TestDeliveryStatusLogging(t *testing.T) {
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
	cfg := aws.Config{Region: awsident.Region, Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	logs := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	// An endpoint that confirms its subscription and then refuses every
	// notification, so an HTTP delivery is a failed attempt.
	confirm := make(chan string, 1)
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["Type"] == "SubscriptionConfirmation" {
			confirm <- body["SubscribeURL"].(string)
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(503)
	}))
	defer refusing.Close()

	topic, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("orders"), Attributes: map[string]string{
		"SQSSuccessFeedbackRoleArn":  "arn:aws:iam::000000000000:role/sns-logs",
		"HTTPFailureFeedbackRoleArn": "arn:aws:iam::000000000000:role/sns-logs",
	}})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("orders-q")})
	attrs, _ := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{TopicArn: topic.TopicArn, Protocol: aws.String("sqs"), Endpoint: aws.String(attrs.Attributes["QueueArn"])}); err != nil {
		t.Fatal(err)
	}
	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{TopicArn: topic.TopicArn, Protocol: aws.String("http"), Endpoint: aws.String(refusing.URL)}); err != nil {
		t.Fatal(err)
	}
	select {
	case u := <-confirm:
		if resp, err := http.Get(u); err != nil {
			t.Fatal(err)
		} else {
			resp.Body.Close()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no SubscriptionConfirmation reached the endpoint")
	}
	pub, err := sns.Publish(ctx, &awssns.PublishInput{TopicArn: topic.TopicArn, Message: aws.String("order 7")})
	if err != nil {
		t.Fatal(err)
	}

	type record struct {
		Notification struct{ MessageID, TopicArn string }
		Delivery     struct {
			Destination      string
			ProviderResponse string
			StatusCode       int
			Attempts         int
		}
		Status string
	}
	read := func(group string, n int) []record {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			res, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(group)})
			if err == nil && len(res.Events) >= n {
				var out []record
				for _, ev := range res.Events {
					var r record
					if err := json.Unmarshal([]byte(aws.ToString(ev.Message)), &r); err != nil {
						t.Fatalf("a delivery record is not JSON: %s", aws.ToString(ev.Message))
					}
					out = append(out, r)
				}
				return out
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never held %d records (%v)", group, n, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	base := "sns/" + awsident.Region + "/" + awsident.AccountID + "/orders"
	ok := read(base, 1)
	if ok[0].Status != "SUCCESS" || ok[0].Notification.MessageID != aws.ToString(pub.MessageId) || ok[0].Delivery.Destination != attrs.Attributes["QueueArn"] || ok[0].Delivery.StatusCode != 200 {
		t.Errorf("success record = %+v", ok[0])
	}
	// The 503 is a failed attempt: it lands in the Failure group with the
	// endpoint's answer, and not in the success group.
	failed := read(base+"/Failure", 1)
	if failed[0].Status != "FAILURE" || failed[0].Delivery.Destination != refusing.URL || failed[0].Delivery.StatusCode != 503 || failed[0].Delivery.ProviderResponse == "" {
		t.Errorf("failure record = %+v", failed[0])
	}
	for _, r := range ok {
		if r.Delivery.Destination == refusing.URL {
			t.Errorf("a failed HTTP delivery should not be in the success group: %+v", r)
		}
	}

	// A topic that never set the attributes writes nothing.
	quiet, _ := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("quiet")})
	sns.Subscribe(ctx, &awssns.SubscribeInput{TopicArn: quiet.TopicArn, Protocol: aws.String("sqs"), Endpoint: aws.String(attrs.Attributes["QueueArn"])})
	sns.Publish(ctx, &awssns.PublishInput{TopicArn: quiet.TopicArn, Message: aws.String("hush")})
	time.Sleep(400 * time.Millisecond)
	if _, err := logs.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String("sns/" + awsident.Region + "/" + awsident.AccountID + "/quiet")}); err == nil {
		t.Errorf("a topic without feedback attributes should have no log group")
	}
}
