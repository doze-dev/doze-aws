package cloudwatch_test

// GetMetricStatistics end to end, through the real v2 SDK.
//
// The unit tests in aggregate_test.go check the arithmetic. This checks that
// what a caller publishes is what a caller reads back — the store's key
// encoding, the time window, the period alignment and the response shape all
// at once, which is where an off-by-one in any of them shows up.

import (
	"context"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/doze-dev/doze-aws/cloudwatch"
)

// clockedServer runs on a fixed clock so a window is exact rather than racing
// wall time.
func clockedServer(t *testing.T, now time.Time) (*awscw.Client, *cloudwatch.Server, context.Context) {
	t.Helper()
	s, err := cloudwatch.New(cloudwatch.Options{
		DataDir: t.TempDir(),
		Clock:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awscw.New(awscw.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(ts.URL),
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
	}), s, context.Background()
}

func TestGetMetricStatistics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	// Ten observations, one per minute, values 1..10.
	var data []cwtypes.MetricDatum
	for i := range 10 {
		data = append(data, cwtypes.MetricDatum{
			MetricName: aws.String("Latency"),
			Value:      aws.Float64(float64(i + 1)),
			Unit:       cwtypes.StandardUnitMilliseconds,
			Timestamp:  aws.Time(base.Add(time.Duration(i) * time.Minute)),
		})
	}
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"), MetricData: data}); err != nil {
		t.Fatal(err)
	}

	// One ten-minute bucket over the whole window.
	out, err := c.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:  aws.String("Shop"),
		MetricName: aws.String("Latency"),
		StartTime:  aws.Time(base),
		EndTime:    aws.Time(base.Add(10 * time.Minute)),
		Period:     aws.Int32(600),
		Statistics: []cwtypes.Statistic{
			cwtypes.StatisticSum, cwtypes.StatisticAverage, cwtypes.StatisticMinimum,
			cwtypes.StatisticMaximum, cwtypes.StatisticSampleCount,
		},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}
	if aws.ToString(out.Label) != "Latency" {
		t.Errorf("Label = %q", aws.ToString(out.Label))
	}
	if len(out.Datapoints) != 1 {
		t.Fatalf("want 1 datapoint, got %d", len(out.Datapoints))
	}
	dp := out.Datapoints[0]
	if got := aws.ToFloat64(dp.Sum); got != 55 {
		t.Errorf("Sum = %v, want 55", got)
	}
	if got := aws.ToFloat64(dp.Average); got != 5.5 {
		t.Errorf("Average = %v, want 5.5", got)
	}
	if got := aws.ToFloat64(dp.Minimum); got != 1 {
		t.Errorf("Minimum = %v, want 1", got)
	}
	if got := aws.ToFloat64(dp.Maximum); got != 10 {
		t.Errorf("Maximum = %v, want 10", got)
	}
	if got := aws.ToFloat64(dp.SampleCount); got != 10 {
		t.Errorf("SampleCount = %v, want 10", got)
	}
	if dp.Unit != cwtypes.StandardUnitMilliseconds {
		t.Errorf("Unit = %v", dp.Unit)
	}
}

// A finer period splits the same observations into more buckets — the thing
// keeping raw samples buys, since AWS cannot re-bucket after ingest.
func TestGetMetricStatisticsRebuckets(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	var data []cwtypes.MetricDatum
	for i := range 10 {
		data = append(data, cwtypes.MetricDatum{
			MetricName: aws.String("Latency"),
			Value:      aws.Float64(1),
			Timestamp:  aws.Time(base.Add(time.Duration(i) * time.Minute)),
		})
	}
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"), MetricData: data}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		period int32
		want   int
	}{
		{600, 1}, // one ten-minute bucket
		{300, 2}, // two five-minute buckets
		{60, 10}, // one per minute
	} {
		out, err := c.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
			Namespace:  aws.String("Shop"),
			MetricName: aws.String("Latency"),
			StartTime:  aws.Time(base),
			EndTime:    aws.Time(base.Add(10 * time.Minute)),
			Period:     aws.Int32(tc.period),
			Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
		})
		if err != nil {
			t.Fatalf("period %d: %v", tc.period, err)
		}
		if len(out.Datapoints) != tc.want {
			t.Errorf("period %d: %d datapoints, want %d", tc.period, len(out.Datapoints), tc.want)
		}
	}
}

// Percentiles ride in ExtendedStatistics and are exact, because the raw
// observations are still there to sort.
func TestGetMetricStatisticsPercentiles(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	var data []cwtypes.MetricDatum
	for i := range 100 {
		data = append(data, cwtypes.MetricDatum{
			MetricName: aws.String("Latency"),
			Value:      aws.Float64(float64(i + 1)), // 1..100
			Timestamp:  aws.Time(base.Add(time.Duration(i) * time.Second)),
		})
	}
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"), MetricData: data}); err != nil {
		t.Fatal(err)
	}
	out, err := c.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace:          aws.String("Shop"),
		MetricName:         aws.String("Latency"),
		StartTime:          aws.Time(base),
		EndTime:            aws.Time(base.Add(10 * time.Minute)),
		Period:             aws.Int32(600),
		ExtendedStatistics: []string{"p50", "p99", "p100"},
	})
	if err != nil {
		t.Fatalf("GetMetricStatistics: %v", err)
	}
	if len(out.Datapoints) != 1 {
		t.Fatalf("want 1 datapoint, got %d", len(out.Datapoints))
	}
	ext := out.Datapoints[0].ExtendedStatistics
	if got := ext["p50"]; math.Abs(got-50.5) > 1e-6 {
		t.Errorf("p50 = %v, want 50.5", got)
	}
	if got := ext["p100"]; got != 100 {
		t.Errorf("p100 = %v, want 100", got)
	}
	// An estimate from a digest would be near but not exact; this is exact.
	if got := ext["p99"]; math.Abs(got-99.01) > 1e-6 {
		t.Errorf("p99 = %v, want 99.01", got)
	}
}

// A period AWS would refuse must be refused here, since a period that works
// locally and fails on deploy is the failure this emulator exists to catch.
func TestGetMetricStatisticsRefusesBadInput(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	in := func(mut func(*awscw.GetMetricStatisticsInput)) *awscw.GetMetricStatisticsInput {
		i := &awscw.GetMetricStatisticsInput{
			Namespace:  aws.String("Shop"),
			MetricName: aws.String("Latency"),
			StartTime:  aws.Time(base),
			EndTime:    aws.Time(base.Add(10 * time.Minute)),
			Period:     aws.Int32(60),
			Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
		}
		mut(i)
		return i
	}
	cases := map[string]func(*awscw.GetMetricStatisticsInput){
		"a period that is not a multiple of 60": func(i *awscw.GetMetricStatisticsInput) {
			i.Period = aws.Int32(45)
		},
		"an end before the start": func(i *awscw.GetMetricStatisticsInput) {
			i.StartTime, i.EndTime = i.EndTime, i.StartTime
		},
		"a window that would exceed the datapoint ceiling": func(i *awscw.GetMetricStatisticsInput) {
			i.EndTime = aws.Time(base.Add(48 * time.Hour))
			i.Period = aws.Int32(60)
		},
	}
	for name, mut := range cases {
		if _, err := c.GetMetricStatistics(ctx, in(mut)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Retention drops what is past the window, and SweepNow makes that testable
// without waiting a minute for the ticker.
func TestRetentionSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	s, err := cloudwatch.New(cloudwatch.Options{
		DataDir:   t.TempDir(),
		Clock:     func() time.Time { return now },
		Retention: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	c := awscw.New(awscw.Options{
		Region: "us-east-1", BaseEndpoint: aws.String(ts.URL),
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	})
	ctx := context.Background()

	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"),
		MetricData: []cwtypes.MetricDatum{
			// Inside the window.
			{MetricName: aws.String("M"), Value: aws.Float64(1),
				Timestamp: aws.Time(now.Add(-30 * time.Minute))},
			// Outside it.
			{MetricName: aws.String("M"), Value: aws.Float64(2),
				Timestamp: aws.Time(now.Add(-2 * time.Hour))},
		},
	}); err != nil {
		t.Fatal(err)
	}

	removed, err := s.SweepNow()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("swept %d samples, want the 1 past the window", removed)
	}
	out, err := c.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace: aws.String("Shop"), MetricName: aws.String("M"),
		StartTime: aws.Time(now.Add(-3 * time.Hour)), EndTime: aws.Time(now),
		Period:     aws.Int32(10800),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticSampleCount},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Datapoints) != 1 || aws.ToFloat64(out.Datapoints[0].SampleCount) != 1 {
		t.Errorf("the surviving sample is not the one inside the window: %+v", out.Datapoints)
	}
}

// Two observations in the same millisecond are two observations. Without a
// sequence in the key the second would overwrite the first and a count would
// silently be wrong.
func TestSameMillisecondSamplesBothSurvive(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	at := aws.Time(base)
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"),
		MetricData: []cwtypes.MetricDatum{
			{MetricName: aws.String("M"), Value: aws.Float64(1), Timestamp: at},
			{MetricName: aws.String("M"), Value: aws.Float64(2), Timestamp: at},
			{MetricName: aws.String("M"), Value: aws.Float64(3), Timestamp: at},
		},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := c.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace: aws.String("Shop"), MetricName: aws.String("M"),
		StartTime: aws.Time(base), EndTime: aws.Time(base.Add(time.Minute)),
		Period:     aws.Int32(60),
		Statistics: []cwtypes.Statistic{cwtypes.StatisticSampleCount, cwtypes.StatisticSum},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Datapoints) != 1 {
		t.Fatalf("want 1 datapoint, got %d", len(out.Datapoints))
	}
	if got := aws.ToFloat64(out.Datapoints[0].SampleCount); got != 3 {
		t.Errorf("SampleCount = %v, want 3 — samples sharing a millisecond overwrote", got)
	}
	if got := aws.ToFloat64(out.Datapoints[0].Sum); got != 6 {
		t.Errorf("Sum = %v, want 6", got)
	}
}
