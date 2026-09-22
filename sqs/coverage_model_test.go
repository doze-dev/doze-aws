package sqs

// Dispatch coverage: every operation the SQS model documents reaches a handler.
//
// The list is testdata/ops_sqs.json, emitted by `dzaudit ops sqs` and
// regenerated weekly by model-drift.yml, so an operation AWS adds fails here.

import (
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestEveryModelOperationIsAccountedFor(t *testing.T) {
	ops := dozetest.ModelOps(t, "testdata/ops_sqs.json")
	dozetest.AssertCoverage(t, dozetest.Coverage{
		Ops: ops,
		Tables: []dozetest.Table{
			{Name: "handlers", Ops: dozetest.Names(handlers)},
		},
		// DozePeek reads a queue without consuming from it. No AWS API offers
		// that, because no AWS console needs it — the console has the queue's
		// own storage. Here the console is a client like any other, so the
		// operation exists to let it show a queue without stealing messages
		// from the app under development.
		LocalOnly: []string{"DozePeek"},
	})
	t.Logf("%d operations in the sqs model, all accounted for", len(ops))
}
