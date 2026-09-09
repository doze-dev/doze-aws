package cloudformation

// API Gateway's resource tree as CloudFormation and CDK spell it:
// AWS::ApiGateway::Resource nodes hang off the API's root by ParentId and
// PathPart, AWS::ApiGateway::Method attaches a verb with its integration and
// authorization, and AWS::ApiGateway::Authorizer names a Lambda authorizer.
// The paths are rebuilt from the parent chain once every resource is known,
// and each method becomes one route on the API. SAM's Auth block on a
// Serverless::Api and on an Api event maps onto the same fields.

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

// apiNode is one AWS::ApiGateway::Resource as declared: resolved to a path
// in the deferred pass.
type apiNode struct {
	api      string
	parent   string // "root", or another resource's logical id
	pathPart string
}

// apiMethodDecl is one AWS::ApiGateway::Method as declared.
type apiMethodDecl struct {
	logical  string
	api      string
	resource string // "root" or a resource's logical id
	route    provision.Route
}

func (m *mapper) apiAuthorizer(name string, props map[string]any) error {
	api := propStr(props, "RestApiId")
	if api == "" {
		return fmt.Errorf("RestApiId is required")
	}
	typ := propStr(props, "Type")
	switch typ {
	case "TOKEN", "REQUEST":
	case "COGNITO_USER_POOLS":
		return fmt.Errorf("COGNITO_USER_POOLS authorizers need a Cognito user pool, which does not exist locally; use a TOKEN or REQUEST Lambda authorizer")
	default:
		return fmt.Errorf("Type %q: doze-aws runs TOKEN and REQUEST Lambda authorizers", typ)
	}
	fn := functionOfInvokeURI(propStr(props, "AuthorizerUri"))
	if fn == "" {
		return fmt.Errorf("AuthorizerUri must name a Lambda function")
	}
	a := provision.APIAuthorizer{
		Type: typ, Lambda: fn, IdentitySource: propStr(props, "IdentitySource"),
		Validation: propStr(props, "IdentityValidationExpression"),
	}
	if _, ok := props["AuthorizerResultTtlInSeconds"]; ok {
		ttl := propInt(props, "AuthorizerResultTtlInSeconds")
		a.TTL = &ttl
	}
	m.deferred = append(m.deferred, func() error {
		rest := m.stack.APIs[api]
		if rest.Authorizers == nil {
			rest.Authorizers = map[string]provision.APIAuthorizer{}
		}
		rest.Authorizers[name] = a
		m.stack.APIs[api] = rest
		return nil
	})
	return nil
}

func (m *mapper) apiResource(logical string, props map[string]any) error {
	parent := propStr(props, "ParentId")
	if parent == "" {
		return fmt.Errorf("ParentId is required")
	}
	m.apiNodes[logical] = apiNode{api: propStr(props, "RestApiId"), parent: parent, pathPart: propStr(props, "PathPart")}
	return nil
}

func (m *mapper) apiMethod(logical string, props map[string]any) error {
	decl := apiMethodDecl{logical: logical, api: propStr(props, "RestApiId"), resource: propStr(props, "ResourceId")}
	decl.route.Method = strings.ToUpper(propStr(props, "HttpMethod"))
	integ := propMap(props, "Integration")
	switch typ := propStr(integ, "Type"); typ {
	case "AWS_PROXY":
		fn := functionOfInvokeURI(propStr(integ, "Uri"))
		if fn == "" {
			return fmt.Errorf("Integration.Uri must name a Lambda function")
		}
		decl.route.Lambda = fn
	case "MOCK":
		decl.route.Mock = mockOf(integ)
	case "":
		return fmt.Errorf("Integration is required")
	default:
		return fmt.Errorf("Integration.Type %q: doze-aws deploys AWS_PROXY and MOCK integrations from a template", typ)
	}
	switch auth := propStr(props, "AuthorizationType"); auth {
	case "", "NONE":
		decl.route.Authorizer = "NONE"
	case "CUSTOM":
		id := propStr(props, "AuthorizerId")
		if id == "" {
			return fmt.Errorf("AuthorizationType CUSTOM needs an AuthorizerId")
		}
		decl.route.Authorizer = id // the authorizer's name: Ref on one yields it
	case "AWS_IAM":
		// Signature checks do not run locally; the route is open.
		decl.route.Authorizer = "NONE"
	default:
		return fmt.Errorf("AuthorizationType %q: doze-aws serves NONE, AWS_IAM (unchecked) and CUSTOM (a Lambda authorizer)", auth)
	}
	if _, ok := props["ApiKeyRequired"]; ok {
		required := propBool(props, "ApiKeyRequired")
		decl.route.APIKeyRequired = &required
	}
	m.apiMethods = append(m.apiMethods, decl)
	return nil
}

// mockOf reads a MOCK integration's first integration response into the
// status, headers and body the route answers.
func mockOf(integ map[string]any) *provision.MockRoute {
	mock := &provision.MockRoute{Status: 200}
	for _, item := range propList(integ, "IntegrationResponses") {
		ir, ok := item.(map[string]any)
		if !ok {
			continue
		}
		mock.Status = propInt(ir, "StatusCode")
		if hs := propMap(ir, "ResponseParameters"); len(hs) > 0 {
			mock.Headers = map[string]string{}
			for k, v := range hs {
				if name, ok := strings.CutPrefix(k, "method.response.header."); ok {
					mock.Headers[name] = strings.Trim(fmt.Sprint(v), "'")
				}
			}
		}
		if ts := propMap(ir, "ResponseTemplates"); ts != nil {
			mock.Body = propStr(ts, "application/json")
		}
		break
	}
	return mock
}

// resolveAPITree turns the declared resources and methods into routes, once
// every resource is known: each method's path is its resource's chain of
// PathParts up to the root.
func (m *mapper) resolveAPITree() error {
	pathOf := func(id string) (string, string, error) {
		var parts []string
		api := ""
		seen := map[string]bool{}
		for id != "root" {
			node, ok := m.apiNodes[id]
			if !ok {
				return "", "", fmt.Errorf("resource %q is not declared in the template", id)
			}
			if seen[id] {
				return "", "", fmt.Errorf("resource %q is its own ancestor", id)
			}
			seen[id] = true
			parts = append([]string{node.pathPart}, parts...)
			api, id = node.api, node.parent
		}
		return api, "/" + strings.Join(parts, "/"), nil
	}
	for _, decl := range m.apiMethods {
		api, path, err := pathOf(decl.resource)
		if err != nil {
			return fmt.Errorf("method %s: %w", decl.logical, err)
		}
		if decl.api != "" {
			api = decl.api
		}
		if api == "" {
			return fmt.Errorf("method %s: RestApiId is required", decl.logical)
		}
		route := decl.route
		route.Path = path
		rest := m.stack.APIs[api]
		rest.Routes = append(rest.Routes, route)
		m.stack.APIs[api] = rest
	}
	return nil
}

// samAuth reads a Serverless::Api's Auth block: the authorizers it declares
// (Lambda ones; Cognito refused), the default, and ApiKeyRequired.
func samAuth(api *provision.API, auth map[string]any) error {
	if auth == nil {
		return nil
	}
	api.DefaultAuthorizer = propStr(auth, "DefaultAuthorizer")
	api.APIKeyRequired = propBool(auth, "ApiKeyRequired")
	for name, raw := range propMap(auth, "Authorizers") {
		spec, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if propStr(spec, "UserPoolArn") != "" || propList(spec, "UserPoolArn") != nil {
			return fmt.Errorf("authorizer %s: Cognito user pool authorizers do not exist locally; use a Lambda authorizer", name)
		}
		fn := nameFromARN(propStr(spec, "FunctionArn"))
		if fn == "" {
			return fmt.Errorf("authorizer %s: FunctionArn is required", name)
		}
		a := provision.APIAuthorizer{Type: "TOKEN", Lambda: fn}
		if propStr(spec, "FunctionPayloadType") == "REQUEST" {
			a.Type = "REQUEST"
		}
		if ident := propMap(spec, "Identity"); ident != nil {
			if a.Type == "TOKEN" {
				a.IdentitySource = propStr(ident, "Header")
				a.Validation = propStr(ident, "ValidationExpression")
			} else {
				var sources []string
				for _, h := range propList(ident, "Headers") {
					sources = append(sources, "method.request.header."+fmt.Sprint(h))
				}
				for _, q := range propList(ident, "QueryStrings") {
					sources = append(sources, "method.request.querystring."+fmt.Sprint(q))
				}
				a.IdentitySource = strings.Join(sources, ",")
			}
			if _, ok := ident["ReauthorizeEvery"]; ok {
				ttl := propInt(ident, "ReauthorizeEvery")
				a.TTL = &ttl
			}
		}
		if api.Authorizers == nil {
			api.Authorizers = map[string]provision.APIAuthorizer{}
		}
		api.Authorizers[name] = a
	}
	return nil
}

// functionOfInvokeURI reads the function name out of an API Gateway Lambda
// invocation URI (…/functions/<function arn>/invocations).
func functionOfInvokeURI(uri string) string {
	_, rest, ok := strings.Cut(uri, ":function:")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	name, _, _ = strings.Cut(name, ":")
	return name
}
