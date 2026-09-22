package logs

// Dispatch coverage: every operation the service model documents is either
// handled or refused by name with what it would need. Nothing falls through to
// InvalidAction, which reads to a caller like a typo in their own code rather
// than a gap in this one.
//
// The list used to be typed into this file and pinned by an exact count.
// `testdata/ops_cloudwatch-logs.json` holds it now — emitted by `dzaudit ops`, regenerated
// weekly by model-drift.yml — so an operation AWS adds fails here on the next
// run instead of on the next occasion somebody re-runs the tool by hand.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_cloudwatch-logs.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			dozetest.Table{Name: "handlers", Ops: dozetest.Names(handlers)},
			dozetest.Table{Name: "notHere", Ops: dozetest.Names(notHere)},
		},
	})
	t.Logf("%d operations in the model, all accounted for", len(ops))
}
