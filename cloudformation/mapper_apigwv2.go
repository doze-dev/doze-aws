package cloudformation

// HTTP APIs (AWS::ApiGatewayV2::*) and SAM's HttpApi, onto the same
// route-shaped IR a REST API uses, with Protocol "HTTP". An Integration is
// declared by logical id and a Route names it through its Target
// ("integrations/<Ref>"), so routes resolve in the deferred pass once every
// integration is known. WebSocket APIs, JWT authorizers and AWS service
// integrations are refused by name.

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

// v2Integration is one AWS::ApiGatewayV2::Integration as declared.
type v2Integration struct {
	api           string
	lambda        string
	httpTarget    string
	payloadFormat string
}

func (m *mapper) httpAPI(name string, props map[string]any) error {
	if pt := strings.ToUpper(propStr(props, "ProtocolType")); pt == "WEBSOCKET" {
		return fmt.Errorf("WebSocket APIs are not implemented locally; ProtocolType must be HTTP")
	}
	api := m.stack.APIs[name]
	api.Protocol = "HTTP"
	if cors := propMap(props, "CorsConfiguration"); cors != nil {
		api.CORS = corsOf(cors)
	}
	// SAM's HttpApi carries the stage and its access log on the API itself.
	if st := propStr(props, "StageName"); st != "" {
		api.Stage = st
	}
	stageLogging(&api, props)
	if err := samHTTPAuth(&api, propMap(props, "Auth")); err != nil {
		return err
	}
	// Quick create: a Target makes one route, the key given or $default.
	if target := propStr(props, "Target"); target != "" {
		route := provision.Route{Path: "$default"}
		if key := propStr(props, "RouteKey"); key != "" && key != "$default" {
			route.Method, route.Path, _ = strings.Cut(key, " ")
		}
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			route.HTTPTarget = target
		} else {
			route.Lambda = nameFromARN(target)
		}
		api.Routes = append(api.Routes, route)
	}
	m.stack.APIs[name] = api
	return nil
}

func corsOf(cors map[string]any) *provision.APICORS {
	c := &provision.APICORS{
		AllowOrigins: strList(propList(cors, "AllowOrigins")), AllowMethods: strList(propList(cors, "AllowMethods")),
		AllowHeaders: strList(propList(cors, "AllowHeaders")), ExposeHeaders: strList(propList(cors, "ExposeHeaders")),
		AllowCredentials: propBool(cors, "AllowCredentials"),
	}
	if _, ok := cors["MaxAge"]; ok {
		age := propInt(cors, "MaxAge")
		c.MaxAge = &age
	}
	return c
}

func (m *mapper) v2Integration(logical string, props map[string]any) error {
	api := nameFromARN(propStr(props, "ApiId"))
	if api == "" {
		return fmt.Errorf("ApiId is required")
	}
	decl := v2Integration{api: api, payloadFormat: propStr(props, "PayloadFormatVersion")}
	if sub := propStr(props, "IntegrationSubtype"); sub != "" {
		return fmt.Errorf("IntegrationSubtype %s: AWS service integrations are not implemented locally; integrate with a function", sub)
	}
	uri := propStr(props, "IntegrationUri")
	switch typ := strings.ToUpper(propStr(props, "IntegrationType")); typ {
	case "AWS_PROXY":
		decl.lambda = firstNonEmpty(functionOfInvokeURI(uri), nameFromARN(uri))
		if decl.lambda == "" {
			return fmt.Errorf("IntegrationUri must name a Lambda function")
		}
	case "HTTP_PROXY":
		if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
			return fmt.Errorf("an HTTP_PROXY integration needs an http(s) IntegrationUri")
		}
		decl.httpTarget = uri
	default:
		return fmt.Errorf("IntegrationType %q: an HTTP API integration is AWS_PROXY or HTTP_PROXY", typ)
	}
	m.v2Integrations[logical] = decl
	return nil
}

func (m *mapper) v2Route(props map[string]any) error {
	api := nameFromARN(propStr(props, "ApiId"))
	if api == "" {
		return fmt.Errorf("ApiId is required")
	}
	key := propStr(props, "RouteKey")
	if key == "" {
		return fmt.Errorf("RouteKey is required")
	}
	target := strings.TrimPrefix(propStr(props, "Target"), "integrations/")
	authType := strings.ToUpper(propStr(props, "AuthorizationType"))
	authorizer := propStr(props, "AuthorizerId")
	if authType == "JWT" {
		return fmt.Errorf("route %s: JWT authorization is not implemented locally", key)
	}
	m.deferred = append(m.deferred, func() error {
		integ, ok := m.v2Integrations[target]
		if !ok {
			return fmt.Errorf("route %s: Target %q does not name an AWS::ApiGatewayV2::Integration of this template", key, target)
		}
		route := provision.Route{Lambda: integ.lambda, HTTPTarget: integ.httpTarget, PayloadFormat: integ.payloadFormat}
		if key == "$default" {
			route.Path = "$default"
		} else {
			route.Method, route.Path, _ = strings.Cut(key, " ")
		}
		switch authType {
		case "CUSTOM":
			if authorizer == "" {
				return fmt.Errorf("route %s: AuthorizationType CUSTOM needs AuthorizerId", key)
			}
			route.Authorizer = authorizer
		case "NONE":
			route.Authorizer = "NONE"
		}
		rest := m.stack.APIs[api]
		rest.Protocol = "HTTP"
		rest.Routes = append(rest.Routes, route)
		m.stack.APIs[api] = rest
		return nil
	})
	return nil
}

func (m *mapper) v2Stage(props map[string]any) error {
	api := nameFromARN(propStr(props, "ApiId"))
	if api == "" {
		return fmt.Errorf("ApiId is required")
	}
	stageName := propStr(props, "StageName")
	m.deferred = append(m.deferred, func() error {
		rest := m.stack.APIs[api]
		rest.Protocol = "HTTP"
		if stageName != "" {
			rest.Stage = stageName
		}
		if al := propMap(props, "AccessLogSettings"); al != nil {
			rest.AccessLog = &provision.APIAccessLog{DestinationARN: propStr(al, "DestinationArn"), Format: propStr(al, "Format")}
		}
		m.stack.APIs[api] = rest
		return nil
	})
	return nil
}

func (m *mapper) v2Authorizer(name string, props map[string]any) error {
	api := nameFromARN(propStr(props, "ApiId"))
	if api == "" {
		return fmt.Errorf("ApiId is required")
	}
	if typ := strings.ToUpper(propStr(props, "AuthorizerType")); typ != "REQUEST" {
		return fmt.Errorf("AuthorizerType %q: doze-aws runs REQUEST Lambda authorizers; JWT needs an identity provider that does not exist locally", typ)
	}
	fn := functionOfInvokeURI(propStr(props, "AuthorizerUri"))
	if fn == "" {
		return fmt.Errorf("AuthorizerUri must name a Lambda function")
	}
	a := provision.APIAuthorizer{
		Type: "REQUEST", Lambda: fn,
		IdentitySource:  strings.Join(strList(propList(props, "IdentitySource")), ","),
		PayloadFormat:   propStr(props, "AuthorizerPayloadFormatVersion"),
		SimpleResponses: propBool(props, "EnableSimpleResponses"),
	}
	if _, ok := props["AuthorizerResultTtlInSeconds"]; ok {
		ttl := propInt(props, "AuthorizerResultTtlInSeconds")
		a.TTL = &ttl
	}
	m.deferred = append(m.deferred, func() error {
		rest := m.stack.APIs[api]
		rest.Protocol = "HTTP"
		if rest.Authorizers == nil {
			rest.Authorizers = map[string]provision.APIAuthorizer{}
		}
		rest.Authorizers[name] = a
		m.stack.APIs[api] = rest
		return nil
	})
	return nil
}

// samHTTPAuth reads a Serverless::HttpApi's Auth block: Lambda authorizers
// with the HttpApi spelling (Identity.Headers, payload format, simple
// responses); JWT authorizers refused.
func samHTTPAuth(api *provision.API, auth map[string]any) error {
	if auth == nil {
		return nil
	}
	api.DefaultAuthorizer = propStr(auth, "DefaultAuthorizer")
	for name, raw := range propMap(auth, "Authorizers") {
		spec, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if propMap(spec, "JwtConfiguration") != nil || propStr(spec, "IdentitySource") != "" && propStr(spec, "FunctionArn") == "" {
			return fmt.Errorf("authorizer %s: JWT authorizers need an identity provider that does not exist locally; use a Lambda authorizer", name)
		}
		fn := nameFromARN(propStr(spec, "FunctionArn"))
		if fn == "" {
			return fmt.Errorf("authorizer %s: FunctionArn is required", name)
		}
		a := provision.APIAuthorizer{
			Type: "REQUEST", Lambda: fn,
			PayloadFormat:   propStr(spec, "AuthorizerPayloadFormatVersion"),
			SimpleResponses: propBool(spec, "EnableSimpleResponses"),
		}
		var sources []string
		if ident := propMap(spec, "Identity"); ident != nil {
			for _, h := range propList(ident, "Headers") {
				sources = append(sources, "$request.header."+fmt.Sprint(h))
			}
			for _, q := range propList(ident, "QueryStrings") {
				sources = append(sources, "$request.querystring."+fmt.Sprint(q))
			}
			if _, ok := ident["ReauthorizeEvery"]; ok {
				ttl := propInt(ident, "ReauthorizeEvery")
				a.TTL = &ttl
			}
		}
		a.IdentitySource = strings.Join(sources, ",")
		if api.Authorizers == nil {
			api.Authorizers = map[string]provision.APIAuthorizer{}
		}
		api.Authorizers[name] = a
	}
	return nil
}

// samHTTPEvent binds a SAM HttpApi event to its function: the implicit API
// is ServerlessHttpApi at $default, and an event with no Path or Method is
// the $default route.
func (m *mapper) samHTTPEvent(fn, evName string, props map[string]any) error {
	apiName := firstNonEmpty(nameFromARN(propStr(props, "ApiId")), "ServerlessHttpApi")
	api := m.stack.APIs[apiName]
	api.Protocol = "HTTP"
	route := provision.Route{Lambda: fn, PayloadFormat: propStr(props, "PayloadFormatVersion")}
	path := propStr(props, "Path")
	method := strings.ToUpper(propStr(props, "Method"))
	switch {
	case path == "" && method == "":
		route.Path = "$default"
	case path == "":
		return fmt.Errorf("SAM event %s: Path is required when Method is set", evName)
	default:
		route.Path = path
		route.Method = firstNonEmpty(method, "ANY")
	}
	if auth := propMap(props, "Auth"); auth != nil {
		route.Authorizer = propStr(auth, "Authorizer")
	}
	api.Routes = append(api.Routes, route)
	m.stack.APIs[apiName] = api
	return nil
}
