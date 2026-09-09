package apigateway

// HTTP API routes, integrations and authorizers: the CRUD half of
// /v2/apis/{id}/{routes,integrations,authorizers}. The data plane that runs
// them is v2_execute.go.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// ---- routes ----

type v2RouteInput struct {
	RouteKey            *string                   `json:"routeKey"`
	Target              *string                   `json:"target"`
	AuthorizationType   *string                   `json:"authorizationType"`
	AuthorizerID        *string                   `json:"authorizerId"`
	AuthorizationScopes []string                  `json:"authorizationScopes"`
	APIKeyRequired      *bool                     `json:"apiKeyRequired"`
	OperationName       *string                   `json:"operationName"`
	RequestParameters   map[string]map[string]any `json:"requestParameters"`
	RequestModels       map[string]string         `json:"requestModels"`
	ModelSelection      *string                   `json:"modelSelectionExpression"`
}

func (s *Server) routeV2Routes(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 0 {
		switch r.Method {
		case http.MethodPost:
			return s.v2CreateRoute(w, r, apiID)
		case http.MethodGet:
			api, err := s.store.GetHTTP(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			items := make([]any, 0, len(api.V2Routes))
			for _, id := range sortedKeys(api.V2Routes) {
				items = append(items, viewV2Route(api.V2Routes[id]))
			}
			writeJSON(w, 200, map[string]any{"items": items})
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on routes")
	}
	routeID := segs[0]
	if len(segs) > 1 {
		switch segs[1] {
		case "routeresponses":
			return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement route responses: an HTTP API route answers with its integration's response")
		case "requestparameters":
			return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement DeleteRouteRequestParameter; update the route's requestParameters instead")
		}
		return errNotFound("unknown route subresource %s", segs[1])
	}
	switch r.Method {
	case http.MethodGet:
		api, err := s.store.GetHTTP(apiID)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		rt, ok := api.V2Routes[routeID]
		if !ok {
			return errNotFound("Invalid route identifier specified %s", routeID)
		}
		writeJSON(w, 200, viewV2Route(rt))
		return nil
	case http.MethodPatch:
		var req v2RouteInput
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		var out *V2Route
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			rt, ok := api.V2Routes[routeID]
			if !ok {
				return errNotFound("Invalid route identifier specified %s", routeID)
			}
			if err := applyV2RouteInput(api, rt, &req); err != nil {
				return err
			}
			s.autoDeployV2(api)
			out = rt
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, viewV2Route(out))
		return nil
	case http.MethodDelete:
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			if _, ok := api.V2Routes[routeID]; !ok {
				return errNotFound("Invalid route identifier specified %s", routeID)
			}
			delete(api.V2Routes, routeID)
			s.autoDeployV2(api)
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on a route")
}

func (s *Server) v2CreateRoute(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req v2RouteInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.RouteKey == nil || *req.RouteKey == "" {
		return errBadRequest("routeKey is required")
	}
	var out *V2Route
	_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
		rt := &V2Route{ID: s.store.newID(), AuthorizationType: "NONE"}
		if err := applyV2RouteInput(api, rt, &req); err != nil {
			return err
		}
		api.V2Routes[rt.ID] = rt
		s.autoDeployV2(api)
		out = rt
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewV2Route(out))
	return nil
}

// applyV2RouteInput validates and applies the fields a create or update
// sent. A route key must be well formed and unique in the API; a target
// must name an integration that exists; CUSTOM needs an authorizer.
func applyV2RouteInput(api *RestAPI, rt *V2Route, in *v2RouteInput) error {
	if in.RouteKey != nil {
		key := strings.TrimSpace(*in.RouteKey)
		if !validRouteKey(key) {
			return errBadRequest("Invalid route key %q: expected \"$default\" or \"<METHOD> /<path>\"", key)
		}
		// Stored in the spelling the data plane matches on, so "get /x" and
		// "GET /x" are the same key and the second is refused as such.
		if method, path := routeKeyParts(key); method != "" {
			key = method + " " + path
		}
		for _, other := range api.V2Routes {
			if other.ID != rt.ID && other.RouteKey == key {
				return errConflict("Route with key %s already exists for this API", key)
			}
		}
		rt.RouteKey = key
	}
	if in.Target != nil {
		target := *in.Target
		if target != "" {
			id := strings.TrimPrefix(target, "integrations/")
			if id == target || api.V2Integrations[id] == nil {
				return errBadRequest("Invalid target %q: expected integrations/<integrationId> naming an integration of this API", target)
			}
		}
		rt.Target = target
	}
	if in.AuthorizationType != nil {
		switch strings.ToUpper(*in.AuthorizationType) {
		case "NONE", "":
			rt.AuthorizationType = "NONE"
		case "CUSTOM":
			rt.AuthorizationType = "CUSTOM"
		case "AWS_IAM":
			// Stored as declared; the execute-api call is not signed locally,
			// so nothing is checked.
			rt.AuthorizationType = "AWS_IAM"
		case "JWT":
			return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement JWT authorization: there is no identity provider locally")
		default:
			return errBadRequest("Invalid authorizationType %q", *in.AuthorizationType)
		}
	}
	if in.AuthorizerID != nil {
		rt.AuthorizerID = *in.AuthorizerID
	}
	if rt.AuthorizationType == "CUSTOM" {
		if rt.AuthorizerID == "" || api.V2Authorizers[rt.AuthorizerID] == nil {
			return errBadRequest("A route with authorizationType CUSTOM must name an authorizer of this API in authorizerId")
		}
	}
	if in.AuthorizationScopes != nil {
		rt.AuthorizationScopes = in.AuthorizationScopes
	}
	if in.APIKeyRequired != nil {
		rt.APIKeyRequired = *in.APIKeyRequired
	}
	if in.OperationName != nil {
		rt.OperationName = *in.OperationName
	}
	if in.RequestParameters != nil {
		rt.RequestParameters = in.RequestParameters
	}
	if in.RequestModels != nil {
		rt.RequestModels = in.RequestModels
	}
	if in.ModelSelection != nil {
		rt.ModelSelection = *in.ModelSelection
	}
	return nil
}

func viewV2Route(rt *V2Route) map[string]any {
	v := map[string]any{
		"routeId": rt.ID, "routeKey": rt.RouteKey, "apiGatewayManaged": false,
		"apiKeyRequired": rt.APIKeyRequired, "authorizationType": rt.AuthorizationType,
	}
	putIfStr(v, "target", rt.Target)
	putIfStr(v, "authorizerId", rt.AuthorizerID)
	putIfStr(v, "operationName", rt.OperationName)
	putIfStr(v, "modelSelectionExpression", rt.ModelSelection)
	if len(rt.AuthorizationScopes) > 0 {
		v["authorizationScopes"] = rt.AuthorizationScopes
	}
	if len(rt.RequestParameters) > 0 {
		v["requestParameters"] = rt.RequestParameters
	}
	if len(rt.RequestModels) > 0 {
		v["requestModels"] = rt.RequestModels
	}
	return v
}

// ---- integrations ----

type v2IntegrationInput struct {
	IntegrationType      *string                      `json:"integrationType"`
	IntegrationURI       *string                      `json:"integrationUri"`
	IntegrationMethod    *string                      `json:"integrationMethod"`
	IntegrationSubtype   *string                      `json:"integrationSubtype"`
	PayloadFormatVersion *string                      `json:"payloadFormatVersion"`
	TimeoutInMillis      *int                         `json:"timeoutInMillis"`
	Description          *string                      `json:"description"`
	ConnectionType       *string                      `json:"connectionType"`
	ConnectionID         *string                      `json:"connectionId"`
	CredentialsARN       *string                      `json:"credentialsArn"`
	RequestParameters    map[string]string            `json:"requestParameters"`
	ResponseParameters   map[string]map[string]string `json:"responseParameters"`
}

func (s *Server) routeV2Integrations(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 0 {
		switch r.Method {
		case http.MethodPost:
			var req v2IntegrationInput
			if aerr := decode(r, &req); aerr != nil {
				return aerr
			}
			if req.IntegrationType == nil {
				return errBadRequest("integrationType is required")
			}
			var out *V2Integration
			_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
				integ := &V2Integration{ID: s.store.newID(), TimeoutInMillis: 30000, ConnectionType: "INTERNET"}
				if err := applyV2IntegrationInput(integ, &req); err != nil {
					return err
				}
				api.V2Integrations[integ.ID] = integ
				s.autoDeployV2(api)
				out = integ
				return nil
			})
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 201, viewV2Integration(out))
			return nil
		case http.MethodGet:
			api, err := s.store.GetHTTP(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			items := make([]any, 0, len(api.V2Integrations))
			for _, id := range sortedKeys(api.V2Integrations) {
				items = append(items, viewV2Integration(api.V2Integrations[id]))
			}
			writeJSON(w, 200, map[string]any{"items": items})
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on integrations")
	}
	integID := segs[0]
	if len(segs) > 1 {
		if segs[1] == "integrationresponses" {
			return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement integration responses: an HTTP API proxies the backend's response as is")
		}
		return errNotFound("unknown integration subresource %s", segs[1])
	}
	switch r.Method {
	case http.MethodGet:
		api, err := s.store.GetHTTP(apiID)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		integ, ok := api.V2Integrations[integID]
		if !ok {
			return errNotFound("Invalid integration identifier specified %s", integID)
		}
		writeJSON(w, 200, viewV2Integration(integ))
		return nil
	case http.MethodPatch:
		var req v2IntegrationInput
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		var out *V2Integration
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			integ, ok := api.V2Integrations[integID]
			if !ok {
				return errNotFound("Invalid integration identifier specified %s", integID)
			}
			if err := applyV2IntegrationInput(integ, &req); err != nil {
				return err
			}
			s.autoDeployV2(api)
			out = integ
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, viewV2Integration(out))
		return nil
	case http.MethodDelete:
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			if _, ok := api.V2Integrations[integID]; !ok {
				return errNotFound("Invalid integration identifier specified %s", integID)
			}
			for _, rt := range api.V2Routes {
				if rt.Target == "integrations/"+integID {
					return errConflict("Integration %s is the target of route %s; delete or retarget the route first", integID, rt.RouteKey)
				}
			}
			delete(api.V2Integrations, integID)
			s.autoDeployV2(api)
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on an integration")
}

// applyV2IntegrationInput validates and applies an integration's fields.
// HTTP APIs run AWS_PROXY (a Lambda function, payload 1.0 or 2.0) and
// HTTP_PROXY (a URL); the WebSocket kinds and AWS service integrations are
// refused by name rather than stored and then failing on the data plane.
func applyV2IntegrationInput(integ *V2Integration, in *v2IntegrationInput) error {
	if in.IntegrationType != nil {
		switch strings.ToUpper(*in.IntegrationType) {
		case "AWS_PROXY", "HTTP_PROXY":
			integ.Type = strings.ToUpper(*in.IntegrationType)
		case "AWS", "HTTP", "MOCK":
			return errBadRequest("integrationType %s is a WebSocket API integration; an HTTP API integration is AWS_PROXY or HTTP_PROXY", strings.ToUpper(*in.IntegrationType))
		default:
			return errBadRequest("Invalid integrationType %q", *in.IntegrationType)
		}
	}
	if in.IntegrationSubtype != nil && *in.IntegrationSubtype != "" {
		return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement the AWS service integration %s; use an AWS_PROXY integration to a function", *in.IntegrationSubtype)
	}
	if in.IntegrationURI != nil {
		integ.URI = *in.IntegrationURI
	}
	if in.IntegrationMethod != nil {
		integ.Method = strings.ToUpper(*in.IntegrationMethod)
	}
	if in.PayloadFormatVersion != nil {
		switch *in.PayloadFormatVersion {
		case "1.0", "2.0":
			integ.PayloadFormatVersion = *in.PayloadFormatVersion
		default:
			return errBadRequest("payloadFormatVersion must be 1.0 or 2.0")
		}
	}
	if in.TimeoutInMillis != nil {
		integ.TimeoutInMillis = *in.TimeoutInMillis
	}
	if in.Description != nil {
		integ.Description = *in.Description
	}
	if in.ConnectionType != nil {
		if strings.ToUpper(*in.ConnectionType) == "VPC_LINK" {
			return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement VPC links: there is no VPC locally")
		}
		integ.ConnectionType = strings.ToUpper(*in.ConnectionType)
	}
	if in.ConnectionID != nil {
		integ.ConnectionID = *in.ConnectionID
	}
	if in.CredentialsARN != nil {
		integ.CredentialsARN = *in.CredentialsARN
	}
	if in.RequestParameters != nil {
		integ.RequestParameters = in.RequestParameters
	}
	if in.ResponseParameters != nil {
		integ.ResponseParameters = in.ResponseParameters
	}
	switch integ.Type {
	case "AWS_PROXY":
		if integ.URI == "" {
			return errBadRequest("An AWS_PROXY integration needs integrationUri: the function ARN or its invoke URI")
		}
		if integ.PayloadFormatVersion == "" {
			integ.PayloadFormatVersion = "2.0"
		}
	case "HTTP_PROXY":
		if !strings.HasPrefix(integ.URI, "http://") && !strings.HasPrefix(integ.URI, "https://") {
			return errBadRequest("An HTTP_PROXY integration needs an http(s) integrationUri")
		}
		if integ.Method == "" {
			integ.Method = "ANY"
		}
		integ.PayloadFormatVersion = "1.0"
	case "":
		return errBadRequest("integrationType is required")
	}
	return nil
}

func viewV2Integration(integ *V2Integration) map[string]any {
	v := map[string]any{
		"integrationId": integ.ID, "integrationType": integ.Type, "apiGatewayManaged": false,
		"timeoutInMillis": integ.TimeoutInMillis, "connectionType": integ.ConnectionType,
		"payloadFormatVersion": integ.PayloadFormatVersion,
	}
	putIfStr(v, "integrationUri", integ.URI)
	putIfStr(v, "integrationMethod", integ.Method)
	putIfStr(v, "description", integ.Description)
	putIfStr(v, "connectionId", integ.ConnectionID)
	putIfStr(v, "credentialsArn", integ.CredentialsARN)
	if len(integ.RequestParameters) > 0 {
		v["requestParameters"] = integ.RequestParameters
	}
	if len(integ.ResponseParameters) > 0 {
		v["responseParameters"] = integ.ResponseParameters
	}
	return v
}

// ---- authorizers ----

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

func (s *Server) routeV2Authorizers(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 0 {
		switch r.Method {
		case http.MethodPost:
			var req v2AuthorizerInput
			if aerr := decode(r, &req); aerr != nil {
				return aerr
			}
			if req.Name == nil || *req.Name == "" {
				return errBadRequest("name is required")
			}
			var out *V2Authorizer
			_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
				for _, other := range api.V2Authorizers {
					if other.Name == *req.Name {
						return errConflict("Authorizer name %s already exists", *req.Name)
					}
				}
				a := &V2Authorizer{ID: s.store.newID(), PayloadFormatVersion: "2.0"}
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
		case http.MethodGet:
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
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on authorizers")
	}
	authID := segs[0]
	switch r.Method {
	case http.MethodGet:
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
	case http.MethodPatch:
		var req v2AuthorizerInput
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		var out *V2Authorizer
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
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
	case http.MethodDelete:
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
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
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on an authorizer")
}

func applyV2AuthorizerInput(a *V2Authorizer, in *v2AuthorizerInput) error {
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

func viewV2Authorizer(a *V2Authorizer) map[string]any {
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
