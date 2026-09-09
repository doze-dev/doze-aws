package logs_test

// From the post-batch audit: a deleted log group's subscription filters
// kept shipping through the fan-out's compiled cache once the group name
// was reused (a function replaced under the same name), and a batch
// enqueued while the server closed sent on a closed channel.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/logs"
	"github.com/doze-dev/doze-aws/peers"
)

func TestDeletedGroupStopsShipping(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	var deliveries atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveries.Add(1)
		w.WriteHeader(202)
	}))
	defer fake.Close()
	s, err := logs.New(logs.Options{DataDir: t.TempDir(), Logf: t.Logf,
		Peers: peers.Static{"lambda": peers.Endpoint{Client: fake.Client(), BaseURL: fake.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	c := cwl.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")},
		func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL); o.Retryer = aws.NopRetryer{} })
	ctx := context.Background()
	group := "/aws/lambda/replaced"
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("all"),
		FilterPattern: aws.String(""), DestinationArn: aws.String(awsident.ARN("lambda", "function:sink"))}); err != nil {
		t.Fatal(err)
	}
	put(t, c, group, "s1", 1000, "one")
	deadline := time.Now().Add(5 * time.Second)
	for deliveries.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if deliveries.Load() != 1 {
		t.Fatalf("the filter did not ship: %d deliveries", deliveries.Load())
	}

	// Delete the group, recreate it with no filter, write again.
	if _, err := c.DeleteLogGroup(ctx, &cwl.DeleteLogGroupInput{LogGroupName: aws.String(group)}); err != nil {
		t.Fatal(err)
	}
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	desc, _ := c.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{LogGroupName: aws.String(group)})
	if len(desc.SubscriptionFilters) != 0 {
		t.Fatalf("the recreated group has filters: %+v", desc.SubscriptionFilters)
	}
	put(t, c, group, "s2", 2000, "two")
	time.Sleep(300 * time.Millisecond)
	if deliveries.Load() != 1 {
		t.Fatalf("the deleted group's filter kept shipping: %d deliveries", deliveries.Load())
	}
}

func TestPutAfterCloseDoesNotPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	s, err := logs.New(logs.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c := cwl.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")},
		func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL); o.Retryer = aws.NopRetryer{} })
	ctx := context.Background()
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/late")})
	c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String("/late"), FilterName: aws.String("all"),
		FilterPattern: aws.String(""), DestinationArn: aws.String(awsident.ARN("lambda", "function:sink"))})
	s.Close()
	// A write after Close is refused or dropped, never a crash.
	c.PutLogEvents(ctx, &cwl.PutLogEventsInput{LogGroupName: aws.String("/late"), LogStreamName: aws.String("s"),
		LogEvents: []cwltypes.InputLogEvent{{Message: aws.String("late"), Timestamp: aws.Int64(time.Now().UnixMilli())}}})
}
