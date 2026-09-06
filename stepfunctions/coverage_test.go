package stepfunctions

// Dispatch coverage: every operation the service model documents is either
// handled, staged with a reason, or refused with a reason. Nothing falls
// through to InvalidAction — a caller must always be able to tell a staged
// gap from a typo.

import "testing"

// modelOperations is the full operation list of com.amazonaws.sfn (37 ops,
// from .audit-models/sfn.json). Frozen here so a future model refresh that
// adds an operation fails this test instead of silently answering
// InvalidAction.
var modelOperations = []string{
	"CreateActivity", "CreateStateMachine", "CreateStateMachineAlias",
	"DeleteActivity", "DeleteStateMachine", "DeleteStateMachineAlias",
	"DeleteStateMachineVersion", "DescribeActivity", "DescribeExecution",
	"DescribeMapRun", "DescribeStateMachine", "DescribeStateMachineAlias",
	"DescribeStateMachineForExecution", "GetActivityTask", "GetExecutionHistory",
	"ListActivities", "ListExecutions", "ListMapRuns", "ListStateMachineAliases",
	"ListStateMachineVersions", "ListStateMachines", "ListTagsForResource",
	"PublishStateMachineVersion", "RedriveExecution", "SendTaskFailure",
	"SendTaskHeartbeat", "SendTaskSuccess", "StartExecution", "StartSyncExecution",
	"StopExecution", "TagResource", "TestState", "UntagResource", "UpdateMapRun",
	"UpdateStateMachine", "UpdateStateMachineAlias", "ValidateStateMachineDefinition",
}

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	if len(modelOperations) != 37 {
		t.Fatalf("the frozen list has %d operations; the sfn model documents 37", len(modelOperations))
	}
	for _, op := range modelOperations {
		_, handled := handlers[op]
		_, staged := notYet[op]
		_, stubbed := stubActions[op]
		n := 0
		for _, hit := range []bool{handled, staged, stubbed} {
			if hit {
				n++
			}
		}
		switch n {
		case 0:
			t.Errorf("%s is in the service model but not in handlers, notYet or stubActions — it would answer InvalidAction, which reads as a typo", op)
		case 1:
		default:
			t.Errorf("%s appears in more than one dispatch table", op)
		}
	}
	for op := range notYet {
		if _, handled := handlers[op]; handled {
			t.Errorf("%s is both handled and listed notYet", op)
		}
	}
}
