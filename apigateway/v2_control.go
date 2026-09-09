package apigateway

// The HTTP API (apigatewayv2) control plane at /v2/apis/...: APIs, stages,
// deployments and tags. Routes, integrations and authorizers are in
// v2_routes.go. The v2 API is restJson1 with lowerCamel JSON, ISO-8601
// dates, and update calls that send the fields to change rather than a
// patch document.

import (
	"net/http"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// routeV2 dispatches everything under /v2/.
func (s *Server) routeV2(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	if len(segs) < 2 {
		return errNotFound("unknown v2 resource")
	}
	switch segs[1] {
	case "apis":
		return s.routeV2APIs(w, r, segs[2:])
	case "tags":
		return s.routeTags(w, r, segs[1:])
	case "domainnames", "vpclinks":
		return awshttp.Errf(501, "NotImplemented",
			"doze-aws does not implement API Gateway v2 %s: there is no DNS, TLS termination or VPC locally", segs[1])
	case "portals", "portalproducts":
		return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement API Gateway v2 portals")
	}
	return errNotFound("unknown v2 resource %s", segs[1])
}

// routeV2APIs handles /v2/apis and everything beneath one API.
func (s *Server) routeV2APIs(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	if len(segs) == 0 {
		switch r.Method {
		case http.MethodPost:
			return s.v2CreateAPI(w, r)
		case http.MethodGet:
			apis, err := s.store.ListProtocol("HTTP")
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			items := make([]any, 0, len(apis))
			for i := range apis {
				items = append(items, viewV2API(&apis[i]))
			}
			writeJSON(w, 200, map[string]any{"items": items})
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on /v2/apis")
	}
	apiID := segs[0]
	if len(segs) == 1 {
		switch r.Method {
		case http.MethodGet:
			api, err := s.store.GetHTTP(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 200, viewV2API(api))
			return nil
		case http.MethodPatch:
			return s.v2UpdateAPI(w, r, apiID)
		case http.MethodDelete:
			api, err := s.store.GetHTTP(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			if err := s.store.Delete(apiID); err != nil {
				return awshttp.AsAPIError(err)
			}
			for id := range api.V2Authorizers {
				s.authCache.forget(id)
			}
			w.WriteHeader(204)
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on an HTTP API")
	}
	switch segs[1] {
	case "routes":
		return s.routeV2Routes(w, r, apiID, segs[2:])
	case "integrations":
		return s.routeV2Integrations(w, r, apiID, segs[2:])
	case "authorizers":
		return s.routeV2Authorizers(w, r, apiID, segs[2:])
	case "stages":
		return s.routeV2Stages(w, r, apiID, segs[2:])
	case "deployments":
		return s.routeV2Deployments(w, r, apiID, segs[2:])
	case "cors":
		if r.Method == http.MethodDelete {
			if _, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error { api.CORS = nil; return nil }); err != nil {
				return awshttp.AsAPIError(err)
			}
			w.WriteHeader(204)
			return nil
		}
	case "models", "exports", "routingrules":
		return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement API Gateway v2 %s", segs[1])
	}
	return errNotFound("unknown HTTP API subresource %s", segs[1])
}

// ---- APIs ----

// v2APIInput is the body of CreateApi and UpdateApi; pointers tell an absent
// field from an empty one on update.
type v2APIInput struct {
	Name                      *string           `json:"name"`
	ProtocolType              string            `json:"protocolType"`
	Description               *string           `json:"description"`
	Version                   *string           `json:"version"`
	CorsConfiguration         *v2CORSInput      `json:"corsConfiguration"`
	RouteSelectionExpression  *string           `json:"routeSelectionExpression"`
	DisableSchemaValidation   *bool             `json:"disableSchemaValidation"`
	DisableExecuteApiEndpoint *bool             `json:"disableExecuteApiEndpoint"`
	IPAddressType             *string           `json:"ipAddressType"`
	Tags                      map[string]string `json:"tags"`
	// Quick-create: a target Lambda ARN or URL and an optional route key make
	// the integration, the route and a $default stage in one call.
	Target                    string `json:"target"`
	RouteKey                  string `json:"routeKey"`
	CredentialsARN            string `json:"credentialsArn"`
	APIKeySelectionExpression string `json:"apiKeySelectionExpression"`
}

type v2CORSInput struct {
	AllowCredentials *bool    `json:"allowCredentials"`
	AllowHeaders     []string `json:"allowHeaders"`
	AllowMethods     []string `json:"allowMethods"`
	AllowOrigins     []string `json:"allowOrigins"`
	ExposeHeaders    []string `json:"exposeHeaders"`
	MaxAge           *int     `json:"maxAge"`
}

func (in *v2CORSInput) config() *CORSConfig {
	c := &CORSConfig{
		AllowHeaders: in.AllowHeaders, AllowMethods: in.AllowMethods,
		AllowOrigins: in.AllowOrigins, ExposeHeaders: in.ExposeHeaders, MaxAge: in.MaxAge,
	}
	if in.AllowCredentials != nil {
		c.AllowCredentials = *in.AllowCredentials
	}
	return c
}

func (s *Server) v2CreateAPI(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	var req v2APIInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.Name == nil || *req.Name == "" {
		return errBadRequest("name is required")
	}
	switch strings.ToUpper(req.ProtocolType) {
	case "HTTP":
	case "WEBSOCKET":
		return awshttp.Errf(501, "NotImplemented", "doze-aws does not implement WebSocket APIs; protocolType must be HTTP")
	default:
		return errBadRequest("protocolType must be HTTP or WEBSOCKET")
	}
	api, err := s.store.CreateHTTP(*req.Name, strVal(req.Description), strVal(req.Version), req.Tags)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	api, err = s.store.Update(api.ID, func(api *RestAPI) error {
		applyV2APIInput(api, &req)
		if req.Target != "" {
			return s.v2QuickCreate(api, req.Target, req.RouteKey)
		}
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	s.logf("apigateway: created HTTP API %s (%s)", api.Name, api.ID)
	writeJSON(w, 201, viewV2API(api))
	return nil
}

// v2QuickCreate is CreateApi's target shortcut: one integration, one route
// (ANY /{proxy+} unless a key is given) and a $default stage that auto
// deploys — what `aws apigatewayv2 create-api --target` makes.
func (s *Server) v2QuickCreate(api *RestAPI, target, routeKey string) error {
	integ := &V2Integration{ID: s.store.newID(), TimeoutInMillis: 30000, PayloadFormatVersion: "2.0"}
	if strings.HasPrefix(target, "arn:") {
		integ.Type, integ.URI = "AWS_PROXY", target
	} else {
		integ.Type, integ.URI, integ.Method = "HTTP_PROXY", target, "ANY"
		integ.PayloadFormatVersion = "1.0"
	}
	api.V2Integrations[integ.ID] = integ
	if routeKey == "" {
		routeKey = "$default"
	}
	if !validRouteKey(routeKey) {
		return errBadRequest("Invalid route key %q", routeKey)
	}
	route := &V2Route{ID: s.store.newID(), RouteKey: routeKey, Target: "integrations/" + integ.ID, AuthorizationType: "NONE"}
	api.V2Routes[route.ID] = route
	now := s.now().Unix()
	dep := &Deployment{ID: s.store.newID(), Created: now, AutoDeployed: true}
	api.Deployments[dep.ID] = dep
	api.Stages["$default"] = &Stage{Name: "$default", AutoDeploy: true, DeploymentID: dep.ID, Created: now, Updated: now}
	return nil
}

func applyV2APIInput(api *RestAPI, in *v2APIInput) {
	if in.Name != nil && *in.Name != "" {
		api.Name = *in.Name
	}
	if in.Description != nil {
		api.Description = *in.Description
	}
	if in.Version != nil {
		api.Version = *in.Version
	}
	if in.CorsConfiguration != nil {
		api.CORS = in.CorsConfiguration.config()
	}
	if in.RouteSelectionExpression != nil {
		api.RouteSelection = *in.RouteSelectionExpression
	}
	if in.DisableSchemaValidation != nil {
		api.DisableSchemaValidation = *in.DisableSchemaValidation
	}
	if in.DisableExecuteApiEndpoint != nil {
		api.DisableExecuteAPI = *in.DisableExecuteApiEndpoint
	}
	if in.IPAddressType != nil {
		api.IPAddressType = *in.IPAddressType
	}
	if in.Tags != nil {
		if api.Tags == nil {
			api.Tags = map[string]string{}
		}
		for k, v := range in.Tags {
			api.Tags[k] = v
		}
	}
}

func (s *Server) v2UpdateAPI(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req v2APIInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	api, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
		applyV2APIInput(api, &req)
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewV2API(api))
	return nil
}

// ---- deployments ----

func (s *Server) routeV2Deployments(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 0 {
		switch r.Method {
		case http.MethodPost:
			var req struct {
				Description string `json:"description"`
				StageName   string `json:"stageName"`
			}
			if aerr := decode(r, &req); aerr != nil {
				return aerr
			}
			var dep *Deployment
			_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
				if req.StageName != "" {
					if _, ok := api.Stages[req.StageName]; !ok {
						return errNotFound("Invalid stage identifier specified")
					}
				}
				dep = &Deployment{ID: s.store.newID(), Description: req.Description, Created: s.now().Unix()}
				api.Deployments[dep.ID] = dep
				if req.StageName != "" {
					api.Stages[req.StageName].DeploymentID = dep.ID
				}
				return nil
			})
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 201, viewV2Deployment(dep))
			return nil
		case http.MethodGet:
			api, err := s.store.GetHTTP(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			items := make([]any, 0, len(api.Deployments))
			for _, id := range sortedKeys(api.Deployments) {
				items = append(items, viewV2Deployment(api.Deployments[id]))
			}
			writeJSON(w, 200, map[string]any{"items": items})
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on deployments")
	}
	depID := segs[0]
	switch r.Method {
	case http.MethodGet:
		api, err := s.store.GetHTTP(apiID)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		dep, ok := api.Deployments[depID]
		if !ok {
			return errNotFound("Invalid deployment identifier specified %s", depID)
		}
		writeJSON(w, 200, viewV2Deployment(dep))
		return nil
	case http.MethodPatch:
		var req struct {
			Description *string `json:"description"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		var dep *Deployment
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			d, ok := api.Deployments[depID]
			if !ok {
				return errNotFound("Invalid deployment identifier specified %s", depID)
			}
			if req.Description != nil {
				d.Description = *req.Description
			}
			dep = d
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, viewV2Deployment(dep))
		return nil
	case http.MethodDelete:
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			if _, ok := api.Deployments[depID]; !ok {
				return errNotFound("Invalid deployment identifier specified %s", depID)
			}
			for _, st := range api.Stages {
				if st.DeploymentID == depID {
					return errBadRequest("Active stages pointing to this deployment must be moved or deleted")
				}
			}
			delete(api.Deployments, depID)
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on a deployment")
}

// autoDeployV2 makes the deployment an auto-deploy stage makes on every
// route or integration change, and points the stage at it. Called inside a
// store Update.
func (s *Server) autoDeployV2(api *RestAPI) {
	for _, st := range api.Stages {
		if !st.AutoDeploy {
			continue
		}
		dep := &Deployment{ID: s.store.newID(), Created: s.now().Unix(), AutoDeployed: true, Description: "Automatic deployment triggered by changes to the Api configuration"}
		api.Deployments[dep.ID] = dep
		st.DeploymentID = dep.ID
		st.Updated = dep.Created
	}
}

// ---- views ----

func v2Time(unix int64) string { return time.Unix(unix, 0).UTC().Format(time.RFC3339) }

// V2Endpoint is where an HTTP API answers: its $default stage at the root,
// a named stage under its name.
func V2Endpoint(apiID string) string {
	return "http://127.0.0.1:4566" + ExecutePrefix + apiID
}

// V2APIARN is the ARN a v2 API is tagged by.
func V2APIARN(apiID string) string {
	return "arn:aws:apigateway:" + awsident.Region + "::/apis/" + apiID
}

func viewV2API(api *RestAPI) map[string]any {
	v := map[string]any{
		"apiId":                     api.ID,
		"name":                      api.Name,
		"protocolType":              "HTTP",
		"apiEndpoint":               V2Endpoint(api.ID),
		"createdDate":               v2Time(api.Created),
		"routeSelectionExpression":  api.RouteSelection,
		"disableExecuteApiEndpoint": api.DisableExecuteAPI,
		"disableSchemaValidation":   api.DisableSchemaValidation,
		"apiKeySelectionExpression": "$request.header.x-api-key",
		"ipAddressType":             api.IPAddressType,
		"tags":                      orEmptyMap(api.Tags),
	}
	putIfStr(v, "description", api.Description)
	putIfStr(v, "version", api.Version)
	if api.CORS != nil {
		v["corsConfiguration"] = viewCORS(api.CORS)
	}
	return v
}

func viewCORS(c *CORSConfig) map[string]any {
	v := map[string]any{"allowCredentials": c.AllowCredentials}
	if len(c.AllowHeaders) > 0 {
		v["allowHeaders"] = c.AllowHeaders
	}
	if len(c.AllowMethods) > 0 {
		v["allowMethods"] = c.AllowMethods
	}
	if len(c.AllowOrigins) > 0 {
		v["allowOrigins"] = c.AllowOrigins
	}
	if len(c.ExposeHeaders) > 0 {
		v["exposeHeaders"] = c.ExposeHeaders
	}
	if c.MaxAge != nil {
		v["maxAge"] = *c.MaxAge
	}
	return v
}

func viewV2Deployment(d *Deployment) map[string]any {
	v := map[string]any{
		"deploymentId": d.ID, "createdDate": v2Time(d.Created),
		"deploymentStatus": "DEPLOYED", "autoDeployed": d.AutoDeployed,
	}
	putIfStr(v, "description", d.Description)
	return v
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
