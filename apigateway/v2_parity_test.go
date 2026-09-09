package apigateway

// The HTTP API (v2) rejection-parity audit: the same replay as
// rejection_parity_test.go over testdata/cases_apigatewayv2.json, with its
// own fixture — an HTTP API carrying an integration, a route, a stage, a
// deployment and a REQUEST authorizer — and its own baselines.

import (
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/auditkit"
)

// fxV2 is the HTTP API the v2 baselines address.
type fxV2 struct {
	api, integ, route, stage, deploy, auth, arn string
}

func bindingForV2(t *testing.T, op string) *httpBinding {
	t.Helper()
	for _, c := range loadCasesFile(t, "cases_apigatewayv2.json") {
		if c.Operation == op {
			return c.HTTP
		}
	}
	if b, ok := loadRoutesFile(t, "routes_apigatewayv2.json")[op]; ok {
		return b
	}
	t.Fatalf("no v2 binding for %s", op)
	return nil
}

func mkV2(t *testing.T, ts *httptest.Server, op string, body map[string]any) string {
	t.Helper()
	code, resp := call(t, ts, bindingForV2(t, op), body)
	if code < 200 || code > 299 {
		t.Fatalf("v2 fixture %s = %d: %s", op, code, resp)
	}
	return resp
}

func setUpFixtureV2(t *testing.T, ts *httptest.Server) fxV2 {
	t.Helper()
	f := fxV2{stage: "audit"}
	resp := mkV2(t, ts, "CreateApi", map[string]any{"name": "audit-http", "protocolType": "HTTP"})
	f.api = field(t, resp, "apiId")
	f.arn = V2APIARN(f.api)
	resp = mkV2(t, ts, "CreateIntegration", map[string]any{
		"apiId": f.api, "integrationType": "AWS_PROXY", "integrationUri": fnARN, "payloadFormatVersion": "2.0",
	})
	f.integ = field(t, resp, "integrationId")
	resp = mkV2(t, ts, "CreateRoute", map[string]any{"apiId": f.api, "routeKey": "GET /audit", "target": "integrations/" + f.integ})
	f.route = field(t, resp, "routeId")
	resp = mkV2(t, ts, "CreateDeployment", map[string]any{"apiId": f.api})
	f.deploy = field(t, resp, "deploymentId")
	mkV2(t, ts, "CreateStage", map[string]any{"apiId": f.api, "stageName": f.stage, "deploymentId": f.deploy})
	resp = mkV2(t, ts, "CreateAuthorizer", map[string]any{
		"apiId": f.api, "name": "audit-auth", "authorizerType": "REQUEST", "authorizerUri": authorizerURI,
		"identitySource": []any{"$request.header.Authorization"}, "authorizerPayloadFormatVersion": "2.0",
	})
	f.auth = field(t, resp, "authorizerId")
	return f
}

func baselinesV2(f fxV2) map[string]map[string]any {
	api := map[string]any{"apiId": f.api}
	with := func(kv ...string) map[string]any {
		m := map[string]any{"apiId": f.api}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	return map[string]map[string]any{
		"CreateApi":               {"name": "made-by-baseline", "protocolType": "HTTP"},
		"GetApi":                  api,
		"GetApis":                 {},
		"UpdateApi":               with("description", "audited"),
		"DeleteApi":               api, // prepare swaps in a fresh API
		"DeleteCorsConfiguration": api,
		"CreateRoute":             with("target", "integrations/"+f.integ), // prepare adds a fresh key
		"GetRoute":                with("routeId", f.route),
		"GetRoutes":               api,
		"UpdateRoute":             with("routeId", f.route, "operationName", "audited"),
		"DeleteRoute":             with("routeId", f.route),
		"CreateIntegration":       with("integrationType", "AWS_PROXY", "integrationUri", fnARN),
		"GetIntegration":          with("integrationId", f.integ),
		"GetIntegrations":         api,
		"UpdateIntegration":       with("integrationId", f.integ, "description", "audited"),
		"DeleteIntegration":       with("integrationId", f.integ),
		"CreateStage":             api, // prepare adds a fresh name
		"GetStage":                with("stageName", f.stage),
		"GetStages":               api,
		"UpdateStage":             with("stageName", f.stage, "description", "audited"),
		"DeleteStage":             with("stageName", f.stage),
		"DeleteAccessLogSettings": with("stageName", f.stage),
		"DeleteRouteSettings":     with("stageName", f.stage, "routeKey", "GET /audit"),
		"ResetAuthorizersCache":   with("stageName", f.stage),
		"CreateDeployment":        api,
		"GetDeployment":           with("deploymentId", f.deploy),
		"GetDeployments":          api,
		"UpdateDeployment":        with("deploymentId", f.deploy, "description", "audited"),
		"DeleteDeployment":        with("deploymentId", f.deploy),
		"GetTags":                 {"resourceArn": f.arn},
		"TagResource":             {"resourceArn": f.arn, "tags": map[string]any{"env": "audit"}},
		"UntagResource":           {"resourceArn": f.arn, "tagKeys": []any{"env"}},
		"CreateAuthorizer": {"apiId": f.api, "authorizerType": "REQUEST", "authorizerUri": authorizerURI,
			"identitySource": []any{"$request.header.Authorization"}, "authorizerPayloadFormatVersion": "2.0"},
		"GetAuthorizer":    with("authorizerId", f.auth),
		"GetAuthorizers":   api,
		"UpdateAuthorizer": with("authorizerId", f.auth, "authorizerUri", authorizerURI),
		"DeleteAuthorizer": with("authorizerId", f.auth),
	}
}

func exemplarsV2() map[string]any {
	return map[string]any{
		"corsConfiguration":    map[string]any{"allowOrigins": []any{"*"}},
		"defaultRouteSettings": map[string]any{"detailedMetricsEnabled": false},
		"routeSettings{}":      map[string]any{"GET /audit": map[string]any{"detailedMetricsEnabled": false}},
		"tags{}":               "dev",
		"stageVariables{}":     "v",
		"identitySource[]":     []any{"$request.header.Authorization"},
		"tagKeys[]":            []any{"env"},
	}
}

// prepareV2 gives each mutating case its own thing to act on, as prepare
// does for the REST surface.
func prepareV2(t *testing.T, ts *httptest.Server, f fxV2, op, mutating string, body map[string]any, n int) {
	t.Helper()
	set := func(k string, v any) {
		if mutating == k || strings.HasPrefix(mutating, k+".") ||
			strings.HasPrefix(mutating, k+"[") || strings.HasPrefix(mutating, k+"{") {
			return
		}
		body[k] = v
	}
	switch op {
	case "CreateRoute":
		set("routeKey", fmt.Sprintf("GET /p%d", n))
	case "CreateStage":
		set("stageName", fmt.Sprintf("s%d", n))
	case "CreateAuthorizer":
		set("name", fmt.Sprintf("a%d", n))
	case "DeleteApi":
		resp := mkV2(t, ts, "CreateApi", map[string]any{"name": fmt.Sprintf("d%d", n), "protocolType": "HTTP"})
		set("apiId", field(t, resp, "apiId"))
	case "DeleteRoute":
		resp := mkV2(t, ts, "CreateRoute", map[string]any{"apiId": f.api, "routeKey": fmt.Sprintf("DELETE /d%d", n), "target": "integrations/" + f.integ})
		set("routeId", field(t, resp, "routeId"))
	case "DeleteIntegration":
		resp := mkV2(t, ts, "CreateIntegration", map[string]any{"apiId": f.api, "integrationType": "AWS_PROXY", "integrationUri": fnARN})
		set("integrationId", field(t, resp, "integrationId"))
	case "DeleteStage":
		mkV2(t, ts, "CreateStage", map[string]any{"apiId": f.api, "stageName": fmt.Sprintf("ds%d", n)})
		set("stageName", fmt.Sprintf("ds%d", n))
	case "DeleteDeployment":
		resp := mkV2(t, ts, "CreateDeployment", map[string]any{"apiId": f.api})
		set("deploymentId", field(t, resp, "deploymentId"))
	case "DeleteAuthorizer":
		resp := mkV2(t, ts, "CreateAuthorizer", map[string]any{
			"apiId": f.api, "name": fmt.Sprintf("da%d", n), "authorizerType": "REQUEST", "authorizerUri": authorizerURI,
			"identitySource": []any{"$request.header.Authorization"}, "authorizerPayloadFormatVersion": "2.0",
		})
		set("authorizerId", field(t, resp, "authorizerId"))
	}
}

// knownGapsV2 are v2 constraints AWS enforces and doze-aws does not.
var knownGapsV2 = map[string]bool{}

func TestAPIGatewayV2RejectsWhatTheModelForbids(t *testing.T) {
	ts := apigwServer(t)
	f := setUpFixtureV2(t, ts)
	base := baselinesV2(f)
	n := 0
	seq := func() int { n++; return n }

	byOp := map[string][]auditCase{}
	bind := map[string]*httpBinding{}
	for _, c := range loadCasesFile(t, "cases_apigatewayv2.json") {
		byOp[c.Operation] = append(byOp[c.Operation], c)
		bind[c.Operation] = c.HTTP
	}
	all := loadRoutesFile(t, "routes_apigatewayv2.json")
	for op, b := range all {
		if _, ok := bind[op]; !ok {
			bind[op] = b
		}
	}
	// Every routed operation has a baseline, and every baseline is routed:
	// the harness and the service must agree on the surface.
	for op := range all {
		if _, ok := base[op]; !ok {
			t.Errorf("%s is routed and has no baseline", op)
		}
	}
	for op := range base {
		if _, ok := all[op]; !ok {
			t.Errorf("%s has a baseline and is not routed", op)
		}
	}
	ops := make([]string, 0, len(byOp))
	for op := range byOp {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	var total, gaps, unbuildable, unwireable int
	for _, op := range ops {
		b, ok := base[op]
		if !ok {
			continue
		}
		t.Run(op, func(t *testing.T) {
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepareV2(t, ts, f, op, "", bl, seq())
			if code, body := call(t, ts, bind[op], bl); code < 200 || code > 299 {
				t.Fatalf("the baseline request was refused (%d): %s\nevery %s case would be meaningless", code, body, op)
			}
			paths := make([]string, 0, len(byOp[op]))
			for _, c := range byOp[op] {
				paths = append(paths, c.Path)
			}
			for _, prefix := range auditkit.Containers(paths) {
				probe := auditkit.DeepCopy(b).(map[string]any)
				if err := auditkit.Apply(probe, exemplarsV2(), prefix+".probe", nil, false); err != nil {
					t.Errorf("container %s: %v", prefix, err)
					continue
				}
				prepareV2(t, ts, f, op, "", probe, seq())
				if code, resp := call(t, ts, bind[op], probe); code < 200 || code > 299 {
					t.Errorf("the exemplar for %q makes the baseline invalid (%d): %s", prefix, code, resp)
				}
			}
			for _, c := range byOp[op] {
				total++
				if why, ok := unexpressibleOn(c, bind); ok {
					unwireable++
					t.Logf("cannot express %s/%s on the wire: %s", op, c.Path, why)
					continue
				}
				t.Run(c.Path+"/"+c.Why, func(t *testing.T) {
					body := auditkit.DeepCopy(b).(map[string]any)
					if err := auditkit.Apply(body, exemplarsV2(), c.Path, c.Value, true); err != nil {
						unbuildable++
						t.Fatalf("could not build the case: %v", err)
					}
					prepareV2(t, ts, f, op, c.Path, body, seq())
					key := op + "/" + c.Path + "/" + c.Why
					code, resp := call(t, ts, bind[op], body)
					if code >= 200 && code <= 299 {
						gaps++
						if !knownGapsV2[key] {
							t.Errorf("accepted %s = %v\n  AWS refuses it: %s\n  constraint: %s\n  This is a NEW gap.", c.Path, c.Value, c.Why, c.Constraint)
						}
						return
					}
					if code >= 500 {
						t.Fatalf("%s = %d (a refusal should be a 4xx): %s", c.Path, code, resp)
					}
					if knownGapsV2[key] {
						t.Errorf("%s is enforced now — delete it from knownGapsV2", key)
					}
				})
			}
		})
	}
	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d HTTP API operations (%d unbuildable, %d not expressible on this wire)",
		total-gaps-unbuildable-unwireable, total, len(ops), unbuildable, unwireable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
}
