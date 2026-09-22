package dynamodb

// Dispatch coverage: every operation the service model documents is either
// handled or refused by name. Nothing falls through to InvalidAction, which
// reads to a caller like a typo in their own code rather than a gap in this one.
//
// The list used to be fifty-eight operation names typed into this file and
// pinned by an exact count. `testdata/ops_dynamodb.json` holds it now, emitted
// by `dzaudit ops dynamodb` and regenerated weekly by model-drift.yml.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_dynamodb.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			dozetest.Table{Name: "handlers", Ops: dozetest.Names(handlers)},
			dozetest.Table{Name: "stubActions", Ops: dozetest.Names(stubActions)},
		},
	})
	t.Logf("%d operations in the dynamodb model, all accounted for", len(ops))
}
