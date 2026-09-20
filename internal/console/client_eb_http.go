package console

import (
	"context"
	"encoding/json"
	"strings"
)

// ---- EventBridge connections and API destinations ----

// EBConnection is one connection as the list and describe report it. No
// secret ever comes back from the service, so none is here.
type EBConnection struct {
	Name        string
	ARN         string
	SecretARN   string
	State       string
	AuthType    string
	Description string
	Username    string // BASIC
	APIKeyName  string // API_KEY
	ClientID    string // OAUTH
	Endpoint    string // OAUTH authorization endpoint
	Method      string // OAUTH method
	Headers     []EBParam
	Query       []EBParam
	Body        []EBParam
	Created     string
}

// EBParam is one invocation parameter; a secret's value is never shown.
type EBParam struct {
	Key, Value string
	Secret     bool
}

// EBDestination is one API destination.
type EBDestination struct {
	Name          string
	ARN           string
	State         string
	ConnectionARN string
	Connection    string // name half of ConnectionARN
	Endpoint      string
	Method        string
	RateLimit     int
	Description   string
	Created       string
}

func (b *backend) ListConnections(ctx context.Context) ([]EBConnection, error) {
	body, err := b.json11(ctx, "AWSEvents", "ListConnections", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Connections []struct {
			Name, ConnectionArn, ConnectionState, AuthorizationType string
			CreationTime                                            float64
		}
	}
	json.Unmarshal(body, &out)
	conns := make([]EBConnection, 0, len(out.Connections))
	for _, c := range out.Connections {
		conns = append(conns, EBConnection{Name: c.Name, ARN: c.ConnectionArn, State: c.ConnectionState,
			AuthType: c.AuthorizationType, Created: epochToTime(c.CreationTime)})
	}
	return conns, nil
}

// EBConnectionForm is what the create and update forms carry.
type EBConnectionForm struct {
	Name, Description, AuthType string
	Username, Password          string
	APIKeyName, APIKeyValue     string
	ClientID, ClientSecret      string
	Endpoint, Method            string
	Headers, Query, Body        string // "k=v" per line; a leading "!" marks a secret
}

// authParameters builds the AuthParameters block from the form.
func (f EBConnectionForm) authParameters() map[string]any {
	auth := map[string]any{}
	switch f.AuthType {
	case "BASIC":
		auth["BasicAuthParameters"] = map[string]any{"Username": f.Username, "Password": f.Password}
	case "API_KEY":
		auth["ApiKeyAuthParameters"] = map[string]any{"ApiKeyName": f.APIKeyName, "ApiKeyValue": f.APIKeyValue}
	case "OAUTH_CLIENT_CREDENTIALS":
		auth["OAuthParameters"] = map[string]any{
			"ClientParameters":      map[string]any{"ClientID": f.ClientID, "ClientSecret": f.ClientSecret},
			"AuthorizationEndpoint": f.Endpoint, "HttpMethod": strings.ToUpper(f.Method),
		}
	}
	inv := map[string]any{}
	for key, lines := range map[string]string{"HeaderParameters": f.Headers, "QueryStringParameters": f.Query, "BodyParameters": f.Body} {
		if ps := parseParamLines(lines); len(ps) > 0 {
			inv[key] = ps
		}
	}
	if len(inv) > 0 {
		auth["InvocationHttpParameters"] = inv
	}
	return auth
}

// parseParamLines reads "key=value" lines; "!key=value" marks a secret.
func parseParamLines(s string) []any {
	var out []any
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		secret := strings.HasPrefix(line, "!")
		k, v, _ := strings.Cut(strings.TrimPrefix(line, "!"), "=")
		out = append(out, map[string]any{"Key": strings.TrimSpace(k), "Value": strings.TrimSpace(v), "IsValueSecret": secret})
	}
	return out
}

func (b *backend) CreateConnection(ctx context.Context, f EBConnectionForm) error {
	in := map[string]any{"Name": f.Name, "AuthorizationType": f.AuthType, "AuthParameters": f.authParameters()}
	if f.Description != "" {
		in["Description"] = f.Description
	}
	_, err := b.json11(ctx, "AWSEvents", "CreateConnection", in)
	return err
}

// UpdateConnection re-authorizes with a fresh credential when one is given;
// with only a description it leaves the credential alone.
func (b *backend) UpdateConnection(ctx context.Context, f EBConnectionForm) error {
	in := map[string]any{"Name": f.Name, "Description": f.Description}
	if f.AuthType != "" {
		in["AuthorizationType"] = f.AuthType
		in["AuthParameters"] = f.authParameters()
	}
	_, err := b.json11(ctx, "AWSEvents", "UpdateConnection", in)
	return err
}

func (b *backend) DeauthorizeConnection(ctx context.Context, name string) error {
	_, err := b.json11(ctx, "AWSEvents", "DeauthorizeConnection", map[string]any{"Name": name})
	return err
}

func (b *backend) DeleteConnection(ctx context.Context, name string) error {
	_, err := b.json11(ctx, "AWSEvents", "DeleteConnection", map[string]any{"Name": name})
	return err
}

func (b *backend) DescribeConnection(ctx context.Context, name string) (EBConnection, error) {
	body, err := b.json11(ctx, "AWSEvents", "DescribeConnection", map[string]any{"Name": name})
	if err != nil {
		return EBConnection{}, err
	}
	var out struct {
		Name, ConnectionArn, SecretArn, ConnectionState, AuthorizationType, Description string
		CreationTime                                                                    float64
		AuthParameters                                                                  struct {
			BasicAuthParameters  *struct{ Username string }
			ApiKeyAuthParameters *struct{ ApiKeyName string }
			OAuthParameters      *struct {
				ClientParameters      struct{ ClientID string }
				AuthorizationEndpoint string
				HttpMethod            string
			}
			InvocationHttpParameters struct {
				HeaderParameters, QueryStringParameters, BodyParameters []struct {
					Key, Value    string
					IsValueSecret bool
				}
			}
		}
	}
	json.Unmarshal(body, &out)
	c := EBConnection{Name: out.Name, ARN: out.ConnectionArn, SecretARN: out.SecretArn, State: out.ConnectionState,
		AuthType: out.AuthorizationType, Description: out.Description, Created: epochToTime(out.CreationTime)}
	a := out.AuthParameters
	switch {
	case a.BasicAuthParameters != nil:
		c.Username = a.BasicAuthParameters.Username
	case a.ApiKeyAuthParameters != nil:
		c.APIKeyName = a.ApiKeyAuthParameters.ApiKeyName
	case a.OAuthParameters != nil:
		c.ClientID, c.Endpoint, c.Method = a.OAuthParameters.ClientParameters.ClientID, a.OAuthParameters.AuthorizationEndpoint, a.OAuthParameters.HttpMethod
	}
	conv := func(in []struct {
		Key, Value    string
		IsValueSecret bool
	}) []EBParam {
		var ps []EBParam
		for _, p := range in {
			ps = append(ps, EBParam{Key: p.Key, Value: p.Value, Secret: p.IsValueSecret})
		}
		return ps
	}
	inv := a.InvocationHttpParameters
	c.Headers, c.Query, c.Body = conv(inv.HeaderParameters), conv(inv.QueryStringParameters), conv(inv.BodyParameters)
	return c, nil
}

func (b *backend) ListDestinations(ctx context.Context) ([]EBDestination, error) {
	body, err := b.json11(ctx, "AWSEvents", "ListApiDestinations", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		ApiDestinations []struct {
			Name, ApiDestinationArn, ApiDestinationState, ConnectionArn, InvocationEndpoint, HttpMethod string
			InvocationRateLimitPerSecond                                                                int
			CreationTime                                                                                float64
		}
	}
	json.Unmarshal(body, &out)
	ds := make([]EBDestination, 0, len(out.ApiDestinations))
	for _, d := range out.ApiDestinations {
		ds = append(ds, EBDestination{Name: d.Name, ARN: d.ApiDestinationArn, State: d.ApiDestinationState,
			ConnectionARN: d.ConnectionArn, Connection: connectionName(d.ConnectionArn), Endpoint: d.InvocationEndpoint,
			Method: d.HttpMethod, RateLimit: d.InvocationRateLimitPerSecond, Created: epochToTime(d.CreationTime)})
	}
	return ds, nil
}

func (b *backend) DescribeDestination(ctx context.Context, name string) (EBDestination, error) {
	body, err := b.json11(ctx, "AWSEvents", "DescribeApiDestination", map[string]any{"Name": name})
	if err != nil {
		return EBDestination{}, err
	}
	var d struct {
		Name, ApiDestinationArn, ApiDestinationState, ConnectionArn, InvocationEndpoint, HttpMethod, Description string
		InvocationRateLimitPerSecond                                                                             int
		CreationTime                                                                                             float64
	}
	json.Unmarshal(body, &d)
	return EBDestination{Name: d.Name, ARN: d.ApiDestinationArn, State: d.ApiDestinationState,
		ConnectionARN: d.ConnectionArn, Connection: connectionName(d.ConnectionArn), Endpoint: d.InvocationEndpoint,
		Method: d.HttpMethod, RateLimit: d.InvocationRateLimitPerSecond, Description: d.Description,
		Created: epochToTime(d.CreationTime)}, nil
}

// destinationRequest is the shared Create/UpdateApiDestination body.
func destinationRequest(name, connARN, endpoint, method, desc string, rate int) map[string]any {
	in := map[string]any{"Name": name, "ConnectionArn": connARN, "InvocationEndpoint": endpoint, "HttpMethod": strings.ToUpper(method)}
	if desc != "" {
		in["Description"] = desc
	}
	if rate > 0 {
		in["InvocationRateLimitPerSecond"] = rate
	}
	return in
}

func (b *backend) CreateDestination(ctx context.Context, name, connARN, endpoint, method, desc string, rate int) error {
	_, err := b.json11(ctx, "AWSEvents", "CreateApiDestination", destinationRequest(name, connARN, endpoint, method, desc, rate))
	return err
}

func (b *backend) UpdateDestination(ctx context.Context, name, connARN, endpoint, method, desc string, rate int) error {
	_, err := b.json11(ctx, "AWSEvents", "UpdateApiDestination", destinationRequest(name, connARN, endpoint, method, desc, rate))
	return err
}

func (b *backend) DeleteDestination(ctx context.Context, name string) error {
	_, err := b.json11(ctx, "AWSEvents", "DeleteApiDestination", map[string]any{"Name": name})
	return err
}

// connectionName reads the name out of arn:...:connection/<name>/<id>.
func connectionName(arn string) string {
	_, rest, ok := strings.Cut(arn, ":connection/")
	if !ok {
		return arn
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}
