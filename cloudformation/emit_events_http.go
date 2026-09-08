package cloudformation

// Export of EventBridge connections and API destinations. Secrets are
// exported blank: the service never reports them, and a template with a
// placeholder is more useful than one that fails to parse.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitEventsHTTP(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Connections) {
		c := s.Connections[name]
		auth := map[string]any{}
		props := map[string]any{"Name": name}
		putIfStr(props, "Description", c.Description)
		switch {
		case c.Basic != nil:
			props["AuthorizationType"] = "BASIC"
			auth["BasicAuthParameters"] = map[string]any{"Username": c.Basic.Username, "Password": c.Basic.Password}
		case c.APIKey != nil:
			props["AuthorizationType"] = "API_KEY"
			auth["ApiKeyAuthParameters"] = map[string]any{"ApiKeyName": c.APIKey.Name, "ApiKeyValue": c.APIKey.Value}
		case c.OAuth != nil:
			props["AuthorizationType"] = "OAUTH_CLIENT_CREDENTIALS"
			o := map[string]any{
				"ClientParameters":      map[string]any{"ClientID": c.OAuth.ClientID, "ClientSecret": c.OAuth.ClientSecret},
				"AuthorizationEndpoint": c.OAuth.Endpoint,
				"HttpMethod":            orDefault(c.OAuth.Method, "POST"),
			}
			if p := connectionParamsProps(c.OAuth.Params); len(p) > 0 {
				o["OAuthHttpParameters"] = p
			}
			auth["OAuthParameters"] = o
		}
		if p := connectionParamsProps(c.Invocation); len(p) > 0 {
			auth["InvocationHttpParameters"] = p
		}
		props["AuthParameters"] = auth
		add("Connection", name, "AWS::Events::Connection", props)
	}
	for _, name := range sortedNames(s.APIDestinations) {
		d := s.APIDestinations[name]
		props := map[string]any{
			"Name":               name,
			"ConnectionArn":      map[string]any{"Fn::GetAtt": []any{logicalID("Connection", d.Connection), "Arn"}},
			"InvocationEndpoint": d.Endpoint,
			"HttpMethod":         orDefault(d.Method, "POST"),
		}
		putIfStr(props, "Description", d.Description)
		putIfNum(props, "InvocationRateLimitPerSecond", d.RateLimit)
		add("ApiDestination", name, "AWS::Events::ApiDestination", props)
	}
}

func connectionParamsProps(p provision.ConnectionParams) map[string]any {
	render := func(ps []provision.ConnectionParam) []any {
		var out []any
		for _, x := range ps {
			m := map[string]any{"Key": x.Key, "Value": x.Value}
			putIf(m, "IsValueSecret", x.Secret)
			out = append(out, m)
		}
		return out
	}
	out := map[string]any{}
	if len(p.Headers) > 0 {
		out["HeaderParameters"] = render(p.Headers)
	}
	if len(p.Query) > 0 {
		out["QueryStringParameters"] = render(p.Query)
	}
	if len(p.Body) > 0 {
		out["BodyParameters"] = render(p.Body)
	}
	return out
}

// apiDestinationTargetProps is the target block for an API destination.
func apiDestinationTargetProps(m map[string]any, t provision.Target) {
	m["Arn"] = map[string]any{"Fn::GetAtt": []any{logicalID("ApiDestination", t.APIDestination), "Arn"}}
	hp := map[string]any{}
	putIfList(hp, "PathParameterValues", t.PathParams)
	if len(t.Headers) > 0 {
		hp["HeaderParameters"] = t.Headers
	}
	if len(t.Query) > 0 {
		hp["QueryStringParameters"] = t.Query
	}
	if len(hp) > 0 {
		m["HttpParameters"] = hp
	}
}
