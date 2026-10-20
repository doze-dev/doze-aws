package console_test

import (
	"net/url"
	"strings"
	"testing"
)

// The create form sends the shard count it was given. It used to turn any
// number under 1 into 1 before the call, so a stream of zero shards, which the
// service refuses as the model says, was created with one and the page said
// "Stream created". The form's browser min is not a defence (novalidate is on):
// the refusal has to be the service's, drawn where the control is.
func TestAZeroShardStreamIsRefusedNotMadeIntoOne(t *testing.T) {
	h := newConsole(t)
	for _, shards := range []string{"0", "-3"} {
		rec := req(t, h, "POST", "/_console/kinesis/create", url.Values{"name": {"zeroed"}, "shards": {shards}})
		if rec.Header().Get("HX-Doze-Error") != "1" {
			t.Fatalf("shards=%s: status %d, no refusal drawn:\n%s", shards, rec.Code, clip(rec.Body.String()))
		}
		body := flatText(rec.Body.String())
		if !strings.Contains(body, "Refused ValidationException") || !strings.Contains(body, "at &#39;shardCount&#39; failed to satisfy constraint: Member must have value greater than or equal to 1") {
			t.Errorf("shards=%s: the page does not carry the service's refusal of ShardCount: %s", shards, body)
		}
	}
	page := req(t, h, "GET", "/_console/kinesis/zeroed", nil)
	if page.Code == 200 {
		t.Errorf("a stream named zeroed exists after both refusals")
	}
	// One is still the smallest stream and still works.
	create(t, h, "/_console/kinesis/create", url.Values{"name": {"smallest"}, "shards": {"1"}})
}
