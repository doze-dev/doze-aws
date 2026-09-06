package console_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// putLogs writes lines through the gateway the way Lambda does, so the
// console pages read what a function would have left.
func putLogs(t *testing.T, gw http.Handler, group, stream string, lines ...string) {
	t.Helper()
	post := func(action, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-amz-json-1.1")
		r.Header.Set("X-Amz-Target", "Logs_20140328."+action)
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, r)
		return w
	}
	post("CreateLogGroup", `{"logGroupName":"`+group+`"}`)
	var events []string
	for i, l := range lines {
		events = append(events, `{"timestamp":`+itoa(time.Now().UnixMilli()-1000+int64(i)*10)+`,"message":"`+l+`","requestId":"req-`+itoa(int64(i/2))+`"}`)
	}
	if w := post("PutLogEvents", `{"logGroupName":"`+group+`","logStreamName":"`+stream+`","logEvents":[`+strings.Join(events, ",")+`]}`); w.Code != 200 {
		t.Fatalf("PutLogEvents = %d: %s", w.Code, w.Body.String())
	}
}

func itoa(n int64) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestConsoleLogsPages(t *testing.T) {
	h, gw := newConsoleStack(t)

	// Empty: the service page says where groups come from.
	home := req(t, h, "GET", "/_console/logs", nil).Body.String()
	if !strings.Contains(home, "No log groups yet") {
		t.Errorf("empty logs page:\n%s", truncateBody(home))
	}

	putLogs(t, gw, "/aws/lambda/orders", "2026/09/07/[$LATEST]abc", "START RequestId: req-0", "hello from 0", "START RequestId: req-1", "[ERROR] boom")

	// The rail and the list pane count the group; the group page tails it.
	home = req(t, h, "GET", "/_console/logs", nil).Body.String()
	if !strings.Contains(home, "/aws/lambda/orders") {
		t.Errorf("the group is not listed:\n%s", truncateBody(home))
	}
	page := req(t, h, "GET", "/_console/logs/group?name="+url.QueryEscape("/aws/lambda/orders"), nil).Body.String()
	for _, want := range []string{`id="log-tail"`, "hello from 0", "[ERROR] boom", "log-err", "data-live=", `aws logs tail /aws/lambda/orders --follow`} {
		if !strings.Contains(page, want) {
			t.Errorf("group page lacks %q:\n%s", want, truncateBody(page))
		}
	}

	// The tail partial: a 204 to its own hash, one invocation on ?rid=, a
	// filter pattern on ?q=.
	tail := req(t, h, "GET", "/_console/logs/tail?name="+url.QueryEscape("/aws/lambda/orders"), nil)
	hash := tail.Header().Get("HX-Live-Hash")
	if hash == "" {
		t.Fatal("the tail partial should answer HX-Live-Hash")
	}
	if again := req(t, h, "GET", "/_console/logs/tail?name="+url.QueryEscape("/aws/lambda/orders")+"&h="+hash, nil); again.Code != http.StatusNoContent {
		t.Errorf("an unchanged tail should be 204, got %d", again.Code)
	}
	one := req(t, h, "GET", "/_console/logs/tail?name="+url.QueryEscape("/aws/lambda/orders")+"&rid=req-1", nil).Body.String()
	if strings.Contains(one, "hello from 0") || !strings.Contains(one, "[ERROR] boom") {
		t.Errorf("?rid= should show one invocation only:\n%s", truncateBody(one))
	}
	filtered := req(t, h, "GET", "/_console/logs/tail?name="+url.QueryEscape("/aws/lambda/orders")+"&q=hello", nil).Body.String()
	if !strings.Contains(filtered, "hello from 0") || strings.Contains(filtered, "boom") {
		t.Errorf("?q= should apply the filter pattern:\n%s", truncateBody(filtered))
	}

	// Settings: retention round-trips; streams list the process stream.
	req(t, h, "POST", "/_console/logs/retention", url.Values{"name": {"/aws/lambda/orders"}, "days": {"7"}})
	settings := req(t, h, "GET", "/_console/logs/group?name="+url.QueryEscape("/aws/lambda/orders")+"&tab=settings", nil).Body.String()
	if !strings.Contains(settings, `value="7" selected`) {
		t.Errorf("retention should read back as 7 days:\n%s", truncateBody(settings))
	}
	streams := req(t, h, "GET", "/_console/logs/group?name="+url.QueryEscape("/aws/lambda/orders")+"&tab=streams", nil).Body.String()
	if !strings.Contains(streams, "2026/09/07/[$LATEST]abc") {
		t.Errorf("the stream should be listed:\n%s", truncateBody(streams))
	}

	// The Lambda page's Logs tab reads the same group (the function need
	// not exist for the partial: the tail is the group's).
	tab := req(t, h, "GET", "/_console/lambda/orders/logs?rid=req-0", nil).Body.String()
	if !strings.Contains(tab, "hello from 0") || strings.Contains(tab, "boom") {
		t.Errorf("the Lambda logs partial should read /aws/lambda/orders for one invocation:\n%s", truncateBody(tab))
	}

	// Delete takes the group with it.
	req(t, h, "POST", "/_console/logs/delete", url.Values{"name": {"/aws/lambda/orders"}})
	if home := req(t, h, "GET", "/_console/logs", nil).Body.String(); strings.Contains(home, "/aws/lambda/orders") {
		t.Errorf("the group should be gone")
	}
}

func truncateBody(s string) string {
	if len(s) > 3000 {
		return s[:3000] + "…"
	}
	return s
}
