package cloudwatch_test

// GetMetricData: the multi-query read CDK and the console emit.

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

func TestGetMetricDataSeveralQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	// Two series that differ only by dimension, so the query has to select
	// on dimensions rather than on name.
	var data []cwtypes.MetricDatum
	for i := range 4 {
		for _, stage := range []string{"prod", "staging"} {
			v := float64(i + 1)
			if stage == "staging" {
				v *= 10
			}
			data = append(data, cwtypes.MetricDatum{
				MetricName: aws.String("Latency"),
				Value:      aws.Float64(v),
				Timestamp:  aws.Time(base.Add(time.Duration(i) * time.Minute)),
				Dimensions: []cwtypes.Dimension{{Name: aws.String("Stage"), Value: aws.String(stage)}},
			})
		}
	}
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"), MetricData: data}); err != nil {
		t.Fatal(err)
	}

	q := func(id, stage string, stat cwtypes.Statistic) cwtypes.MetricDataQuery {
		return cwtypes.MetricDataQuery{
			Id: aws.String(id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("Shop"),
					MetricName: aws.String("Latency"),
					Dimensions: []cwtypes.Dimension{
						{Name: aws.String("Stage"), Value: aws.String(stage)},
					},
				},
				Period: aws.Int32(240),
				Stat:   aws.String(string(stat)),
			},
		}
	}
	out, err := c.GetMetricData(ctx, &awscw.GetMetricDataInput{
		StartTime: aws.Time(base),
		EndTime:   aws.Time(base.Add(4 * time.Minute)),
		MetricDataQueries: []cwtypes.MetricDataQuery{
			q("prod", "prod", cwtypes.StatisticSum),
			q("stg", "staging", cwtypes.StatisticSum),
		},
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}
	if len(out.MetricDataResults) != 2 {
		t.Fatalf("want 2 results, got %d", len(out.MetricDataResults))
	}
	byID := map[string]cwtypes.MetricDataResult{}
	for _, r := range out.MetricDataResults {
		byID[aws.ToString(r.Id)] = r
	}
	// 1+2+3+4 for prod, ten times that for staging.
	if got := byID["prod"].Values; len(got) != 1 || got[0] != 10 {
		t.Errorf("prod values = %v, want [10]", got)
	}
	if got := byID["stg"].Values; len(got) != 1 || got[0] != 100 {
		t.Errorf("staging values = %v, want [100]", got)
	}
	if len(byID["prod"].Timestamps) != len(byID["prod"].Values) {
		t.Error("Timestamps and Values are parallel arrays and must be the same length")
	}
	if byID["prod"].StatusCode != cwtypes.StatusCodeComplete {
		t.Errorf("StatusCode = %v", byID["prod"].StatusCode)
	}
}

// ScanBy orders the points. Descending is AWS's default, and the one a
// console wants: newest first.
func TestGetMetricDataScanBy(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	var data []cwtypes.MetricDatum
	for i := range 3 {
		data = append(data, cwtypes.MetricDatum{
			MetricName: aws.String("M"),
			Value:      aws.Float64(float64(i + 1)),
			Timestamp:  aws.Time(base.Add(time.Duration(i) * time.Minute)),
		})
	}
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"), MetricData: data}); err != nil {
		t.Fatal(err)
	}

	read := func(scan cwtypes.ScanBy) []float64 {
		t.Helper()
		in := &awscw.GetMetricDataInput{
			StartTime: aws.Time(base),
			EndTime:   aws.Time(base.Add(3 * time.Minute)),
			MetricDataQueries: []cwtypes.MetricDataQuery{{
				Id: aws.String("m"),
				MetricStat: &cwtypes.MetricStat{
					Metric: &cwtypes.Metric{
						Namespace: aws.String("Shop"), MetricName: aws.String("M")},
					Period: aws.Int32(60), Stat: aws.String("Sum"),
				},
			}},
		}
		if scan != "" {
			in.ScanBy = scan
		}
		out, err := c.GetMetricData(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return out.MetricDataResults[0].Values
	}

	if got := read(cwtypes.ScanByTimestampAscending); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("ascending = %v, want [1 2 3]", got)
	}
	if got := read(cwtypes.ScanByTimestampDescending); len(got) != 3 || got[0] != 3 || got[2] != 1 {
		t.Errorf("descending = %v, want [3 2 1]", got)
	}
	// The default is descending, as on AWS.
	if got := read(""); len(got) != 3 || got[0] != 3 {
		t.Errorf("default = %v, want newest first", got)
	}
}

// Metric math is refused by name rather than half-evaluated. A partial
// evaluator returning a confident wrong number for the operators it did not
// implement would be worse than one that says it cannot.
func TestGetMetricDataRefusesMetricMath(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c, _, ctx := clockedServer(t, base.Add(time.Hour))

	_, err := c.GetMetricData(ctx, &awscw.GetMetricDataInput{
		StartTime: aws.Time(base),
		EndTime:   aws.Time(base.Add(time.Hour)),
		MetricDataQueries: []cwtypes.MetricDataQuery{{
			Id:         aws.String("e1"),
			Expression: aws.String("SUM(METRICS())"),
		}},
	})
	if err == nil {
		t.Fatal("a metric-math expression was accepted")
	}
	if !containsAll(err.Error(), "metric math", "MetricStat") {
		t.Errorf("the refusal does not say what is supported instead: %v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
