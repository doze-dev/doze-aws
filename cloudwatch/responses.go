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

import "time"

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

// datapointView is one period's statistics.
//
// Every statistic is a pointer, because absent and zero are different
// answers: a Sum of 0 over a period that had observations is a fact, and
// omitting the member is how "you did not ask for this statistic" is said.
// Percentiles ride in ExtendedStatistics, keyed by their pNN name.
type datapointView struct {
	Timestamp   time.Time          `json:"Timestamp" xml:"Timestamp"`
	SampleCount *float64           `json:"SampleCount,omitempty" xml:"SampleCount,omitempty"`
	Average     *float64           `json:"Average,omitempty" xml:"Average,omitempty"`
	Sum         *float64           `json:"Sum,omitempty" xml:"Sum,omitempty"`
	Minimum     *float64           `json:"Minimum,omitempty" xml:"Minimum,omitempty"`
	Maximum     *float64           `json:"Maximum,omitempty" xml:"Maximum,omitempty"`
	Unit        string             `json:"Unit,omitempty" xml:"Unit,omitempty"`
	Extended    map[string]float64 `json:"ExtendedStatistics,omitempty" xml:"-"`
}

// set places one computed statistic on the datapoint.
func (d *datapointView) set(st stat, v float64) {
	val := v
	switch st.Name {
	case "SampleCount":
		d.SampleCount = &val
	case "Average":
		d.Average = &val
	case "Sum":
		d.Sum = &val
	case "Minimum":
		d.Minimum = &val
	case "Maximum":
		d.Maximum = &val
	default:
		if d.Extended == nil {
			d.Extended = map[string]float64{}
		}
		d.Extended[st.Name] = v
	}
}

// getMetricStatisticsResult answers GetMetricStatistics.
type getMetricStatisticsResult struct {
	Label      string          `json:"Label" xml:"Label"`
	Datapoints []datapointView `json:"Datapoints" xml:"Datapoints>member"`
}
