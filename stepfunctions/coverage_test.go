package stepfunctions

// Dispatch coverage: every operation the service model documents is either
// handled, staged with a reason, or refused with a reason. Nothing falls
// through to InvalidAction — a caller must always be able to tell a staged gap
// from a typo.
//
// The list used to be thirty-seven operation names typed into this file and
// pinned by an exact count. It is `testdata/ops_sfn.json` now, emitted by
// `dzaudit ops sfn` and regenerated weekly by model-drift.yml, so an operation
// AWS adds fails this test on the next run rather than on the next occasion
// somebody thinks to re-run the tool.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_sfn.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			dozetest.Table{Name: "handlers", Ops: dozetest.Names(handlers)},
			dozetest.Table{Name: "notYet", Ops: dozetest.Names(notYet)},
			dozetest.Table{Name: "stubActions", Ops: dozetest.Names(stubActions)},
		},
	})
	t.Logf("%d operations in the sfn model, all accounted for", len(ops))
}
