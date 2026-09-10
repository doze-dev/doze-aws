package logs

// CloudWatch Logs' model-derived input validation: the constraint tables,
// walked by internal/modelcheck.
//
// Transcribed from `dzaudit list cloudwatch-logs` for the operations this
// build dispatches, and replayed case by case in rejection_parity_test.go.

import (
	"regexp"

	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

var (
	reGroup   = regexp.MustCompile(`^[\.\-_/#A-Za-z0-9]+$`)
	reStream  = regexp.MustCompile(`^[^:*]*$`)
	reGroupID = regexp.MustCompile(`^[\w#+=/:,.@-]*$`)
	reTagVal  = regexp.MustCompile(`^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$`)
	reTagKey  = regexp.MustCompile(`^([\p{L}\p{Z}\p{N}_.:/=+\-@]+)$`)
	reARN     = regexp.MustCompile(`^[\w+=/:,.@-]*$`)
	reAccount = regexp.MustCompile(`^\d{12}$`)
	// A metric namespace or name may not carry a colon, a star or a dollar —
	// the last because `$.field` is metricValue's reference syntax.
	reMetricName = regexp.MustCompile(`^[^:*$]*$`)
)

func groupName(required bool) []modelcheck.Constraint {
	cs := []modelcheck.Constraint{
		{Path: "logGroupName", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "logGroupName", Kind: modelcheck.KindPattern, Pat: reGroup},
	}
	if required {
		cs = append(cs, modelcheck.Constraint{Path: "logGroupName", Kind: modelcheck.KindRequired})
	}
	return cs
}

func streamName(required bool) []modelcheck.Constraint {
	cs := []modelcheck.Constraint{
		{Path: "logStreamName", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "logStreamName", Kind: modelcheck.KindPattern, Pat: reStream},
	}
	if required {
		cs = append(cs, modelcheck.Constraint{Path: "logStreamName", Kind: modelcheck.KindRequired})
	}
	return cs
}

var groupIdentifier = []modelcheck.Constraint{
	{Path: "logGroupIdentifier", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	{Path: "logGroupIdentifier", Kind: modelcheck.KindPattern, Pat: reGroupID},
}

var tagsMap = []modelcheck.Constraint{
	{Path: "tags{}", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	{Path: "tags{}", Kind: modelcheck.KindPattern, Pat: reTagVal},
}

var resourceARN = []modelcheck.Constraint{
	{Path: "resourceArn", Kind: modelcheck.KindRequired},
	{Path: "resourceArn", Kind: modelcheck.KindLength, Min: 1, Max: 1011},
	{Path: "resourceArn", Kind: modelcheck.KindPattern, Pat: reARN},
}

func cat(parts ...[]modelcheck.Constraint) []modelcheck.Constraint {
	var out []modelcheck.Constraint
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var constraintTables = map[string][]modelcheck.Constraint{
	"CreateLogGroup": cat(groupName(true), tagsMap, []modelcheck.Constraint{
		{Path: "kmsKeyId", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "logGroupClass", Kind: modelcheck.KindEnum, Enum: []string{"STANDARD", "INFREQUENT_ACCESS", "DELIVERY"}},
	}),
	"DeleteLogGroup":        groupName(true),
	"PutRetentionPolicy":    cat(groupName(true), []modelcheck.Constraint{{Path: "retentionInDays", Kind: modelcheck.KindRequired}}),
	"DeleteRetentionPolicy": groupName(true),
	"DescribeLogGroups": cat(groupIdentifierList(), []modelcheck.Constraint{
		{Path: "accountIdentifiers[]", Kind: modelcheck.KindLength, Min: 12, Max: 12},
		{Path: "accountIdentifiers[]", Kind: modelcheck.KindPattern, Pat: reAccount},
		{Path: "limit", Kind: modelcheck.KindRange, Min: 1, Max: 50},
		{Path: "logGroupClass", Kind: modelcheck.KindEnum, Enum: []string{"STANDARD", "INFREQUENT_ACCESS", "DELIVERY"}},
		{Path: "logGroupNamePattern", Kind: modelcheck.KindLength, Min: 0, Max: 512},
		{Path: "logGroupNamePattern", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[\.\-_/#A-Za-z0-9]*$`)},
		{Path: "logGroupNamePrefix", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "logGroupNamePrefix", Kind: modelcheck.KindPattern, Pat: reGroup},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
	}),
	"ListLogGroups": {
		{Path: "accountIdentifiers[]", Kind: modelcheck.KindLength, Min: 12, Max: 12},
		{Path: "accountIdentifiers[]", Kind: modelcheck.KindPattern, Pat: reAccount},
		{Path: "dataSources[].name", Kind: modelcheck.KindRequired},
		{Path: "fieldIndexNames[]", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "fieldIndexNames[]", Kind: modelcheck.KindPattern, Pat: reGroup},
		{Path: "limit", Kind: modelcheck.KindRange, Min: 1, Max: 1000},
		{Path: "logGroupClass", Kind: modelcheck.KindEnum, Enum: []string{"STANDARD", "INFREQUENT_ACCESS", "DELIVERY"}},
		{Path: "logGroupNamePattern", Kind: modelcheck.KindLength, Min: 3, Max: 129},
		{Path: "logGroupNamePattern", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^(\^?[\.\-_\/#A-Za-z0-9]{3,24})(\|\^?[\.\-_\/#A-Za-z0-9]{3,24}){0,4}$`)},
		{Path: "logGroupTags[].key", Kind: modelcheck.KindRequired},
		{Path: "logGroupTags[].key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "logGroupTags[].key", Kind: modelcheck.KindPattern, Pat: reTagKey},
		{Path: "logGroupTags[].values[]", Kind: modelcheck.KindLength, Min: 0, Max: 259},
		{Path: "logGroupTags[].values[]", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^!?\*?([\p{L}\p{Z}\p{N}_.:/=+\-@]*)\*?$`)},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
	},
	"CreateLogStream": cat(groupName(true), streamName(true)),
	"DeleteLogStream": cat(groupName(true), streamName(true)),
	"DescribeLogStreams": cat(groupName(false), groupIdentifier, []modelcheck.Constraint{
		{Path: "limit", Kind: modelcheck.KindRange, Min: 1, Max: 50},
		{Path: "logStreamNamePrefix", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "logStreamNamePrefix", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
		{Path: "orderBy", Kind: modelcheck.KindEnum, Enum: []string{"LogStreamName", "LastEventTime"}},
	}),
	"PutLogEvents": cat(groupName(true), streamName(true), []modelcheck.Constraint{
		{Path: "entity.attributes{}", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "entity.keyAttributes{}", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "logEvents", Kind: modelcheck.KindRequired},
		{Path: "logEvents[].message", Kind: modelcheck.KindRequired},
		{Path: "logEvents[].message", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
		{Path: "logEvents[].timestamp", Kind: modelcheck.KindRequired},
		{Path: "logEvents[].timestamp", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
		{Path: "sequenceToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
	}),
	"GetLogEvents": cat(groupName(false), groupIdentifier, streamName(true), []modelcheck.Constraint{
		{Path: "endTime", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
		{Path: "limit", Kind: modelcheck.KindRange, Min: 1, Max: 10000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
		{Path: "startTime", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
	}),
	"FilterLogEvents": cat(groupName(false), groupIdentifier, []modelcheck.Constraint{
		{Path: "endTime", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
		{Path: "filterPattern", Kind: modelcheck.KindLength, Min: 0, Max: 1024},
		{Path: "limit", Kind: modelcheck.KindRange, Min: 1, Max: 10000},
		{Path: "logStreamNamePrefix", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "logStreamNamePrefix", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "logStreamNames[]", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "logStreamNames[]", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
		{Path: "startTime", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
	}),
	"TagResource":         cat(resourceARN, tagsMap, []modelcheck.Constraint{{Path: "tags", Kind: modelcheck.KindRequired}}),
	"UntagResource":       cat(resourceARN, tagKeys("tagKeys")),
	"ListTagsForResource": resourceARN,
	"TagLogGroup":         cat(groupName(true), tagsMap, []modelcheck.Constraint{{Path: "tags", Kind: modelcheck.KindRequired}}),
	"UntagLogGroup":       cat(groupName(true), tagKeys("tags")),
	"ListTagsLogGroup":    groupName(true),
	"PutSubscriptionFilter": cat(groupName(true), []modelcheck.Constraint{
		{Path: "destinationArn", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
		{Path: "destinationArn", Kind: modelcheck.KindRequired},
		{Path: "distribution", Kind: modelcheck.KindEnum, Enum: []string{"Random", "ByLogStream"}},
		{Path: "fieldSelectionCriteria", Kind: modelcheck.KindLength, Min: 0, Max: 2000},
		{Path: "filterName", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "filterName", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "filterName", Kind: modelcheck.KindRequired},
		{Path: "filterPattern", Kind: modelcheck.KindLength, Min: 0, Max: 1024},
		{Path: "filterPattern", Kind: modelcheck.KindRequired},
		{Path: "roleArn", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
	}),
	"DeleteSubscriptionFilter": cat(groupName(true), []modelcheck.Constraint{
		{Path: "filterName", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "filterName", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "filterName", Kind: modelcheck.KindRequired},
	}),
	"DescribeSubscriptionFilters": cat(groupName(true), []modelcheck.Constraint{
		{Path: "filterNamePrefix", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "filterNamePrefix", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "limit", Kind: modelcheck.KindRange, Min: 1, Max: 50},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
	}),
	"PutMetricFilter": cat(groupName(true), []modelcheck.Constraint{
		{Path: "fieldSelectionCriteria", Kind: modelcheck.KindLength, Min: 0, Max: 2000},
		{Path: "filterName", Kind: modelcheck.KindRequired},
		{Path: "filterName", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "filterName", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "filterPattern", Kind: modelcheck.KindRequired},
		{Path: "filterPattern", Kind: modelcheck.KindLength, Min: 0, Max: 1024},
		{Path: "metricTransformations", Kind: modelcheck.KindRequired},
		{Path: "metricTransformations[].dimensions{}", Kind: modelcheck.KindLength, Min: 0, Max: 255},
		{Path: "metricTransformations[].metricName", Kind: modelcheck.KindRequired},
		{Path: "metricTransformations[].metricName", Kind: modelcheck.KindLength, Min: 0, Max: 255},
		{Path: "metricTransformations[].metricName", Kind: modelcheck.KindPattern, Pat: reMetricName},
		{Path: "metricTransformations[].metricNamespace", Kind: modelcheck.KindRequired},
		{Path: "metricTransformations[].metricNamespace", Kind: modelcheck.KindLength, Min: 0, Max: 255},
		{Path: "metricTransformations[].metricNamespace", Kind: modelcheck.KindPattern, Pat: reMetricName},
		{Path: "metricTransformations[].metricValue", Kind: modelcheck.KindRequired},
		{Path: "metricTransformations[].metricValue", Kind: modelcheck.KindLength, Min: 0, Max: 100},
		{Path: "metricTransformations[].unit", Kind: modelcheck.KindEnum, Enum: standardUnits},
	}),
	"DeleteMetricFilter": cat(groupName(true), []modelcheck.Constraint{
		{Path: "filterName", Kind: modelcheck.KindRequired},
		{Path: "filterName", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "filterName", Kind: modelcheck.KindPattern, Pat: reStream},
	}),
	"DescribeMetricFilters": cat(groupName(false), []modelcheck.Constraint{
		{Path: "filterNamePrefix", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "filterNamePrefix", Kind: modelcheck.KindPattern, Pat: reStream},
		{Path: "limit", Kind: modelcheck.KindRange, Min: 1, Max: 50},
		{Path: "metricName", Kind: modelcheck.KindLength, Min: 0, Max: 255},
		{Path: "metricName", Kind: modelcheck.KindPattern, Pat: reMetricName},
		{Path: "metricNamespace", Kind: modelcheck.KindLength, Min: 0, Max: 255},
		{Path: "metricNamespace", Kind: modelcheck.KindPattern, Pat: reMetricName},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
	}),
	"TestMetricFilter": {
		{Path: "filterPattern", Kind: modelcheck.KindRequired},
		{Path: "filterPattern", Kind: modelcheck.KindLength, Min: 0, Max: 1024},
		{Path: "logEventMessages", Kind: modelcheck.KindRequired},
		{Path: "logEventMessages[]", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
	},
}

// standardUnits is StandardUnit, which CloudWatch Logs shares with CloudWatch
// Metrics: a metric transformation names the unit the produced metric carries.
var standardUnits = []string{
	"Gigabytes", "Terabits", "Gigabytes/Second", "None", "Microseconds", "Milliseconds",
	"Bytes", "Terabytes", "Bits", "Bytes/Second", "Kilobytes/Second", "Terabytes/Second",
	"Seconds", "Kilobits", "Count", "Bits/Second", "Kilobits/Second", "Megabits/Second",
	"Gigabits/Second", "Count/Second", "Gigabits", "Percent", "Kilobytes", "Megabytes",
	"Megabits", "Megabytes/Second", "Terabits/Second",
}

func groupIdentifierList() []modelcheck.Constraint {
	return []modelcheck.Constraint{
		{Path: "logGroupIdentifiers[]", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "logGroupIdentifiers[]", Kind: modelcheck.KindPattern, Pat: reGroupID},
	}
}

func tagKeys(field string) []modelcheck.Constraint {
	return []modelcheck.Constraint{
		{Path: field, Kind: modelcheck.KindRequired},
		{Path: field + "[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: field + "[]", Kind: modelcheck.KindPattern, Pat: reTagKey},
	}
}

// notHere is the rest of the model: every operation this build does not
// dispatch, with what it would need. Nothing falls through to InvalidAction.
var notHere = map[string]string{}

func init() {
	groups := map[string][]string{
		"Logs Insights queries run on a query engine that does not exist locally; FilterLogEvents covers what a developer reads": {
			"StartQuery", "StopQuery", "GetQueryResults", "DescribeQueries", "PutQueryDefinition", "DeleteQueryDefinition",
			"DescribeQueryDefinitions", "GetLogGroupFields", "GetLogRecord", "GetLogFields", "GetLogObject",
			"ListLogGroupsForQuery", "CreateScheduledQuery", "DeleteScheduledQuery", "GetScheduledQuery",
			"GetScheduledQueryHistory", "ListScheduledQueries", "UpdateScheduledQuery", "ListAggregateLogGroupSummaries",
			"CreateLookupTable", "DeleteLookupTable", "GetLookupTable", "DescribeLookupTables", "UpdateLookupTable",
		},
		"live tail is an HTTP event stream to a fleet of tailers; `aws logs tail --follow` polls FilterLogEvents, which works": {
			"StartLiveTail",
		},
		"cross-account destinations receive another account's subscription filters; subscribe a Lambda function or a Kinesis stream directly": {
			"PutDestination", "DeleteDestination", "DescribeDestinations", "PutDestinationPolicy",
		},
		"vended-log deliveries move other services' logs into groups, buckets and Firehose; not built": {
			"CreateDelivery", "DeleteDelivery", "GetDelivery", "DescribeDeliveries", "UpdateDeliveryConfiguration",
			"PutDeliveryDestination", "GetDeliveryDestination", "DeleteDeliveryDestination", "DescribeDeliveryDestinations",
			"PutDeliveryDestinationPolicy", "GetDeliveryDestinationPolicy", "DeleteDeliveryDestinationPolicy",
			"PutDeliverySource", "GetDeliverySource", "DeleteDeliverySource", "DescribeDeliverySources",
			"DescribeConfigurationTemplates",
		},
		"export and import tasks move logs through S3 batch jobs; not built": {
			"CreateExportTask", "CancelExportTask", "DescribeExportTasks",
			"CreateImportTask", "CancelImportTask", "DescribeImportTasks", "DescribeImportTaskBatches",
		},
		"anomaly detection is a trained model over an account's logs; not built": {
			"CreateLogAnomalyDetector", "DeleteLogAnomalyDetector", "GetLogAnomalyDetector", "ListLogAnomalyDetectors",
			"UpdateLogAnomalyDetector", "ListAnomalies", "UpdateAnomaly",
		},
		"account, data-protection, index, storage-tier and resource policies govern an account, not a local store; not built": {
			"PutAccountPolicy", "DeleteAccountPolicy", "DescribeAccountPolicies",
			"PutDataProtectionPolicy", "GetDataProtectionPolicy", "DeleteDataProtectionPolicy",
			"PutIndexPolicy", "DeleteIndexPolicy", "DescribeIndexPolicies", "DescribeFieldIndexes",
			"PutResourcePolicy", "DeleteResourcePolicy", "DescribeResourcePolicies",
			"PutStorageTierPolicy", "GetStorageTierPolicy", "PutLogGroupDeletionProtection", "PutBearerTokenAuthentication",
		},
		"transformers rewrite events in the ingestion pipeline; not built": {
			"PutTransformer", "GetTransformer", "DeleteTransformer", "TestTransformer",
		},
		"integrations with OpenSearch and S3 Tables need those services; not built": {
			"PutIntegration", "GetIntegration", "ListIntegrations", "DeleteIntegration",
			"AssociateSourceToS3TableIntegration", "DisassociateSourceFromS3TableIntegration", "ListSourcesForS3TableIntegration",
		},
		"KMS-encrypted groups: the local store is a file on your disk; not built": {
			"AssociateKmsKey", "DisassociateKmsKey",
		},
		"syslog configurations receive from network devices; not built": {
			"PutSyslogConfiguration", "DeleteSyslogConfiguration", "ListSyslogConfigurations",
		},
	}
	for why, ops := range groups {
		for _, op := range ops {
			notHere[op] = why
		}
	}
}
