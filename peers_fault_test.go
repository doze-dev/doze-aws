package dozeaws_test

// Breaking the path between two services.
//
// Every cascade goes through a peers.Directory, and until now nothing could
// make one fail — so "the sibling is down" was a case answered by reading code.
// These tests use the real wiring with a fault in front of it, which is the
// only way to be sure the failure reaches the code that has to handle it.
//
// What is asserted is deliberately modest: the cascade's SOURCE still behaves.
// SNS accepting a Publish whose delivery then fails is correct — AWS does the
// same, delivery is asynchronous and at-least-once — and a test that demanded
// otherwise would be asserting a bug. The value here is that the failure is now
// reachable at all, and that the service survives it.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/peers"
)

func faultStack(t *testing.T, wrap func(peers.Directory) peers.Directory) (*dozeaws.Stack, aws.Config, *string) {
	t.Helper()
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: func(string, ...any) {}, Peers: wrap})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(st.Handler())
	t.Cleanup(ts.Close)
	return st, aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, aws.String(ts.URL)
}

// SNS → SQS with SQS refusing every delivery. Publish must still succeed:
// delivery is asynchronous, and the topic is not the queue's keeper.
func TestAPublishSurvivesAFailingSubscriber(t *testing.T) {
	var refused atomic.Int64
	st, cfg, ep := faultStack(t, func(d peers.Directory) peers.Directory {
		return peers.WithFaults(d, func(service string, _ *http.Request) peers.Fault {
			if service == "sqs" {
				refused.Add(1)
				return peers.Fault{Status: 500}
			}
			return peers.Fault{}
		})
	})

	ctx := context.Background()
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = ep })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("victim")})
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	if err != nil {
		t.Fatal(err)
	}
	top, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("t")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: top.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String(attrs.Attributes["QueueArn"]), ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: top.TopicArn, Message: aws.String("hello")}); err != nil {
		t.Fatalf("Publish failed because its SUBSCRIBER was down: %v\n"+
			"delivery is asynchronous; the topic is not the queue's keeper", err)
	}

	// The delivery attempt is asynchronous, so give it a moment to happen.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && refused.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if refused.Load() == 0 {
		t.Error("the fault directory was never consulted for sqs — the seam is not wired")
	}
	// And the stack did not fault answering any of it.
	if f := st.Faults(); len(f) != 0 {
		t.Errorf("a failing subscriber made the stack answer %d fault(s): %+v", len(f), f)
	}
}

// A transport error is a different failure from a 500, and takes a different
// path in every SDK. Same expectation: the publisher survives it.
func TestAPublishSurvivesAnUnreachableSubscriber(t *testing.T) {
	boom := errors.New("connection refused")
	var tried atomic.Int64
	st, cfg, ep := faultStack(t, func(d peers.Directory) peers.Directory {
		return peers.WithFaults(d, func(service string, _ *http.Request) peers.Fault {
			if service == "sqs" {
				tried.Add(1)
				return peers.Fault{Err: boom}
			}
			return peers.Fault{}
		})
	})

	ctx := context.Background()
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = ep })
	sqs := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })

	q, err := sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("victim")})
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	if err != nil {
		t.Fatal(err)
	}
	top, _ := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("t")})
	if _, err := sns.Subscribe(ctx, &awssns.SubscribeInput{
		TopicArn: top.TopicArn, Protocol: aws.String("sqs"),
		Endpoint: aws.String(attrs.Attributes["QueueArn"]), ReturnSubscriptionArn: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: top.TopicArn, Message: aws.String("hello")}); err != nil {
		t.Fatalf("Publish failed because its subscriber was unreachable: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && tried.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if tried.Load() == 0 {
		t.Error("the fault directory was never consulted for sqs")
	}
	if f := st.Faults(); len(f) != 0 {
		t.Errorf("an unreachable subscriber made the stack answer %d fault(s): %+v", len(f), f)
	}
}

// Unreachable is the other shape: not a failure, an absence. A service that is
// simply not wired must be degraded around, not errored on.
func TestAnAbsentSiblingIsDegradedAround(t *testing.T) {
	st, cfg, ep := faultStack(t, func(d peers.Directory) peers.Directory {
		return peers.Unreachable(d, "sqs")
	})
	ctx := context.Background()
	sns := awssns.NewFromConfig(cfg, func(o *awssns.Options) { o.BaseEndpoint = ep })
	top, err := sns.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("t")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sns.Publish(ctx, &awssns.PublishInput{
		TopicArn: top.TopicArn, Message: aws.String("hello")}); err != nil {
		t.Fatalf("Publish failed with no subscribers at all: %v", err)
	}
	if f := st.Faults(); len(f) != 0 {
		t.Errorf("an absent sibling made the stack answer %d fault(s): %+v", len(f), f)
	}
}
