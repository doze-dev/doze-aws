package eventbridge

// Dispatch coverage: every operation the service model documents is either
// handled or refused by name. Nothing falls through to InvalidAction, which
// reads to a caller like a typo in their own code rather than a gap in this one.
//
// The list used to be fifty-seven operation names typed into this file and
// pinned by an exact count. `testdata/ops_eventbridge.json` holds it now,
// emitted by `dzaudit ops eventbridge` and regenerated weekly by
// model-drift.yml, so an operation AWS adds fails here on the next run rather
// than on the next occasion somebody re-runs the tool.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_eventbridge.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			dozetest.Table{Name: "handlers", Ops: dozetest.Names(handlers)},
			dozetest.Table{Name: "stubActions", Ops: dozetest.Names(stubActions)},
		},
	})
	// A refusal with no reason is a dead end: the caller learns the operation
	// is not here and nothing about what it would take. Kept from the
	// hand-written version, because it is the only rule here the shared helper
	// does not express.
	for op, reason := range stubActions {
		if reason == "" {
			t.Errorf("%s is refused with no reason", op)
		}
	}
	t.Logf("%d operations in the eventbridge model, all accounted for", len(ops))
}
