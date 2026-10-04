package apigateway

// REST API stages: a named, addressable point at a deployment.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func (s *Server) getStages(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	items := make([]any, 0, len(api.Stages))
	for _, name := range sortedKeys(api.Stages) {
		items = append(items, viewStage(s.invokeBase(r), apiID, api.Stages[name]))
	}
	writeJSON(w, 200, map[string]any{"item": items})
	return nil
}

func (s *Server) getStage(w http.ResponseWriter, r *http.Request, apiID, name string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	st, ok := api.Stages[name]
	if !ok {
		return errNotFound("Invalid stage identifier specified")
	}
	writeJSON(w, 200, viewStage(s.invokeBase(r), apiID, st))
	return nil
}

func (s *Server) deleteStage(w http.ResponseWriter, apiID, name string) *awshttp.APIError {
	if _, err := s.store.Update(apiID, func(api *restAPI) error {
		delete(api.Stages, name)
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(202)
	return nil
}

func (s *Server) createStage(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req struct {
		StageName      string            `json:"stageName"`
		DeploymentID   string            `json:"deploymentId"`
		Description    string            `json:"description"`
		Variables      map[string]string `json:"variables"`
		Tags           map[string]string `json:"tags"`
		TracingEnabled bool              `json:"tracingEnabled"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.StageName == "" {
		return errBadRequest("stageName is required")
	}
	var out *stage
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		if _, exists := api.Stages[req.StageName]; exists {
			return errConflict("Stage already exists: %s", req.StageName)
		}
		now := s.now().Unix()
		out = &stage{
			Name: req.StageName, DeploymentID: req.DeploymentID, Description: req.Description,
			Variables: req.Variables, Tags: req.Tags, TracingEnabled: req.TracingEnabled,
			Created: now, Updated: now,
		}
		if api.Stages == nil {
			api.Stages = map[string]*stage{}
		}
		api.Stages[req.StageName] = out
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewStage(s.invokeBase(r), apiID, out))
	return nil
}

func (s *Server) patchStage(w http.ResponseWriter, r *http.Request, apiID, name string) *awshttp.APIError {
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	var out *stage
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		st, ok := api.Stages[name]
		if !ok {
			return errNotFound("Invalid stage identifier specified")
		}
		for _, op := range ops {
			switch {
			case op.Path == "/description":
				st.Description = op.Value
			case op.Path == "/deploymentId":
				st.DeploymentID = op.Value
			case op.Path == "/tracingEnabled":
				st.TracingEnabled = op.Value == "true"
			case strings.HasPrefix(op.Path, "/variables/"):
				key := strings.TrimPrefix(op.Path, "/variables/")
				if st.Variables == nil {
					st.Variables = map[string]string{}
				}
				if op.Op == "remove" {
					delete(st.Variables, key)
				} else {
					st.Variables[key] = op.Value
				}
			case op.Path == "/accessLogSettings" && op.Op == "remove":
				st.AccessLog = nil
			case op.Path == "/accessLogSettings/destinationArn", op.Path == "/accessLogSettings/format":
				if st.AccessLog == nil {
					st.AccessLog = &accessLogSettings{}
				}
				if op.Path == "/accessLogSettings/destinationArn" {
					if op.Value != "" && logGroupFromARN(op.Value) == "" {
						return errBadRequest("Invalid patch value '%s' for path '/accessLogSettings/destinationArn': a CloudWatch Logs log group ARN is required", op.Value)
					}
					st.AccessLog.DestinationARN = op.Value
				} else {
					st.AccessLog.Format = op.Value
				}
			default:
				handled, err := applyMethodSettingPatch(st, op)
				if err != nil {
					return err
				}
				if !handled {
					// AWS refuses a path it does not know rather than
					// answering success for a change it did not make.
					return errBadRequest("Invalid patch path '%s'", op.Path)
				}
			}
		}
		st.Updated = s.now().Unix()
		out = st
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewStage(s.invokeBase(r), apiID, out))
	return nil
}
