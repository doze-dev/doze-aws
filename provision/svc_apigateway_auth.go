package provision

// API Gateway authorizers and the method-level settings a route carries:
// which authorizer gates it, whether it needs an API key, and MOCK routes
// that answer without a backend.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// lambdaInvokeURI is the integration and authorizer URI shape for a function.
func lambdaInvokeURI(fn string) string {
	return "arn:aws:apigateway:" + awsident.Region + ":lambda:path/2015-03-31/functions/" + lambdaARN(fn) + "/invocations"
}

// ensureAuthorizers creates or updates the API's authorizers by name and
// returns name → id, which routes need since a method names an authorizer
// by id.
func ensureAuthorizers(ctx context.Context, c *client, apiID string, api API) (map[string]string, error) {
	ids := map[string]string{}
	out, err := c.do(ctx, "GET", "/restapis/"+apiID+"/authorizers", nil, nil)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Item []struct{ ID, Name string }
	}
	json.Unmarshal(out, &listed)
	for _, item := range listed.Item {
		ids[item.Name] = item.ID
	}
	for _, name := range sortedNames(api.Authorizers) {
		a := api.Authorizers[name]
		typ := a.Type
		if typ == "" {
			typ = "TOKEN"
		}
		source := a.IdentitySource
		if typ == "TOKEN" {
			if source == "" {
				source = "Authorization"
			}
			if !strings.HasPrefix(source, "method.request.") {
				source = "method.request.header." + source
			}
		}
		fields := map[string]any{
			"name": name, "type": typ, "authorizerUri": lambdaInvokeURI(a.Lambda), "identitySource": source,
		}
		if a.Validation != "" {
			fields["identityValidationExpression"] = a.Validation
		}
		if a.TTL != nil {
			fields["authorizerResultTtlInSeconds"] = *a.TTL
		}
		if id, ok := ids[name]; ok {
			var ops []map[string]any
			for k, v := range fields {
				if k == "name" {
					continue
				}
				ops = append(ops, map[string]any{"op": "replace", "path": "/" + k, "value": fmt.Sprint(v)})
			}
			if _, err := c.do(ctx, "PATCH", "/restapis/"+apiID+"/authorizers/"+id,
				map[string]string{"Content-Type": "application/json"}, mustJSON(map[string]any{"patchOperations": ops})); err != nil {
				return nil, fmt.Errorf("authorizer %q: %w", name, err)
			}
			continue
		}
		out, err := c.do(ctx, "POST", "/restapis/"+apiID+"/authorizers",
			map[string]string{"Content-Type": "application/json"}, mustJSON(fields))
		if err != nil {
			return nil, fmt.Errorf("authorizer %q: %w", name, err)
		}
		var created struct{ ID string }
		json.Unmarshal(out, &created)
		ids[name] = created.ID
	}
	return ids, nil
}

// methodRequest is the PutMethod body for a route: its authorizer (the
// route's own, else the API's default, unless the route says NONE) and
// whether it requires an API key.
func methodRequest(api API, route Route, authIDs map[string]string) (map[string]any, error) {
	body := map[string]any{"authorizationType": "NONE"}
	name := route.Authorizer
	if name == "" {
		name = api.DefaultAuthorizer
	}
	if name != "" && !strings.EqualFold(name, "NONE") {
		id, ok := authIDs[name]
		if !ok {
			return nil, fmt.Errorf("names authorizer %q, which the API does not declare", name)
		}
		body["authorizationType"] = "CUSTOM"
		body["authorizerId"] = id
	}
	required := api.APIKeyRequired
	if route.APIKeyRequired != nil {
		required = *route.APIKeyRequired
	}
	if required {
		body["apiKeyRequired"] = true
	}
	return body, nil
}

// putMockIntegration wires a MOCK integration and its 200 responses so the
// data plane answers the route's status, headers and body.
func putMockIntegration(ctx context.Context, c *client, base string, mock *MockRoute) error {
	status := mock.Status
	if status == 0 {
		status = 200
	}
	code := fmt.Sprint(status)
	hdr := map[string]string{"Content-Type": "application/json"}
	if _, err := c.do(ctx, "PUT", base+"/integration", hdr, mustJSON(map[string]any{
		"type": "MOCK", "requestTemplates": map[string]string{"application/json": `{"statusCode": ` + code + `}`},
	})); err != nil {
		return err
	}
	respParams := map[string]string{}
	methodParams := map[string]bool{}
	for k, v := range mock.Headers {
		respParams["method.response.header."+k] = "'" + v + "'"
		methodParams["method.response.header."+k] = true
	}
	if _, err := c.do(ctx, "PUT", base+"/responses/"+url.PathEscape(code), hdr, mustJSON(map[string]any{
		"statusCode": code, "responseParameters": methodParams,
	})); err != nil {
		return err
	}
	ir := map[string]any{"statusCode": code, "responseParameters": respParams}
	if mock.Body != "" {
		ir["responseTemplates"] = map[string]string{"application/json": mock.Body}
	}
	_, err := c.do(ctx, "PUT", base+"/integration/responses/"+url.PathEscape(code), hdr, mustJSON(ir))
	return err
}
