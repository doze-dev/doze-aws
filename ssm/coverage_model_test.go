package ssm

// Dispatch coverage against testdata/ops_ssm.json.
//
// SSM has the largest model of any service here — 152 operations — and
// doze-aws implements Parameter Store. The rest is maintenance windows, patch
// baselines, OpsItems, Cloud Connectors, node management and the document
// lifecycle, none of which has anything to act on locally.
//
// What this test records is that the refusal is INCONSISTENT. `fleetOps`
// refuses the managed-instance family by name, with what it would need; the
// other forty-nine answer a generic InvalidAction, which tells a caller their
// action was unrecognised rather than that doze-aws does not serve it. Those
// are different messages and only one of them is true.
//
// Listing them is not an endorsement. It is the burn-down list: the gap cannot
// grow without this failing, and every name moved into a by-name refusal makes
// it shorter.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_ssm.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			{Name: "handlers", Ops: dozetest.Names(handlers)},
			{Name: "fleetOps", Ops: dozetest.Names(fleetOps)},
		},
		Unreached: []string{
			// Maintenance windows and their executions.
			"CancelMaintenanceWindowExecution", "DescribeMaintenanceWindowExecutionTaskInvocations",
			"DescribeMaintenanceWindowExecutionTasks", "DescribeMaintenanceWindowSchedule",
			"DescribeMaintenanceWindowTargets", "DescribeMaintenanceWindowTasks",
			"DescribeMaintenanceWindowsForTarget", "GetMaintenanceWindowExecution",
			"GetMaintenanceWindowExecutionTask", "GetMaintenanceWindowExecutionTaskInvocation",
			"GetMaintenanceWindowTask", "UpdateMaintenanceWindowTarget", "UpdateMaintenanceWindowTask",
			// Patching: there are no instances to patch.
			"DescribeAvailablePatches", "DescribeEffectivePatchesForPatchBaseline",
			"DescribeInstancePatchStatesForPatchGroup", "DescribePatchGroupState",
			"DescribePatchProperties", "GetDefaultPatchBaseline",
			"GetDeployablePatchSnapshotForInstance", "GetPatchBaselineForPatchGroup",
			"RegisterDefaultPatchBaseline",
			// OpsCenter.
			"AssociateOpsItemRelatedItem", "DeleteOpsItem", "DisassociateOpsItemRelatedItem",
			"ListOpsItemEvents", "ListOpsItemRelatedItems",
			// Cloud Connectors and node management.
			"CreateCloudConnector", "DeleteCloudConnector", "GetCloudConnector",
			"ListCloudConnectors", "UpdateCloudConnector", "ValidateCloudConnector",
			"ListNodes", "ListNodesSummary",
			// Associations, inventory, documents, change requests.
			"DescribeAssociationExecutionTargets", "DescribeAssociationExecutions",
			"DescribeInventoryDeletions", "ListDocumentMetadataHistory",
			"StartChangeRequestExecution", "GetExecutionPreview", "StartExecutionPreview",
			// Resource policies, calendars and access.
			"DeleteResourcePolicy", "GetResourcePolicies", "PutResourcePolicy",
			"GetCalendarState", "GetAccessToken", "StartAccessRequest", "GetConnectionStatus",
		},
	})
	t.Logf("%d operations in the ssm model, %d refused only by a generic InvalidAction",
		len(ops), 49)
}
