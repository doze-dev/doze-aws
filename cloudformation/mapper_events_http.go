package cloudformation

// AWS::Events::Connection and AWS::Events::ApiDestination, and the
// api-destination arm of a rule target.

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

func (m *mapper) connection(name string, props map[string]any) error {
	auth := propMap(props, "AuthParameters")
	conn := provision.Connection{
		Description: propStr(props, "Description"),
		Invocation:  connectionParamsFrom(propMap(auth, "InvocationHttpParameters")),
	}
	switch typ := propStr(props, "AuthorizationType"); typ {
	case "BASIC":
		b := propMap(auth, "BasicAuthParameters")
		conn.Basic = &provision.BasicAuth{Username: propStr(b, "Username"), Password: propStr(b, "Password")}
	case "API_KEY":
		k := propMap(auth, "ApiKeyAuthParameters")
		conn.APIKey = &provision.APIKeyAuth{Name: propStr(k, "ApiKeyName"), Value: propStr(k, "ApiKeyValue")}
	case "OAUTH_CLIENT_CREDENTIALS":
		o := propMap(auth, "OAuthParameters")
		cp := propMap(o, "ClientParameters")
		conn.OAuth = &provision.OAuthAuth{
			ClientID: propStr(cp, "ClientID"), ClientSecret: propStr(cp, "ClientSecret"),
			Endpoint: propStr(o, "AuthorizationEndpoint"), Method: propStr(o, "HttpMethod"),
			Params: connectionParamsFrom(propMap(o, "OAuthHttpParameters")),
		}
	default:
		return fmt.Errorf("AuthorizationType %q: doze-aws supports BASIC, API_KEY and OAUTH_CLIENT_CREDENTIALS", typ)
	}
	if propMap(auth, "ConnectivityParameters") != nil || propMap(props, "InvocationConnectivityParameters") != nil {
		return fmt.Errorf("private API destinations through VPC Lattice are cloud infrastructure; doze-aws delivers over the local network")
	}
	if m.stack.Connections == nil {
		m.stack.Connections = map[string]provision.Connection{}
	}
	m.stack.Connections[name] = conn
	return nil
}

// connectionParamsFrom reads a ConnectionHttpParameters block.
func connectionParamsFrom(block map[string]any) provision.ConnectionParams {
	read := func(key string) []provision.ConnectionParam {
		var out []provision.ConnectionParam
		for _, item := range propList(block, key) {
			p, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, provision.ConnectionParam{Key: propStr(p, "Key"), Value: propStr(p, "Value"), Secret: propBool(p, "IsValueSecret")})
		}
		return out
	}
	return provision.ConnectionParams{Headers: read("HeaderParameters"), Query: read("QueryStringParameters"), Body: read("BodyParameters")}
}

func (m *mapper) apiDestination(name string, props map[string]any) error {
	connARN := propStr(props, "ConnectionArn")
	connName := connectionNameFromARN(connARN)
	if connName == "" {
		return fmt.Errorf("ConnectionArn %q is not a connection ARN", connARN)
	}
	d := provision.APIDestination{
		Description: propStr(props, "Description"),
		Connection:  connName,
		Endpoint:    propStr(props, "InvocationEndpoint"),
		Method:      propStr(props, "HttpMethod"),
		RateLimit:   propInt(props, "InvocationRateLimitPerSecond"),
	}
	if m.stack.APIDestinations == nil {
		m.stack.APIDestinations = map[string]provision.APIDestination{}
	}
	m.stack.APIDestinations[name] = d
	return nil
}

// connectionNameFromARN reads arn:...:connection/<name>[/<id>]; a bare name
// (a template that Refs the connection) is returned as is.
func connectionNameFromARN(v string) string {
	if !strings.HasPrefix(v, "arn:") {
		return v
	}
	_, rest, ok := strings.Cut(v, ":connection/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

// apiDestinationTarget fills a rule target that points at an API destination.
func apiDestinationTarget(t *provision.Target, arn string, tgt map[string]any) {
	_, rest, _ := strings.Cut(arn, ":api-destination/")
	t.APIDestination, _, _ = strings.Cut(rest, "/")
	hp := propMap(tgt, "HttpParameters")
	if hp == nil {
		return
	}
	for _, v := range propList(hp, "PathParameterValues") {
		t.PathParams = append(t.PathParams, fmt.Sprint(v))
	}
	if h := propMap(hp, "HeaderParameters"); len(h) > 0 {
		t.Headers = map[string]string{}
		for k, v := range h {
			t.Headers[k] = fmt.Sprint(v)
		}
	}
	if q := propMap(hp, "QueryStringParameters"); len(q) > 0 {
		t.Query = map[string]string{}
		for k, v := range q {
			t.Query[k] = fmt.Sprint(v)
		}
	}
}
