package sns

// Every operation AWS's SNS model documents reaches a handler, checked against
// the committed ops_sns.json that .github/workflows/model-drift.yml
// regenerates weekly.
//
// SNS registers its refusals INTO dispatch — actions_ext.go's init adds the
// mobile-push, SMS and phone-number surface as handlers that answer a clean
// coded error — so there is one table here rather than two, and "covered"
// means "answers by name" rather than "implemented". That is the distinction
// worth keeping: an action that falls through to InvalidAction reads to a
// caller like a typo in their own code rather than a gap in this one.
//
// There is no Unreached list because there is nothing in it. If AWS adds an
// operation, this test is what says so.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_sns.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops:    ops,
		Tables: []dozetest.Table{{Name: "dispatch", Ops: dozetest.Names(dispatch)}},
		// Answers "if I published this, who would actually receive it, and why
		// not" — a question the console's topic panel asks and no AWS API
		// offers, because no AWS console needs it.
		LocalOnly: []string{"DozeMatchSubscriptions"},
	})
	t.Logf("%d operations in the sns model, all dispatched", len(ops))
}
