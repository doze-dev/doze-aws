package apigateway

// HTTP API routes: a route key and the target it forwards to.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

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

func (s *Server) v2GetRoutes(w http.ResponseWriter, apiID string) *awshttp.APIError {
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

func (s *Server) v2GetRoute(w http.ResponseWriter, apiID, routeID string) *awshttp.APIError {
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
}

func (s *Server) v2UpdateRoute(w http.ResponseWriter, r *http.Request, apiID, routeID string) *awshttp.APIError {
	var req v2RouteInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	var out *v2Route
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
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
}

func (s *Server) v2DeleteRoute(w http.ResponseWriter, apiID, routeID string) *awshttp.APIError {
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
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

func (s *Server) v2CreateRoute(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req v2RouteInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.RouteKey == nil || *req.RouteKey == "" {
		return errBadRequest("routeKey is required")
	}
	var out *v2Route
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
		rt := &v2Route{ID: s.store.newID(), AuthorizationType: "NONE"}
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
func applyV2RouteInput(api *restAPI, rt *v2Route, in *v2RouteInput) error {
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

func viewV2Route(rt *v2Route) map[string]any {
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
