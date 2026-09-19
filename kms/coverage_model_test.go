package kms

// Dispatch coverage against testdata/ops_kms.json.
//
// KMS registers its refusals INTO handlers — an init loop wraps each
// unsupported name in a stub that answers UnsupportedOperationException — so
// there is one table here rather than two, and "handled" means "answers by
// name" rather than "implemented".

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_kms.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops:    ops,
		Tables: []dozetest.Table{{Name: "handlers", Ops: dozetest.Names(handlers)}},
		// VerifyMacForImport is in the stub list and no longer in AWS's model.
		// A stub for an operation that does not exist covers nothing; it is
		// left in place because removing a refusal is a wire change, and named
		// here so the discrepancy is recorded rather than puzzling.
		LocalOnly: []string{"VerifyMacForImport"},
		// GetKeyLastUsage was added to the model after this service was
		// written.
		Unreached: []string{"GetKeyLastUsage"},
	})
	t.Logf("%d operations in the kms model", len(ops))
}
