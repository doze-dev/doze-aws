package cloudwatch

// Model-derived input validation, and the operations refused by name.
//
// Both tables are generated from AWS's own service model rather than
// transcribed — `dzaudit list cloudwatch` for the constraints, the model's
// operation list for the refusals — on the principle that a check nobody
// wrote is indistinguishable from a check that passes.
//
// One table serves all three wires. The paths describe the shape a JSON body
// has, and CBOR decodes to that shape natively while Query is rebuilt into it
// by modelcheck.FromQuery, so a constraint written once is enforced three
// times. `MetricData[].Dimensions[].Name` is the load-bearing example: it is
// a LIST of Name/Value structures, and it only resolves on the Query wire
// because FromQuery now keeps `.member.` containers as lists.

import (
	"encoding/json"
	"regexp"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

// standardUnits is StandardUnit, in the model's order.
var standardUnits = []string{
	"Gigabytes", "Terabits", "Gigabytes/Second", "None", "Microseconds", "Milliseconds",
	"Bytes", "Terabytes", "Bits", "Bytes/Second", "Kilobytes/Second", "Terabytes/Second",
	"Seconds", "Kilobits", "Count", "Bits/Second", "Kilobits/Second", "Megabits/Second",
	"Gigabits/Second", "Count/Second", "Gigabits", "Percent", "Kilobytes", "Megabytes",
	"Megabits", "Megabytes/Second", "Terabits/Second",
}

// reNamespace is the model's ^[^:] — a namespace may not begin with a colon.
var reNamespace = regexp.MustCompile(`^[^:]`)

var constraintTables = map[string][]modelcheck.Constraint{
	"PutMetricData": {
		{Path: "Namespace", Kind: modelcheck.KindRequired},
		{Path: "Namespace", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Namespace", Kind: modelcheck.KindPattern, Pat: reNamespace},
		{Path: "MetricData[].MetricName", Kind: modelcheck.KindRequired},
		{Path: "MetricData[].MetricName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "MetricData[].Dimensions[].Name", Kind: modelcheck.KindRequired},
		{Path: "MetricData[].Dimensions[].Value", Kind: modelcheck.KindRequired},
		{Path: "MetricData[].StorageResolution", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "MetricData[].Unit", Kind: modelcheck.KindEnum, Enum: standardUnits},
		{Path: "MetricData[].StatisticValues.Maximum", Kind: modelcheck.KindRequired},
		{Path: "MetricData[].StatisticValues.Minimum", Kind: modelcheck.KindRequired},
		{Path: "MetricData[].StatisticValues.SampleCount", Kind: modelcheck.KindRequired},
		{Path: "MetricData[].StatisticValues.Sum", Kind: modelcheck.KindRequired},
		{Path: "EntityMetricData[].MetricData[].MetricName", Kind: modelcheck.KindRequired},
	},
	"ListMetrics": {
		{Path: "Namespace", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Namespace", Kind: modelcheck.KindPattern, Pat: reNamespace},
		{Path: "MetricName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindRequired},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Dimensions[].Value", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "OwningAccount", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "RecentlyActive", Kind: modelcheck.KindEnum, Enum: []string{"PT3H"}},
	},
}

// notHere is every documented operation doze-aws refuses on purpose, with
// what it would need. Grouped by reason, because the reason is the useful
// part: a developer reading a refusal wants to know whether to work around it
// or stop trying.
//
// Everything here is staged for a later sub-batch or deliberately out of
// scope; coverage_test.go asserts that the model's 50 operations are exactly
// handlers ∪ notHere, so an operation cannot quietly fall through to
// InvalidAction and read as a typo.
var notHere = map[string]string{}

func init() {
	groups := map[string][]string{
		"the metric store lands in the next sub-batch": {
			"GetMetricData", "GetMetricStatistics",
		},
		"alarms land in the sub-batch after the metric store": {
			"PutMetricAlarm", "DescribeAlarms", "DescribeAlarmsForMetric", "DeleteAlarms",
			"SetAlarmState", "DescribeAlarmHistory", "EnableAlarmActions", "DisableAlarmActions",
		},
		"dashboards are stored and returned once the store lands": {
			"PutDashboard", "GetDashboard", "ListDashboards", "DeleteDashboards",
		},
		"tagging lands with the resources there are to tag": {
			"TagResource", "UntagResource", "ListTagsForResource",
		},
		"a composite alarm evaluates a rule over other alarms' states; doze-aws " +
			"evaluates metric alarms only": {
			"PutCompositeAlarm", "DescribeAlarmContributors",
		},
		"anomaly detection is a trained model over historical data, which a " +
			"local emulator has neither the history nor the model for": {
			"PutAnomalyDetector", "DeleteAnomalyDetector", "DescribeAnomalyDetectors",
		},
		"Contributor Insights analyses log patterns against rules doze-aws does " +
			"not evaluate": {
			"PutInsightRule", "DeleteInsightRules", "DescribeInsightRules",
			"EnableInsightRules", "DisableInsightRules", "GetInsightRuleReport",
			"ListManagedInsightRules", "PutManagedInsightRules",
		},
		"a metric stream delivers to Firehose, which does not exist locally": {
			"PutMetricStream", "DeleteMetricStream", "GetMetricStream", "ListMetricStreams",
			"StartMetricStreams", "StopMetricStreams",
		},
		"alarm mute rules suppress notifications on a schedule; doze-aws fires " +
			"alarm actions immediately and keeps no schedule": {
			"PutAlarmMuteRule", "GetAlarmMuteRule", "DeleteAlarmMuteRule", "ListAlarmMuteRules",
		},
		"OpenTelemetry enrichment is an account-level ingestion feature with no " +
			"local equivalent": {
			"GetOTelEnrichment", "StartOTelEnrichment", "StopOTelEnrichment",
		},
		"datasets and their KMS association are cloud-side storage configuration": {
			"GetDataset", "AssociateDatasetKmsKey", "DisassociateDatasetKmsKey",
		},
		"GetMetricWidgetImage renders a PNG chart, which needs a graphics stack " +
			"a single static binary does not carry": {
			"GetMetricWidgetImage",
		},
		"PutLogAlarm alarms on a Logs Insights query, which doze-aws does not run": {
			"PutLogAlarm",
		},
	}
	for why, ops := range groups {
		for _, op := range ops {
			notHere[op] = why
		}
	}
}

// decodeJSONBody reads an AWS JSON 1.0 body into the same shape the other two
// wires produce. An empty body is an empty input, not an error: several
// operations take no required members.
func decodeJSONBody(raw []byte) (params, *awshttp.APIError) {
	if len(raw) == 0 {
		return params{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "malformed JSON body: %v", err)
	}
	if obj == nil {
		return params{}, nil
	}
	return params(obj), nil
}
