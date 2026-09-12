package eventbridge

import (
	"context"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// The six connection operations.

func (s *Server) createConnection(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "Name")
	authType := awsjson.Str(p, "AuthorizationType")
	auth, _ := p["AuthParameters"].(map[string]any)
	basic, key, oauth, invocation, aerr := parseAuthParameters(authType, p, auth)
	if aerr != nil {
		return nil, aerr
	}
	now := s.now().UnixMilli()
	c := Connection{
		Name: name, ID: newID(), Desc: awsjson.Str(p, "Description"), AuthType: authType,
		State: "AUTHORIZED", Basic: basic, APIKey: key, OAuth: oauth, Invocation: invocation,
		CreatedMs: now, ModifiedMs: now,
	}
	if err := s.store.CreateConnection(c); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"ConnectionArn": c.ARN(s.id), "ConnectionState": c.State,
		"CreationTime": float64(now) / 1000, "LastModifiedTime": float64(now) / 1000,
	}, nil
}

func (s *Server) updateConnection(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "Name")
	c, err := s.store.UpdateConnection(name, func(c *Connection) error {
		if d, ok := p["Description"].(string); ok {
			c.Desc = d
		}
		authType := awsjson.Str(p, "AuthorizationType")
		auth, hasAuth := p["AuthParameters"].(map[string]any)
		if authType == "" {
			authType = c.AuthType
		}
		if hasAuth || authType != c.AuthType {
			basic, key, oauth, invocation, aerr := parseAuthParameters(authType, p, auth)
			if aerr != nil {
				return aerr
			}
			c.AuthType, c.Basic, c.APIKey, c.OAuth, c.Invocation = authType, basic, key, oauth, invocation
			c.State = "AUTHORIZED"
		}
		c.ModifiedMs = s.now().UnixMilli()
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	s.forgetToken(c.ARN(s.id))
	return connectionStateView(s.id, c), nil
}

func (s *Server) deauthorizeConnection(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	c, err := s.store.UpdateConnection(awsjson.Str(p, "Name"), func(c *Connection) error {
		c.Basic, c.APIKey, c.OAuth = nil, nil, nil
		c.State = "DEAUTHORIZED"
		c.ModifiedMs = s.now().UnixMilli()
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	s.forgetToken(c.ARN(s.id))
	return connectionStateView(s.id, c), nil
}

func (s *Server) deleteConnection(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "Name")
	c, err := s.store.GetConnection(name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if err := s.store.DeleteConnection(name); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// Destinations on the connection stay, inactive, as on AWS.
	s.store.markDestinationsInactive(c.ARN(s.id))
	s.forgetToken(c.ARN(s.id))
	v := connectionStateView(s.id, c)
	v["ConnectionState"] = "DELETING"
	return v, nil
}

func (s *Server) describeConnection(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	c, err := s.store.GetConnection(awsjson.Str(p, "Name"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return connectionView(s.id, c, true), nil
}

func (s *Server) listConnections(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	conns, err := s.store.ListConnections(awsjson.Str(p, "NamePrefix"), awsjson.Str(p, "ConnectionState"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := []any{}
	for i := range conns {
		views = append(views, connectionView(s.id, &conns[i], false))
	}
	return map[string]any{"Connections": views}, nil
}

// connectionStateView is the Update/Deauthorize/Delete answer.
func connectionStateView(id awsident.Identity, c *Connection) map[string]any {
	return map[string]any{
		"ConnectionArn": c.ARN(id), "ConnectionState": c.State,
		"CreationTime": float64(c.CreatedMs) / 1000, "LastModifiedTime": float64(c.ModifiedMs) / 1000,
		"LastAuthorizedTime": float64(c.ModifiedMs) / 1000,
	}
}
