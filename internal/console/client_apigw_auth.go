package console

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// ---- API Gateway Lambda authorizers ----

// APIAuthorizer is one authorizer as the console shows it.
type APIAuthorizer struct {
	ID       string
	Name     string
	Type     string // TOKEN | REQUEST
	Function string // the function the URI names
	Source   string
	TTL      int
}

func (b *backend) APIAuthorizers(ctx context.Context, apiID string) ([]APIAuthorizer, error) {
	body, err := b.apigwJSON(ctx, "GET", "/restapis/"+apiID+"/authorizers", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Item []struct {
			ID, Name, Type, AuthorizerURI, IdentitySource string
			AuthorizerResultTTLInSeconds                  int `json:"authorizerResultTtlInSeconds"`
		} `json:"item"`
	}
	json.Unmarshal(body, &out)
	auths := make([]APIAuthorizer, 0, len(out.Item))
	for _, a := range out.Item {
		auths = append(auths, APIAuthorizer{ID: a.ID, Name: a.Name, Type: a.Type,
			Function: functionOfInvokeURI(a.AuthorizerURI), Source: a.IdentitySource, TTL: a.AuthorizerResultTTLInSeconds})
	}
	return auths, nil
}

// CreateAPIAuthorizer adds a TOKEN or REQUEST authorizer backed by a function.
func (b *backend) CreateAPIAuthorizer(ctx context.Context, apiID, name, typ, function, source string, ttl int) error {
	in := map[string]any{
		"name": name, "type": typ, "authorizerUri": lambdaInvokeURI(function),
		"authorizerResultTtlInSeconds": ttl,
	}
	if source != "" {
		if typ == "TOKEN" && !strings.HasPrefix(source, "method.request.") {
			source = "method.request.header." + source
		}
		in["identitySource"] = source
	}
	_, err := b.apigwJSON(ctx, "POST", "/restapis/"+apiID+"/authorizers", in)
	return err
}

func (b *backend) UpdateAPIAuthorizer(ctx context.Context, apiID, id string, ttl int) error {
	_, err := b.apigwJSON(ctx, "PATCH", "/restapis/"+apiID+"/authorizers/"+id, map[string]any{
		"patchOperations": []map[string]string{{"op": "replace", "path": "/authorizerResultTtlInSeconds", "value": itoa(ttl)}},
	})
	return err
}

func (b *backend) DeleteAPIAuthorizer(ctx context.Context, apiID, id string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/restapis/"+apiID+"/authorizers/"+id, nil)
	return err
}

// GetAPIAuthorizer reads one authorizer.
func (b *backend) GetAPIAuthorizer(ctx context.Context, apiID, id string) (APIAuthorizer, error) {
	body, err := b.apigwJSON(ctx, "GET", "/restapis/"+apiID+"/authorizers/"+id, nil)
	if err != nil {
		return APIAuthorizer{}, err
	}
	var a struct {
		ID, Name, Type, AuthorizerURI, IdentitySource string
		AuthorizerResultTTLInSeconds                  int `json:"authorizerResultTtlInSeconds"`
	}
	json.Unmarshal(body, &a)
	return APIAuthorizer{ID: a.ID, Name: a.Name, Type: a.Type, Function: functionOfInvokeURI(a.AuthorizerURI),
		Source: a.IdentitySource, TTL: a.AuthorizerResultTTLInSeconds}, nil
}

// lambdaInvokeURI is the API Gateway invocation URI for a function.
func lambdaInvokeURI(function string) string {
	return "arn:aws:apigateway:" + awsident.Region + ":lambda:path/2015-03-31/functions/" +
		awsident.ARN("lambda", "function:"+function) + "/invocations"
}

func itoa(n int) string { return strconv.Itoa(n) }

// functionOfInvokeURI reads the function name out of an invocation URI.
func functionOfInvokeURI(uri string) string {
	_, rest, ok := strings.Cut(uri, ":function:")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	name, _, _ = strings.Cut(name, ":")
	return name
}
