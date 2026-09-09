package console

// The HTTP API (apigatewayv2) half of the API Gateway client: /v2/apis.
// An HTTP API is routes by key with an integration each, so the console
// shows it as a flat route table rather than the REST resource tree.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// HTTPAPI is one HTTP API as the list and detail pages show it.
type HTTPAPI struct {
	ID, Name, Description, Endpoint, Created string
	CORS                                     *HTTPCORS
	Routes                                   int
}

// HTTPCORS is the API's CORS configuration.
type HTTPCORS struct {
	Origins, Methods, Headers, Expose []string
	Credentials                       bool
	MaxAge                            int
}

// HTTPRoute is one route with its integration resolved.
type HTTPRoute struct {
	ID, Key, Method, Path           string
	IntegrationID, Type, Target     string // Target: the function name or the URL
	PayloadFormat, AuthType, AuthID string
	Lambda                          string // set when Type is AWS_PROXY
}

// HTTPStage is one stage of an HTTP API.
type HTTPStage struct {
	Name, DeploymentID, Description, InvokeURL, LogGroup string
	AutoDeploy                                           bool
	Variables                                            map[string]string
}

// HTTPAuthorizer is a REQUEST authorizer of an HTTP API.
type HTTPAuthorizer struct {
	ID, Name, Lambda, PayloadFormat string
	IdentitySource                  []string
	SimpleResponses                 bool
	TTL                             int
}

func (b *backend) ListHTTPAPIs(ctx context.Context) ([]HTTPAPI, error) {
	var out struct {
		Items []struct {
			ID          string `json:"apiId"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Endpoint    string `json:"apiEndpoint"`
			Created     string `json:"createdDate"`
			CORS        *struct {
				AllowOrigins     []string `json:"allowOrigins"`
				AllowMethods     []string `json:"allowMethods"`
				AllowHeaders     []string `json:"allowHeaders"`
				ExposeHeaders    []string `json:"exposeHeaders"`
				AllowCredentials bool     `json:"allowCredentials"`
				MaxAge           int      `json:"maxAge"`
			} `json:"corsConfiguration"`
		} `json:"items"`
	}
	if err := b.apigwGet(ctx, "/v2/apis", &out); err != nil {
		return nil, err
	}
	apis := make([]HTTPAPI, 0, len(out.Items))
	for _, a := range out.Items {
		api := HTTPAPI{ID: a.ID, Name: a.Name, Description: a.Description, Endpoint: a.Endpoint, Created: a.Created}
		if a.CORS != nil {
			api.CORS = &HTTPCORS{Origins: a.CORS.AllowOrigins, Methods: a.CORS.AllowMethods, Headers: a.CORS.AllowHeaders,
				Expose: a.CORS.ExposeHeaders, Credentials: a.CORS.AllowCredentials, MaxAge: a.CORS.MaxAge}
		}
		apis = append(apis, api)
	}
	sort.Slice(apis, func(i, j int) bool { return apis[i].Name < apis[j].Name })
	return apis, nil
}

func (b *backend) HTTPAPI(ctx context.Context, id string) (*HTTPAPI, error) {
	apis, err := b.ListHTTPAPIs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range apis {
		if apis[i].ID == id {
			return &apis[i], nil
		}
	}
	return nil, fmt.Errorf("http api %s does not exist", id)
}

// CreateHTTPAPI creates an HTTP API, with CORS open to every origin when
// asked — the local default a browser app wants.
func (b *backend) CreateHTTPAPI(ctx context.Context, name string, cors bool) (string, error) {
	body := map[string]any{"name": name, "protocolType": "HTTP"}
	if cors {
		body["corsConfiguration"] = map[string]any{"allowOrigins": []string{"*"}, "allowMethods": []string{"*"}, "allowHeaders": []string{"*"}}
	}
	out, err := b.apigwJSON(ctx, "POST", "/v2/apis", body)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"apiId"`
	}
	return created.ID, decodeJSON(out, &created)
}

func (b *backend) DeleteHTTPAPI(ctx context.Context, id string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/v2/apis/"+url.PathEscape(id), nil)
	return err
}

// UpdateHTTPAPI patches the name, description and CORS (UpdateApi); an
// empty origin list removes the CORS configuration (DeleteCorsConfiguration).
func (b *backend) UpdateHTTPAPI(ctx context.Context, id, name, description string, cors *HTTPCORS) error {
	body := map[string]any{"name": name, "description": description}
	if cors != nil {
		body["corsConfiguration"] = map[string]any{
			"allowOrigins": cors.Origins, "allowMethods": cors.Methods, "allowHeaders": cors.Headers,
			"exposeHeaders": cors.Expose, "allowCredentials": cors.Credentials, "maxAge": cors.MaxAge,
		}
	}
	if _, err := b.apigwJSON(ctx, "PATCH", "/v2/apis/"+url.PathEscape(id), body); err != nil {
		return err
	}
	if cors == nil {
		_, err := b.apigwJSON(ctx, "DELETE", "/v2/apis/"+url.PathEscape(id)+"/cors", nil)
		return err
	}
	return nil
}

// HTTPRoutes lists the routes with their integrations resolved
// (GetRoutes + GetIntegrations).
func (b *backend) HTTPRoutes(ctx context.Context, apiID string) ([]HTTPRoute, error) {
	var integs struct {
		Items []struct {
			ID      string `json:"integrationId"`
			Type    string `json:"integrationType"`
			URI     string `json:"integrationUri"`
			Payload string `json:"payloadFormatVersion"`
		} `json:"items"`
	}
	if err := b.apigwGet(ctx, "/v2/apis/"+url.PathEscape(apiID)+"/integrations", &integs); err != nil {
		return nil, err
	}
	byID := map[string]HTTPRoute{}
	for _, in := range integs.Items {
		rt := HTTPRoute{IntegrationID: in.ID, Type: in.Type, Target: in.URI, PayloadFormat: in.Payload}
		if in.Type == "AWS_PROXY" {
			rt.Lambda = functionOfInvokeURI(in.URI)
			rt.Target = rt.Lambda
		}
		byID[in.ID] = rt
	}
	var routes struct {
		Items []struct {
			ID       string `json:"routeId"`
			Key      string `json:"routeKey"`
			Target   string `json:"target"`
			AuthType string `json:"authorizationType"`
			AuthID   string `json:"authorizerId"`
		} `json:"items"`
	}
	if err := b.apigwGet(ctx, "/v2/apis/"+url.PathEscape(apiID)+"/routes", &routes); err != nil {
		return nil, err
	}
	out := make([]HTTPRoute, 0, len(routes.Items))
	for _, r := range routes.Items {
		rt := byID[strings.TrimPrefix(r.Target, "integrations/")]
		rt.ID, rt.Key, rt.AuthType, rt.AuthID = r.ID, r.Key, r.AuthType, r.AuthID
		if r.Key == "$default" {
			rt.Method, rt.Path = "ANY", "$default"
		} else {
			rt.Method, rt.Path, _ = strings.Cut(r.Key, " ")
		}
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out, nil
}

// decodeJSON unmarshals a control-plane response.
func decodeJSON(raw []byte, out any) error { return json.Unmarshal(raw, out) }

// logGroupOfARN reads the group name out of a log-group ARN.
func logGroupOfARN(arn string) string {
	if _, rest, ok := strings.Cut(arn, ":log-group:"); ok {
		name, _, _ := strings.Cut(rest, ":")
		return name
	}
	return ""
}

// AddHTTPRoute creates the integration and the route in one go: a Lambda
// function (payload 2.0 unless told otherwise) or an HTTP URL, and an
// optional authorizer (CreateIntegration, CreateRoute).
func (b *backend) AddHTTPRoute(ctx context.Context, apiID, key, lambda, urlTarget, payload, authorizerID string) error {
	integ := map[string]any{}
	switch {
	case lambda != "":
		integ["integrationType"], integ["integrationUri"] = "AWS_PROXY", awsident.ARN("lambda", "function:"+lambda)
		integ["payloadFormatVersion"] = firstOf(payload, "2.0")
	case urlTarget != "":
		integ["integrationType"], integ["integrationUri"], integ["integrationMethod"] = "HTTP_PROXY", urlTarget, "ANY"
	default:
		return fmt.Errorf("a route needs a function or a URL to forward to")
	}
	out, err := b.apigwJSON(ctx, "POST", "/v2/apis/"+url.PathEscape(apiID)+"/integrations", integ)
	if err != nil {
		return err
	}
	var created struct {
		ID string `json:"integrationId"`
	}
	if err := decodeJSON(out, &created); err != nil {
		return err
	}
	route := map[string]any{"routeKey": key, "target": "integrations/" + created.ID}
	if authorizerID != "" {
		route["authorizationType"], route["authorizerId"] = "CUSTOM", authorizerID
	}
	if _, err := b.apigwJSON(ctx, "POST", "/v2/apis/"+url.PathEscape(apiID)+"/routes", route); err != nil {
		// The route was refused: do not leave its integration behind.
		b.apigwJSON(ctx, "DELETE", "/v2/apis/"+url.PathEscape(apiID)+"/integrations/"+created.ID, nil)
		return err
	}
	return nil
}

// DeleteHTTPRoute removes a route and the integration it alone used
// (DeleteRoute, DeleteIntegration).
func (b *backend) DeleteHTTPRoute(ctx context.Context, apiID, routeID string) error {
	routes, err := b.HTTPRoutes(ctx, apiID)
	if err != nil {
		return err
	}
	integ, shared := "", false
	for _, rt := range routes {
		if rt.ID == routeID {
			integ = rt.IntegrationID
		} else if integ != "" && rt.IntegrationID == integ {
			shared = true
		}
	}
	for _, rt := range routes {
		if rt.ID != routeID && rt.IntegrationID == integ {
			shared = true
		}
	}
	if _, err := b.apigwJSON(ctx, "DELETE", "/v2/apis/"+url.PathEscape(apiID)+"/routes/"+url.PathEscape(routeID), nil); err != nil {
		return err
	}
	if integ != "" && !shared {
		_, err = b.apigwJSON(ctx, "DELETE", "/v2/apis/"+url.PathEscape(apiID)+"/integrations/"+url.PathEscape(integ), nil)
	}
	return err
}

func (b *backend) HTTPStages(ctx context.Context, apiID, host string) ([]HTTPStage, error) {
	var out struct {
		Items []struct {
			Name        string            `json:"stageName"`
			Deployment  string            `json:"deploymentId"`
			Description string            `json:"description"`
			AutoDeploy  bool              `json:"autoDeploy"`
			Variables   map[string]string `json:"stageVariables"`
			AccessLog   *struct {
				DestinationARN string `json:"destinationArn"`
			} `json:"accessLogSettings"`
		} `json:"items"`
	}
	if err := b.apigwGet(ctx, "/v2/apis/"+url.PathEscape(apiID)+"/stages", &out); err != nil {
		return nil, err
	}
	stages := make([]HTTPStage, 0, len(out.Items))
	for _, s := range out.Items {
		st := HTTPStage{Name: s.Name, DeploymentID: s.Deployment, Description: s.Description, AutoDeploy: s.AutoDeploy, Variables: s.Variables}
		st.InvokeURL = "http://" + host + "/_aws/execute-api/" + apiID
		if s.Name != "$default" {
			st.InvokeURL += "/" + s.Name
		}
		if s.AccessLog != nil {
			st.LogGroup = logGroupOfARN(s.AccessLog.DestinationARN)
		}
		stages = append(stages, st)
	}
	sort.Slice(stages, func(i, j int) bool { return stages[i].Name < stages[j].Name })
	return stages, nil
}

func (b *backend) CreateHTTPStage(ctx context.Context, apiID, name string, autoDeploy bool) error {
	_, err := b.apigwJSON(ctx, "POST", "/v2/apis/"+url.PathEscape(apiID)+"/stages", map[string]any{"stageName": name, "autoDeploy": autoDeploy})
	return err
}

func (b *backend) DeleteHTTPStage(ctx context.Context, apiID, name string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/v2/apis/"+url.PathEscape(apiID)+"/stages/"+url.PathEscape(name), nil)
	return err
}

// DeployHTTPStage makes a deployment and points the stage at it
// (CreateDeployment) — what a stage without autoDeploy needs after a change.
func (b *backend) DeployHTTPStage(ctx context.Context, apiID, stage string) error {
	_, err := b.apigwJSON(ctx, "POST", "/v2/apis/"+url.PathEscape(apiID)+"/deployments", map[string]any{"stageName": stage})
	return err
}

func (b *backend) HTTPAuthorizers(ctx context.Context, apiID string) ([]HTTPAuthorizer, error) {
	var out struct {
		Items []struct {
			ID      string   `json:"authorizerId"`
			Name    string   `json:"name"`
			URI     string   `json:"authorizerUri"`
			Sources []string `json:"identitySource"`
			Payload string   `json:"authorizerPayloadFormatVersion"`
			Simple  bool     `json:"enableSimpleResponses"`
			TTL     int      `json:"authorizerResultTtlInSeconds"`
		} `json:"items"`
	}
	if err := b.apigwGet(ctx, "/v2/apis/"+url.PathEscape(apiID)+"/authorizers", &out); err != nil {
		return nil, err
	}
	auths := make([]HTTPAuthorizer, 0, len(out.Items))
	for _, a := range out.Items {
		auths = append(auths, HTTPAuthorizer{ID: a.ID, Name: a.Name, Lambda: functionOfInvokeURI(a.URI),
			IdentitySource: a.Sources, PayloadFormat: a.Payload, SimpleResponses: a.Simple, TTL: a.TTL})
	}
	sort.Slice(auths, func(i, j int) bool { return auths[i].Name < auths[j].Name })
	return auths, nil
}

// CreateHTTPAuthorizer makes a REQUEST authorizer on a function, reading
// the named header, answering the simple response (CreateAuthorizer).
func (b *backend) CreateHTTPAuthorizer(ctx context.Context, apiID, name, lambda, header string, ttl int) error {
	_, err := b.apigwJSON(ctx, "POST", "/v2/apis/"+url.PathEscape(apiID)+"/authorizers", map[string]any{
		"name": name, "authorizerType": "REQUEST",
		"authorizerUri":                  "arn:aws:apigateway:" + awsident.Region + ":lambda:path/2015-03-31/functions/" + awsident.ARN("lambda", "function:"+lambda) + "/invocations",
		"identitySource":                 []string{"$request.header." + firstOf(header, "Authorization")},
		"authorizerPayloadFormatVersion": "2.0", "enableSimpleResponses": true,
		"authorizerResultTtlInSeconds": ttl,
	})
	return err
}

func (b *backend) DeleteHTTPAuthorizer(ctx context.Context, apiID, id string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/v2/apis/"+url.PathEscape(apiID)+"/authorizers/"+url.PathEscape(id), nil)
	return err
}

// InvokeHTTPAPI sends a request through the execute-api plane; the
// $default stage is at the API's root.
func (b *backend) InvokeHTTPAPI(ctx context.Context, apiID, stage, method, path, body string) (*APICallResult, error) {
	if stage == "$default" || stage == "" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		res, err := b.InvokeAPI(ctx, apiID, "$default", method, path, body)
		if res != nil {
			res.URL = "/_aws/execute-api/" + apiID + path
		}
		return res, err
	}
	return b.InvokeAPI(ctx, apiID, stage, method, path, body)
}

func firstOf(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ListAllAPIs is the sidebar's list: REST APIs and HTTP APIs together, by
// name, the HTTP ones marked so the template links them to their own page.
func (b *backend) ListAllAPIs(ctx context.Context) ([]RestAPI, error) {
	rest, err := b.ListRestAPIs(ctx)
	if err != nil {
		return nil, err
	}
	https, err := b.ListHTTPAPIs(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range https {
		rest = append(rest, RestAPI{ID: h.ID, Name: h.Name, Description: h.Description, Created: h.Created, Protocol: "HTTP"})
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	return rest, nil
}
