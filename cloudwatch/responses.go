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

import (
	"strings"
	"time"
)

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

// metricDataResultView is one query's answer: parallel Timestamps and Values
// rather than a list of points, which is the shape GetMetricData uses.
type metricDataResultView struct {
	Id         string      `json:"Id" xml:"Id"`
	Label      string      `json:"Label,omitempty" xml:"Label,omitempty"`
	Timestamps []time.Time `json:"Timestamps" xml:"Timestamps>member"`
	Values     []float64   `json:"Values" xml:"Values>member"`
	// StatusCode is Complete, InternalError or PartialData. Everything this
	// answers is complete: the samples are local, so there is no partial read
	// to report.
	StatusCode string `json:"StatusCode,omitempty" xml:"StatusCode,omitempty"`
}

// getMetricDataResult answers GetMetricData.
type getMetricDataResult struct {
	MetricDataResults []metricDataResultView `json:"MetricDataResults" xml:"MetricDataResults>member"`
	NextToken         string                 `json:"NextToken,omitempty" xml:"NextToken,omitempty"`
}

// alarmView is one alarm as the API reports it.
type alarmView struct {
	AlarmName                          string          `json:"AlarmName" xml:"AlarmName"`
	AlarmArn                           string          `json:"AlarmArn" xml:"AlarmArn"`
	AlarmDescription                   string          `json:"AlarmDescription,omitempty" xml:"AlarmDescription,omitempty"`
	AlarmConfigurationUpdatedTimestamp time.Time       `json:"AlarmConfigurationUpdatedTimestamp" xml:"AlarmConfigurationUpdatedTimestamp"`
	ActionsEnabled                     bool            `json:"ActionsEnabled" xml:"ActionsEnabled"`
	OKActions                          []string        `json:"OKActions" xml:"OKActions>member"`
	AlarmActions                       []string        `json:"AlarmActions" xml:"AlarmActions>member"`
	InsufficientDataActions            []string        `json:"InsufficientDataActions" xml:"InsufficientDataActions>member"`
	StateValue                         string          `json:"StateValue" xml:"StateValue"`
	StateReason                        string          `json:"StateReason,omitempty" xml:"StateReason,omitempty"`
	StateReasonData                    string          `json:"StateReasonData,omitempty" xml:"StateReasonData,omitempty"`
	StateUpdatedTimestamp              time.Time       `json:"StateUpdatedTimestamp" xml:"StateUpdatedTimestamp"`
	MetricName                         string          `json:"MetricName" xml:"MetricName"`
	Namespace                          string          `json:"Namespace" xml:"Namespace"`
	Statistic                          string          `json:"Statistic,omitempty" xml:"Statistic,omitempty"`
	ExtendedStatistic                  string          `json:"ExtendedStatistic,omitempty" xml:"ExtendedStatistic,omitempty"`
	Dimensions                         []dimensionView `json:"Dimensions" xml:"Dimensions>member"`
	Period                             int             `json:"Period" xml:"Period"`
	Unit                               string          `json:"Unit,omitempty" xml:"Unit,omitempty"`
	EvaluationPeriods                  int             `json:"EvaluationPeriods" xml:"EvaluationPeriods"`
	DatapointsToAlarm                  int             `json:"DatapointsToAlarm,omitempty" xml:"DatapointsToAlarm,omitempty"`
	Threshold                          float64         `json:"Threshold" xml:"Threshold"`
	ComparisonOperator                 string          `json:"ComparisonOperator" xml:"ComparisonOperator"`
	TreatMissingData                   string          `json:"TreatMissingData,omitempty" xml:"TreatMissingData,omitempty"`
}

// viewOf renders a stored alarm. A percentile statistic goes in
// ExtendedStatistic and the named ones in Statistic — the same split AWS
// makes, and the reason a client reading Statistic for a p99 alarm finds
// nothing rather than a value it cannot parse.
func viewOf(a *alarm) alarmView {
	v := alarmView{
		AlarmName: a.Name, AlarmArn: a.ARN(), AlarmDescription: a.Description,
		AlarmConfigurationUpdatedTimestamp: time.UnixMilli(a.UpdatedMs).UTC(),
		ActionsEnabled:                     a.ActionsEnabled,
		OKActions:                          nonNil(a.OKActions),
		AlarmActions:                       nonNil(a.AlarmActions),
		InsufficientDataActions:            nonNil(a.InsufficientData),
		StateValue:                         a.State,
		StateReason:                        a.StateReason,
		StateReasonData:                    a.StateReasonData,
		StateUpdatedTimestamp:              time.UnixMilli(a.StateUpdatedMs).UTC(),
		MetricName:                         a.MetricName,
		Namespace:                          a.Namespace,
		Dimensions:                         nonNilDims(dimensionViews(a.Dimensions)),
		Period:                             a.Period,
		Unit:                               a.Unit,
		EvaluationPeriods:                  a.EvaluationPeriods,
		DatapointsToAlarm:                  a.DatapointsToAlarm,
		Threshold:                          a.Threshold,
		ComparisonOperator:                 a.ComparisonOp,
		TreatMissingData:                   a.TreatMissingData,
	}
	if strings.HasPrefix(a.Statistic, "p") {
		v.ExtendedStatistic = a.Statistic
	} else {
		v.Statistic = a.Statistic
	}
	return v
}

// A list member that is empty should serialise as an empty list, not null: a
// caller ranging over AlarmActions should find none rather than crash.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilDims(d []dimensionView) []dimensionView {
	if d == nil {
		return []dimensionView{}
	}
	return d
}

type describeAlarmsResult struct {
	MetricAlarms    []alarmView `json:"MetricAlarms" xml:"MetricAlarms>member"`
	CompositeAlarms []alarmView `json:"CompositeAlarms" xml:"CompositeAlarms>member"`
	NextToken       string      `json:"NextToken,omitempty" xml:"NextToken,omitempty"`
}

type describeAlarmsForMetricResult struct {
	MetricAlarms []alarmView `json:"MetricAlarms" xml:"MetricAlarms>member"`
}

// historyView is one entry in an alarm's log.
type historyView struct {
	AlarmName       string    `json:"AlarmName" xml:"AlarmName"`
	AlarmType       string    `json:"AlarmType,omitempty" xml:"AlarmType,omitempty"`
	Timestamp       time.Time `json:"Timestamp" xml:"Timestamp"`
	HistoryItemType string    `json:"HistoryItemType" xml:"HistoryItemType"`
	HistorySummary  string    `json:"HistorySummary" xml:"HistorySummary"`
	HistoryData     string    `json:"HistoryData,omitempty" xml:"HistoryData,omitempty"`
}

type describeAlarmHistoryResult struct {
	AlarmHistoryItems []historyView `json:"AlarmHistoryItems" xml:"AlarmHistoryItems>member"`
	NextToken         string        `json:"NextToken,omitempty" xml:"NextToken,omitempty"`
}
