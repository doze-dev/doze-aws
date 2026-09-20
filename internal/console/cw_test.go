package console_test

// The CloudWatch pane, end to end through the console's own handlers: publish
// a metric, find it in the browser, chart it, put an alarm on it, and set the
// alarm's state by hand.
//
// Every page here is rendered, not just requested for its status. A template
// that fails mid-render still answers 200 — html/template has already written
// the head by the time it panics — which is how the Lambda create page shipped
// broken behind a green status sweep. So each assertion looks for something
// only a fully-rendered page contains.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestConsoleCloudWatchFlow(t *testing.T) {
	h, gw := newConsoleStack(t)

	// A metric to watch. Published through the gateway rather than the
	// console: the console deliberately has no form that publishes metrics,
	// because a metric nothing measured would be a lie about what happened.
	putMetric(t, gw, `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts","Value":7,`+
		`"Unit":"Count","Dimensions":[{"Name":"Stage","Value":"prod"}]}]}`)

	// The service page lists the metric under its namespace.
	home := req(t, h, "GET", "/_console/cw", nil)
	if home.Code != 200 {
		t.Fatalf("cw home: %d", home.Code)
	}
	for _, want := range []string{"Shop", "Checkouts", "Stage=prod"} {
		if !strings.Contains(home.Body.String(), want) {
			t.Errorf("the metric browser does not mention %q", want)
		}
	}

	// The chart. The sparkline is a server-rendered polyline, so a rendered
	// chart means real points reached the template — there is no client-side
	// draw that could be covering for an empty response.
	key := "Shop|Checkouts|Stage~prod"
	chart := req(t, h, "GET", "/_console/cw/metric?key="+url.QueryEscape(key), nil)
	if chart.Code != 200 {
		t.Fatalf("cw metric: %d", chart.Code)
	}
	if !strings.Contains(chart.Body.String(), "cw-line") {
		t.Errorf("the chart did not render a line:\n%s", truncateBody(chart.Body.String()))
	}
	if !strings.Contains(chart.Body.String(), "Nothing watches this metric") {
		t.Errorf("a metric with no alarms should say so")
	}

	// An alarm on it, through the create form.
	loc := create(t, h, "/_console/cw/create-alarm", url.Values{
		"name": {"checkouts-low"}, "metric": {key}, "statistic": {"Sum"},
		"operator": {"LessThanThreshold"}, "threshold": {"3"},
		"period": {"60"}, "evaluation": {"1"}, "missing": {"notBreaching"},
	})
	if !strings.Contains(loc, "/cw/alarm/checkouts-low") {
		t.Fatalf("create alarm location = %q", loc)
	}

	// The alarm's own page carries the condition and links to its metric.
	page := req(t, h, "GET", "/_console/cw/alarm/checkouts-low", nil)
	if page.Code != 200 {
		t.Fatalf("alarm page: %d", page.Code)
	}
	for _, want := range []string{"checkouts-low", "LessThanThreshold", "Shop/Checkouts"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("the alarm page does not mention %q", want)
		}
	}

	// And the chart now knows an alarm watches it.
	chart = req(t, h, "GET", "/_console/cw/metric?key="+url.QueryEscape(key), nil)
	if !strings.Contains(chart.Body.String(), "checkouts-low") {
		t.Errorf("the chart does not list the alarm watching this metric")
	}

	// Setting the state by hand is what makes an alarm testable before the
	// metric it watches has ever breached.
	if r := req(t, h, "POST", "/_console/cw/alarm/checkouts-low/state", url.Values{
		"state": {"ALARM"}, "reason": {"by hand"}}); r.Code != 303 {
		t.Fatalf("set state: %d\n%s", r.Code, r.Body)
	}
	page = req(t, h, "GET", "/_console/cw/alarm/checkouts-low", nil)
	if !strings.Contains(page.Body.String(), "ALARM") {
		t.Errorf("the alarm did not read as ALARM after being set:\n%s", truncateBody(page.Body.String()))
	}
	// The state change is in the history, which is the pane a person reads to
	// find out why an alarm is where it is.
	hist := req(t, h, "GET", "/_console/cw/alarm/checkouts-low?tab=history", nil)
	if !strings.Contains(hist.Body.String(), "StateUpdate") {
		t.Errorf("the history does not carry the state change:\n%s", truncateBody(hist.Body.String()))
	}

	// Actions off, then the page says so.
	if r := req(t, h, "POST", "/_console/cw/alarm/checkouts-low/actions", url.Values{
		"enabled": {"false"}}); r.Code != 303 {
		t.Fatalf("disable actions: %d", r.Code)
	}
	page = req(t, h, "GET", "/_console/cw/alarm/checkouts-low", nil)
	if !strings.Contains(page.Body.String(), "actions off") {
		t.Errorf("the page does not report that actions are disabled")
	}

	// Tags go through the shared panel, which reaches CloudWatch's own tag
	// shape — a list of Key/Value, not the map CloudWatch Logs uses.
	if r := req(t, h, "POST", "/_console/tags/set", url.Values{
		"svc": {"cw"}, "id": {"checkouts-low"},
		"key": {"team"}, "value": {"shop"}}); r.Code != 200 {
		t.Fatalf("tag alarm: %d\n%s", r.Code, r.Body)
	}
	// The panel loads its rows through /tags/view, so that is where the tag is.
	tags := req(t, h, "GET", "/_console/tags/view?svc=cw&id=checkouts-low", nil)
	if !strings.Contains(tags.Body.String(), "shop") {
		t.Errorf("the tag did not come back:\n%s", truncateBody(tags.Body.String()))
	}

	// And it can be deleted.
	if r := req(t, h, "POST", "/_console/cw/alarm/checkouts-low/delete", nil); r.Code != 303 {
		t.Fatalf("delete alarm: %d", r.Code)
	}
	home = req(t, h, "GET", "/_console/cw", nil)
	if strings.Contains(home.Body.String(), "checkouts-low") {
		t.Errorf("the deleted alarm is still listed")
	}
}

// putMetric publishes through the gateway on the AWS CLI's wire (JSON 1.0),
// which is also the wire the console's own pane speaks.
func putMetric(t *testing.T, gw http.Handler, body string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	r.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801.PutMetricData")
	r.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/monitoring/aws4_request, SignedHeaders=host, Signature=x")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("PutMetricData: %d %s", rec.Code, rec.Body)
	}
}
