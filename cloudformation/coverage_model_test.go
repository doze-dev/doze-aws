package cloudformation

// Dispatch coverage against testdata/ops_cloudformation.json.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_cloudformation.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			{Name: "handlers", Ops: dozetest.Names(handlers)},
			{Name: "stubActions", Ops: dozetest.Names(stubActions)},
		},
	})
	t.Logf("%d operations in the cloudformation model, all accounted for", len(ops))
}
