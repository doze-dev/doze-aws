package apigateway

// HTTP API stages: /v2/apis/{id}/stages. A stage is the same record a REST
// stage is; what differs is the v2 shape (stageVariables, autoDeploy, route
// settings) and that an HTTP API has access logs only — there is no
// execution log to switch on, as on AWS.

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

type v2StageInput struct {
	StageName         string            `json:"stageName"`
	AutoDeploy        *bool             `json:"autoDeploy"`
	DeploymentID      *string           `json:"deploymentId"`
	Description       *string           `json:"description"`
	StageVariables    map[string]string `json:"stageVariables"`
	AccessLogSettings *struct {
		DestinationARN string `json:"destinationArn"`
		Format         string `json:"format"`
	} `json:"accessLogSettings"`
	DefaultRouteSettings map[string]any            `json:"defaultRouteSettings"`
	RouteSettings        map[string]map[string]any `json:"routeSettings"`
	Tags                 map[string]string         `json:"tags"`
	ClientCertificateID  *string                   `json:"clientCertificateId"`
}

func (s *Server) routeV2Stages(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 0 {
		switch r.Method {
		case http.MethodPost:
			var req v2StageInput
			if aerr := decode(r, &req); aerr != nil {
				return aerr
			}
			if req.StageName == "" {
				return errBadRequest("stageName is required")
			}
			var out *Stage
			_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
				if _, exists := api.Stages[req.StageName]; exists {
					return errConflict("Stage already exists: %s", req.StageName)
				}
				now := s.now().Unix()
				st := &Stage{Name: req.StageName, Created: now, Updated: now}
				if err := applyV2StageInput(api, st, &req); err != nil {
					return err
				}
				api.Stages[st.Name] = st
				if st.AutoDeploy && st.DeploymentID == "" {
					s.autoDeployV2(api)
				}
				out = st
				return nil
			})
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 201, viewV2Stage(s.invokeBase(r), apiID, out))
			return nil
		case http.MethodGet:
			api, err := s.store.GetHTTP(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			items := make([]any, 0, len(api.Stages))
			for _, name := range sortedKeys(api.Stages) {
				items = append(items, viewV2Stage(s.invokeBase(r), apiID, api.Stages[name]))
			}
			writeJSON(w, 200, map[string]any{"items": items})
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on stages")
	}
	name := segs[0]
	if len(segs) > 1 {
		return s.routeV2StageSub(w, r, apiID, name, segs[1:])
	}
	switch r.Method {
	case http.MethodGet:
		api, err := s.store.GetHTTP(apiID)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		st, ok := api.Stages[name]
		if !ok {
			return errNotFound("Invalid stage identifier specified %s", name)
		}
		writeJSON(w, 200, viewV2Stage(s.invokeBase(r), apiID, st))
		return nil
	case http.MethodPatch:
		var req v2StageInput
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		var out *Stage
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			st, ok := api.Stages[name]
			if !ok {
				return errNotFound("Invalid stage identifier specified %s", name)
			}
			if err := applyV2StageInput(api, st, &req); err != nil {
				return err
			}
			st.Updated = s.now().Unix()
			if st.AutoDeploy && st.DeploymentID == "" {
				s.autoDeployV2(api)
			}
			out = st
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, viewV2Stage(s.invokeBase(r), apiID, out))
		return nil
	case http.MethodDelete:
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			if _, ok := api.Stages[name]; !ok {
				return errNotFound("Invalid stage identifier specified %s", name)
			}
			delete(api.Stages, name)
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on a stage")
}

// routeV2StageSub is the three DELETEs beneath a stage: its access log
// settings, one route's settings, and the authorizer cache.
func (s *Server) routeV2StageSub(w http.ResponseWriter, r *http.Request, apiID, name string, segs []string) *awshttp.APIError {
	if r.Method != http.MethodDelete {
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on %s", segs[0])
	}
	mutate := func(fn func(st *Stage) error) *awshttp.APIError {
		_, err := s.store.UpdateHTTP(apiID, func(api *RestAPI) error {
			st, ok := api.Stages[name]
			if !ok {
				return errNotFound("Invalid stage identifier specified %s", name)
			}
			if err := fn(st); err != nil {
				return err
			}
			st.Updated = s.now().Unix()
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	switch segs[0] {
	case "accesslogsettings":
		return mutate(func(st *Stage) error { st.AccessLog = nil; return nil })
	case "routesettings":
		// The key ("GET /items/{id}") arrives percent-encoded and the router
		// split the decoded path, so it is re-read from the escaped one.
		key := v2RouteKeyLabel(r)
		if key == "" {
			return errNotFound("a route key is required")
		}
		return mutate(func(st *Stage) error {
			if _, ok := st.RouteSettings[key]; !ok {
				return errNotFound("Invalid route key specified %s", key)
			}
			delete(st.RouteSettings, key)
			return nil
		})
	case "cache":
		if len(segs) == 2 && segs[1] == "authorizers" {
			api, err := s.store.GetHTTP(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			if _, ok := api.Stages[name]; !ok {
				return errNotFound("Invalid stage identifier specified %s", name)
			}
			for id := range api.V2Authorizers {
				s.authCache.forget(id)
			}
			w.WriteHeader(204)
			return nil
		}
	}
	return errNotFound("unknown stage subresource %s", segs[0])
}

func applyV2StageInput(api *RestAPI, st *Stage, in *v2StageInput) error {
	if in.AutoDeploy != nil {
		st.AutoDeploy = *in.AutoDeploy
	}
	if in.DeploymentID != nil {
		if *in.DeploymentID != "" && api.Deployments[*in.DeploymentID] == nil {
			return errNotFound("Invalid deployment identifier specified %s", *in.DeploymentID)
		}
		st.DeploymentID = *in.DeploymentID
	}
	if in.Description != nil {
		st.Description = *in.Description
	}
	if in.StageVariables != nil {
		if st.Variables == nil {
			st.Variables = map[string]string{}
		}
		for k, v := range in.StageVariables {
			st.Variables[k] = v
		}
	}
	if in.AccessLogSettings != nil {
		if in.AccessLogSettings.DestinationARN != "" && logGroupFromARN(in.AccessLogSettings.DestinationARN) == "" {
			return errBadRequest("Invalid accessLogSettings.destinationArn %q: a CloudWatch Logs log group ARN is required", in.AccessLogSettings.DestinationARN)
		}
		st.AccessLog = &AccessLogSettings{DestinationARN: in.AccessLogSettings.DestinationARN, Format: in.AccessLogSettings.Format}
	}
	if in.DefaultRouteSettings != nil {
		st.DefaultRouteSettings = in.DefaultRouteSettings
	}
	if in.RouteSettings != nil {
		if st.RouteSettings == nil {
			st.RouteSettings = map[string]map[string]any{}
		}
		for k, v := range in.RouteSettings {
			st.RouteSettings[k] = v
		}
	}
	if in.Tags != nil {
		if st.Tags == nil {
			st.Tags = map[string]string{}
		}
		for k, v := range in.Tags {
			st.Tags[k] = v
		}
	}
	return nil
}

func viewV2Stage(base, apiID string, st *Stage) map[string]any {
	v := map[string]any{
		"stageName":            st.Name,
		"apiGatewayManaged":    false,
		"autoDeploy":           st.AutoDeploy,
		"createdDate":          v2Time(st.Created),
		"lastUpdatedDate":      v2Time(st.Updated),
		"stageVariables":       orEmptyMap(st.Variables),
		"tags":                 orEmptyMap(st.Tags),
		"defaultRouteSettings": orEmptyAny(st.DefaultRouteSettings),
		"routeSettings":        map[string]any{},
	}
	if len(st.RouteSettings) > 0 {
		rs := map[string]any{}
		for k, setting := range st.RouteSettings {
			rs[k] = setting
		}
		v["routeSettings"] = rs
	}
	putIfStr(v, "deploymentId", st.DeploymentID)
	putIfStr(v, "description", st.Description)
	if st.AccessLog != nil {
		v["accessLogSettings"] = map[string]any{"destinationArn": st.AccessLog.DestinationARN, "format": st.AccessLog.Format}
	}
	if st.AutoDeploy && st.DeploymentID != "" {
		v["lastDeploymentStatusMessage"] = "Successfully deployed stage with deployment ID '" + st.DeploymentID + "'"
	}
	// The invoke URL is the practical output, as it is for a REST stage.
	if st.Name == "$default" {
		v["invokeUrl"] = V2Endpoint(base, apiID)
	} else {
		v["invokeUrl"] = V2Endpoint(base, apiID) + "/" + st.Name
	}
	return v
}

func orEmptyAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// v2RouteKeyLabel reads the route key label of a .../routesettings/{routeKey}
// path from the escaped path, since the decoded one has the key's own "/"
// in it. "" when the path carries no key.
func v2RouteKeyLabel(r *http.Request) string {
	segs := strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/")
	for i, seg := range segs {
		if seg == "routesettings" && i+1 < len(segs) {
			key, err := url.PathUnescape(strings.Join(segs[i+1:], "/"))
			if err != nil {
				return ""
			}
			return key
		}
	}
	return ""
}
