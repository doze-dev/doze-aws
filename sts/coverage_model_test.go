package sts

// Every operation AWS's STS model documents is either dispatched here or a
// listed gap, checked against the committed ops_sts.json that
// .github/workflows/model-drift.yml regenerates weekly.
//
// STS has no refusal table: an action with no handler answers InvalidAction,
// which reads to a caller like a typo in their own code rather than a gap in
// this one. That makes the Unreached list below the only place the gap is
// recorded, and frozen in both directions is what stops AWS adding an
// operation nobody notices.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_sts.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops:    ops,
		Tables: []dozetest.Table{{Name: "handlers", Ops: dozetest.Names(handlers)}},
		Unreached: []string{
			// Both mint a token for a federated identity that has to come from
			// a real identity provider. docs/SUPPORT.md carries the
			// argument; there is nothing local for either to assert about.
			"GetDelegatedAccessToken",
			"GetWebIdentityToken",
		},
	})
	t.Logf("%d operations in the sts model", len(ops))
}
