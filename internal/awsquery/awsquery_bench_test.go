package awsquery

// Un-flattening is what every request from a v1-era SDK costs before the
// handler sees it: the Query protocol spells a nested structure as
// `MetricData.member.1.Dimensions.member.2.Name`, and this rebuilds the tree.
//
// It exists because modelcheck.FromQuery kept only the first element of every
// list, which silently dropped data. Correctness came first; this says what
// the correct version costs.

import (
	"net/url"
	"strconv"
	"testing"
)

func benchForm(datums, dims int) url.Values {
	v := url.Values{"Action": {"PutMetricData"}, "Version": {"2010-08-01"}}
	for i := 1; i <= datums; i++ {
		p := "MetricData.member." + strconv.Itoa(i)
		v.Set(p+".MetricName", "Checkouts")
		v.Set(p+".Value", "1.5")
		v.Set(p+".Unit", "Count")
		for d := 1; d <= dims; d++ {
			dp := p + ".Dimensions.member." + strconv.Itoa(d)
			v.Set(dp+".Name", "Stage")
			v.Set(dp+".Value", "prod")
		}
	}
	return v
}

func BenchmarkUnflatten(b *testing.B) {
	for _, n := range []int{1, 20} {
		b.Run("datums="+strconv.Itoa(n), func(b *testing.B) {
			form := benchForm(n, 2)
			b.ReportAllocs()
			for b.Loop() {
				if got := Unflatten(form); len(got) == 0 {
					b.Fatal("empty result")
				}
			}
		})
	}
}
