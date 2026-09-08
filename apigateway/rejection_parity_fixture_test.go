package apigateway

// The state the API Gateway baselines address, and the preconditions each
// mutating operation needs.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fx is the API the baselines address: one REST API with a resource carrying a
// GET method, a MOCK integration, and a 200 response on both sides, plus a
// deployment and a stage.
type fx struct {
	api      string
	rootID   string
	resource string
	deploy   string
	stage    string
}

// mk sends a request built from the operation's own model binding, so the
// fixture goes over the same wire the cases do.
func mk(t *testing.T, ts *httptest.Server, op string, body map[string]any) string {
	t.Helper()
	b := bindingFor(t, op)
	code, resp := call(t, ts, b, body)
	if code < 200 || code > 299 {
		t.Fatalf("fixture %s = %d: %s", op, code, resp)
	}
	return resp
}

// bindingFor reads an operation's REST binding out of the committed cases,
// rather than restating it here — the harness and the service must agree with
// the model, not with each other.
func bindingFor(t *testing.T, op string) *httpBinding {
	t.Helper()
	for _, c := range loadCases(t) {
		if c.Operation == op {
			return c.HTTP
		}
	}
	t.Fatalf("no binding for %s", op)
	return nil
}

func field(t *testing.T, resp, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(resp), &m); err != nil {
		t.Fatalf("response is not JSON: %s", resp)
	}
	v, ok := m[name].(string)
	if !ok || v == "" {
		t.Fatalf("response has no %s: %s", name, resp)
	}
	return v
}

func setUpFixture(t *testing.T, ts *httptest.Server) fx {
	t.Helper()
	f := fx{stage: "audit"}
	resp := mk(t, ts, "CreateRestApi", map[string]any{"name": "audit-api"})
	f.api = field(t, resp, "id")
	f.rootID = field(t, resp, "rootResourceId")

	resp = mk(t, ts, "CreateResource", map[string]any{
		"restApiId": f.api, "parentId": f.rootID, "pathPart": "audit",
	})
	f.resource = field(t, resp, "id")

	mk(t, ts, "PutMethod", map[string]any{
		"restApiId": f.api, "resourceId": f.resource, "httpMethod": "GET",
		"authorizationType": "NONE",
	})
	mk(t, ts, "PutIntegration", map[string]any{
		"restApiId": f.api, "resourceId": f.resource, "httpMethod": "GET",
		"type": "MOCK",
	})
	mk(t, ts, "PutMethodResponse", map[string]any{
		"restApiId": f.api, "resourceId": f.resource, "httpMethod": "GET",
		"statusCode": "200",
	})
	mk(t, ts, "PutIntegrationResponse", map[string]any{
		"restApiId": f.api, "resourceId": f.resource, "httpMethod": "GET",
		"statusCode": "200",
	})
	resp = mk(t, ts, "CreateDeployment", map[string]any{"restApiId": f.api})
	f.deploy = field(t, resp, "id")
	mk(t, ts, "CreateStage", map[string]any{
		"restApiId": f.api, "stageName": f.stage, "deploymentId": f.deploy,
	})
	return f
}

// patch is the JSON-Patch document API Gateway's Update* operations take.
func patch() []any {
	return []any{map[string]any{"op": "replace", "path": "/description", "value": "audited"}}
}

func baselines(f fx) map[string]map[string]any {
	api := map[string]any{"restApiId": f.api}
	method := map[string]any{"restApiId": f.api, "resourceId": f.resource, "httpMethod": "GET"}
	withStatus := func() map[string]any {
		m := map[string]any{}
		for k, v := range method {
			m[k] = v
		}
		m["statusCode"] = "200"
		return m
	}
	return map[string]map[string]any{
		"CreateRestApi": {"name": "made-by-baseline"},
		"GetRestApi":    api,
		"UpdateRestApi": {"restApiId": f.api, "patchOperations": patch()},
		"DeleteRestApi": {"restApiId": "made-by-baseline"},

		"GetResources":   api,
		"GetResource":    {"restApiId": f.api, "resourceId": f.resource},
		"CreateResource": {"restApiId": f.api, "parentId": f.rootID, "pathPart": "made-by-baseline"},
		"UpdateResource": {"restApiId": f.api, "resourceId": "made-by-baseline", "patchOperations": patch()},
		"DeleteResource": {"restApiId": f.api, "resourceId": "made-by-baseline"},

		"GetMethod":    method,
		"PutMethod":    {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST", "authorizationType": "NONE"},
		"DeleteMethod": {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST"},

		"GetIntegration":    method,
		"PutIntegration":    {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST", "type": "MOCK"},
		"DeleteIntegration": {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST"},

		"GetMethodResponse":    withStatus(),
		"PutMethodResponse":    {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST", "statusCode": "201"},
		"DeleteMethodResponse": {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST", "statusCode": "201"},

		"GetIntegrationResponse":    withStatus(),
		"PutIntegrationResponse":    {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST", "statusCode": "201"},
		"DeleteIntegrationResponse": {"restApiId": f.api, "resourceId": "made-by-baseline", "httpMethod": "POST", "statusCode": "201"},

		"GetDeployments":   api,
		"GetDeployment":    {"restApiId": f.api, "deploymentId": f.deploy},
		"CreateDeployment": api,
		"DeleteDeployment": {"restApiId": f.api, "deploymentId": "made-by-baseline"},

		"GetStages":   api,
		"GetStage":    {"restApiId": f.api, "stageName": f.stage},
		"CreateStage": {"restApiId": f.api, "stageName": "made-by-baseline", "deploymentId": f.deploy},
		"UpdateStage": {"restApiId": f.api, "stageName": f.stage, "patchOperations": patch()},
		"DeleteStage": {"restApiId": f.api, "stageName": "made-by-baseline"},
		// The account's one patchable path; a description would be refused.
		"UpdateAccount": {"patchOperations": []any{map[string]any{"op": "replace", "path": "/cloudwatchRoleArn", "value": "arn:aws:iam::000000000000:role/apigw-logs"}}},
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"patchOperations[]":                       patch(),
		"tags{}":                                  "dev",
		"variables{}":                             "v",
		"stageVariableOverrides{}":                "v",
		"requestParameters{}":                     "true",
		"requestTemplates{}":                      "{}",
		"responseTemplates{}":                     "{}",
		"responseParameters{}":                    "'*'",
		"requestModels{}":                         "Empty",
		"responseModels{}":                        "Empty",
		"binaryMediaTypes[]":                      []any{"application/octet-stream"},
		"cacheKeyParameters[]":                    []any{"method.request.path.id"},
		"endpointConfiguration":                   map[string]any{"types": []any{"REGIONAL"}},
		"endpointConfiguration.types[]":           []any{"REGIONAL"},
		"canarySettings":                          map[string]any{"percentTraffic": 0},
		"canarySettings.stageVariableOverrides{}": "v",
	}
}

// prepare gives each mutating case its own thing to act on. An API Gateway
// resource tree is a chain — a method needs a resource, an integration needs a
// method, a response needs an integration — so a case that deletes any link
// takes everything under it.
func prepare(t *testing.T, ts *httptest.Server, f fx, op, mutating string, body map[string]any, n int) {
	t.Helper()
	// set fills a precondition, unless the case is ABOUT that field or about
	// something inside it. Overwriting a container because the case is on a
	// member inside it would replace the violating value with a valid one, and
	// the case would silently test nothing.
	set := func(k, v string) {
		if mutating == k || strings.HasPrefix(mutating, k+".") ||
			strings.HasPrefix(mutating, k+"[") || strings.HasPrefix(mutating, k+"{") {
			return
		}
		body[k] = v
	}
	// newResource builds a resource with a POST method, integration and 201
	// responses, so any of those links can be consumed without touching the
	// fixture the read baselines address.
	newResource := func(withMethod, withIntegration, withResponses bool) string {
		resp := mk(t, ts, "CreateResource", map[string]any{
			"restApiId": f.api, "parentId": f.rootID, "pathPart": fmt.Sprintf("p%d", n),
		})
		id := field(t, resp, "id")
		if withMethod {
			mk(t, ts, "PutMethod", map[string]any{
				"restApiId": f.api, "resourceId": id, "httpMethod": "POST",
				"authorizationType": "NONE",
			})
		}
		if withIntegration {
			mk(t, ts, "PutIntegration", map[string]any{
				"restApiId": f.api, "resourceId": id, "httpMethod": "POST", "type": "MOCK",
			})
		}
		if withResponses {
			mk(t, ts, "PutMethodResponse", map[string]any{
				"restApiId": f.api, "resourceId": id, "httpMethod": "POST", "statusCode": "201",
			})
			mk(t, ts, "PutIntegrationResponse", map[string]any{
				"restApiId": f.api, "resourceId": id, "httpMethod": "POST", "statusCode": "201",
			})
		}
		return id
	}

	switch op {
	case "CreateRestApi":
		set("name", fmt.Sprintf("created-api-%d", n))
	case "DeleteRestApi":
		resp := mk(t, ts, "CreateRestApi", map[string]any{"name": fmt.Sprintf("doomed-api-%d", n)})
		set("restApiId", field(t, resp, "id"))

	case "CreateResource":
		set("pathPart", fmt.Sprintf("created-%d", n))
	case "UpdateResource", "DeleteResource":
		set("resourceId", newResource(false, false, false))

	case "PutMethod":
		// PUT on an existing method is a conflict, so each case needs a
		// resource with nothing on it yet.
		set("resourceId", newResource(false, false, false))
	case "DeleteMethod":
		set("resourceId", newResource(true, false, false))

	case "PutIntegration":
		set("resourceId", newResource(true, false, false))
	case "DeleteIntegration":
		set("resourceId", newResource(true, true, false))

	case "PutMethodResponse", "PutIntegrationResponse":
		set("resourceId", newResource(true, true, false))
	case "DeleteMethodResponse", "DeleteIntegrationResponse":
		set("resourceId", newResource(true, true, true))

	case "DeleteDeployment":
		resp := mk(t, ts, "CreateDeployment", map[string]any{"restApiId": f.api})
		set("deploymentId", field(t, resp, "id"))

	case "CreateStage":
		set("stageName", fmt.Sprintf("created%d", n))
	case "DeleteStage":
		name := fmt.Sprintf("doomed%d", n)
		mk(t, ts, "CreateStage", map[string]any{
			"restApiId": f.api, "stageName": name, "deploymentId": f.deploy,
		})
		set("stageName", name)
	}
}

var _ = http.StatusOK
