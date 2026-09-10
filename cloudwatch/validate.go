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

// alarmTypes is the model's AlarmType. doze-aws only ever holds MetricAlarm,
// but the other two are accepted as filter values and answered with nothing,
// which is the truthful answer rather than a refusal.
var alarmTypes = []string{"MetricAlarm", "CompositeAlarm", "LogAlarm"}

// historyItemTypes includes the two contributor spellings the model carries.
// They select history doze-aws never writes, so they filter to nothing.
var historyItemTypes = []string{
	historyConfigUpdate, historyStateUpdate, historyAction,
	"AlarmContributorStateUpdate", "AlarmContributorAction",
}

// scanByValues orders DescribeAlarmHistory. AWS defaults to newest first.
var scanByValues = []string{scanDescending, scanAscending}

const (
	scanDescending = "TimestampDescending"
	scanAscending  = "TimestampAscending"
)

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
	"GetMetricData": {
		{Path: "StartTime", Kind: modelcheck.KindRequired},
		{Path: "EndTime", Kind: modelcheck.KindRequired},
		{Path: "MetricDataQueries", Kind: modelcheck.KindRequired},
		{Path: "MetricDataQueries[].Id", Kind: modelcheck.KindRequired},
		{Path: "MetricDataQueries[].Id", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "MetricDataQueries[].Expression", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "MetricDataQueries[].AccountId", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "MetricDataQueries[].Period", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "MetricDataQueries[].MetricStat.Metric", Kind: modelcheck.KindRequired},
		{Path: "MetricDataQueries[].MetricStat.Period", Kind: modelcheck.KindRequired},
		{Path: "MetricDataQueries[].MetricStat.Period", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "MetricDataQueries[].MetricStat.Stat", Kind: modelcheck.KindRequired},
		{Path: "MetricDataQueries[].MetricStat.Unit", Kind: modelcheck.KindEnum, Enum: standardUnits},
		{Path: "ScanBy", Kind: modelcheck.KindEnum,
			Enum: []string{"TimestampDescending", "TimestampAscending"}},
	},
	"GetMetricStatistics": {
		{Path: "Namespace", Kind: modelcheck.KindRequired},
		{Path: "Namespace", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Namespace", Kind: modelcheck.KindPattern, Pat: reNamespace},
		{Path: "MetricName", Kind: modelcheck.KindRequired},
		{Path: "MetricName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "StartTime", Kind: modelcheck.KindRequired},
		{Path: "EndTime", Kind: modelcheck.KindRequired},
		{Path: "Period", Kind: modelcheck.KindRequired},
		{Path: "Period", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindRequired},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Dimensions[].Value", Kind: modelcheck.KindRequired},
		{Path: "Dimensions[].Value", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "Statistics[]", Kind: modelcheck.KindEnum,
			Enum: []string{"Average", "Sum", "Minimum", "Maximum", "SampleCount"}},
		{Path: "Unit", Kind: modelcheck.KindEnum, Enum: standardUnits},
	},
	"PutMetricAlarm": {
		{Path: "AlarmName", Kind: modelcheck.KindRequired},
		{Path: "AlarmName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "AlarmDescription", Kind: modelcheck.KindLength, Min: 0, Max: 1024},
		{Path: "ComparisonOperator", Kind: modelcheck.KindEnum, Enum: comparisonOperators},
		{Path: "EvaluationPeriods", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "DatapointsToAlarm", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "Period", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "Namespace", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Namespace", Kind: modelcheck.KindPattern, Pat: reNamespace},
		{Path: "MetricName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Statistic", Kind: modelcheck.KindEnum,
			Enum: []string{"SampleCount", "Average", "Sum", "Minimum", "Maximum"}},
		{Path: "Unit", Kind: modelcheck.KindEnum, Enum: standardUnits},
		{Path: "TreatMissingData", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "ThresholdMetricId", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "EvaluateLowSampleCountPercentile", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "EvaluationInterval", Kind: modelcheck.KindRange, Min: 10, Max: 3600},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindRequired},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Dimensions[].Value", Kind: modelcheck.KindRequired},
		{Path: "Dimensions[].Value", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "AlarmActions[]", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "OKActions[]", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "InsufficientDataActions[]", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Metrics[].Id", Kind: modelcheck.KindRequired},
		{Path: "Metrics[].Id", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Metrics[].Expression", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Metrics[].AccountId", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Metrics[].Period", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "Metrics[].MetricStat.Metric", Kind: modelcheck.KindRequired},
		{Path: "Metrics[].MetricStat.Period", Kind: modelcheck.KindRequired},
		{Path: "Metrics[].MetricStat.Period", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "Metrics[].MetricStat.Stat", Kind: modelcheck.KindRequired},
		{Path: "Metrics[].MetricStat.Unit", Kind: modelcheck.KindEnum, Enum: standardUnits},
		{Path: "WarmUpConfiguration.WarmUpPeriodDurationInMinutes", Kind: modelcheck.KindRequired},
		{Path: "WarmUpConfiguration.WarmUpPeriodDurationInMinutes", Kind: modelcheck.KindRange, Min: 1, Max: 2880},
	},
	"DescribeAlarms": {
		{Path: "AlarmNames[]", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "AlarmNamePrefix", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "ActionPrefix", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "AlarmTypes[]", Kind: modelcheck.KindEnum, Enum: alarmTypes},
		// Composite-alarm filters. doze-aws evaluates metric alarms only, so
		// the honest answer to "the children of X" is an empty list — but the
		// value still has to be shaped like a name, as on AWS.
		{Path: "ChildrenOfAlarmName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "ParentsOfAlarmName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "StateValue", Kind: modelcheck.KindEnum,
			Enum: []string{stateOK, stateAlarm, stateInsufficientData}},
		{Path: "MaxRecords", Kind: modelcheck.KindRange, Min: 1, Max: 100},
	},
	"DescribeAlarmsForMetric": {
		{Path: "Namespace", Kind: modelcheck.KindRequired},
		{Path: "Namespace", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Namespace", Kind: modelcheck.KindPattern, Pat: reNamespace},
		{Path: "MetricName", Kind: modelcheck.KindRequired},
		{Path: "MetricName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Period", Kind: modelcheck.KindRange, Min: 1, Max: modelcheck.NoMax},
		{Path: "Statistic", Kind: modelcheck.KindEnum,
			Enum: []string{"SampleCount", "Average", "Sum", "Minimum", "Maximum"}},
		{Path: "Unit", Kind: modelcheck.KindEnum, Enum: standardUnits},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindRequired},
		{Path: "Dimensions[].Name", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "Dimensions[].Value", Kind: modelcheck.KindRequired},
		{Path: "Dimensions[].Value", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"DeleteAlarms": {
		{Path: "AlarmNames", Kind: modelcheck.KindRequired},
		{Path: "AlarmNames[]", Kind: modelcheck.KindLength, Min: 1, Max: 255},
	},
	"SetAlarmState": {
		{Path: "AlarmName", Kind: modelcheck.KindRequired},
		{Path: "AlarmName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "StateValue", Kind: modelcheck.KindRequired},
		{Path: "StateValue", Kind: modelcheck.KindEnum,
			Enum: []string{stateOK, stateAlarm, stateInsufficientData}},
		{Path: "StateReason", Kind: modelcheck.KindRequired},
		{Path: "StateReason", Kind: modelcheck.KindLength, Min: 0, Max: 1023},
		{Path: "StateReasonData", Kind: modelcheck.KindLength, Min: 0, Max: 4000},
	},
	"DescribeAlarmHistory": {
		{Path: "AlarmName", Kind: modelcheck.KindLength, Min: 1, Max: 255},
		{Path: "AlarmContributorId", Kind: modelcheck.KindLength, Min: 1, Max: 16},
		{Path: "AlarmTypes[]", Kind: modelcheck.KindEnum, Enum: alarmTypes},
		{Path: "HistoryItemType", Kind: modelcheck.KindEnum, Enum: historyItemTypes},
		{Path: "ScanBy", Kind: modelcheck.KindEnum, Enum: scanByValues},
		{Path: "MaxRecords", Kind: modelcheck.KindRange, Min: 1, Max: 100},
	},
	"EnableAlarmActions": {
		{Path: "AlarmNames", Kind: modelcheck.KindRequired},
		{Path: "AlarmNames[]", Kind: modelcheck.KindLength, Min: 1, Max: 255},
	},
	"DisableAlarmActions": {
		{Path: "AlarmNames", Kind: modelcheck.KindRequired},
		{Path: "AlarmNames[]", Kind: modelcheck.KindLength, Min: 1, Max: 255},
	},

	"PutDashboard": {
		{Path: "DashboardName", Kind: modelcheck.KindRequired},
		{Path: "DashboardBody", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"GetDashboard": {
		{Path: "DashboardName", Kind: modelcheck.KindRequired},
	},
	"DeleteDashboards": {
		{Path: "DashboardNames", Kind: modelcheck.KindRequired},
	},
	// ListDashboards carries no constraint traits in the model.
	"ListDashboards": {},

	"TagResource": {
		{Path: "ResourceARN", Kind: modelcheck.KindRequired},
		{Path: "ResourceARN", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "Tags", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"UntagResource": {
		{Path: "ResourceARN", Kind: modelcheck.KindRequired},
		{Path: "ResourceARN", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "TagKeys", Kind: modelcheck.KindRequired},
		{Path: "TagKeys[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
	},
	"ListTagsForResource": {
		{Path: "ResourceARN", Kind: modelcheck.KindRequired},
		{Path: "ResourceARN", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
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
