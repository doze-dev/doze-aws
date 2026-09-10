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

// getMetricData answers several metrics at once, which is the read CDK and
// the console emit.
//
// Metric math is refused rather than half-implemented. A query carrying an
// Expression is asking for an arithmetic language over other queries' results
// — SUM(METRICS()), RATE(m1), FILL(m1, 0) — and a partial evaluator that
// silently returned the wrong number for the operators it did not know would
// be worse than one that says so. A single MetricStat per query is accepted,
// which is what CDK emits for an alarm and what the console reads.
func (s *Server) getMetricData(req *request) (any, *awshttp.APIError) {
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
	queries := req.params.List("MetricDataQueries")
	if len(queries) == 0 {
		return nil, errMissingParameter("The parameter MetricDataQueries is required.")
	}
	// Descending is AWS's default, and the one the console wants: newest
	// first, so a truncated read shows the most recent data rather than the
	// oldest.
	descending := req.params.Str("ScanBy") != "TimestampAscending"

	out := getMetricDataResult{MetricDataResults: []metricDataResultView{}}
	for _, q := range queries {
		id := q.Str("Id")
		if id == "" {
			return nil, errMissingParameter("The parameter MetricDataQueries.member.N.Id is required.")
		}
		if q.Str("Expression") != "" {
			return nil, errf("InvalidParameterValueException",
				"doze-aws does not evaluate metric math: query %s carries an Expression. "+
					"A single MetricStat per query is supported, which is what CDK emits.", id)
		}
		ms := q.Map("MetricStat")
		if ms == nil {
			return nil, errMissingParameter(
				"The parameter MetricDataQueries.member.N.MetricStat is required for query %s.", id)
		}
		res, aerr := s.oneMetricStat(id, q, ms, from, to, descending)
		if aerr != nil {
			return nil, aerr
		}
		// ReturnData false means "compute it for a later expression but do
		// not send it". With no expressions to feed, the honest answer is an
		// empty result carrying its id, not silence.
		if q.Has("ReturnData") && !q.Bool("ReturnData") {
			res.Timestamps, res.Values = []time.Time{}, []float64{}
		}
		out.MetricDataResults = append(out.MetricDataResults, res)
	}
	return out, nil
}

// oneMetricStat resolves a single query against the store.
func (s *Server) oneMetricStat(id string, q, ms params, from, to time.Time,
	descending bool) (metricDataResultView, *awshttp.APIError) {
	metric := ms.Map("Metric")
	if metric == nil {
		return metricDataResultView{}, errMissingParameter(
			"The parameter MetricStat.Metric is required for query %s.", id)
	}
	ns, name := metric.Str("Namespace"), metric.Str("MetricName")
	if ns == "" || name == "" {
		return metricDataResultView{}, errMissingParameter(
			"MetricStat.Metric requires Namespace and MetricName for query %s.", id)
	}
	periodSecs := ms.Int("Period", q.Int("Period", 0))
	if periodSecs <= 0 {
		return metricDataResultView{}, errMissingParameter(
			"The parameter MetricStat.Period is required for query %s.", id)
	}
	if aerr := checkPeriod(periodSecs); aerr != nil {
		return metricDataResultView{}, aerr
	}
	st, ok := parseStat(ms.Str("Stat"))
	if !ok {
		return metricDataResultView{}, errInvalidParameter(
			"The value %s is not a valid statistic for query %s.", ms.Str("Stat"), id)
	}

	dims := map[string]string{}
	for _, d := range metric.List("Dimensions") {
		n, v := d.Str("Name"), d.Str("Value")
		if n == "" || v == "" {
			return metricDataResultView{}, errInvalidParameter(
				"The dimension Name and Value must both be set for query %s.", id)
		}
		dims[n] = v
	}

	samples, err := s.readSamples(seriesKey(ns, name, dims), from, to)
	if err != nil {
		return metricDataResultView{}, awshttp.AsAPIError(err)
	}
	if unit := ms.Str("Unit"); unit != "" {
		kept := samples[:0]
		for _, sm := range samples {
			if sm.Unit == unit {
				kept = append(kept, sm)
			}
		}
		samples = kept
	}

	label := q.Str("Label")
	if label == "" {
		label = name + " " + st.Name
	}
	res := metricDataResultView{Id: id, Label: label, StatusCode: "Complete",
		Timestamps: []time.Time{}, Values: []float64{}}
	buckets := bucketize(samples, from, to, time.Duration(periodSecs)*time.Second)
	if descending {
		for i, j := 0, len(buckets)-1; i < j; i, j = i+1, j-1 {
			buckets[i], buckets[j] = buckets[j], buckets[i]
		}
	}
	for _, b := range buckets {
		res.Timestamps = append(res.Timestamps, b.Start.UTC())
		res.Values = append(res.Values, b.value(st))
	}
	return res, nil
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
