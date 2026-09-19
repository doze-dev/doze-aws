package kinesis

// Dispatch coverage against testdata/ops_kinesis.json.
//
// This one found something the day it landed: AWS added a Channels family to
// Kinesis and doze-aws has neither handlers nor refusals for it, so those five
// operations answer a generic InvalidAction — indistinguishable, from the
// caller's side, from having misspelled the action. Written down below rather
// than quietly fixed or quietly ignored.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_kinesis.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			{Name: "handlers", Ops: dozetest.Names(handlers)},
			{Name: "stubActions", Ops: dozetest.Names(stubActions)},
		},
		// The Channels family, added to the model after this service was
		// written. They should be refused by name with what they would need;
		// until then the gap is here so it cannot grow unnoticed.
		Unreached: []string{
			"CreateChannel", "DeleteChannel", "DescribeChannel",
			"ListChannels", "UpdateChannel",
		},
	})
	t.Logf("%d operations in the kinesis model", len(ops))
}
