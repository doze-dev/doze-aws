package console_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

// The rail counts what somebody made. The default bus is there on a fresh stack
// and nobody made it, so counting it said "1" beside EventBridge where the
// first-run board (Wire-Empty) draws none, and a stack with a rule on the
// default bus said 2 for one thing. A rule counts, and so does a bus other than
// the default one: the same figure the map's glance gives (glanceEventBridge),
// so the rail and the map cannot disagree.
func TestTheRailCountsTheBusesAndRulesSomebodyMade(t *testing.T) {
	c, rec, _ := chromeConsole(t, iam.ModeSoft, awsident.Default())

	count := func() (int, string) {
		var counts map[string]json.RawMessage
		body := req(t, c, "GET", "/_console/api/counts", nil).Body.Bytes()
		if err := json.Unmarshal(body, &counts); err != nil {
			t.Fatalf("the counts are not JSON: %v\n%s", err, body)
		}
		var n int
		if err := json.Unmarshal(counts["eb"], &n); err != nil {
			t.Fatalf("no EventBridge count in %s", body)
		}
		// The rail the page draws says the same number, or nothing for zero.
		m := regexp.MustCompile(`<span class="ri-ct" id="ct-eb"[^>]*>([^<]*)</span>`).FindStringSubmatch(wire(t, c))
		if m == nil {
			t.Fatal("the rail has no EventBridge count slot")
		}
		return n, m[1]
	}

	if n, shown := count(); n != 0 || shown != "" {
		t.Errorf("a fresh stack counts %d (rail %q) for EventBridge: only the default bus is there and nobody made it", n, shown)
	}

	send(rec, "AWSEvents.PutRule", "application/x-amz-json-1.1", `{"Name":"nightly","ScheduleExpression":"rate(1 day)"}`)
	if n, shown := count(); n != 1 || shown != "1" {
		t.Errorf("one rule on the default bus counts %d (rail %q), want 1", n, shown)
	}

	send(rec, "AWSEvents.CreateEventBus", "application/x-amz-json-1.1", `{"Name":"billing"}`)
	if n, shown := count(); n != 2 || shown != "2" {
		t.Errorf("a rule and a bus other than the default count %d (rail %q), want 2", n, shown)
	}

	send(rec, "AWSEvents.PutRule", "application/x-amz-json-1.1", `{"Name":"invoices","EventBusName":"billing","EventPattern":"{\"source\":[\"shop\"]}"}`)
	if n, shown := count(); n != 3 || shown != "3" {
		t.Errorf("a rule on that bus counts %d (rail %q), want 3: two rules and the bus", n, shown)
	}
}
