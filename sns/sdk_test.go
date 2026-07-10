// SDK contract tests: real aws-sdk-go-v2 SNS + SQS clients against a full
// doze-aws Stack — SNS→SQS fanout crosses the peers wiring and both services
// are reached through the shared-endpoint gateway, exactly as an application
// sees them.
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
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

var creds = credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")

// startStack boots sqs+sns+sts behind the gateway on one endpoint.
func startStack(t *testing.T) (*awssns.Client, *awssqs.Client) {
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

	snsClient := awssns.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds},
		func(o *awssns.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sqsClient := awssqs.NewFromConfig(aws.Config{Region: awsident.Region, Credentials: creds},
		func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	return snsClient, sqsClient
}

func TestSNSSDKFanout(t *testing.T) {
	ctx := context.Background()
	snsClient, sqsClient := startStack(t)

	t.Run("fanout to SQS (raw delivery)", func(t *testing.T) {
		qURL := createQueue(t, ctx, sqsClient, "emails")
		qARN := queueARNOf(t, ctx, sqsClient, qURL)
		topic := createTopic(t, ctx, snsClient, "events")

		subscribe(t, ctx, snsClient, topic, "sqs", qARN, map[string]string{"RawMessageDelivery": "true"})
		publish(t, ctx, snsClient, topic, "hello fanout", nil)

		got := receiveOne(t, ctx, sqsClient, qURL)
		if got != "hello fanout" {
			t.Fatalf("fanout body = %q, want %q", got, "hello fanout")
		}
	})

	t.Run("envelope delivery carries topic + message", func(t *testing.T) {
		qURL := createQueue(t, ctx, sqsClient, "enveloped")
		qARN := queueARNOf(t, ctx, sqsClient, qURL)
		topic := createTopic(t, ctx, snsClient, "env-topic")
		subscribe(t, ctx, snsClient, topic, "sqs", qARN, nil) // no raw delivery

		publish(t, ctx, snsClient, topic, "wrapped", nil)
		body := receiveOne(t, ctx, sqsClient, qURL)
		var envelope map[string]any
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			t.Fatalf("expected JSON envelope, got %q", body)
		}
		if envelope["Type"] != "Notification" || envelope["Message"] != "wrapped" || envelope["TopicArn"] != topic {
			t.Fatalf("envelope = %v", envelope)
		}
	})

	t.Run("filter policy", func(t *testing.T) {
		qURL := createQueue(t, ctx, sqsClient, "filtered")
		qARN := queueARNOf(t, ctx, sqsClient, qURL)
		topic := createTopic(t, ctx, snsClient, "filter-topic")
		subscribe(t, ctx, snsClient, topic, "sqs", qARN, map[string]string{
			"RawMessageDelivery": "true",
			"FilterPolicy":       `{"eventType":["created"]}`,
		})

		attr := func(v string) map[string]snstypes.MessageAttributeValue {
			return map[string]snstypes.MessageAttributeValue{
				"eventType": {DataType: aws.String("String"), StringValue: aws.String(v)},
			}
		}
		publish(t, ctx, snsClient, topic, "nope", attr("deleted")) // filtered out
		publish(t, ctx, snsClient, topic, "yes", attr("created"))  // matches

		got := receiveOne(t, ctx, sqsClient, qURL)
		if got != "yes" {
			t.Fatalf("filter delivered %q, want only %q", got, "yes")
		}
	})

	t.Run("http webhook with confirmation", func(t *testing.T) {
		received := make(chan map[string]any, 4)
		webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			received <- body
			w.WriteHeader(http.StatusOK)
		}))
		defer webhook.Close()

		topic := createTopic(t, ctx, snsClient, "webhook-topic")
		subscribe(t, ctx, snsClient, topic, "http", webhook.URL, nil)

		conf := waitMsg(t, received)
		if conf["Type"] != "SubscriptionConfirmation" {
			t.Fatalf("expected SubscriptionConfirmation, got %v", conf["Type"])
		}
		// Confirm by fetching SubscribeURL, as a real endpoint would.
		resp, err := http.Get(conf["SubscribeURL"].(string))
		if err != nil {
			t.Fatalf("confirm: %v", err)
		}
		resp.Body.Close()

		publish(t, ctx, snsClient, topic, "ping", nil)
		note := waitMsg(t, received)
		if note["Type"] != "Notification" || note["Message"] != "ping" {
			t.Fatalf("webhook notification wrong: %v", note)
		}
	})
}

func TestSNSSDKTagsAndAttributes(t *testing.T) {
	ctx := context.Background()
	snsClient, _ := startStack(t)

	topic := createTopic(t, ctx, snsClient, "tagged")
	if _, err := snsClient.TagResource(ctx, &awssns.TagResourceInput{
		ResourceArn: aws.String(topic),
		Tags:        []snstypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
	tags, err := snsClient.ListTagsForResource(ctx, &awssns.ListTagsForResourceInput{ResourceArn: aws.String(topic)})
	if err != nil || len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("ListTagsForResource: %v %+v", err, tags)
	}

	if _, err := snsClient.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{
		TopicArn: aws.String(topic), AttributeName: aws.String("DisplayName"), AttributeValue: aws.String("Tagged Topic"),
	}); err != nil {
		t.Fatalf("SetTopicAttributes: %v", err)
	}
	attrs, err := snsClient.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{TopicArn: aws.String(topic)})
	if err != nil {
		t.Fatalf("GetTopicAttributes: %v", err)
	}
	if attrs.Attributes["DisplayName"] != "Tagged Topic" {
		t.Errorf("DisplayName = %q; attrs = %v", attrs.Attributes["DisplayName"], attrs.Attributes)
	}
}

// ---- helpers ----

func createQueue(t *testing.T, ctx context.Context, c *awssqs.Client, name string) string {
	t.Helper()
	out, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateQueue %s: %v", name, err)
	}
	return aws.ToString(out.QueueUrl)
}

func queueARNOf(t *testing.T, ctx context.Context, c *awssqs.Client, qurl string) string {
	t.Helper()
	out, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(qurl), AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}
	return out.Attributes["QueueArn"]
}

func receiveOne(t *testing.T, ctx context.Context, c *awssqs.Client, qurl string) string {
	t.Helper()
	out, err := c.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl: aws.String(qurl), MaxNumberOfMessages: 1, WaitTimeSeconds: 3,
	})
	if err != nil || len(out.Messages) == 0 {
		t.Fatalf("ReceiveMessage: %v, %d msgs", err, len(out.Messages))
	}
	return aws.ToString(out.Messages[0].Body)
}

func createTopic(t *testing.T, ctx context.Context, c *awssns.Client, name string) string {
	t.Helper()
	out, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateTopic %s: %v", name, err)
	}
	return aws.ToString(out.TopicArn)
}

func subscribe(t *testing.T, ctx context.Context, c *awssns.Client, topic, proto, endpoint string, attrs map[string]string) {
	t.Helper()
	_, err := c.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: aws.String(topic), Protocol: aws.String(proto), Endpoint: aws.String(endpoint),
		Attributes: attrs, ReturnSubscriptionArn: true,
	})
	if err != nil {
		t.Fatalf("Subscribe %s->%s: %v", proto, endpoint, err)
	}
}

func publish(t *testing.T, ctx context.Context, c *awssns.Client, topic, msg string, attrs map[string]snstypes.MessageAttributeValue) {
	t.Helper()
	_, err := c.Publish(ctx, &awssns.PublishInput{TopicArn: aws.String(topic), Message: aws.String(msg), MessageAttributes: attrs})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func waitMsg(t *testing.T, ch chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
		return nil
	}
}
