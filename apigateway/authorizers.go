package apigateway

// Lambda authorizers: the control plane. A TOKEN authorizer reads one header
// and hands the token to a function; a REQUEST authorizer hands the function
// the request's headers, query string, path parameters and stage variables.
// The function answers an IAM policy that allows or denies the method, and
// the answer is cached for the authorizer's TTL. COGNITO_USER_POOLS needs a
// user pool, which does not exist locally, and is refused by name.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Authorizer is one Lambda authorizer on an API.
type Authorizer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // TOKEN | REQUEST
	// URI is the Lambda invocation URI, in the same shape an integration
	// carries: arn:aws:apigateway:<region>:lambda:path/2015-03-31/functions/<fn arn>/invocations.
	URI         string `json:"uri"`
	Credentials string `json:"credentials,omitempty"`
	// IdentitySource is "method.request.header.Authorization" for TOKEN, or
	// a comma-separated list of header, querystring, path, stageVariables
	// and context sources for REQUEST.
	IdentitySource string `json:"identity_source,omitempty"`
	// IdentityValidation is the regex a TOKEN must match before the function
	// is called; a mismatch is a 401 without an invoke.
	IdentityValidation string `json:"identity_validation,omitempty"`
	// ResultTTL is the cache lifetime in seconds; 0 disables caching. The
	// default is 300, as on AWS.
	ResultTTL int    `json:"result_ttl"`
	AuthType  string `json:"auth_type,omitempty"`
}

// DefaultAuthorizerTTL is AWS's default authorizerResultTtlInSeconds.
const DefaultAuthorizerTTL = 300

func viewAuthorizer(a *Authorizer) map[string]any {
	v := map[string]any{
		"id": a.ID, "name": a.Name, "type": a.Type,
		"authorizerUri": a.URI, "authorizerResultTtlInSeconds": a.ResultTTL,
	}
	putIfStr(v, "authorizerCredentials", a.Credentials)
	putIfStr(v, "identitySource", a.IdentitySource)
	putIfStr(v, "identityValidationExpression", a.IdentityValidation)
	putIfStr(v, "authType", a.AuthType)
	return v
}

// routeAuthorizers serves /restapis/{id}/authorizers[/{authorizerId}].
func (s *Server) routeAuthorizers(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 3 {
		switch r.Method {
		case http.MethodPost:
			return s.createAuthorizer(w, r, apiID)
		case http.MethodGet:
			api, err := s.store.Get(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			items := make([]any, 0, len(api.Authorizers))
			for _, id := range sortedKeys(api.Authorizers) {
				items = append(items, viewAuthorizer(api.Authorizers[id]))
			}
			writeJSON(w, 200, map[string]any{"item": items})
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on /authorizers")
	}
	if len(segs) == 4 {
		id := segs[3]
		switch r.Method {
		case http.MethodGet:
			api, err := s.store.Get(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			a, ok := api.Authorizers[id]
			if !ok {
				return errNotFound("Invalid Authorizer identifier specified")
			}
			writeJSON(w, 200, viewAuthorizer(a))
			return nil
		case http.MethodPatch:
			return s.patchAuthorizer(w, r, apiID, id)
		case http.MethodDelete:
			if _, err := s.store.Update(apiID, func(api *RestAPI) error {
				if _, ok := api.Authorizers[id]; !ok {
					return errNotFound("Invalid Authorizer identifier specified")
				}
				delete(api.Authorizers, id)
				return nil
			}); err != nil {
				return awshttp.AsAPIError(err)
			}
			s.authCache.forget(id)
			w.WriteHeader(202)
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on an authorizer")
	}
	return errNotFound("unknown authorizer path")
}

func (s *Server) createAuthorizer(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req struct {
		Name               string `json:"name"`
		Type               string `json:"type"`
		AuthorizerURI      string `json:"authorizerUri"`
		Credentials        string `json:"authorizerCredentials"`
		IdentitySource     string `json:"identitySource"`
		IdentityValidation string `json:"identityValidationExpression"`
		TTL                *int   `json:"authorizerResultTtlInSeconds"`
		AuthType           string `json:"authType"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	a := &Authorizer{
		ID: s.store.newID(), Name: req.Name, Type: req.Type, URI: req.AuthorizerURI,
		Credentials: req.Credentials, IdentitySource: req.IdentitySource,
		IdentityValidation: req.IdentityValidation, ResultTTL: DefaultAuthorizerTTL, AuthType: req.AuthType,
	}
	if req.TTL != nil {
		a.ResultTTL = *req.TTL
	}
	if aerr := checkAuthorizer(a); aerr != nil {
		return aerr
	}
	if _, err := s.store.Update(apiID, func(api *RestAPI) error {
		for _, other := range api.Authorizers {
			if other.Name == a.Name {
				return errConflict("Authorizer name must be unique. Authorizer %s already exists in this RestApi.", a.Name)
			}
		}
		if api.Authorizers == nil {
			api.Authorizers = map[string]*Authorizer{}
		}
		api.Authorizers[a.ID] = a
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewAuthorizer(a))
	return nil
}

// checkAuthorizer refuses what cannot work: a Cognito authorizer with no user
// pool behind it, and a Lambda authorizer with no function URI.
func checkAuthorizer(a *Authorizer) *awshttp.APIError {
	switch a.Type {
	case "COGNITO_USER_POOLS":
		return errBadRequest("COGNITO_USER_POOLS authorizers need a Cognito user pool, which does not exist locally; use a TOKEN or REQUEST Lambda authorizer")
	case "TOKEN", "REQUEST":
	default:
		return errBadRequest("Invalid authorizer type: %s", a.Type)
	}
	if lambdaFromURI(a.URI) == "" {
		return errBadRequest("AuthorizerUri must name a Lambda function: arn:aws:apigateway:<region>:lambda:path/2015-03-31/functions/<function arn>/invocations")
	}
	if a.Type == "TOKEN" && a.IdentitySource == "" {
		a.IdentitySource = "method.request.header.Authorization"
	}
	if a.ResultTTL < 0 || a.ResultTTL > 3600 {
		return errBadRequest("authorizerResultTtlInSeconds must be between 0 and 3600")
	}
	return nil
}

func (s *Server) patchAuthorizer(w http.ResponseWriter, r *http.Request, apiID, id string) *awshttp.APIError {
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	var out *Authorizer
	api, err := s.store.Update(apiID, func(api *RestAPI) error {
		a, ok := api.Authorizers[id]
		if !ok {
			return errNotFound("Invalid Authorizer identifier specified")
		}
		for _, op := range ops {
			if op.Op != "replace" && op.Op != "add" {
				return errBadRequest("Invalid patch operation %s on %s", op.Op, op.Path)
			}
			switch op.Path {
			case "/name":
				a.Name = op.Value
			case "/authorizerUri":
				a.URI = op.Value
			case "/authorizerCredentials":
				a.Credentials = op.Value
			case "/identitySource":
				a.IdentitySource = op.Value
			case "/identityValidationExpression":
				a.IdentityValidation = op.Value
			case "/authorizerResultTtlInSeconds":
				a.ResultTTL = atoiOr(op.Value, a.ResultTTL)
			case "/type":
				a.Type = op.Value
			case "/authType":
				a.AuthType = op.Value
			default:
				return errBadRequest("Invalid patch path %s", op.Path)
			}
		}
		if aerr := checkAuthorizer(a); aerr != nil {
			return aerr
		}
		out = a
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	_ = api
	s.authCache.forget(id)
	writeJSON(w, 200, viewAuthorizer(out))
	return nil
}

// authorizerFor resolves a method's CUSTOM authorizer, or nil.
func authorizerFor(api *RestAPI, m *Method) *Authorizer {
	if !strings.EqualFold(m.AuthorizationType, "CUSTOM") || m.AuthorizerID == "" {
		return nil
	}
	return api.Authorizers[m.AuthorizerID]
}
