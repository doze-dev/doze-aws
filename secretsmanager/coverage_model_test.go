package secretsmanager

// Dispatch coverage: every operation the Secrets Manager model documents
// reaches a handler. See testdata/ops_secrets-manager.json.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_secrets-manager.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops:    ops,
		Tables: []dozetest.Table{{Name: "handlers", Ops: dozetest.Names(handlers)}},
	})
	t.Logf("%d operations in the secrets-manager model, all accounted for", len(ops))
}
