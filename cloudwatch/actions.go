package cloudwatch

// The dispatch table.
//
// D1a carries one operation. PutMetricData is deliberately first: it returns
// `smithy.api#Unit` — no response body at all — so it exercises the whole
// decode path across three wires without response encoding in the picture, and
// its input is the awkward one (a list of structures, each with a nested list
// of dimensions, a double, an optional timestamp and a small integer). If the
// three-wire design is wrong, this is where it shows.

import (
	"encoding/base64"
	"sort"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// handlers is every operation with a real implementation.
var handlers = map[string]handler{
	"PutMetricData":       (*Server).putMetricData,
	"ListMetrics":         (*Server).listMetrics,
	"GetMetricStatistics": (*Server).getMetricStatistics,
}

// datum is one validated metric observation. D1a stops at validation — the
// store arrives in D2 — but the parsing is real, because parsing it wrongly
// on one wire is exactly the failure this batch exists to rule out.
type datum struct {
	Namespace  string
	MetricName string
	Dimensions map[string]string
	Value      float64
	Unit       string
	Resolution int
	// Timestamp is when the caller says the observation happened. Zero means
	// "now", which is what AWS does with an omitted one.
	Timestamp time.Time
	// Stats is set when the caller pre-summarised: one datum standing for
	// many observations. Value is then meaningless on its own.
	Stats *statisticValues
}

// statisticValues is a pre-summarised observation set.
type statisticValues struct {
	SampleCount float64
	Sum         float64
	Minimum     float64
	Maximum     float64
}

// putMetricData accepts observations. AWS answers with an empty body, so this
// returns nil and the codec renders whatever an empty result is on the wire
// it came in on.
func (s *Server) putMetricData(req *request) (any, *awshttp.APIError) {
	ns := req.params.Str("Namespace")
	if ns == "" {
		return nil, errMissingParameter("The parameter Namespace is required.")
	}
	data := req.params.List("MetricData")
	if len(data) == 0 {
		return nil, errMissingParameter("The parameter MetricData is required.")
	}
	parsed := make([]datum, 0, len(data))
	for _, md := range data {
		d, aerr := parseDatum(ns, md)
		if aerr != nil {
			return nil, aerr
		}
		parsed = append(parsed, d)
	}
	// The catalogue only. Samples land with the metric store in the next
	// sub-batch; recording the series now is what gives ListMetrics something
	// true to answer.
	if err := s.putSamples(parsed); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

// listMetricsPageSize is AWS's page size for ListMetrics.
const listMetricsPageSize = 500

// listMetrics answers the metric catalogue.
//
// It is the first operation here with a response body, which is the point of
// it: a list of structures, each with a nested list of dimensions, rendered
// three ways from one struct.
func (s *Server) listMetrics(req *request) (any, *awshttp.APIError) {
	f := seriesFilter{
		Namespace:  req.params.Str("Namespace"),
		MetricName: req.params.Str("MetricName"),
	}
	for _, df := range req.params.List("Dimensions") {
		name := df.Str("Name")
		if name == "" {
			return nil, errMissingParameter("The parameter Dimensions.member.N.Name is required.")
		}
		f.Dimensions = append(f.Dimensions, dimensionFilter{
			Name: name, Value: df.Str("Value"), HasValue: df.Has("Value")})
	}
	// RecentlyActive has exactly one legal value, so the window is fixed.
	if req.params.Str("RecentlyActive") == "PT3H" {
		f.SinceMs = s.now().Add(-3 * time.Hour).UnixMilli()
	}

	after, aerr := decodeToken(req.params.Str("NextToken"))
	if aerr != nil {
		return nil, aerr
	}
	found, next, err := s.listSeries(f, after, listMetricsPageSize)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}

	out := listMetricsResult{Metrics: make([]metricView, 0, len(found))}
	for _, rec := range found {
		out.Metrics = append(out.Metrics, metricView{
			Namespace: rec.Namespace, MetricName: rec.MetricName,
			Dimensions: dimensionViews(rec.Dimensions),
		})
	}
	out.NextToken = encodeToken(next)
	return out, nil
}

// dimensionViews renders a dimension set in a stable order, so two identical
// listings are identical rather than differing by map iteration.
func dimensionViews(dims map[string]string) []dimensionView {
	if len(dims) == 0 {
		return nil
	}
	names := make([]string, 0, len(dims))
	for n := range dims {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]dimensionView, 0, len(names))
	for _, n := range names {
		out = append(out, dimensionView{Name: n, Value: dims[n]})
	}
	return out
}

// encodeToken and decodeToken carry a store key across a page boundary. It is
// opaque to the client, so it is base64 — a raw key contains NUL bytes and
// would not survive a query string.
func encodeToken(key []byte) string {
	if len(key) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(key)
}

func decodeToken(tok string) ([]byte, *awshttp.APIError) {
	if tok == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return nil, errf("InvalidNextToken", "The next token is not valid.")
	}
	return raw, nil
}

// parseStatisticValues reads a pre-summarised observation set. The model
// requires all four members; these are the checks it cannot state — that the
// count is positive and the extremes bracket the mean, without which an
// average over the record is nonsense.
func parseStatisticValues(sv params) (*statisticValues, *awshttp.APIError) {
	count, hasCount := sv.Float("SampleCount")
	sum, _ := sv.Float("Sum")
	min, hasMin := sv.Float("Minimum")
	max, hasMax := sv.Float("Maximum")
	if !hasCount || !hasMin || !hasMax {
		return nil, errMissingParameter(
			"StatisticValues requires SampleCount, Sum, Minimum and Maximum.")
	}
	if count <= 0 {
		return nil, errInvalidParameter(
			"The parameter StatisticValues.SampleCount must be greater than 0.")
	}
	if min > max {
		return nil, errInvalidParameter(
			"The parameter StatisticValues.Minimum must not exceed Maximum.")
	}
	return &statisticValues{SampleCount: count, Sum: sum, Minimum: min, Maximum: max}, nil
}

// parseDatum reads one MetricDatum. The checks here are the ones the model
// cannot state: that a value was supplied at all, and that a dimension has
// both halves.
func parseDatum(ns string, md params) (datum, *awshttp.APIError) {
	name := md.Str("MetricName")
	if name == "" {
		return datum{}, errMissingParameter("The parameter MetricName is required.")
	}
	d := datum{Namespace: ns, MetricName: name, Unit: md.Str("Unit"),
		Resolution: md.Int("StorageResolution", 60)}
	if ts, ok := md.Time("Timestamp"); ok {
		d.Timestamp = ts
	}

	// A value is required unless StatisticValues carries one, and 0 is a
	// legal value — so presence is the question, not truthiness.
	v, ok := md.Float("Value")
	if sv := md.Map("StatisticValues"); sv != nil {
		stats, aerr := parseStatisticValues(sv)
		if aerr != nil {
			return datum{}, aerr
		}
		d.Stats = stats
	} else if !ok {
		return datum{}, errMissingParameter(
			"The parameter MetricDatum.Value or MetricDatum.StatisticValues is required.")
	}
	d.Value = v

	dims := md.List("Dimensions")
	if len(dims) > 0 {
		d.Dimensions = make(map[string]string, len(dims))
		for _, dim := range dims {
			dn, dv := dim.Str("Name"), dim.Str("Value")
			if dn == "" || dv == "" {
				return datum{}, errInvalidParameter(
					"The dimension Name and Value must both be set.")
			}
			d.Dimensions[dn] = dv
		}
	}
	return d, nil
}
