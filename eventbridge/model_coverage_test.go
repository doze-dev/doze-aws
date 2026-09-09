package eventbridge

// Dispatch coverage: every operation the model documents is either handled or
// refused with a reason. Nothing falls through to InvalidAction.
//
// logs, dynamodb and stepfunctions each have this test. EventBridge did not,
// so a model refresh that added an operation would answer InvalidAction —
// which reads to a developer like a typo in their own call rather than a gap
// in the emulator — and nothing would notice. The same hole exists in
// kinesis, iam and cloudformation; this is the shape to copy.

import "testing"

// modelOperations is the full operation list of com.amazonaws.eventbridge
// (57 ops, from `dzaudit list eventbridge`). Frozen so a model refresh that
// adds an operation fails this test instead of answering InvalidAction.
var modelOperations = []string{
	"ActivateEventSource", "CancelReplay", "CreateApiDestination", "CreateArchive", "CreateConnection",
	"CreateEndpoint", "CreateEventBus", "CreatePartnerEventSource", "DeactivateEventSource",
	"DeauthorizeConnection", "DeleteApiDestination", "DeleteArchive", "DeleteConnection", "DeleteEndpoint",
	"DeleteEventBus", "DeletePartnerEventSource", "DeleteRule", "DescribeApiDestination", "DescribeArchive",
	"DescribeConnection", "DescribeEndpoint", "DescribeEventBus", "DescribeEventSource",
	"DescribePartnerEventSource", "DescribeReplay", "DescribeRule", "DisableRule", "EnableRule",
	"ListApiDestinations", "ListArchives", "ListConnections", "ListEndpoints", "ListEventBuses",
	"ListEventSources", "ListPartnerEventSourceAccounts", "ListPartnerEventSources", "ListReplays",
	"ListRuleNamesByTarget", "ListRules", "ListTagsForResource", "ListTargetsByRule", "PutEvents",
	"PutPartnerEvents", "PutPermission", "PutRule", "PutTargets", "RemovePermission", "RemoveTargets",
	"StartReplay", "TagResource", "TestEventPattern", "UntagResource", "UpdateApiDestination",
	"UpdateArchive", "UpdateConnection", "UpdateEndpoint", "UpdateEventBus",
}

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	if len(modelOperations) != 57 {
		t.Fatalf("the frozen list has %d operations; the model documents 57", len(modelOperations))
	}
	inModel := map[string]bool{}
	for _, op := range modelOperations {
		inModel[op] = true
	}

	for _, op := range modelOperations {
		_, handled := handlers[op]
		reason, stubbed := stubActions[op]
		switch {
		case handled && stubbed:
			t.Errorf("%s is both handled and refused", op)
		case !handled && !stubbed:
			t.Errorf("%s is neither handled nor refused — it answers InvalidAction, "+
				"which reads like the caller's typo rather than a gap here", op)
		case stubbed && reason == "":
			t.Errorf("%s is refused with no reason", op)
		}
	}

	for op := range handlers {
		if !inModel[op] {
			t.Errorf("handler for %q, which the model does not document — a stale name or a typo", op)
		}
	}
	for op := range stubActions {
		if !inModel[op] {
			t.Errorf("refusal for %q, which the model does not document", op)
		}
	}
}
