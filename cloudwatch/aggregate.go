package cloudwatch

// Turning samples into statistics.
//
// A period is a half-open bucket [t, t+period) aligned to the epoch, which is
// how AWS aligns them: a 300-second period starts at a multiple of 300 past
// the epoch, not at the first observation. Aligning to the data instead would
// give two callers asking the same question different buckets.
//
// # Weighted, because a sample can stand for many
//
// A StatisticValues datum is one record carrying SampleCount, Sum, Minimum
// and Maximum for observations the caller already summarised. So Sum is a sum
// of sums, SampleCount a sum of counts, and Average their quotient — not the
// mean of the record values, which would weight a thousand observations the
// same as one.
//
// # Percentiles are exact
//
// Because the raw samples are kept (samples.go), pNN is the actual order
// statistic rather than an estimate from a digest. The one place this differs
// from AWS is a StatisticValues record, which cannot be expanded back into
// the observations it summarised — it contributes its mean, once, and that is
// written down in the ledger rather than papered over.

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// stat is a statistic a caller can ask for.
type stat struct {
	Name string
	// Pct is the percentile rank for a pNN statistic, and -1 otherwise.
	Pct float64
}

// parseStat reads one Statistics member or ExtendedStatistics member.
func parseStat(s string) (stat, bool) {
	switch s {
	case "Average", "Sum", "Minimum", "Maximum", "SampleCount":
		return stat{Name: s, Pct: -1}, true
	}
	// pNN, pNN.N — AWS accepts one decimal place.
	if len(s) > 1 && (s[0] == 'p' || s[0] == 'P') {
		if f, err := strconv.ParseFloat(s[1:], 64); err == nil && f >= 0 && f <= 100 {
			return stat{Name: "p" + strings.TrimPrefix(s[1:], "+"), Pct: f}, true
		}
	}
	return stat{}, false
}

// bucket is one period's worth of samples.
type bucket struct {
	Start time.Time
	// values holds each record's representative value for percentile work,
	// which is why they are kept rather than folded away as they arrive.
	values   []float64
	Sum      float64
	Count    float64
	Min, Max float64
	Unit     string
	seenAny  bool
}

func (b *bucket) add(s tsSample) {
	if !b.seenAny {
		b.Min, b.Max, b.seenAny = s.Min, s.Max, true
	} else {
		b.Min = math.Min(b.Min, s.Min)
		b.Max = math.Max(b.Max, s.Max)
	}
	b.Sum += s.Value
	b.Count += s.Count
	// A StatisticValues record has no individual observations to contribute,
	// so its mean stands in — see the note above.
	rep := s.Value
	if s.Count > 1 {
		rep = s.Value / s.Count
	}
	b.values = append(b.values, rep)
	if s.Unit != "" && b.Unit == "" {
		b.Unit = s.Unit
	}
}

// value computes one statistic over the bucket.
func (b *bucket) value(st stat) float64 {
	switch st.Name {
	case "Sum":
		return b.Sum
	case "SampleCount":
		return b.Count
	case "Minimum":
		return b.Min
	case "Maximum":
		return b.Max
	case "Average":
		if b.Count == 0 {
			return 0
		}
		return b.Sum / b.Count
	}
	return percentile(b.values, st.Pct)
}

// percentile is the exact order statistic, interpolated between neighbours
// the way AWS documents. p0 is the minimum and p100 the maximum.
func percentile(values []float64, pct float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if pct <= 0 {
		return sorted[0]
	}
	if pct >= 100 {
		return sorted[len(sorted)-1]
	}
	// Linear interpolation on the rank, which is what AWS's definition
	// reduces to for a small sample set.
	rank := pct / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}

// alignDown rounds a time down to a period boundary measured from the epoch,
// which is where AWS puts them.
func alignDown(t time.Time, period time.Duration) time.Time {
	if period <= 0 {
		return t
	}
	return time.Unix(0, (t.UnixNano()/int64(period))*int64(period)).UTC()
}

// bucketize groups samples into aligned periods. Empty periods are omitted
// rather than emitted as zero: a metric with no observations in a window did
// not observe zero, and a graph that draws a line through zero is a lie about
// what happened.
func bucketize(samples []tsSample, from, to time.Time, period time.Duration) []*bucket {
	if period <= 0 || len(samples) == 0 {
		return nil
	}
	byStart := map[int64]*bucket{}
	var order []int64
	for _, s := range samples {
		at := time.UnixMilli(s.At).UTC()
		if at.Before(from) || !at.Before(to) {
			continue
		}
		start := alignDown(at, period)
		key := start.UnixNano()
		b, ok := byStart[key]
		if !ok {
			b = &bucket{Start: start}
			byStart[key] = b
			order = append(order, key)
		}
		b.add(s)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]*bucket, 0, len(order))
	for _, k := range order {
		out = append(out, byStart[k])
	}
	return out
}
