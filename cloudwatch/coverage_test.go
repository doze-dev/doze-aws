package cloudwatch

// Dispatch coverage: every operation the service model documents is either
// handled or refused with a reason. Nothing falls through to InvalidAction,
// which reads to a caller like a typo rather than like a boundary.

import "testing"

// modelOperations is the full operation list of
// com.amazonaws.cloudwatch#GraniteServiceVersion20100801 (50 ops, from
// `dzaudit list cloudwatch`). Frozen so a model refresh that adds an
// operation fails this test instead of quietly answering InvalidAction —
// which matters more here than elsewhere, since this model is actively
// growing: alarm mute rules, OTel enrichment and datasets all postdate the
// plan that scoped this service.
var modelOperations = []string{
	"AssociateDatasetKmsKey", "DeleteAlarmMuteRule", "DeleteAlarms", "DeleteAnomalyDetector",
	"DeleteDashboards", "DeleteInsightRules", "DeleteMetricStream", "DescribeAlarmContributors",
	"DescribeAlarmHistory", "DescribeAlarms", "DescribeAlarmsForMetric", "DescribeAnomalyDetectors",
	"DescribeInsightRules", "DisableAlarmActions", "DisableInsightRules", "DisassociateDatasetKmsKey",
	"EnableAlarmActions", "EnableInsightRules", "GetAlarmMuteRule", "GetDashboard",
	"GetDataset", "GetInsightRuleReport", "GetMetricData", "GetMetricStatistics",
	"GetMetricStream", "GetMetricWidgetImage", "GetOTelEnrichment", "ListAlarmMuteRules",
	"ListDashboards", "ListManagedInsightRules", "ListMetrics", "ListMetricStreams",
	"ListTagsForResource", "PutAlarmMuteRule", "PutAnomalyDetector", "PutCompositeAlarm",
	"PutDashboard", "PutInsightRule", "PutLogAlarm", "PutManagedInsightRules",
	"PutMetricAlarm", "PutMetricData", "PutMetricStream", "SetAlarmState",
	"StartMetricStreams", "StartOTelEnrichment", "StopMetricStreams", "StopOTelEnrichment",
	"TagResource", "UntagResource",
}

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	if len(modelOperations) != 50 {
		t.Fatalf("the frozen list has %d operations; the model documents 50", len(modelOperations))
	}
	for _, op := range modelOperations {
		_, handled := handlers[op]
		_, refused := notHere[op]
		switch {
		case handled && refused:
			t.Errorf("%s is both handled and refused", op)
		case !handled && !refused:
			t.Errorf("%s is in the service model but neither handled nor refused — "+
				"it would answer InvalidAction, which reads as a typo", op)
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
	for op := range notHere {
		if !known[op] {
			t.Errorf("%s is refused but not in the frozen model list", op)
		}
	}
}

// A refusal with no reason is worse than no refusal: it tells a caller to stop
// without telling them whether to work around it.
func TestEveryRefusalCarriesAReason(t *testing.T) {
	for op, why := range notHere {
		if why == "" {
			t.Errorf("%s is refused with no reason", op)
		}
	}
}

// Every operation with a handler needs a constraint table, or its inputs go
// unchecked while the ledger implies otherwise. The reverse is also an error:
// a table for an operation nothing dispatches is dead weight that reads as
// coverage.
func TestHandledOperationsAreValidated(t *testing.T) {
	for op := range handlers {
		if _, ok := constraintTables[op]; !ok {
			t.Errorf("%s is handled but has no constraint table", op)
		}
	}
	for op := range constraintTables {
		if _, ok := handlers[op]; !ok {
			t.Errorf("%s has a constraint table but no handler", op)
		}
	}
}

// The legacy error codes are only useful if they are reachable: an entry for a
// code nothing ever answers with is a claim about compatibility that is not
// being kept. This checks the shape rather than the reachability — a Fault
// name with no legacy spelling would render an empty header.
func TestLegacyCodeTableIsWellFormed(t *testing.T) {
	for modern, e := range queryCodes {
		if modern == "" || e.Legacy == "" {
			t.Errorf("queryCodes has an empty spelling: %q -> %q", modern, e.Legacy)
		}
		if e.Status < 400 || e.Status > 599 {
			t.Errorf("%s has status %d, which is not an error status", modern, e.Status)
		}
	}
	// The two modelcheck answers must be mapped, or a Query client asking for
	// compatible errors gets a code no old SDK ever saw.
	for _, code := range []string{"ValidationException", "ValidationError"} {
		if legacyCode(code) == "" {
			t.Errorf("%s has no legacy spelling", code)
		}
	}
}
