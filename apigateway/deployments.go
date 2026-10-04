package apigateway

// REST API deployments: a snapshot of an API that can answer.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func (s *Server) getDeployments(w http.ResponseWriter, apiID string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	items := make([]any, 0, len(api.Deployments))
	for _, id := range sortedKeys(api.Deployments) {
		items = append(items, viewDeployment(api.Deployments[id]))
	}
	writeJSON(w, 200, map[string]any{"item": items})
	return nil
}

func (s *Server) getDeployment(w http.ResponseWriter, apiID, depID string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	dep, ok := api.Deployments[depID]
	if !ok {
		return errNotFound("Invalid Deployment identifier specified")
	}
	writeJSON(w, 200, viewDeployment(dep))
	return nil
}

// deleteDeployment is as the HTTP plane's (v2_control.go): a deployment a
// stage still serves is refused, and so is one that does not exist.
func (s *Server) deleteDeployment(w http.ResponseWriter, apiID, depID string) *awshttp.APIError {
	if _, err := s.store.Update(apiID, func(api *restAPI) error {
		if _, ok := api.Deployments[depID]; !ok {
			return errNotFound("Invalid Deployment identifier specified")
		}
		for _, st := range api.Stages {
			if st.DeploymentID == depID {
				return errBadRequest("Active stages pointing to this deployment must be moved or deleted")
			}
		}
		delete(api.Deployments, depID)
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(202)
	return nil
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req struct {
		StageName        string            `json:"stageName"`
		StageDescription string            `json:"stageDescription"`
		Description      string            `json:"description"`
		Variables        map[string]string `json:"variables"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	var dep *deployment
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		// A deployment is a snapshot of something that can answer. An API
		// with no methods, or a method with no integration behind it, is
		// refused — it used to deploy, and then 404 or 500 on every request,
		// which reads as a routing bug rather than as an unfinished API.
		methods := 0
		for _, res := range api.Resources {
			for _, m := range res.Methods {
				methods++
				if m.Integration == nil {
					return errBadRequest("No integration defined for method")
				}
			}
		}
		if methods == 0 {
			return errBadRequest("The REST API doesn't contain any methods")
		}
		now := s.now().Unix()
		dep = &deployment{ID: s.store.newID(), Description: req.Description, Created: now}
		if api.Deployments == nil {
			api.Deployments = map[string]*deployment{}
		}
		api.Deployments[dep.ID] = dep
		// CreateDeployment with a stageName creates the stage too, which is
		// how most templates and the CLI deploy in one call.
		if req.StageName != "" {
			if api.Stages == nil {
				api.Stages = map[string]*stage{}
			}
			api.Stages[req.StageName] = &stage{
				Name: req.StageName, DeploymentID: dep.ID, Description: req.StageDescription,
				Variables: req.Variables, Created: now, Updated: now,
			}
		}
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	s.logf("apigateway: deployed api %s stage %q", apiID, req.StageName)
	writeJSON(w, 201, viewDeployment(dep))
	return nil
}
