package apigateway

// HTTP API authorizers: Lambda and JWT.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

type v2AuthorizerInput struct {
	Name                           *string  `json:"name"`
	AuthorizerType                 *string  `json:"authorizerType"`
	AuthorizerURI                  *string  `json:"authorizerUri"`
	IdentitySource                 []string `json:"identitySource"`
	AuthorizerPayloadFormatVersion *string  `json:"authorizerPayloadFormatVersion"`
	EnableSimpleResponses          *bool    `json:"enableSimpleResponses"`
	AuthorizerResultTTL            *int     `json:"authorizerResultTtlInSeconds"`
	AuthorizerCredentialsARN       *string  `json:"authorizerCredentialsArn"`
	JWTConfiguration               any      `json:"jwtConfiguration"`
}

func (s *Server) v2CreateAuthorizer(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req v2AuthorizerInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.Name == nil || *req.Name == "" {
		return errBadRequest("name is required")
	}
	var out *v2Authorizer
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
		for _, other := range api.V2Authorizers {
			if other.Name == *req.Name {
				return errConflict("Authorizer name %s already exists", *req.Name)
			}
		}
		a := &v2Authorizer{ID: s.store.newID(), PayloadFormatVersion: "2.0"}
		if err := applyV2AuthorizerInput(a, &req); err != nil {
			return err
		}
		api.V2Authorizers[a.ID] = a
		out = a
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewV2Authorizer(out))
	return nil
}

func (s *Server) v2GetAuthorizers(w http.ResponseWriter, apiID string) *awshttp.APIError {
	api, err := s.store.GetHTTP(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	items := make([]any, 0, len(api.V2Authorizers))
	for _, id := range sortedKeys(api.V2Authorizers) {
		items = append(items, viewV2Authorizer(api.V2Authorizers[id]))
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (s *Server) v2GetAuthorizer(w http.ResponseWriter, apiID, authID string) *awshttp.APIError {
	api, err := s.store.GetHTTP(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	a, ok := api.V2Authorizers[authID]
	if !ok {
		return errNotFound("Invalid authorizer identifier specified %s", authID)
	}
	writeJSON(w, 200, viewV2Authorizer(a))
	return nil
}

func (s *Server) v2UpdateAuthorizer(w http.ResponseWriter, r *http.Request, apiID, authID string) *awshttp.APIError {
	var req v2AuthorizerInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	var out *v2Authorizer
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
		a, ok := api.V2Authorizers[authID]
		if !ok {
			return errNotFound("Invalid authorizer identifier specified %s", authID)
		}
		if err := applyV2AuthorizerInput(a, &req); err != nil {
			return err
		}
		out = a
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	s.authCache.forget(authID)
	writeJSON(w, 200, viewV2Authorizer(out))
	return nil
}

func (s *Server) v2DeleteAuthorizer(w http.ResponseWriter, apiID, authID string) *awshttp.APIError {
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
		if _, ok := api.V2Authorizers[authID]; !ok {
			return errNotFound("Invalid authorizer identifier specified %s", authID)
		}
		for _, rt := range api.V2Routes {
			if rt.AuthorizerID == authID {
				return errConflict("Authorizer %s is used by route %s", authID, rt.RouteKey)
			}
		}
		delete(api.V2Authorizers, authID)
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	s.authCache.forget(authID)
	w.WriteHeader(204)
	return nil
}

func applyV2AuthorizerInput(a *v2Authorizer, in *v2AuthorizerInput) error {
	if in.Name != nil {
		a.Name = *in.Name
	}
	if in.AuthorizerType != nil {
		switch strings.ToUpper(*in.AuthorizerType) {
		case "REQUEST":
			a.Type = "REQUEST"
		case "JWT":
			return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement JWT authorizers: there is no identity provider locally to issue or verify tokens")
		default:
			return errBadRequest("Invalid authorizerType %q", *in.AuthorizerType)
		}
	}
	if in.JWTConfiguration != nil {
		return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement JWT authorizers")
	}
	if in.AuthorizerURI != nil {
		a.URI = *in.AuthorizerURI
	}
	if in.IdentitySource != nil {
		a.IdentitySource = in.IdentitySource
	}
	if in.AuthorizerPayloadFormatVersion != nil {
		switch *in.AuthorizerPayloadFormatVersion {
		case "1.0", "2.0":
			a.PayloadFormatVersion = *in.AuthorizerPayloadFormatVersion
		default:
			return errBadRequest("authorizerPayloadFormatVersion must be 1.0 or 2.0")
		}
	}
	if in.EnableSimpleResponses != nil {
		a.EnableSimpleResponses = *in.EnableSimpleResponses
	}
	if in.AuthorizerResultTTL != nil {
		a.ResultTTL = *in.AuthorizerResultTTL
	}
	if in.AuthorizerCredentialsARN != nil {
		a.CredentialsARN = *in.AuthorizerCredentialsARN
	}
	if a.Type == "" {
		return errBadRequest("authorizerType is required")
	}
	if a.URI == "" || lambdaFromURI(a.URI) == "" {
		return errBadRequest("A REQUEST authorizer needs authorizerUri: the Lambda invoke URI of the function")
	}
	if a.EnableSimpleResponses && a.PayloadFormatVersion != "2.0" {
		return errBadRequest("enableSimpleResponses needs authorizerPayloadFormatVersion 2.0")
	}
	return nil
}

func viewV2Authorizer(a *v2Authorizer) map[string]any {
	v := map[string]any{
		"authorizerId": a.ID, "name": a.Name, "authorizerType": a.Type,
		"authorizerUri": a.URI, "identitySource": a.IdentitySource,
		"authorizerPayloadFormatVersion": a.PayloadFormatVersion,
		"enableSimpleResponses":          a.EnableSimpleResponses,
		"authorizerResultTtlInSeconds":   a.ResultTTL,
	}
	putIfStr(v, "authorizerCredentialsArn", a.CredentialsARN)
	return v
}
