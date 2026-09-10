package cloudwatch

import (
	"math"
	"testing"
	"time"
)

func TestParseStat(t *testing.T) {
	cases := []struct {
		in   string
		name string
		pct  float64
		ok   bool
	}{
		{"Average", "Average", -1, true},
		{"Sum", "Sum", -1, true},
		{"SampleCount", "SampleCount", -1, true},
		{"p99", "p99", 99, true},
		{"p99.9", "p99.9", 99.9, true},
		{"p0", "p0", 0, true},
		{"p100", "p100", 100, true},
		{"P50", "p50", 50, true},
		{"p101", "", 0, false},
		{"p-1", "", 0, false},
		{"average", "", 0, false}, // AWS is case-sensitive on the named ones
		{"", "", 0, false},
		{"p", "", 0, false},
		{"pfoo", "", 0, false},
	}
	for _, c := range cases {
		st, ok := parseStat(c.in)
		if ok != c.ok || (ok && (st.Name != c.name || st.Pct != c.pct)) {
			t.Errorf("parseStat(%q) = (%+v, %v), want (%s/%v, %v)",
				c.in, st, ok, c.name, c.pct, c.ok)
		}
	}
}

// Percentiles are exact because the raw samples are kept, so these are the
// real order statistics rather than a digest's estimate.
func TestPercentile(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		pct  float64
		want float64
	}{
		{0, 1},    // p0 is the minimum
		{100, 10}, // p100 is the maximum
		{50, 5.5}, // the median of an even set interpolates
		{25, 3.25},
		{90, 9.1},
	}
	for _, c := range cases {
		if got := percentile(vals, c.pct); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("p%v = %v, want %v", c.pct, got, c.want)
		}
	}
	// Order of the input must not matter.
	shuffled := []float64{7, 2, 9, 4, 1, 10, 3, 8, 5, 6}
	if got := percentile(shuffled, 50); math.Abs(got-5.5) > 1e-9 {
		t.Errorf("percentile depends on input order: %v", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("percentile of nothing = %v, want 0", got)
	}
	if got := percentile([]float64{42}, 99); got != 42 {
		t.Errorf("percentile of one value = %v, want 42", got)
	}
}

// Periods are aligned to the epoch, not to the first observation, because two
// callers asking the same question must get the same buckets.
func TestAlignDown(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 34, 56, 0, time.UTC)
	cases := []struct {
		period time.Duration
		want   string
	}{
		{time.Minute, "2026-09-10T12:34:00Z"},
		{5 * time.Minute, "2026-09-10T12:30:00Z"},
		{time.Hour, "2026-09-10T12:00:00Z"},
		{time.Second, "2026-09-10T12:34:56Z"},
	}
	for _, c := range cases {
		if got := alignDown(at, c.period).Format(time.RFC3339); got != c.want {
			t.Errorf("alignDown(%v) = %s, want %s", c.period, got, c.want)
		}
	}
}

// A StatisticValues record stands for many observations, so Sum is a sum of
// sums and Average divides by the total count — not by the number of records.
// Getting this wrong weights a thousand observations the same as one.
func TestBucketWeightsStatisticValues(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	samples := []tsSample{
		// One plain observation of 10.
		{At: base.UnixMilli(), sample: sample{Value: 10, Count: 1, Min: 10, Max: 10}},
		// One record standing for 99 observations summing to 99 (mean 1).
		{At: base.Add(time.Second).UnixMilli(),
			sample: sample{Value: 99, Count: 99, Min: 1, Max: 1}},
	}
	buckets := bucketize(samples, base, base.Add(time.Minute), time.Minute)
	if len(buckets) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(buckets))
	}
	b := buckets[0]
	if got := b.value(stat{Name: "Sum"}); got != 109 {
		t.Errorf("Sum = %v, want 109", got)
	}
	if got := b.value(stat{Name: "SampleCount"}); got != 100 {
		t.Errorf("SampleCount = %v, want 100 observations, not 2 records", got)
	}
	if got := b.value(stat{Name: "Average"}); math.Abs(got-1.09) > 1e-9 {
		t.Errorf("Average = %v, want 109/100; a per-record mean would be 54.5", got)
	}
	if got := b.value(stat{Name: "Minimum"}); got != 1 {
		t.Errorf("Minimum = %v, want the summarised record's minimum", got)
	}
	if got := b.value(stat{Name: "Maximum"}); got != 10 {
		t.Errorf("Maximum = %v, want 10", got)
	}
}

// Empty periods are omitted rather than reported as zero: a metric with no
// observations did not observe zero, and a graph drawn through zero lies.
func TestBucketizeOmitsEmptyPeriods(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	samples := []tsSample{
		{At: base.UnixMilli(), sample: sample{Value: 1, Count: 1}},
		// Nothing in the second or third minute.
		{At: base.Add(3 * time.Minute).UnixMilli(), sample: sample{Value: 2, Count: 1}},
	}
	buckets := bucketize(samples, base, base.Add(10*time.Minute), time.Minute)
	if len(buckets) != 2 {
		t.Fatalf("want 2 buckets for 2 populated minutes, got %d", len(buckets))
	}
	if !buckets[0].Start.Equal(base) || !buckets[1].Start.Equal(base.Add(3*time.Minute)) {
		t.Errorf("buckets are not at the populated minutes: %v, %v",
			buckets[0].Start, buckets[1].Start)
	}
}

// The window is half-open: a sample exactly at the end belongs to the next
// window, or two adjacent queries would both count it.
func TestBucketizeWindowIsHalfOpen(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	end := base.Add(time.Minute)
	samples := []tsSample{
		{At: base.UnixMilli(), sample: sample{Value: 1, Count: 1}},
		{At: end.UnixMilli(), sample: sample{Value: 2, Count: 1}},
	}
	buckets := bucketize(samples, base, end, time.Minute)
	if len(buckets) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(buckets))
	}
	if got := buckets[0].value(stat{Name: "SampleCount"}); got != 1 {
		t.Errorf("the sample at the boundary was counted: SampleCount = %v", got)
	}
}

func TestCheckPeriod(t *testing.T) {
	for _, ok := range []int{1, 5, 10, 20, 30, 60, 120, 300, 3600} {
		if err := checkPeriod(ok); err != nil {
			t.Errorf("period %d refused: %v", ok, err)
		}
	}
	for _, bad := range []int{7, 45, 61, 119} {
		if err := checkPeriod(bad); err == nil {
			t.Errorf("period %d accepted; AWS would refuse it at deploy", bad)
		}
	}
}
