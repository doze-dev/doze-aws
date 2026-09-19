package iam

// Dispatch coverage against testdata/ops_iam.json.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_iam.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			{Name: "handlers", Ops: dozetest.Names(handlers)},
			{Name: "stubActions", Ops: dozetest.Names(stubActions)},
		},
		// Two doze-aws extensions backing the IAM console panel: the access
		// log the soft mode records, and the least-privilege generator that
		// reads it. Neither has an AWS equivalent because neither question
		// arises when IAM is the real thing.
		LocalOnly: []string{"DozeAccessLog", "DozeGeneratePolicy"},
		// Added to the model after this service was written.
		Unreached: []string{
			"AcquireRole", "GetAccountProperties",
			"GetRoleTemplateVersion", "PutAccountProperties",
		},
	})
	t.Logf("%d operations in the iam model", len(ops))
}
