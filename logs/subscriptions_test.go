package logs_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kintypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/logs"
	"github.com/doze-dev/doze-aws/peers"
)

// awslogsMessage is the decoded subscription envelope.
type awslogsMessage struct {
	MessageType         string   `json:"messageType"`
	Owner               string   `json:"owner"`
	LogGroup            string   `json:"logGroup"`
	LogStream           string   `json:"logStream"`
	SubscriptionFilters []string `json:"subscriptionFilters"`
	LogEvents           []struct {
		ID        string `json:"id"`
		Timestamp int64  `json:"timestamp"`
		Message   string `json:"message"`
	} `json:"logEvents"`
}

func gunzipMessage(t *testing.T, data []byte) awslogsMessage {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("envelope is not gzip: %v", err)
	}
	raw, _ := io.ReadAll(zr)
	var m awslogsMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, raw)
	}
	return m
}

// A filter on a group forwards the matching lines to a Lambda function, in
// the envelope AWS sends: gzip under awslogs.data, with the group, stream,
// filter name and per-event ids.
func TestSubscriptionFilterToLambda(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	var mu sync.Mutex
	var got []awslogsMessage
	var invoked []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Awslogs struct{ Data string } `json:"awslogs"`
		}
		json.Unmarshal(body, &payload)
		data, err := base64.StdEncoding.DecodeString(payload.Awslogs.Data)
		if err != nil {
			t.Errorf("awslogs.data is not base64: %v", err)
		}
		mu.Lock()
		invoked = append(invoked, r.URL.Path+" "+r.Header.Get("X-Amz-Invocation-Type"))
		got = append(got, gunzipMessage(t, data))
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer fake.Close()
	dir := peers.Static{"lambda": peers.Endpoint{Client: fake.Client(), BaseURL: fake.URL}}
	s, err := logs.New(logs.Options{DataDir: t.TempDir(), Logf: t.Logf, Peers: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	c := cwl.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")},
		// No retries: the SDK treats LimitExceededException as throttling and
		// would back off for seconds on the limit case below.
		func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL); o.Retryer = aws.NopRetryer{} })
	ctx := context.Background()

	group, fn := "/aws/lambda/producer", awsident.ARN("lambda", "function:sink")
	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)}); err != nil {
		t.Fatal(err)
	}
	// The producer cannot subscribe itself; Firehose is refused by name.
	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("self"),
		FilterPattern: aws.String(""), DestinationArn: aws.String(awsident.ARN("lambda", "function:producer"))}); err == nil || !strings.Contains(err.Error(), "own log group") {
		t.Errorf("self-subscription should be refused, got %v", err)
	}
	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("fh"),
		FilterPattern: aws.String(""), DestinationArn: aws.String(awsident.ARN("firehose", "deliverystream/x"))}); err == nil || !strings.Contains(err.Error(), "Firehose") {
		t.Errorf("Firehose should be refused by name, got %v", err)
	}
	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("errors"),
		FilterPattern: aws.String("ERROR"), DestinationArn: aws.String(fn)}); err != nil {
		t.Fatal(err)
	}
	desc, err := c.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{LogGroupName: aws.String(group)})
	if err != nil || len(desc.SubscriptionFilters) != 1 || aws.ToString(desc.SubscriptionFilters[0].FilterPattern) != "ERROR" {
		t.Fatalf("DescribeSubscriptionFilters: %+v %v", desc, err)
	}

	put(t, c, group, "2026/09/08/[$LATEST]abc", 1000, "INFO started", "ERROR boom", "ERROR again", "INFO done")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("wanted one delivery, got %d", len(got))
	}
	if invoked[0] != "/2015-03-31/functions/sink/invocations Event" {
		t.Errorf("invoked %q", invoked[0])
	}
	m := got[0]
	if m.MessageType != "DATA_MESSAGE" || m.Owner != awsident.AccountID || m.LogGroup != group ||
		m.LogStream != "2026/09/08/[$LATEST]abc" || len(m.SubscriptionFilters) != 1 || m.SubscriptionFilters[0] != "errors" {
		t.Errorf("envelope header: %+v", m)
	}
	if len(m.LogEvents) != 2 || m.LogEvents[0].Message != "ERROR boom" || m.LogEvents[1].Message != "ERROR again" {
		t.Errorf("filtered events: %+v", m.LogEvents)
	}
	// The envelope's ids are the ids FilterLogEvents reports, so a consumer
	// can correlate a subscription record with the API.
	filtered, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(group), FilterPattern: aws.String("ERROR")})
	if err != nil || len(filtered.Events) != 2 {
		t.Fatalf("FilterLogEvents: %v %+v", err, filtered)
	}
	if m.LogEvents[0].ID != aws.ToString(filtered.Events[0].EventId) || m.LogEvents[1].ID != aws.ToString(filtered.Events[1].EventId) || m.LogEvents[0].ID == m.LogEvents[1].ID {
		t.Errorf("event ids %q %q differ from the API's %q %q", m.LogEvents[0].ID, m.LogEvents[1].ID, aws.ToString(filtered.Events[0].EventId), aws.ToString(filtered.Events[1].EventId))
	}

	// Two per group, then LimitExceededException; delete frees a slot.
	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("all"),
		FilterPattern: aws.String(""), DestinationArn: aws.String(fn)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("third"),
		FilterPattern: aws.String(""), DestinationArn: aws.String(fn)}); err == nil || !strings.Contains(err.Error(), "LimitExceeded") {
		t.Errorf("a third filter should hit the limit, got %v", err)
	}
	if _, err := c.DeleteSubscriptionFilter(ctx, &cwl.DeleteSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("all")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteSubscriptionFilter(ctx, &cwl.DeleteSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("all")}); err == nil {
		t.Error("deleting a deleted filter should be not-found")
	}
	// Deleting the group cascades.
	c.DeleteLogGroup(ctx, &cwl.DeleteLogGroupInput{LogGroupName: aws.String(group)})
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	desc, _ = c.DescribeSubscriptionFilters(ctx, &cwl.DescribeSubscriptionFiltersInput{LogGroupName: aws.String(group)})
	if len(desc.SubscriptionFilters) != 0 {
		t.Errorf("filters survived the group's deletion: %+v", desc.SubscriptionFilters)
	}
}

// Through a full stack, a Kinesis subscription lands the gzip envelope as a
// record on the stream, keyed by the log stream under ByLogStream.
func TestSubscriptionFilterToKinesis(t *testing.T) {
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
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	c := cwl.NewFromConfig(cfg, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	kin := awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	if _, err := kin.CreateStream(ctx, &awskinesis.CreateStreamInput{StreamName: aws.String("logs-out"), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatal(err)
	}
	group := "/app/api"
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	if _, err := c.PutSubscriptionFilter(ctx, &cwl.PutSubscriptionFilterInput{LogGroupName: aws.String(group), FilterName: aws.String("to-kinesis"),
		FilterPattern: aws.String(""), DestinationArn: aws.String(awsident.ARN("kinesis", "stream/logs-out")),
		Distribution: cwltypes.DistributionByLogStream}); err != nil {
		t.Fatal(err)
	}
	put(t, c, group, "web-1", 5000, "GET /health 200")

	shards, _ := kin.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String("logs-out")})
	it, err := kin.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{StreamName: aws.String("logs-out"),
		ShardId: shards.Shards[0].ShardId, ShardIteratorType: kintypes.ShardIteratorTypeTrimHorizon})
	if err != nil {
		t.Fatal(err)
	}
	var recs []kintypes.Record
	deadline := time.Now().Add(5 * time.Second)
	for len(recs) == 0 && time.Now().Before(deadline) {
		out, err := kin.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator})
		if err != nil {
			t.Fatal(err)
		}
		recs = out.Records
		time.Sleep(20 * time.Millisecond)
	}
	if len(recs) != 1 {
		t.Fatalf("wanted one record, got %d", len(recs))
	}
	if aws.ToString(recs[0].PartitionKey) != "web-1" {
		t.Errorf("ByLogStream should key by the log stream, got %q", aws.ToString(recs[0].PartitionKey))
	}
	m := gunzipMessage(t, recs[0].Data)
	if m.LogGroup != group || len(m.LogEvents) != 1 || m.LogEvents[0].Message != "GET /health 200" {
		t.Errorf("record envelope: %+v", m)
	}
}
