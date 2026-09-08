package console_test

import (
	"net/url"
	"strings"
	"testing"
)

// The API destinations page: create a connection and a destination, read
// both back, update, deauthorize, delete — every one of the eleven
// operations reached through the console, and no secret in any response.
func TestConsoleEventBridgeDestinations(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/eb/destinations", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Create connection") {
		t.Fatalf("destinations page: %d\n%s", page.Code, page.Body)
	}
	conn := req(t, h, "POST", "/_console/eb/destinations/create-connection", url.Values{
		"name": {"partner"}, "auth_type": {"API_KEY"}, "api_key_name": {"X-Api-Key"}, "api_key_value": {"hunter2"},
		"headers": {"X-Src=doze\n!X-Token=shh"},
	})
	if conn.Code != 200 || !strings.Contains(conn.Body.String(), "partner") {
		t.Fatalf("create connection: %d\n%s", conn.Code, conn.Body)
	}
	detail := req(t, h, "GET", "/_console/eb/destinations/connection/partner", nil)
	body := detail.Body.String()
	if detail.Code != 200 || !strings.Contains(body, "X-Api-Key") || !strings.Contains(body, "X-Src=doze") || !strings.Contains(body, "events!connection/partner") {
		t.Fatalf("connection detail: %d\n%s", detail.Code, body)
	}
	if strings.Contains(body, "hunter2") || strings.Contains(body, "shh") {
		t.Fatalf("a secret leaked into the connection detail:\n%s", body)
	}

	// The destination's connection select carries the connection's ARN.
	arn := body[strings.Index(body, "arn:aws:events:"):]
	arn = arn[:strings.IndexAny(arn, "<\"")]
	dest := req(t, h, "POST", "/_console/eb/destinations/create-destination", url.Values{
		"name": {"hook"}, "connection": {arn}, "endpoint": {"http://127.0.0.1:1/hooks/*"}, "method": {"POST"}, "rate": {"3"},
	})
	if dest.Code != 200 || !strings.Contains(dest.Body.String(), "hook") || !strings.Contains(dest.Body.String(), "ACTIVE") {
		t.Fatalf("create destination: %d\n%s", dest.Code, dest.Body)
	}
	dd := req(t, h, "GET", "/_console/eb/destinations/destination/hook", nil)
	if dd.Code != 200 || !strings.Contains(dd.Body.String(), "/hooks/*") {
		t.Fatalf("destination detail: %d\n%s", dd.Code, dd.Body)
	}
	upd := req(t, h, "POST", "/_console/eb/destinations/destination/hook/update", url.Values{
		"connection": {arn}, "endpoint": {"http://127.0.0.1:1/hooks/v2/*"}, "method": {"PUT"}, "description": {"renamed"},
	})
	if upd.Code != 200 || !strings.Contains(upd.Body.String(), "/hooks/v2/*") || !strings.Contains(upd.Body.String(), "renamed") {
		t.Fatalf("update destination: %d\n%s", upd.Code, upd.Body)
	}

	// The rule page offers the destination as a target.
	create(t, h, "/_console/eb/default/create-rule", url.Values{"name": {"to-hook"}, "pattern": {`{"source":["x"]}`}})
	rule := req(t, h, "GET", "/_console/eb/default/rule/to-hook", nil)
	if !strings.Contains(rule.Body.String(), "API destination · hook") {
		t.Fatalf("rule page does not offer the destination as a target:\n%s", rule.Body)
	}

	// Update re-authorizes; deauthorize drops the credential.
	cu := req(t, h, "POST", "/_console/eb/destinations/connection/partner/update", url.Values{
		"description": {"partner api"}, "auth_type": {"BASIC"}, "username": {"u"}, "password": {"p"},
	})
	if cu.Code != 200 || !strings.Contains(cu.Body.String(), "BASIC") || !strings.Contains(cu.Body.String(), "partner api") {
		t.Fatalf("update connection: %d\n%s", cu.Code, cu.Body)
	}
	de := req(t, h, "POST", "/_console/eb/destinations/connection/partner/deauthorize", nil)
	if de.Code != 200 || !strings.Contains(de.Body.String(), "DEAUTHORIZED") {
		t.Fatalf("deauthorize: %d\n%s", de.Code, de.Body)
	}

	// Deleting the connection leaves the destination INACTIVE; then delete it.
	dc := req(t, h, "POST", "/_console/eb/destinations/delete-connection", url.Values{"name": {"partner"}})
	if dc.Code != 200 || strings.Contains(dc.Body.String(), ">partner<") || !strings.Contains(dc.Body.String(), "INACTIVE") {
		t.Fatalf("delete connection: %d\n%s", dc.Code, dc.Body)
	}
	dx := req(t, h, "POST", "/_console/eb/destinations/delete-destination", url.Values{"name": {"hook"}})
	if dx.Code != 200 || strings.Contains(dx.Body.String(), ">hook<") {
		t.Fatalf("delete destination: %d\n%s", dx.Code, dx.Body)
	}
}
