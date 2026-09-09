package apigateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/peers"
)

// A method that requires an API key is served only with a key that exists,
// is enabled, and sits in a usage plan covering the stage.
func TestAPIKeyGate(t *testing.T) {
	fake := &fakeLambda{}
	peer := httptest.NewServer(fake)
	defer peer.Close()
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf,
		Peers: peers.Static{"lambda": peers.Endpoint{Client: peer.Client(), BaseURL: peer.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	apiID := authorizedAPI(t, s, 300)
	// GET /keyed requires a key and no authorizer.
	s.store.Update(apiID, func(a *RestAPI) error {
		res := &Resource{ID: "keyed-id", ParentID: rootID(a), PathPart: "keyed", Path: "/keyed", Methods: map[string]*Method{}}
		res.Methods["GET"] = &Method{HTTPMethod: "GET", AuthorizationType: "NONE", APIKeyRequired: true,
			Integration: &Integration{Type: "AWS_PROXY", HTTPMethod: "POST", URI: "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:backend/invocations"}}
		a.Resources[res.ID] = res
		return nil
	})
	get := func(key string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+ExecutePrefix+apiID+"/v1/keyed", nil)
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(""); code != 403 || !strings.Contains(body, "Forbidden") {
		t.Errorf("no key: %d %s", code, body)
	}
	key := &APIKey{ID: "k1", Name: "partner", Value: "partner-key-value-0123456789", Enabled: true}
	s.store.PutAPIKey(key)
	if code, _ := get(key.Value); code != 403 {
		t.Errorf("a key in no plan should be forbidden, got %d", code)
	}
	plan := &UsagePlan{ID: "p1", Name: "basic", Stages: []PlanStage{{APIID: apiID, Stage: "v1"}}, KeyIDs: []string{"k1"}}
	s.store.PutUsagePlan(plan)
	if code, _ := get(key.Value); code != 200 {
		t.Errorf("a key in a covering plan should pass, got %d", code)
	}
	key.Enabled = false
	s.store.PutAPIKey(key)
	if code, _ := get(key.Value); code != 403 {
		t.Errorf("a disabled key should be forbidden, got %d", code)
	}
	key.Enabled = true
	s.store.PutAPIKey(key)
	plan.Stages = []PlanStage{{APIID: apiID, Stage: "other"}}
	s.store.PutUsagePlan(plan)
	if code, _ := get(key.Value); code != 403 {
		t.Errorf("a plan covering another stage should not admit the key, got %d", code)
	}
	// Deleting the key detaches it from its plans.
	plan.Stages = []PlanStage{{APIID: apiID, Stage: "v1"}}
	s.store.PutUsagePlan(plan)
	if err := s.store.DeleteAPIKey("k1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.store.GetUsagePlan("p1"); p.hasKey("k1") {
		t.Error("a deleted key stayed in its plan")
	}
}
