package apigateway

// The HTTP API (apigatewayv2) model. An HTTP API is the same record as a
// REST API — one blob per API, the same stages, deployments and tags — with
// Protocol "HTTP" and, instead of a resource tree, routes and integrations
// keyed by id the way the v2 API addresses them.

import (
	"encoding/json"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// CORSConfig is the API's CORS configuration; the data plane answers
// preflights from it and stamps the headers on every response.
type CORSConfig struct {
	AllowCredentials bool     `json:"allow_credentials,omitempty"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	AllowMethods     []string `json:"allow_methods,omitempty"`
	AllowOrigins     []string `json:"allow_origins,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	MaxAge           *int     `json:"max_age,omitempty"`
}

// V2Route is one route: a key such as "GET /items/{id}", "ANY /{proxy+}" or
// "$default", and the integration it targets.
type V2Route struct {
	ID                  string   `json:"id"`
	RouteKey            string   `json:"route_key"`
	Target              string   `json:"target,omitempty"` // "integrations/<id>"
	AuthorizationType   string   `json:"authorization_type,omitempty"`
	AuthorizerID        string   `json:"authorizer_id,omitempty"`
	AuthorizationScopes []string `json:"authorization_scopes,omitempty"`
	APIKeyRequired      bool     `json:"api_key_required,omitempty"`
	OperationName       string   `json:"operation_name,omitempty"`
	// Stored and reported, no local effect.
	RequestParameters map[string]map[string]any `json:"request_parameters,omitempty"`
	RequestModels     map[string]string         `json:"request_models,omitempty"`
	ModelSelection    string                    `json:"model_selection,omitempty"`
}

// V2Integration is a route's backend: AWS_PROXY to a Lambda function with a
// payload format version, or HTTP_PROXY to a URL.
type V2Integration struct {
	ID                   string                       `json:"id"`
	Type                 string                       `json:"type"`
	URI                  string                       `json:"uri,omitempty"`
	Method               string                       `json:"method,omitempty"`
	PayloadFormatVersion string                       `json:"payload_format_version,omitempty"`
	TimeoutInMillis      int                          `json:"timeout_in_millis,omitempty"`
	Description          string                       `json:"description,omitempty"`
	ConnectionType       string                       `json:"connection_type,omitempty"`
	ConnectionID         string                       `json:"connection_id,omitempty"`
	CredentialsARN       string                       `json:"credentials_arn,omitempty"`
	RequestParameters    map[string]string            `json:"request_parameters,omitempty"`
	ResponseParameters   map[string]map[string]string `json:"response_parameters,omitempty"`
	// Subtype is the AWS service integration kind (e.g. SQS-SendMessage),
	// stored so the record round-trips; the data plane refuses to run it.
	Subtype string `json:"subtype,omitempty"`
}

// V2Authorizer is a REQUEST Lambda authorizer for an HTTP API (JWT
// authorizers are refused at create: there is no identity provider locally).
type V2Authorizer struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Type                  string   `json:"type"` // REQUEST
	URI                   string   `json:"uri,omitempty"`
	IdentitySource        []string `json:"identity_source,omitempty"`
	PayloadFormatVersion  string   `json:"payload_format_version,omitempty"`
	EnableSimpleResponses bool     `json:"enable_simple_responses,omitempty"`
	ResultTTL             int      `json:"result_ttl"`
	CredentialsARN        string   `json:"credentials_arn,omitempty"`
}

// CreateHTTP creates an HTTP API record.
func (s *Store) CreateHTTP(name, description, version string, tags map[string]string) (*RestAPI, error) {
	if name == "" {
		return nil, errBadRequest("Name is required")
	}
	api := &RestAPI{
		ID: s.newID(), Name: name, Description: description, Version: version,
		Created: s.now().Unix(), Tags: tags, Protocol: "HTTP",
		Resources:      map[string]*Resource{},
		Deployments:    map[string]*Deployment{},
		Stages:         map[string]*Stage{},
		V2Routes:       map[string]*V2Route{},
		V2Integrations: map[string]*V2Integration{},
		V2Authorizers:  map[string]*V2Authorizer{},
		RouteSelection: "$request.method $request.path",
		IPAddressType:  "ipv4",
	}
	return api, s.Put(api)
}

// GetHTTP is Get for the v2 control plane: a REST API's id is not an HTTP
// API's, so the v2 surface answers NotFound for it, as AWS does.
func (s *Store) GetHTTP(id string) (*RestAPI, error) {
	api, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if api.Protocol != "HTTP" {
		return nil, errNotFound("Invalid API identifier specified %s", id)
	}
	return api, nil
}

// UpdateHTTP is Update with the same protocol check.
func (s *Store) UpdateHTTP(id string, fn func(*RestAPI) error) (*RestAPI, error) {
	if _, err := s.GetHTTP(id); err != nil {
		return nil, err
	}
	return s.Update(id, func(api *RestAPI) error {
		// The maps are omitted from the record while empty.
		if api.V2Routes == nil {
			api.V2Routes = map[string]*V2Route{}
		}
		if api.V2Integrations == nil {
			api.V2Integrations = map[string]*V2Integration{}
		}
		if api.V2Authorizers == nil {
			api.V2Authorizers = map[string]*V2Authorizer{}
		}
		if api.Deployments == nil {
			api.Deployments = map[string]*Deployment{}
		}
		if api.Stages == nil {
			api.Stages = map[string]*Stage{}
		}
		return fn(api)
	})
}

// ListProtocol lists the APIs of one protocol: "" for REST, "HTTP" for v2.
// Each control plane lists only its own, as on AWS.
func (s *Store) ListProtocol(protocol string) ([]RestAPI, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, api := range all {
		if api.Protocol == protocol {
			out = append(out, api)
		}
	}
	return out, nil
}

// CountHTTP reports how many HTTP APIs exist, for the console badge.
func (s *Store) CountHTTP() int {
	n := 0
	s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(apiBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var probe struct {
				Protocol string `json:"protocol"`
			}
			if json.Unmarshal(raw, &probe) == nil && probe.Protocol == "HTTP" {
				n++
			}
			return nil
		})
	})
	return n
}

// routeKeyParts splits "GET /items/{id}" into its method and path; "$default"
// answers ("", "").
func routeKeyParts(key string) (method, path string) {
	if key == "$default" {
		return "", ""
	}
	method, path, _ = strings.Cut(key, " ")
	return strings.ToUpper(strings.TrimSpace(method)), strings.TrimSpace(path)
}

// validRouteKey reports whether a key is "$default" or "<METHOD> /<path>".
func validRouteKey(key string) bool {
	if key == "$default" {
		return true
	}
	method, path := routeKeyParts(key)
	if method == "" || !strings.HasPrefix(path, "/") {
		return false
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "ANY":
		return true
	}
	return false
}
