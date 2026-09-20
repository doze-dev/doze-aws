package console_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The keys page: create a key, reveal it, create a plan on a deployed
// stage, attach the key, detach it, disable the key, delete both.
func TestConsoleAPIGatewayKeysAndPlans(t *testing.T) {
	h := newConsole(t)
	loc := create(t, h, "/_console/apigw/create", url.Values{"name": {"keyed"}})
	api := regexp.MustCompile(`/apigw/([a-z0-9]+)`).FindStringSubmatch(loc)[1]
	if rec := req(t, h, "POST", "/_console/apigw/"+api+"/deploy", url.Values{"stage": {"v1"}}); rec.Code >= 400 {
		t.Fatalf("deploy: %d\n%s", rec.Code, rec.Body)
	}

	page := req(t, h, "GET", "/_console/apigw-keys", nil).Body.String()
	if !strings.Contains(page, "No API keys yet") || !strings.Contains(page, "keyed · v1") {
		t.Fatalf("keys page:\n%s", truncateBody(page))
	}
	mk := req(t, h, "POST", "/_console/apigw-keys/create", url.Values{"name": {"partner"}, "value": {"partner-value-0123456789abcdef"}})
	if mk.Code != 200 || !strings.Contains(mk.Body.String(), "partner") || strings.Contains(mk.Body.String(), "partner-value-0123456789abcdef") {
		t.Fatalf("create key (the list must not carry the value): %d\n%s", mk.Code, mk.Body)
	}
	keyID := regexp.MustCompile(`apigw-keys/key/([a-z0-9]+)/reveal`).FindStringSubmatch(mk.Body.String())[1]
	reveal := req(t, h, "GET", "/_console/apigw-keys/key/"+keyID+"/reveal", nil)
	if reveal.Code != 200 || !strings.Contains(reveal.Body.String(), "partner-value-0123456789abcdef") {
		t.Fatalf("reveal: %d\n%s", reveal.Code, reveal.Body)
	}

	plan := req(t, h, "POST", "/_console/apigw-keys/plans/create", url.Values{"name": {"partners"}, "stage": {api + ":v1"}})
	if plan.Code != 200 || !strings.Contains(plan.Body.String(), "partners") || !strings.Contains(plan.Body.String(), "keyed</a> · v1") {
		t.Fatalf("create plan: %d\n%s", plan.Code, plan.Body)
	}
	planID := regexp.MustCompile(`apigw-keys/plans/([a-z0-9]+)/attach-key`).FindStringSubmatch(plan.Body.String())[1]
	att := req(t, h, "POST", "/_console/apigw-keys/plans/"+planID+"/attach-key", url.Values{"key": {keyID}})
	if att.Code != 200 || !strings.Contains(att.Body.String(), `<span class="chip">partner `) {
		t.Fatalf("attach key: %d\n%s", att.Code, att.Body)
	}
	detail := req(t, h, "GET", "/_console/apigw-keys/plans/"+planID+"?key="+keyID, nil)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "partners") || !strings.Contains(detail.Body.String(), "partner-value-0123456789abcdef") {
		t.Fatalf("plan detail: %d\n%s", detail.Code, detail.Body)
	}
	det := req(t, h, "POST", "/_console/apigw-keys/plans/"+planID+"/detach-key", url.Values{"key": {keyID}})
	if det.Code != 200 || strings.Contains(det.Body.String(), `<span class="chip">partner `) {
		t.Fatalf("detach key: %d\n%s", det.Code, det.Body)
	}
	tog := req(t, h, "POST", "/_console/apigw-keys/toggle", url.Values{"id": {keyID}, "enable": {"false"}})
	if tog.Code != 200 || !strings.Contains(tog.Body.String(), "disabled") {
		t.Fatalf("toggle key: %d\n%s", tog.Code, tog.Body)
	}
	if rec := req(t, h, "POST", "/_console/apigw-keys/plans/delete", url.Values{"id": {planID}}); rec.Code != 200 || !strings.Contains(rec.Body.String(), "No usage plans") {
		t.Fatalf("delete plan: %d\n%s", rec.Code, rec.Body)
	}
	if rec := req(t, h, "POST", "/_console/apigw-keys/delete", url.Values{"id": {keyID}}); rec.Code != 200 || !strings.Contains(rec.Body.String(), "No API keys yet") {
		t.Fatalf("delete key: %d\n%s", rec.Code, rec.Body)
	}
}
