package cloudwatch

// Reading metrics back.

import (
	"sort"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// maxDatapoints is AWS's ceiling on one GetMetricStatistics answer. A window
// and period that would exceed it is a caller error rather than a truncated
// answer, because a silently short series reads as "the metric stopped".
const maxDatapoints = 1440

// getMetricStatistics answers one metric's statistics over a window.
func (s *Server) getMetricStatistics(req *request) (any, *awshttp.APIError) {
	ns := req.params.Str("Namespace")
	name := req.params.Str("MetricName")
	if ns == "" || name == "" {
		return nil, errMissingParameter("Namespace and MetricName are required.")
	}
	from, ok := req.params.Time("StartTime")
	if !ok {
		return nil, errMissingParameter("The parameter StartTime is required.")
	}
	to, ok := req.params.Time("EndTime")
	if !ok {
		return nil, errMissingParameter("The parameter EndTime is required.")
	}
	if !from.Before(to) {
		return nil, errInvalidParameter("The parameter StartTime must be less than EndTime.")
	}
	periodSecs := req.params.Int("Period", 0)
	if periodSecs <= 0 {
		return nil, errMissingParameter("The parameter Period is required.")
	}
	// AWS requires the period to be a multiple of 60 above 60s, and one of
	// 1/5/10/30 below it. Refusing here is the point of the emulator: a
	// period that works locally and is rejected on deploy is the failure this
	// exists to catch.
	if aerr := checkPeriod(periodSecs); aerr != nil {
		return nil, aerr
	}
	period := time.Duration(periodSecs) * time.Second
	if n := to.Sub(from) / period; n > maxDatapoints {
		return nil, errInvalidParameter(
			"The requested window and period would return %d datapoints; the maximum is %d.",
			n, maxDatapoints)
	}

	stats, aerr := requestedStats(req)
	if aerr != nil {
		return nil, aerr
	}
	if len(stats) == 0 {
		return nil, errMissingParameter(
			"The parameter Statistics or ExtendedStatistics is required.")
	}

	dims := map[string]string{}
	for _, d := range req.params.List("Dimensions") {
		n, v := d.Str("Name"), d.Str("Value")
		if n == "" || v == "" {
			return nil, errInvalidParameter("The dimension Name and Value must both be set.")
		}
		dims[n] = v
	}

	samples, err := s.readSamples(seriesKey(ns, name, dims), from, to)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	wantUnit := req.params.Str("Unit")
	if wantUnit != "" {
		kept := samples[:0]
		for _, sm := range samples {
			if sm.Unit == wantUnit {
				kept = append(kept, sm)
			}
		}
		samples = kept
	}

	out := getMetricStatisticsResult{Label: name, Datapoints: []datapointView{}}
	for _, b := range bucketize(samples, from, to, period) {
		dp := datapointView{Timestamp: b.Start.UTC(), Unit: b.Unit}
		for _, st := range stats {
			dp.set(st, b.value(st))
		}
		out.Datapoints = append(out.Datapoints, dp)
	}
	// AWS does not promise datapoint order, but an unstable one makes every
	// caller sort and every test flaky, so they come back oldest first.
	sort.Slice(out.Datapoints, func(i, j int) bool {
		return out.Datapoints[i].Timestamp.Before(out.Datapoints[j].Timestamp)
	})
	return out, nil
}

// checkPeriod applies AWS's granularity rule.
func checkPeriod(secs int) *awshttp.APIError {
	switch secs {
	case 1, 5, 10, 20, 30:
		return nil
	}
	if secs%60 != 0 {
		return errInvalidParameter(
			"The parameter Period must be 1, 5, 10, 20, 30, or a multiple of 60.")
	}
	return nil
}

// requestedStats reads Statistics and ExtendedStatistics together, which is
// how a caller mixes Average with p99 in one request.
func requestedStats(req *request) ([]stat, *awshttp.APIError) {
	var out []stat
	seen := map[string]bool{}
	for _, name := range append(req.params.Strs("Statistics"),
		req.params.Strs("ExtendedStatistics")...) {
		st, ok := parseStat(name)
		if !ok {
			return nil, errInvalidParameter(
				"The value %s is not a valid statistic.", name)
		}
		if seen[st.Name] {
			continue
		}
		seen[st.Name] = true
		out = append(out, st)
	}
	return out, nil
}
