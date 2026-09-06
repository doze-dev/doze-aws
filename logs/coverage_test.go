package logs

// Dispatch coverage: every operation the service model documents is either
// handled or refused with a reason. Nothing falls through to InvalidAction.

import "testing"

// modelOperations is the full operation list of com.amazonaws.cloudwatchlogs
// (118 ops, from `dzaudit list cloudwatch-logs`). Frozen so a model refresh
// that adds an operation fails this test instead of answering InvalidAction.
var modelOperations = []string{
	"AssociateKmsKey", "AssociateSourceToS3TableIntegration", "CancelExportTask", "CancelImportTask",
	"CreateDelivery", "CreateExportTask", "CreateImportTask", "CreateLogAnomalyDetector", "CreateLogGroup",
	"CreateLogStream", "CreateLookupTable", "CreateScheduledQuery", "DeleteAccountPolicy",
	"DeleteDataProtectionPolicy", "DeleteDelivery", "DeleteDeliveryDestination", "DeleteDeliveryDestinationPolicy",
	"DeleteDeliverySource", "DeleteDestination", "DeleteIndexPolicy", "DeleteIntegration",
	"DeleteLogAnomalyDetector", "DeleteLogGroup", "DeleteLogStream", "DeleteLookupTable", "DeleteMetricFilter",
	"DeleteQueryDefinition", "DeleteResourcePolicy", "DeleteRetentionPolicy", "DeleteScheduledQuery",
	"DeleteSubscriptionFilter", "DeleteSyslogConfiguration", "DeleteTransformer", "DescribeAccountPolicies",
	"DescribeConfigurationTemplates", "DescribeDeliveries", "DescribeDeliveryDestinations",
	"DescribeDeliverySources", "DescribeDestinations", "DescribeExportTasks", "DescribeFieldIndexes",
	"DescribeImportTaskBatches", "DescribeImportTasks", "DescribeIndexPolicies", "DescribeLogGroups",
	"DescribeLogStreams", "DescribeLookupTables", "DescribeMetricFilters", "DescribeQueries",
	"DescribeQueryDefinitions", "DescribeResourcePolicies", "DescribeSubscriptionFilters", "DisassociateKmsKey",
	"DisassociateSourceFromS3TableIntegration", "FilterLogEvents", "GetDataProtectionPolicy", "GetDelivery",
	"GetDeliveryDestination", "GetDeliveryDestinationPolicy", "GetDeliverySource", "GetIntegration",
	"GetLogAnomalyDetector", "GetLogEvents", "GetLogFields", "GetLogGroupFields", "GetLogObject", "GetLogRecord",
	"GetLookupTable", "GetQueryResults", "GetScheduledQuery", "GetScheduledQueryHistory", "GetStorageTierPolicy", "GetTransformer",
	"ListAggregateLogGroupSummaries", "ListAnomalies", "ListIntegrations", "ListLogAnomalyDetectors",
	"ListLogGroups", "ListLogGroupsForQuery", "ListScheduledQueries", "ListSourcesForS3TableIntegration",
	"ListSyslogConfigurations", "ListTagsForResource", "ListTagsLogGroup", "PutAccountPolicy",
	"PutBearerTokenAuthentication", "PutDataProtectionPolicy", "PutDeliveryDestination",
	"PutDeliveryDestinationPolicy", "PutDeliverySource", "PutDestination", "PutDestinationPolicy",
	"PutIndexPolicy", "PutIntegration", "PutLogEvents", "PutLogGroupDeletionProtection", "PutMetricFilter",
	"PutQueryDefinition", "PutResourcePolicy", "PutRetentionPolicy", "PutStorageTierPolicy",
	"PutSubscriptionFilter", "PutSyslogConfiguration", "PutTransformer", "StartLiveTail", "StartQuery",
	"StopQuery", "TagLogGroup", "TagResource", "TestMetricFilter", "TestTransformer", "UntagLogGroup",
	"UntagResource", "UpdateAnomaly", "UpdateDeliveryConfiguration", "UpdateLogAnomalyDetector",
	"UpdateLookupTable", "UpdateScheduledQuery",
}

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	if len(modelOperations) != 118 {
		t.Fatalf("the frozen list has %d operations; the model documents 118", len(modelOperations))
	}
	for _, op := range modelOperations {
		_, handled := handlers[op]
		_, refused := notHere[op]
		switch {
		case handled && refused:
			t.Errorf("%s is both handled and listed as not here", op)
		case !handled && !refused:
			t.Errorf("%s is in the service model but neither handled nor refused — it would answer InvalidAction, which reads as a typo", op)
		}
	}
	known := map[string]bool{}
	for _, op := range modelOperations {
		known[op] = true
	}
	for op := range handlers {
		if !known[op] {
			t.Errorf("%s is handled but not in the model", op)
		}
	}
	for op := range notHere {
		if !known[op] {
			t.Errorf("%s is refused but not in the model", op)
		}
	}
}
