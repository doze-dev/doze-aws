package console_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/peers"
)

// The wire under a resource is that resource's calls and nobody else's: the
// app's traffic to this queue, newest first, with a refusal reading as one.
func TestTheWireUnderAResourceIsThatResourcesCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	rec := console.NewRecorder(stack.Handler(), awsident.Default())
	c, err := console.New(console.Options{Peers: peers.InProcess(stack.Service), Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	call := func(target, body string) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("X-Amz-Target", "AmazonSQS."+target)
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		rec.ServeHTTP(httptest.NewRecorder(), r)
	}
	call("CreateQueue", `{"QueueName":"orders"}`)
	call("CreateQueue", `{"QueueName":"other"}`)
	call("SendMessage", `{"QueueUrl":"http://x/000000000000/orders","MessageBody":"one"}`)
	call("SendMessage", `{"QueueUrl":"http://x/000000000000/other","MessageBody":"not ours"}`)
	// Refused for a missing body. (A delay of 9999 would have been the natural
	// refusal to use, and SQS accepts it: SQS-10.)
	call("SendMessage", `{"QueueUrl":"http://x/000000000000/orders"}`)

	dock := req(t, c, "GET", "/_console/traffic/for?svc=sqs&res=orders&noun=queue", nil).Body.String()
	if n := strings.Count(dock, `class="rw-row`); n != 3 {
		t.Fatalf("want the three calls to orders (create and two sends), drew %d:\n%s", n, dock)
	}
	if strings.Count(dock, "SendMessage") != 2 || !strings.Contains(dock, "CreateQueue") {
		t.Errorf("the wrong calls:\n%s", dock)
	}
	if !strings.Contains(dock, `class="rw-row refused"`) {
		t.Errorf("the send with no body should read as refused:\n%s", dock)
	}
	if !strings.Contains(dock, "this queue") {
		t.Errorf("the dock does not name what it is the wire of")
	}

	// The console's own reading of the queue is not the app's traffic.
	req(t, c, "GET", "/_console/sqs/orders", nil)
	again := req(t, c, "GET", "/_console/traffic/for?svc=sqs&res=orders&noun=queue", nil).Body.String()
	if n := strings.Count(again, `class="rw-row`); n != 3 {
		t.Errorf("opening the queue's page added %d rows to its wire", n-3)
	}

	// A queue nothing has called says so, and the page carries the slot.
	none := req(t, c, "GET", "/_console/traffic/for?svc=sqs&res=nobody&noun=queue", nil).Body.String()
	if !strings.Contains(none, "Nothing yet") {
		t.Errorf("an uncalled queue's dock:\n%s", none)
	}
	page := req(t, c, "GET", "/_console/sqs/orders", nil).Body.String()
	if !strings.Contains(page, `id="res-wire"`) || !strings.Contains(page, "res=orders") {
		t.Errorf("the queue page has no wire slot")
	}
}
