package cloudwatch

// Result shapes.
//
// One struct per result, carrying BOTH `json` and `xml` tags — the pattern
// sqs/responses.go already uses for its two wires. The CBOR encoder reads the
// `json` tag by reflection (internal/rpcv2cbor), because CBOR's
// map/array/scalar model is structurally what JSON's is, so three wires are
// served without a third tag vocabulary.
//
// The two spellings differ in exactly one way that matters: the Query
// protocol wraps every list element in a <member> element, which Go's XML
// encoder writes as `xml:"Name>member"`. JSON and CBOR carry a plain array.

// metricView is one entry in the metric catalogue: what was published, not
// what its values were.
type metricView struct {
	Namespace  string          `json:"Namespace" xml:"Namespace"`
	MetricName string          `json:"MetricName" xml:"MetricName"`
	Dimensions []dimensionView `json:"Dimensions,omitempty" xml:"Dimensions>member,omitempty"`
}

// dimensionView is one Name/Value pair. This is the shape whose Query
// flattening is indistinguishable from a map's — see validate.go.
type dimensionView struct {
	Name  string `json:"Name" xml:"Name"`
	Value string `json:"Value" xml:"Value"`
}

// listMetricsResult answers ListMetrics.
//
// Metrics is never nil, so an empty catalogue answers with an empty list
// rather than a null. A client paging through metrics should see "none" as a
// list of no metrics, which is what AWS sends.
type listMetricsResult struct {
	Metrics   []metricView `json:"Metrics" xml:"Metrics>member"`
	NextToken string       `json:"NextToken,omitempty" xml:"NextToken,omitempty"`
}
