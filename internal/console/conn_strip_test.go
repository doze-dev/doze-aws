package console_test

import (
	"net/url"
	"strings"
	"testing"
)

// The wiring strip states only what the graph knows. The graph holds
// configuration (subscriptions, targets, triggers, notifications) and cannot see
// an SDK client, so a resource nothing in the stack is configured to feed is
// not a resource nothing feeds.
func TestTheStripNeverClaimsNothingFeedsAResource(t *testing.T) {
	h := newConsole(t)
	req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}})
	req(t, h, "POST", "/_console/sns/create", url.Values{"name": {"events"}})
	req(t, h, "POST", "/_console/s3/create", url.Values{"name": {"uploads"}})
	req(t, h, "POST", "/_console/kinesis/create", url.Values{"name": {"clicks"}, "shards": {"1"}})
	req(t, h, "POST", "/_console/eb/default/create-rule", url.Values{"name": {"on-order"}, "pattern": {`{"source":["shop"]}`}})

	for _, page := range []string{
		"/_console/sqs/orders", "/_console/sns/events", "/_console/s3/uploads",
		"/_console/kinesis/clicks", "/_console/eb/default/rule/on-order",
	} {
		t.Run(page, func(t *testing.T) {
			body := req(t, h, "GET", page, nil).Body.String()
			if !strings.Contains(body, `class="conn"`) {
				t.Fatalf("no wiring strip on the page:\n%.400s", body)
			}
			if strings.Contains(body, "nothing feeds this") {
				t.Errorf("the strip claims nothing feeds this resource; the graph cannot know that")
			}
		})
	}
}
