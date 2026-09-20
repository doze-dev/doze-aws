package console_test

// The console under a non-default account.
//
// Every other test here builds a console with no Identity, so every one of them
// runs under the default account — and that is precisely why this went
// unnoticed: the console held an Identity, passed it to its backend, and then
// built ARNs with awsident.Default().ARN(), which ignores it and uses the package
// defaults. Under `doze-aws --account-id 123456789012` the SQS service reported
// arn:aws:sqs:us-east-1:123456789012:q and the console rendered
// arn:aws:sqs:us-east-1:000000000000:q for the same queue.
//
// The rule this pins is not "ARNs are built a particular way". It is that the
// console and the service agree, whatever the account is.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/peers"
)

// otherAccount is deliberately not awsident.AccountID, and is all-distinct
// digits so a partial match cannot look like a pass.
const otherAccount = "123456789012"

func consoleUnderAccount(t *testing.T, account string) http.Handler {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	id := awsident.Identity{AccountID: account}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Identity: id, Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	c, err := console.New(console.Options{Peers: peers.InProcess(stack.Service), Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTheConsoleRendersTheInstancesAccountNotTheDefault(t *testing.T) {
	c := consoleUnderAccount(t, otherAccount)

	if w := req(t, c, "POST", "/_console/sqs/create", url.Values{"name": {"acctcheck"}}); w.Code >= 400 {
		t.Fatalf("creating the queue: %d %s", w.Code, w.Body.String())
	}
	body := req(t, c, "GET", "/_console/sqs/acctcheck", nil).Body.String()

	if !strings.Contains(body, otherAccount) {
		t.Errorf("the queue page names no ARN under account %s.\n"+
			"  The console holds the instance's Identity; anything it builds from "+
			"the package\n  defaults instead is wrong for every instance started "+
			"with --account-id.", otherAccount)
	}
	if strings.Contains(body, awsident.AccountID) {
		t.Errorf("the queue page still shows the DEFAULT account %s while the "+
			"instance runs as %s.\n  That is what a user sees: an ARN they cannot "+
			"paste anywhere, differing from\n  the one the service itself reports.",
			awsident.AccountID, otherAccount)
	}
}
