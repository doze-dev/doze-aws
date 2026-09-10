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
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// handlers is every operation with a real implementation.
var handlers = map[string]handler{
	"PutMetricData": (*Server).putMetricData,
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
	for _, d := range parsed {
		s.logf("cloudwatch: %s/%s = %v %s (%d dimensions)",
			d.Namespace, d.MetricName, d.Value, d.Unit, len(d.Dimensions))
	}
	return nil, nil
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

	// A value is required unless StatisticValues carries one, and 0 is a
	// legal value — so presence is the question, not truthiness.
	v, ok := md.Float("Value")
	if !ok && !md.Has("StatisticValues") {
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
