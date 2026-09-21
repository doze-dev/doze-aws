package provision

// HTTP APIs (apigatewayv2) at apply: the same route-shaped IR, driven
// through the v2 control plane — an integration per distinct target, a
// route per key, REQUEST authorizers by name, and one auto-deploying stage
// ($default unless the stack names one).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func applyHTTPAPI(ctx context.Context, c *client, name string, api API, rep *Report) error {
	jsonHdr := map[string]string{"Content-Type": "application/json"}
	id, existing, err := findHTTPAPI(ctx, c, name)
	if err != nil {
		return err
	}
	if !existing {
		body := map[string]any{"name": name, "protocolType": "HTTP"}
		if api.CORS != nil {
			body["corsConfiguration"] = corsBody(api.CORS)
		}
		out, err := c.do(ctx, "POST", "/v2/apis", jsonHdr, mustJSON(body))
		if err != nil {
			return fmt.Errorf("http api %q: %w", name, err)
		}
		var created struct {
			ID string `json:"apiId"`
		}
		if err := json.Unmarshal(out, &created); err != nil {
			return err
		}
		id = created.ID
		rep.add("created", "api/"+name, "HTTP API")
	} else {
		if api.CORS != nil {
			if _, err := c.do(ctx, "PATCH", "/v2/apis/"+id, jsonHdr, mustJSON(map[string]any{"corsConfiguration": corsBody(api.CORS)})); err != nil {
				return fmt.Errorf("http api %q: cors: %w", name, err)
			}
		}
		rep.add("skipped", "api/"+name, "already in place")
	}

	authIDs, err := ensureV2Authorizers(ctx, c, id, api)
	if err != nil {
		return fmt.Errorf("http api %q: %w", name, err)
	}
	existingRoutes, err := listV2(ctx, c, "/v2/apis/"+id+"/routes", "routeKey", "routeId")
	if err != nil {
		return err
	}
	integrations := map[string]string{} // target key -> integration id
	for _, route := range api.Routes {
		key := v2RouteKey(route)
		if _, ok := existingRoutes[key]; ok {
			continue
		}
		integID, err := ensureV2Integration(ctx, c, id, route, integrations)
		if err != nil {
			return fmt.Errorf("http api %q route %s: %w", name, key, err)
		}
		body := map[string]any{"routeKey": key, "target": "integrations/" + integID}
		authName := route.Authorizer
		if authName == "" {
			authName = api.DefaultAuthorizer
		}
		if authName != "" && authName != "NONE" {
			authID, ok := authIDs[authName]
			if !ok {
				return fmt.Errorf("http api %q route %s: authorizer %q is not declared on the API", name, key, authName)
			}
			body["authorizationType"], body["authorizerId"] = "CUSTOM", authID
		}
		if _, err := c.do(ctx, "POST", "/v2/apis/"+id+"/routes", jsonHdr, mustJSON(body)); err != nil {
			return fmt.Errorf("http api %q route %s: %w", name, key, err)
		}
	}

	stage := api.Stage
	if stage == "" {
		stage = "$default"
	}
	stageBody := map[string]any{"stageName": stage, "autoDeploy": true}
	if api.AccessLog != nil {
		stageBody["accessLogSettings"] = map[string]any{"destinationArn": api.AccessLog.DestinationARN, "format": api.AccessLog.Format}
	}
	existingStages, err := listV2(ctx, c, "/v2/apis/"+id+"/stages", "stageName", "stageName")
	if err != nil {
		return err
	}
	method, path := "POST", "/v2/apis/"+id+"/stages"
	if _, ok := existingStages[stage]; ok {
		method, path = "PATCH", "/v2/apis/"+id+"/stages/"+stage
		delete(stageBody, "stageName")
	}
	if _, err := c.do(ctx, method, path, jsonHdr, mustJSON(stageBody)); err != nil {
		return fmt.Errorf("http api %q: stage %s: %w", name, stage, err)
	}
	rep.add("updated", "api/"+name, "deployed to stage "+stage)
	return nil
}

// v2RouteKey spells a route as the v2 key: "$default", or "METHOD /path".
func v2RouteKey(route Route) string {
	if route.Path == "$default" || route.Path == "" {
		return "$default"
	}
	method := strings.ToUpper(route.Method)
	if method == "" {
		method = "ANY"
	}
	return method + " " + route.Path
}

// ensureV2Integration creates the integration a route needs, once per
// distinct target within one apply.
func ensureV2Integration(ctx context.Context, c *client, apiID string, route Route, seen map[string]string) (string, error) {
	var body map[string]any
	var key string
	switch {
	case route.HTTPTarget != "":
		key = "http:" + route.HTTPTarget
		body = map[string]any{"integrationType": "HTTP_PROXY", "integrationUri": route.HTTPTarget, "integrationMethod": "ANY"}
	case route.Lambda != "":
		format := route.PayloadFormat
		if format == "" {
			format = "2.0"
		}
		key = "lambda:" + route.Lambda + ":" + format
		body = map[string]any{
			"integrationType": "AWS_PROXY", "payloadFormatVersion": format,
			"integrationUri": c.id.ARN("lambda", "function:"+route.Lambda),
		}
	default:
		return "", fmt.Errorf("an HTTP API route needs a Lambda function or an HTTP target; MOCK integrations exist only on REST APIs")
	}
	if id, ok := seen[key]; ok {
		return id, nil
	}
	out, err := c.do(ctx, "POST", "/v2/apis/"+apiID+"/integrations", map[string]string{"Content-Type": "application/json"}, mustJSON(body))
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"integrationId"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return "", err
	}
	seen[key] = created.ID
	return created.ID, nil
}

// ensureV2Authorizers creates the REQUEST authorizers the API declares and
// returns their ids by name.
func ensureV2Authorizers(ctx context.Context, c *client, apiID string, api API) (map[string]string, error) {
	ids, err := listV2(ctx, c, "/v2/apis/"+apiID+"/authorizers", "name", "authorizerId")
	if err != nil {
		return nil, err
	}
	for _, name := range sortedNames(api.Authorizers) {
		if _, ok := ids[name]; ok {
			continue
		}
		a := api.Authorizers[name]
		sources := strings.Split(a.IdentitySource, ",")
		if a.IdentitySource == "" {
			sources = []string{"$request.header.Authorization"}
		}
		for i, s := range sources {
			s = strings.TrimSpace(s)
			// A REST-style source names the same thing in the v2 spelling.
			s = strings.Replace(s, "method.request.header.", "$request.header.", 1)
			s = strings.Replace(s, "method.request.querystring.", "$request.querystring.", 1)
			if !strings.HasPrefix(s, "$") {
				s = "$request.header." + s
			}
			sources[i] = s
		}
		format := a.PayloadFormat
		if format == "" {
			format = "2.0"
		}
		body := map[string]any{
			"name": name, "authorizerType": "REQUEST", "identitySource": sources,
			"authorizerUri":                  lambdaInvokeURI(c.id, a.Lambda),
			"authorizerPayloadFormatVersion": format,
			"enableSimpleResponses":          a.SimpleResponses,
		}
		if a.TTL != nil {
			body["authorizerResultTtlInSeconds"] = *a.TTL
		}
		out, err := c.do(ctx, "POST", "/v2/apis/"+apiID+"/authorizers", map[string]string{"Content-Type": "application/json"}, mustJSON(body))
		if err != nil {
			return nil, fmt.Errorf("authorizer %q: %w", name, err)
		}
		var created struct {
			ID string `json:"authorizerId"`
		}
		if err := json.Unmarshal(out, &created); err != nil {
			return nil, err
		}
		ids[name] = created.ID
	}
	return ids, nil
}

// listV2 reads a v2 collection into a map of one field to another.
func listV2(ctx context.Context, c *client, path, keyField, valueField string) (map[string]string, error) {
	out, err := c.do(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, item := range listed.Items {
		k, _ := item[keyField].(string)
		v, _ := item[valueField].(string)
		if k != "" {
			m[k] = v
		}
	}
	return m, nil
}

func findHTTPAPI(ctx context.Context, c *client, name string) (id string, found bool, err error) {
	ids, err := listV2(ctx, c, "/v2/apis", "name", "apiId")
	if err != nil {
		return "", false, err
	}
	id, found = ids[name]
	return id, found, nil
}

func corsBody(c *APICORS) map[string]any {
	body := map[string]any{"allowCredentials": c.AllowCredentials}
	if len(c.AllowOrigins) > 0 {
		body["allowOrigins"] = c.AllowOrigins
	}
	if len(c.AllowMethods) > 0 {
		body["allowMethods"] = c.AllowMethods
	}
	if len(c.AllowHeaders) > 0 {
		body["allowHeaders"] = c.AllowHeaders
	}
	if len(c.ExposeHeaders) > 0 {
		body["exposeHeaders"] = c.ExposeHeaders
	}
	if c.MaxAge != nil {
		body["maxAge"] = *c.MaxAge
	}
	return body
}

func destroyHTTPAPI(ctx context.Context, c *client, name string, rep *DestroyReport) {
	id, found, err := findHTTPAPI(ctx, c, name)
	if err != nil || !found {
		rep.add("absent", "api/"+name, "")
		return
	}
	_, err = c.do(ctx, "DELETE", "/v2/apis/"+id, nil, nil)
	record(rep, "api/"+name, err)
}
