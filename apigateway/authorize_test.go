package apigateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

// A fake Lambda peer: the authorizer function answers by token, the backend
// function echoes the event so the test can read requestContext.authorizer.
type fakeLambda struct {
	calls atomic.Int32 // authorizer invocations
}

func (f *fakeLambda) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var ev map[string]any
	json.Unmarshal(body, &ev)
	switch {
	case strings.Contains(r.URL.Path, "/functions/auth/"):
		f.calls.Add(1)
		token, _ := ev["authorizationToken"].(string)
		if ev["type"] == "REQUEST" {
			h, _ := ev["headers"].(map[string]any)
			token, _ = h["X-Token"].(string)
		}
		arn, _ := ev["methodArn"].(string)
		switch token {
		case "allow":
			json.NewEncoder(w).Encode(map[string]any{
				"principalId": "user-1", "context": map[string]any{"tier": "gold", "n": 7},
				"policyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{
					map[string]any{"Effect": "Allow", "Action": "execute-api:Invoke", "Resource": strings.SplitN(arn, "/", 2)[0] + "/*/GET/*"},
				}},
			})
		case "deny":
			json.NewEncoder(w).Encode(map[string]any{
				"principalId": "user-2",
				"policyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{
					map[string]any{"Effect": "Allow", "Action": "*", "Resource": "*"},
					map[string]any{"Effect": "Deny", "Action": "execute-api:Invoke", "Resource": arn},
				}},
			})
		case "garbage":
			io.WriteString(w, `"not a policy"`)
		default:
			w.WriteHeader(500)
			io.WriteString(w, `{"errorMessage":"boom"}`)
		}
	default:
		rc, _ := ev["requestContext"].(map[string]any)
		out, _ := json.Marshal(rc["authorizer"])
		json.NewEncoder(w).Encode(map[string]any{"statusCode": 200, "body": string(out)})
	}
}

// authorizedAPI builds an API with GET /items behind a TOKEN authorizer and
// GET /open behind a REQUEST authorizer reading X-Token.
func authorizedAPI(t *testing.T, s *Server, ttl int) (apiID string) {
	t.Helper()
	api, err := s.store.Create("auth-api", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	uri := func(fn string) string {
		return "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:" + fn + "/invocations"
	}
	tokenAuth := &Authorizer{ID: "tok1", Name: "token", Type: "TOKEN", URI: uri("auth"), ResultTTL: ttl}
	reqAuth := &Authorizer{ID: "req1", Name: "request", Type: "REQUEST", URI: uri("auth"), IdentitySource: "method.request.header.X-Token", ResultTTL: 0}
	_, err = s.store.Update(api.ID, func(a *RestAPI) error {
		a.Authorizers = map[string]*Authorizer{"tok1": tokenAuth, "req1": reqAuth}
		for _, spec := range []struct{ path, auth string }{{"items", "tok1"}, {"open", "req1"}} {
			res := &Resource{ID: spec.path + "-id", ParentID: rootID(a), PathPart: spec.path, Path: "/" + spec.path, Methods: map[string]*Method{}}
			res.Methods["GET"] = &Method{HTTPMethod: "GET", AuthorizationType: "CUSTOM", AuthorizerID: spec.auth,
				Integration: &Integration{Type: "AWS_PROXY", HTTPMethod: "POST", URI: uri("backend")}}
			a.Resources[res.ID] = res
		}
		if a.Stages == nil {
			a.Stages = map[string]*Stage{}
		}
		a.Stages["v1"] = &Stage{Name: "v1", DeploymentID: "d1"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return api.ID
}

func TestLambdaAuthorizerGate(t *testing.T) {
	fake := &fakeLambda{}
	peer := httptest.NewServer(fake)
	defer peer.Close()
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf,
		Peers: peers.Static{"lambda": peers.Endpoint{Client: peer.Client(), BaseURL: peer.URL}},
		Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	apiID := authorizedAPI(t, s, 300)

	get := func(path string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+ExecutePrefix+apiID+"/v1"+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// No token: 401 before any invoke.
	if code, body := get("/items", nil); code != 401 || !strings.Contains(body, `"Unauthorized"`) {
		t.Errorf("missing token: %d %s", code, body)
	}
	if fake.calls.Load() != 0 {
		t.Errorf("the authorizer was invoked without an identity")
	}
	// Allow: the backend sees the principal and context.
	code, body := get("/items", map[string]string{"Authorization": "allow"})
	if code != 200 || !strings.Contains(body, `"principalId":"user-1"`) || !strings.Contains(body, `"tier":"gold"`) {
		t.Errorf("allow: %d %s", code, body)
	}
	// Cached: a second call with the same token does not invoke again.
	get("/items", map[string]string{"Authorization": "allow"})
	if fake.calls.Load() != 1 {
		t.Errorf("the allow verdict was not cached: %d invokes", fake.calls.Load())
	}
	// Past the TTL, it is invoked again.
	now = now.Add(301 * time.Second)
	get("/items", map[string]string{"Authorization": "allow"})
	if fake.calls.Load() != 2 {
		t.Errorf("the cache did not expire: %d invokes", fake.calls.Load())
	}
	// Deny wins over allow.
	if code, body := get("/items", map[string]string{"Authorization": "deny"}); code != 403 || !strings.Contains(body, "explicit deny") {
		t.Errorf("deny: %d %s", code, body)
	}
	// Garbage and a failing function are 500s naming the cause.
	if code, body := get("/items", map[string]string{"Authorization": "garbage"}); code != 500 || !strings.Contains(body, "Authorizer error") {
		t.Errorf("garbage: %d %s", code, body)
	}
	if code, body := get("/items", map[string]string{"Authorization": "explode"}); code != 500 || !strings.Contains(body, "Authorizer error") {
		t.Errorf("failing function: %d %s", code, body)
	}
	// REQUEST authorizer reads its identity source; TTL 0 never caches.
	before := fake.calls.Load()
	if code, _ := get("/open", map[string]string{"X-Token": "allow"}); code != 200 {
		t.Errorf("request authorizer allow: %d", code)
	}
	get("/open", map[string]string{"X-Token": "allow"})
	if fake.calls.Load() != before+2 {
		t.Errorf("a TTL of 0 should not cache: %d invokes", fake.calls.Load()-before)
	}
	if code, _ := get("/open", map[string]string{"Authorization": "allow"}); code != 401 {
		t.Errorf("request authorizer without X-Token: %d", code)
	}
}

func TestPolicyAllows(t *testing.T) {
	arn := "arn:aws:execute-api:us-east-1:000000000000:abc/v1/GET/items/42"
	doc := func(stmts ...statement) *policyDoc { return &policyDoc{Statement: stmts} }
	cases := []struct {
		name string
		p    *policyDoc
		want bool
	}{
		{"exact allow", doc(statement{"Allow", "execute-api:Invoke", arn}), true},
		{"glob allow", doc(statement{"Allow", "execute-api:Invoke", "arn:aws:execute-api:us-east-1:000000000000:abc/*/GET/items/*"}), true},
		{"question glob", doc(statement{"Allow", "execute-api:Invoke", "arn:aws:execute-api:us-east-1:000000000000:abc/v1/GET/items/4?"}), true},
		{"other method", doc(statement{"Allow", "execute-api:Invoke", "arn:aws:execute-api:us-east-1:000000000000:abc/v1/POST/*"}), false},
		{"deny wins", doc(statement{"Allow", "*", "*"}, statement{"Deny", "execute-api:Invoke", arn}), false},
		{"wrong action", doc(statement{"Allow", "s3:GetObject", "*"}), false},
		{"list resources", doc(statement{"Allow", []any{"execute-api:Invoke"}, []any{"arn:aws:execute-api:*:*:zzz/*", "arn:aws:execute-api:*:*:abc/*"}}), true},
		{"empty", doc(), false},
	}
	for _, c := range cases {
		if got := policyAllows(c.p, arn); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
