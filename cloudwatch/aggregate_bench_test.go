package cloudwatch

// Aggregation is what makes a chart or an alarm evaluation cost anything.
// doze-aws keeps raw samples rather than a sketch, which is what makes its
// percentiles exact — and means a percentile is a sort over the window rather
// than a table lookup. That trade is worth a number.
//
// The window sizes are the ones the console and the evaluator actually ask
// for: an alarm looks at one to a few periods, a chart at sixty.

import (
	"math/rand/v2"
	"strconv"
	"testing"
	"time"
)

func benchSamples(n int, period time.Duration) []tsSample {
	// A fixed seed: a benchmark whose input changes between runs cannot be
	// compared between runs.
	r := rand.New(rand.NewPCG(1, 2))
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	span := period * 60
	out := make([]tsSample, 0, n)
	for i := range n {
		at := base.Add(time.Duration(float64(span) * float64(i) / float64(n)))
		out = append(out, tsSample{
			At:     at.UnixMilli(),
			sample: sample{Value: r.Float64() * 1000, Count: 1},
		})
	}
	return out
}

func BenchmarkBucketize(b *testing.B) {
	period := time.Minute
	for _, n := range []int{60, 1000, 10000} {
		b.Run("samples="+strconv.Itoa(n), func(b *testing.B) {
			samples := benchSamples(n, period)
			from := time.UnixMilli(samples[0].At)
			to := time.UnixMilli(samples[len(samples)-1].At).Add(period)
			b.ReportAllocs()
			for b.Loop() {
				if got := bucketize(samples, from, to, period); len(got) == 0 {
					b.Fatal("no buckets")
				}
			}
		})
	}
}

// The percentile path, which is the reason samples are kept raw. Measured on
// one bucket's worth of values, since that is how it is called.
func BenchmarkPercentile(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run("values="+strconv.Itoa(n), func(b *testing.B) {
			r := rand.New(rand.NewPCG(3, 4))
			src := make([]float64, n)
			for i := range src {
				src[i] = r.Float64() * 1000
			}
			// percentile sorts in place, so each iteration gets a fresh copy —
			// otherwise every run after the first would sort sorted input and
			// measure the best case.
			buf := make([]float64, n)
			b.ReportAllocs()
			for b.Loop() {
				copy(buf, src)
				percentile(buf, 99)
			}
		})
	}
}
