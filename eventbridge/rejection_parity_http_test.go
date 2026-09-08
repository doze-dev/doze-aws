package eventbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The connection and API destination half of the parity fixture: a BASIC
// connection and a destination on it, baselines for the eleven operations,
// exemplars for the auth blocks a BASIC baseline does not carry, and
// preconditions for the operations that consume what they name.

// httpFx is the connection/destination state the baselines address.
type httpFx struct {
	conn        string
	connARN     string
	destination string
}

func setUpHTTPFixture(t *testing.T, ts *httptest.Server) httpFx {
	t.Helper()
	f := httpFx{conn: "audit-conn", destination: "audit-dest"}
	code, body := call(t, ts, "CreateConnection", map[string]any{
		"Name": f.conn, "AuthorizationType": "BASIC",
		"AuthParameters": map[string]any{"BasicAuthParameters": map[string]any{"Username": "u", "Password": "p"}},
	})
	if code != http.StatusOK {
		t.Fatalf("fixture CreateConnection = %d: %s", code, body)
	}
	var out struct {
		ConnectionArn string `json:"ConnectionArn"`
	}
	if json.Unmarshal([]byte(body), &out) != nil || out.ConnectionArn == "" {
		t.Fatalf("fixture CreateConnection gave no ARN: %s", body)
	}
	f.connARN = out.ConnectionArn
	if code, body := call(t, ts, "CreateApiDestination", map[string]any{
		"Name": f.destination, "ConnectionArn": f.connARN,
		"InvocationEndpoint": "https://example.test/hook", "HttpMethod": "POST",
	}); code != http.StatusOK {
		t.Fatalf("fixture CreateApiDestination = %d: %s", code, body)
	}
	return f
}

func httpBaselines(f httpFx) map[string]map[string]any {
	basic := map[string]any{"BasicAuthParameters": map[string]any{"Username": "u", "Password": "p"}}
	return map[string]map[string]any{
		"CreateConnection":      {"Name": "made-by-baseline-conn", "AuthorizationType": "BASIC", "AuthParameters": basic},
		"UpdateConnection":      {"Name": f.conn, "AuthorizationType": "BASIC", "AuthParameters": basic},
		"DeauthorizeConnection": {"Name": "made-by-baseline-conn"},
		"DeleteConnection":      {"Name": "made-by-baseline-conn"},
		"DescribeConnection":    {"Name": f.conn},
		"ListConnections":       {},
		"CreateApiDestination": {"Name": "made-by-baseline-dest", "ConnectionArn": f.connARN,
			"InvocationEndpoint": "https://example.test/hook", "HttpMethod": "POST"},
		"UpdateApiDestination":   {"Name": f.destination, "InvocationEndpoint": "https://example.test/hook2"},
		"DeleteApiDestination":   {"Name": "made-by-baseline-dest"},
		"DescribeApiDestination": {"Name": f.destination},
		"ListApiDestinations":    {},
	}
}

// httpExemplars are the auth blocks a BASIC baseline does not carry. Each is
// valid on its own; the handler reads only the block the AuthorizationType
// names, so an extra block leaves the baseline acceptable.
func httpExemplars() map[string]any {
	lattice := map[string]any{"ResourceParameters": map[string]any{"ResourceConfigurationArn": ""}}
	return map[string]any{
		"AuthParameters":                                           map[string]any{"BasicAuthParameters": map[string]any{"Username": "u", "Password": "p"}},
		"AuthParameters.ApiKeyAuthParameters":                      map[string]any{"ApiKeyName": "x-api-key", "ApiKeyValue": "secret"},
		"AuthParameters.BasicAuthParameters":                       map[string]any{"Username": "u", "Password": "p"},
		"AuthParameters.ConnectivityParameters":                    lattice,
		"AuthParameters.ConnectivityParameters.ResourceParameters": lattice["ResourceParameters"],
		"AuthParameters.OAuthParameters": map[string]any{
			"ClientParameters":      map[string]any{"ClientID": "id", "ClientSecret": "s"},
			"AuthorizationEndpoint": "https://auth.example.test/token", "HttpMethod": "POST",
		},
		"AuthParameters.OAuthParameters.ClientParameters":     map[string]any{"ClientID": "id", "ClientSecret": "s"},
		"InvocationConnectivityParameters":                    lattice,
		"InvocationConnectivityParameters.ResourceParameters": lattice["ResourceParameters"],
	}
}

// prepareHTTP gives the consuming operations something of their own to
// consume, so groups stay independent of one another.
func prepareHTTP(t *testing.T, ts *httptest.Server, f httpFx, op, mutating string, body map[string]any, n int) {
	t.Helper()
	newConn := func(name string) {
		call(t, ts, "CreateConnection", map[string]any{
			"Name": name, "AuthorizationType": "BASIC",
			"AuthParameters": map[string]any{"BasicAuthParameters": map[string]any{"Username": "u", "Password": "p"}},
		})
	}
	switch op {
	case "CreateConnection":
		if mutating != "Name" {
			body["Name"] = fmt.Sprintf("created-conn-%d", n)
		}
	case "DeauthorizeConnection", "DeleteConnection":
		if mutating != "Name" {
			name := fmt.Sprintf("doomed-conn-%d", n)
			newConn(name)
			body["Name"] = name
		}
	case "CreateApiDestination":
		if mutating != "Name" {
			body["Name"] = fmt.Sprintf("created-dest-%d", n)
		}
	case "DeleteApiDestination":
		if mutating != "Name" {
			name := fmt.Sprintf("doomed-dest-%d", n)
			call(t, ts, "CreateApiDestination", map[string]any{
				"Name": name, "ConnectionArn": f.connARN,
				"InvocationEndpoint": "https://example.test/hook", "HttpMethod": "POST",
			})
			body["Name"] = name
		}
	}
}
