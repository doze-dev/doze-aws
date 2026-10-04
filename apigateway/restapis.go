package apigateway

// REST APIs: create, list, read, patch and delete.

import (
	"net/http"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func (s *Server) getRestAPI(w http.ResponseWriter, apiID string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewAPI(api))
	return nil
}

func (s *Server) deleteRestAPI(w http.ResponseWriter, apiID string) *awshttp.APIError {
	if err := s.store.Delete(apiID); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(202)
	return nil
}

func (s *Server) createRestAPI(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	var req struct {
		Name                   string            `json:"name"`
		Description            string            `json:"description"`
		Version                string            `json:"version"`
		Tags                   map[string]string `json:"tags"`
		APIKeySource           string            `json:"apiKeySource"`
		BinaryMediaTypes       []string          `json:"binaryMediaTypes"`
		MinimumCompressionSize *int              `json:"minimumCompressionSize"`
		DisableExecuteAPI      bool              `json:"disableExecuteApiEndpoint"`
		Policy                 string            `json:"policy"`
		EndpointConfiguration  *struct {
			Types []string `json:"types"`
		} `json:"endpointConfiguration"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	api, err := s.store.Create(req.Name, req.Description, req.Version, req.Tags)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	api.BinaryMediaTypes = req.BinaryMediaTypes
	api.MinimumCompressionSize = req.MinimumCompressionSize
	api.DisableExecuteAPI = req.DisableExecuteAPI
	api.Policy = req.Policy
	if req.APIKeySource != "" {
		api.APIKeySource = req.APIKeySource
	}
	if req.EndpointConfiguration != nil {
		api.EndpointTypes = req.EndpointConfiguration.Types
	}
	if len(api.EndpointTypes) == 0 {
		api.EndpointTypes = []string{"EDGE"}
	}
	if err := s.store.Put(api); err != nil {
		return awshttp.AsAPIError(err)
	}
	s.logf("apigateway: created api %s (%s)", api.ID, api.Name)
	writeJSON(w, 201, viewAPI(api))
	return nil
}

func (s *Server) listRestAPIs(w http.ResponseWriter) *awshttp.APIError {
	apis, err := s.store.ListProtocol("")
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	items := make([]any, 0, len(apis))
	for i := range apis {
		items = append(items, viewAPI(&apis[i]))
	}
	writeJSON(w, 200, map[string]any{"item": items})
	return nil
}

func (s *Server) patchRestAPI(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	api, err := s.store.Update(apiID, func(api *restAPI) error {
		for _, op := range ops {
			switch op.Path {
			case "/name":
				api.Name = op.Value
			case "/description":
				api.Description = op.Value
			case "/version":
				api.Version = op.Value
			case "/apiKeySource":
				api.APIKeySource = op.Value
			case "/policy":
				api.Policy = op.Value
			}
		}
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewAPI(api))
	return nil
}
