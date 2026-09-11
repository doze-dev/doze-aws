package rpcv2cbor

// The CBOR decoder is hand-rolled and sits on the request path of every
// modern AWS SDK that speaks CloudWatch — Go v2, Java, Rust, Swift, Kotlin,
// C++, .NET v4. It is also the one piece of binary parsing in the tree, so
// its cost per byte is worth knowing rather than assuming.
//
// The fixtures are the shapes that actually arrive: a small alarm request, and
// a PutMetricData carrying twenty datums with dimensions, which is what a
// batching producer sends.

import (
	"strconv"
	"testing"
)

func smallBody(tb testing.TB) []byte {
	tb.Helper()
	raw, err := Marshal(map[string]any{
		"AlarmName":          "too-many-errors",
		"Namespace":          "Shop",
		"MetricName":         "Errors",
		"Statistic":          "Sum",
		"Period":             60.0,
		"EvaluationPeriods":  1.0,
		"Threshold":          5.0,
		"ComparisonOperator": "GreaterThanThreshold",
	})
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func metricBody(tb testing.TB, n int) []byte {
	tb.Helper()
	data := make([]any, 0, n)
	for i := range n {
		data = append(data, map[string]any{
			"MetricName": "Checkouts" + strconv.Itoa(i),
			"Value":      float64(i) + 0.5,
			"Unit":       "Count",
			"Dimensions": []any{
				map[string]any{"Name": "Stage", "Value": "prod"},
				map[string]any{"Name": "Region", "Value": "us-east-1"},
			},
		})
	}
	raw, err := Marshal(map[string]any{"Namespace": "Shop", "MetricData": data})
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func BenchmarkDecodeSmall(b *testing.B) {
	body := smallBody(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := DecodeMap(body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeMetricData(b *testing.B) {
	for _, n := range []int{1, 20} {
		b.Run("datums="+strconv.Itoa(n), func(b *testing.B) {
			body := metricBody(b, n)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := DecodeMap(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMarshal(b *testing.B) {
	v := map[string]any{
		"MetricDataResults": []any{map[string]any{
			"Id": "m1", "Label": "Checkouts Sum",
			"Timestamps": []any{"2026-09-10T12:00:00Z", "2026-09-10T12:01:00Z"},
			"Values":     []any{1.0, 2.0},
			"StatusCode": "Complete",
		}},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}
