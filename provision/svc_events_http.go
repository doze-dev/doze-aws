package provision

// EventBridge connections and API destinations: apply, export, destroy.
//
// Neither Create call is an upsert, so apply creates and falls back to Update
// when the name exists. A destination names its connection by NAME; the ARN
// the service wants (it carries a generated id) is looked up at apply time.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func applyConnections(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Connections) {
		conn := s.Connections[name]
		in, err := connectionRequest(name, conn)
		if err != nil {
			return err
		}
		op, err := createOrUpdate(ctx, c, "CreateConnection", "UpdateConnection", in)
		if err != nil {
			return fmt.Errorf("connection %q: %w", name, err)
		}
		rep.add(op, "connection/"+name, "")
	}
	for _, name := range sortedNames(s.APIDestinations) {
		d := s.APIDestinations[name]
		connARN, err := connectionARN(ctx, c, d.Connection)
		if err != nil {
			return fmt.Errorf("api destination %q: %w", name, err)
		}
		in := map[string]any{
			"Name": name, "ConnectionArn": connARN,
			"InvocationEndpoint": d.Endpoint, "HttpMethod": strings.ToUpper(orDefaultStr(d.Method, "POST")),
		}
		if d.Description != "" {
			in["Description"] = d.Description
		}
		if d.RateLimit > 0 {
			in["InvocationRateLimitPerSecond"] = d.RateLimit
		}
		op, err := createOrUpdate(ctx, c, "CreateApiDestination", "UpdateApiDestination", in)
		if err != nil {
			return fmt.Errorf("api destination %q: %w", name, err)
		}
		rep.add(op, "api-destination/"+name, "")
	}
	return nil
}

// createOrUpdate tries the create action and, when the name exists, the
// update action with the same body. Returns the report op.
func createOrUpdate(ctx context.Context, c *client, create, update string, in map[string]any) (string, error) {
	_, err := c.json11(ctx, "AWSEvents", create, in)
	if err == nil {
		return "created", nil
	}
	var ae *apiErr
	if !asAPIErr(err, &ae) || !strings.Contains(ae.body, "AlreadyExists") {
		return "", err
	}
	if _, err := c.json11(ctx, "AWSEvents", update, in); err != nil {
		return "", err
	}
	return "updated", nil
}

// connectionRequest is the Create/UpdateConnection body for a connection.
func connectionRequest(name string, conn Connection) (map[string]any, error) {
	auth := map[string]any{}
	var authType string
	switch {
	case conn.Basic != nil:
		authType = "BASIC"
		auth["BasicAuthParameters"] = map[string]any{"Username": conn.Basic.Username, "Password": conn.Basic.Password}
	case conn.APIKey != nil:
		authType = "API_KEY"
		auth["ApiKeyAuthParameters"] = map[string]any{"ApiKeyName": conn.APIKey.Name, "ApiKeyValue": conn.APIKey.Value}
	case conn.OAuth != nil:
		authType = "OAUTH_CLIENT_CREDENTIALS"
		o := map[string]any{
			"ClientParameters":      map[string]any{"ClientID": conn.OAuth.ClientID, "ClientSecret": conn.OAuth.ClientSecret},
			"AuthorizationEndpoint": conn.OAuth.Endpoint,
			"HttpMethod":            strings.ToUpper(orDefaultStr(conn.OAuth.Method, "POST")),
		}
		if p := paramsRequest(conn.OAuth.Params); len(p) > 0 {
			o["OAuthHttpParameters"] = p
		}
		auth["OAuthParameters"] = o
	default:
		return nil, fmt.Errorf("connection %q: one of basic, api_key or oauth is required", name)
	}
	if p := paramsRequest(conn.Invocation); len(p) > 0 {
		auth["InvocationHttpParameters"] = p
	}
	in := map[string]any{"Name": name, "AuthorizationType": authType, "AuthParameters": auth}
	if conn.Description != "" {
		in["Description"] = conn.Description
	}
	return in, nil
}

func paramsRequest(p ConnectionParams) map[string]any {
	render := func(ps []ConnectionParam) []any {
		var out []any
		for _, x := range ps {
			out = append(out, map[string]any{"Key": x.Key, "Value": x.Value, "IsValueSecret": x.Secret})
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

// connectionARN resolves a connection name to the ARN the service minted.
func connectionARN(ctx context.Context, c *client, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("no connection named")
	}
	out, err := c.json11(ctx, "AWSEvents", "DescribeConnection", map[string]any{"Name": name})
	if err != nil {
		return "", fmt.Errorf("connection %q: %w", name, err)
	}
	var v struct{ ConnectionArn string }
	json.Unmarshal(out, &v)
	return v.ConnectionArn, nil
}

// apiDestinationARN resolves a destination name to its ARN.
func apiDestinationARN(ctx context.Context, c *client, name string) (string, error) {
	out, err := c.json11(ctx, "AWSEvents", "DescribeApiDestination", map[string]any{"Name": name})
	if err != nil {
		return "", fmt.Errorf("api destination %q: %w", name, err)
	}
	var v struct{ ApiDestinationArn string }
	json.Unmarshal(out, &v)
	return v.ApiDestinationArn, nil
}

func exportConnections(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "AWSEvents", "ListConnections", map[string]any{})
	if err != nil {
		return err
	}
	var list struct{ Connections []struct{ Name string } }
	json.Unmarshal(out, &list)
	for _, item := range list.Connections {
		raw, err := c.json11(ctx, "AWSEvents", "DescribeConnection", map[string]any{"Name": item.Name})
		if err != nil {
			continue
		}
		var d struct {
			Description       string
			AuthorizationType string
			AuthParameters    struct {
				BasicAuthParameters  *struct{ Username string }
				ApiKeyAuthParameters *struct{ ApiKeyName string }
				OAuthParameters      *struct {
					ClientParameters      struct{ ClientID string }
					AuthorizationEndpoint string
					HttpMethod            string
					OAuthHttpParameters   exportedParams
				}
				InvocationHttpParameters exportedParams
			}
		}
		json.Unmarshal(raw, &d)
		conn := Connection{Description: d.Description, Invocation: d.AuthParameters.InvocationHttpParameters.model()}
		switch {
		case d.AuthParameters.BasicAuthParameters != nil:
			conn.Basic = &BasicAuth{Username: d.AuthParameters.BasicAuthParameters.Username}
		case d.AuthParameters.ApiKeyAuthParameters != nil:
			conn.APIKey = &APIKeyAuth{Name: d.AuthParameters.ApiKeyAuthParameters.ApiKeyName}
		case d.AuthParameters.OAuthParameters != nil:
			o := d.AuthParameters.OAuthParameters
			conn.OAuth = &OAuthAuth{ClientID: o.ClientParameters.ClientID, Endpoint: o.AuthorizationEndpoint,
				Method: o.HttpMethod, Params: o.OAuthHttpParameters.model()}
		}
		if s.Connections == nil {
			s.Connections = map[string]Connection{}
		}
		s.Connections[item.Name] = conn
	}

	out, err = c.json11(ctx, "AWSEvents", "ListApiDestinations", map[string]any{})
	if err != nil {
		return err
	}
	var dests struct {
		ApiDestinations []struct {
			Name, ConnectionArn, InvocationEndpoint, HttpMethod string
			InvocationRateLimitPerSecond                        int
		}
	}
	json.Unmarshal(out, &dests)
	for _, d := range dests.ApiDestinations {
		if s.APIDestinations == nil {
			s.APIDestinations = map[string]APIDestination{}
		}
		s.APIDestinations[d.Name] = APIDestination{
			Connection: connectionNameOf(d.ConnectionArn), Endpoint: d.InvocationEndpoint,
			Method: d.HttpMethod, RateLimit: d.InvocationRateLimitPerSecond,
		}
	}
	return nil
}

// exportedParams is a ConnectionHttpParameters block as Describe reports it.
type exportedParams struct {
	HeaderParameters, QueryStringParameters, BodyParameters []struct {
		Key, Value    string
		IsValueSecret bool
	}
}

func (e exportedParams) model() ConnectionParams {
	conv := func(in []struct {
		Key, Value    string
		IsValueSecret bool
	}) []ConnectionParam {
		var out []ConnectionParam
		for _, p := range in {
			out = append(out, ConnectionParam{Key: p.Key, Value: p.Value, Secret: p.IsValueSecret})
		}
		return out
	}
	return ConnectionParams{Headers: conv(e.HeaderParameters), Query: conv(e.QueryStringParameters), Body: conv(e.BodyParameters)}
}

// connectionNameOf reads the name out of arn:...:connection/<name>/<id>.
func connectionNameOf(arn string) string {
	_, rest, ok := strings.Cut(arn, ":connection/")
	if !ok {
		return arn
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

// apiDestinationNameOf reads the name out of arn:...:api-destination/<name>/<id>.
func apiDestinationNameOf(arn string) string {
	_, rest, ok := strings.Cut(arn, ":api-destination/")
	if !ok {
		return arn
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

// destroyConnections removes destinations first, then the connections they
// used — a deleted connection would only leave its destinations INACTIVE.
func destroyConnections(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.APIDestinations) {
		_, err := c.json11(ctx, "AWSEvents", "DeleteApiDestination", map[string]any{"Name": name})
		record(rep, "api-destination/"+name, err)
	}
	for _, name := range sortedNames(s.Connections) {
		_, err := c.json11(ctx, "AWSEvents", "DeleteConnection", map[string]any{"Name": name})
		record(rep, "connection/"+name, err)
	}
	return nil
}

// targetHTTPRequest is a target's HttpParameters block, or nil when it has
// none.
func targetHTTPRequest(t Target) map[string]any {
	if len(t.PathParams) == 0 && len(t.Headers) == 0 && len(t.Query) == 0 {
		return nil
	}
	hp := map[string]any{}
	if len(t.PathParams) > 0 {
		hp["PathParameterValues"] = t.PathParams
	}
	if len(t.Headers) > 0 {
		hp["HeaderParameters"] = t.Headers
	}
	if len(t.Query) > 0 {
		hp["QueryStringParameters"] = t.Query
	}
	return hp
}

func orDefaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
