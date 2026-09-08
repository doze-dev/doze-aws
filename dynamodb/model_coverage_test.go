package dynamodb

// Dispatch coverage: every operation the DynamoDB service model documents is
// either handled or refused with a reason. Nothing falls through to
// InvalidAction, which reads as a typo rather than a boundary.

import "testing"

// modelOperations is the full operation list of DynamoDB_20120810 (58 ops,
// from `dzaudit list dynamodb`, plus DescribeEndpoints and DescribeLimits,
// which have no constrained input and so are not listed). Frozen so a refresh that adds an
// operation fails this test instead of answering InvalidAction.
var modelOperations = []string{
	"BatchExecuteStatement", "BatchGetItem", "BatchWriteItem", "CreateBackup", "CreateGlobalTable",
	"CreateTable", "DeleteBackup", "DeleteItem", "DeleteResourcePolicy", "DeleteTable",
	"DescribeBackup", "DescribeContinuousBackups", "DescribeContributorInsights", "DescribeEndpoints", "DescribeExport",
	"DescribeGlobalTable", "DescribeGlobalTableSettings", "DescribeImport",
	"DescribeKinesisStreamingDestination", "DescribeLimits", "DescribeTable", "DescribeTableReplicaAutoScaling",
	"DescribeTimeToLive", "DisableKinesisStreamingDestination", "EnableKinesisStreamingDestination",
	"ExecuteStatement", "ExecuteTransaction", "ExportTableToPointInTime", "GetItem",
	"GetResourcePolicy", "ImportTable", "ListBackups", "ListContributorInsights", "ListExports",
	"ListGlobalTables", "ListImports", "ListTables", "ListTagsOfResource", "PutItem",
	"PutResourcePolicy", "Query", "RestoreTableFromBackup", "RestoreTableToPointInTime", "Scan",
	"SearchVectors", "TagResource", "TransactGetItems", "TransactWriteItems", "UntagResource",
	"UpdateContinuousBackups", "UpdateContributorInsights", "UpdateGlobalTable",
	"UpdateGlobalTableSettings", "UpdateItem", "UpdateKinesisStreamingDestination", "UpdateTable",
	"UpdateTableReplicaAutoScaling", "UpdateTimeToLive",
}

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	if len(modelOperations) != 58 {
		t.Fatalf("the frozen list has %d operations; the model documents 58", len(modelOperations))
	}
	for _, op := range modelOperations {
		_, handled := handlers[op]
		_, refused := stubActions[op]
		switch {
		case handled && refused:
			t.Errorf("%s is both handled and refused", op)
		case !handled && !refused:
			t.Errorf("%s is in the service model but neither handled nor refused — it would answer InvalidAction", op)
		}
	}
	known := map[string]bool{}
	for _, op := range modelOperations {
		known[op] = true
	}
	for op := range handlers {
		if !known[op] {
			t.Errorf("%s is handled but not in the frozen model list", op)
		}
	}
	for op := range stubActions {
		if !known[op] {
			t.Errorf("%s is refused but not in the frozen model list", op)
		}
	}
}
