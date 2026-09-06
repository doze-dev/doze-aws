package logs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/logs"
)

// The contract with the two tools people actually use — `aws logs tail
// --follow` and `sam logs` — is FilterLogEvents: unique eventIds, interleaved
// across streams, a nextToken only while more remain. The rest of the tests
// pin what the SDK's types and paginators expect.

func logsClient(t *testing.T) *cwl.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	s, err := logs.New(logs.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return cwl.NewFromConfig(aws.Config{
		Region:      awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func put(t *testing.T, c *cwl.Client, group, stream string, base int64, msgs ...string) {
	t.Helper()
	events := make([]cwltypes.InputLogEvent, 0, len(msgs))
	for i, m := range msgs {
		events = append(events, cwltypes.InputLogEvent{Timestamp: aws.Int64(base + int64(i)*10), Message: aws.String(m)})
	}
	if _, err := c.PutLogEvents(context.Background(), &cwl.PutLogEventsInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String(stream), LogEvents: events,
	}); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}
}

func TestSDKGroupsAndStreams(t *testing.T) {
	ctx := context.Background()
	c := logsClient(t)
	group := "/aws/lambda/orders"
	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group), Tags: map[string]string{"env": "dev"}}); err != nil {
		t.Fatal(err)
	}
	var exists *cwltypes.ResourceAlreadyExistsException
	if _, err := c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)}); !errors.As(err, &exists) {
		t.Errorf("a second create should be ResourceAlreadyExistsException, got %v", err)
	}
	if _, err := c.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{LogGroupName: aws.String(group), RetentionInDays: aws.Int32(7)}); err != nil {
		t.Fatal(err)
	}
	var invalid *cwltypes.InvalidParameterException
	if _, err := c.PutRetentionPolicy(ctx, &cwl.PutRetentionPolicyInput{LogGroupName: aws.String(group), RetentionInDays: aws.Int32(8)}); !errors.As(err, &invalid) {
		t.Errorf("8 days is not a retention AWS accepts: %v", err)
	}
	groups, err := c.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{LogGroupNamePrefix: aws.String("/aws/lambda/")})
	if err != nil || len(groups.LogGroups) != 1 {
		t.Fatalf("DescribeLogGroups = %+v, %v", groups, err)
	}
	g := groups.LogGroups[0]
	if aws.ToString(g.LogGroupName) != group || aws.ToInt32(g.RetentionInDays) != 7 || aws.ToString(g.Arn) != awsident.ARN("logs", "log-group:"+group+":*") {
		t.Errorf("group = %+v", g)
	}
	tags, err := c.ListTagsForResource(ctx, &cwl.ListTagsForResourceInput{ResourceArn: g.LogGroupArn})
	if err != nil || tags.Tags["env"] != "dev" {
		t.Errorf("tags = %v, %v", tags, err)
	}

	for _, name := range []string{"2026/09/07/[$LATEST]aaaa", "2026/09/07/[$LATEST]bbbb"} {
		if _, err := c.CreateLogStream(ctx, &cwl.CreateLogStreamInput{LogGroupName: aws.String(group), LogStreamName: aws.String(name)}); err != nil {
			t.Fatal(err)
		}
	}
	put(t, c, group, "2026/09/07/[$LATEST]aaaa", 1000, "a1", "a2")
	put(t, c, group, "2026/09/07/[$LATEST]bbbb", 1005, "b1")
	streams, err := c.DescribeLogStreams(ctx, &cwl.DescribeLogStreamsInput{
		LogGroupName: aws.String(group), OrderBy: cwltypes.OrderByLastEventTime, Descending: aws.Bool(true),
	})
	if err != nil || len(streams.LogStreams) != 2 {
		t.Fatalf("DescribeLogStreams = %+v, %v", streams, err)
	}
	if got := aws.ToString(streams.LogStreams[0].LogStreamName); got != "2026/09/07/[$LATEST]aaaa" {
		t.Errorf("newest last event first: got %s", got)
	}
	if aws.ToInt64(streams.LogStreams[0].LastEventTimestamp) != 1010 || aws.ToInt64(streams.LogStreams[0].FirstEventTimestamp) != 1000 {
		t.Errorf("stream timestamps = %+v", streams.LogStreams[0])
	}

	var notFound *cwltypes.ResourceNotFoundException
	if _, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String("/aws/lambda/ghost")}); !errors.As(err, &notFound) {
		t.Errorf("a missing group is ResourceNotFoundException (sam logs keys on it): %v", err)
	}
	if _, err := c.DeleteLogGroup(ctx, &cwl.DeleteLogGroupInput{LogGroupName: aws.String(group)}); err != nil {
		t.Fatal(err)
	}
	if out, _ := c.DescribeLogGroups(ctx, &cwl.DescribeLogGroupsInput{}); len(out.LogGroups) != 0 {
		t.Errorf("the group should be gone: %+v", out.LogGroups)
	}
}

// TestSDKTailLoop follows a group the way the CLI does: FilterLogEvents with
// startTime advanced to the newest timestamp seen, deduplicated on eventId.
// Every event must arrive exactly once, across streams, in time order.
func TestSDKTailLoop(t *testing.T) {
	ctx := context.Background()
	c := logsClient(t)
	group := "/aws/lambda/tail"
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	put(t, c, group, "s1", 1000, "one", "two", "three")
	put(t, c, group, "s2", 1015, "interleaved")

	seen := map[string]bool{}
	var order []string
	var start int64
	poll := func() {
		p := cwl.NewFilterLogEventsPaginator(c, &cwl.FilterLogEventsInput{
			LogGroupName: aws.String(group), Interleaved: aws.Bool(true), StartTime: aws.Int64(start), Limit: aws.Int32(2),
		})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range page.Events {
				if seen[aws.ToString(ev.EventId)] {
					continue
				}
				seen[aws.ToString(ev.EventId)] = true
				order = append(order, aws.ToString(ev.Message))
				if aws.ToInt64(ev.Timestamp) > start {
					start = aws.ToInt64(ev.Timestamp)
				}
				if ev.IngestionTime == nil || ev.LogStreamName == nil {
					t.Errorf("event lacks ingestionTime or logStreamName: %+v", ev)
				}
			}
		}
	}
	poll()
	put(t, c, group, "s1", 1030, "four")
	poll()
	poll() // nothing new: no duplicates
	want := "one|two|interleaved|three|four"
	if got := join(order); got != want {
		t.Errorf("tail order = %s, want %s", got, want)
	}
}

func TestSDKGetLogEventsPaginates(t *testing.T) {
	ctx := context.Background()
	c := logsClient(t)
	group, stream := "/aws/lambda/pages", "s"
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	put(t, c, group, stream, 1000, "1", "2", "3", "4", "5")

	// Forward from the head, two at a time: the paginator stops when the
	// token stops changing (StopOnDuplicateToken, the same setting the SDK
	// needs against AWS), which needs the end-of-stream token to repeat.
	p := cwl.NewGetLogEventsPaginator(c, &cwl.GetLogEventsInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String(stream), StartFromHead: aws.Bool(true), Limit: aws.Int32(2),
	}, func(o *cwl.GetLogEventsPaginatorOptions) { o.StopOnDuplicateToken = true })
	var got []string
	pages := 0
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, ev := range page.Events {
			got = append(got, aws.ToString(ev.Message))
		}
		if pages > 10 {
			t.Fatal("the paginator never stopped")
		}
	}
	if join(got) != "1|2|3|4|5" {
		t.Errorf("forward = %s", join(got))
	}
	// The default is the newest page, oldest-first within it.
	tail, err := c.GetLogEvents(ctx, &cwl.GetLogEventsInput{LogGroupName: aws.String(group), LogStreamName: aws.String(stream), Limit: aws.Int32(2)})
	if err != nil {
		t.Fatal(err)
	}
	var last []string
	for _, ev := range tail.Events {
		last = append(last, aws.ToString(ev.Message))
	}
	if join(last) != "4|5" {
		t.Errorf("newest page = %s, want 4|5", join(last))
	}
	older, err := c.GetLogEvents(ctx, &cwl.GetLogEventsInput{LogGroupName: aws.String(group), LogStreamName: aws.String(stream), Limit: aws.Int32(2), NextToken: tail.NextBackwardToken})
	if err != nil {
		t.Fatal(err)
	}
	var prev []string
	for _, ev := range older.Events {
		prev = append(prev, aws.ToString(ev.Message))
	}
	if join(prev) != "2|3" {
		t.Errorf("backward page = %s, want 2|3", join(prev))
	}
}

func TestSDKFilterPatterns(t *testing.T) {
	ctx := context.Background()
	c := logsClient(t)
	group := "/aws/lambda/pat"
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String(group)})
	put(t, c, group, "s", 1000,
		"START RequestId: 1",
		`{"level":"error","status":503,"path":"/orders"}`,
		"[ERROR] out of memory in handler",
		`{"level":"info","status":200,"path":"/health"}`,
		"DEBUG noise",
	)
	cases := []struct{ pattern, want string }{
		{"", "5"},
		{"ERROR", "1"},
		{`"out of memory"`, "1"},
		{"ERROR -memory", "0"},
		{"?START ?DEBUG", "2"},
		{`{ $.level = "error" }`, "1"},
		{`{ $.status >= 500 }`, "1"},
		{`{ $.status = 200 && $.path = "/h*" }`, "1"},
		{`{ $.level = "error" || $.level = "info" }`, "2"},
		{`{ $.missing NOT EXISTS }`, "2"},
	}
	for _, tc := range cases {
		out, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(group), FilterPattern: aws.String(tc.pattern)})
		if err != nil {
			t.Errorf("pattern %q: %v", tc.pattern, err)
			continue
		}
		if got := len(out.Events); got != atoi(tc.want) {
			t.Errorf("pattern %q matched %d events, want %s", tc.pattern, got, tc.want)
		}
	}
	var invalid *cwltypes.InvalidParameterException
	if _, err := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String(group), FilterPattern: aws.String("%regex%")}); !errors.As(err, &invalid) {
		t.Errorf("a regex pattern is refused by name: %v", err)
	}
}

func TestSDKRefusedOperationsAreNamed(t *testing.T) {
	ctx := context.Background()
	c := logsClient(t)
	_, err := c.StartQuery(ctx, &cwl.StartQueryInput{LogGroupName: aws.String("/x"), QueryString: aws.String("fields @message"), StartTime: aws.Int64(0), EndTime: aws.Int64(1)})
	var ae interface{ ErrorCode() string }
	if !errors.As(err, &ae) || ae.ErrorCode() != "UnsupportedOperationException" {
		t.Errorf("StartQuery should be refused by name, got %v", err)
	}
}

func TestSweepDropsOldEvents(t *testing.T) {
	now := time.Now()
	clock := now
	s, err := logs.New(logs.Options{DataDir: t.TempDir(), Logf: t.Logf, Clock: func() time.Time { return clock }, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	c := cwl.NewFromConfig(aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")},
		func(o *cwl.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()
	c.CreateLogGroup(ctx, &cwl.CreateLogGroupInput{LogGroupName: aws.String("/g")})
	put(t, c, "/g", "s", now.Add(-2*time.Hour).UnixMilli(), "old")
	put(t, c, "/g", "s", now.UnixMilli(), "new")
	if n := s.SweepNow(); n != 1 {
		t.Errorf("sweep dropped %d, want the one old event", n)
	}
	out, _ := c.FilterLogEvents(ctx, &cwl.FilterLogEventsInput{LogGroupName: aws.String("/g")})
	if len(out.Events) != 1 || aws.ToString(out.Events[0].Message) != "new" {
		t.Errorf("after sweep: %+v", out.Events)
	}
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "|"
		}
		out += p
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
