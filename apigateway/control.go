package apigateway

// The REST (v1) control plane: APIs, resources, methods, integrations,
// responses, deployments and stages.
//
// API Gateway's control plane is path-routed, and the paths nest deeply:
//
//	/restapis/{api}/resources/{res}/methods/{verb}/integration/responses/{code}
//
// so the dispatcher walks the segments rather than pattern-matching, and each
// level hands off to the next.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// ---- helpers ----

func errNotFound(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(404, "NotFoundException", format, args...)
}

func errBadRequest(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "BadRequestException", format, args...)
}

func errConflict(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(409, "ConflictException", format, args...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, awshttp.AsAPIError(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amzn-RequestId", awshttp.RequestID())
	w.WriteHeader(status)
	w.Write(body)
}

func writeError(w http.ResponseWriter, e *awshttp.APIError) {
	body, _ := json.Marshal(map[string]string{"message": e.Message})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amzn-ErrorType", e.Code)
	w.Header().Set("x-amzn-RequestId", awshttp.RequestID())
	w.WriteHeader(e.Status)
	w.Write(body)
}

func decode(r *http.Request, dst any) *awshttp.APIError {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		return errBadRequest("read request body: %v", err)
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return errBadRequest("malformed JSON body: %v", err)
	}
	return nil
}

// patchOp is one entry of the JSON-Patch-ish document API Gateway uses for
// updates. Only replace/add/remove on simple paths are honoured.
type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
	From  string `json:"from"`
}

func decodePatch(r *http.Request) ([]patchOp, *awshttp.APIError) {
	var req struct {
		PatchOperations []patchOp `json:"patchOperations"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return nil, aerr
	}
	return req.PatchOperations, nil
}

// ---- /restapis ----

func (s *Server) routeRestAPIs(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	if len(segs) == 1 {
		switch r.Method {
		case http.MethodPost:
			return s.createRestAPI(w, r)
		case http.MethodGet:
			return s.listRestAPIs(w)
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on /restapis")
	}
	apiID := segs[1]

	if len(segs) == 2 {
		switch r.Method {
		case http.MethodGet:
			api, err := s.store.Get(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 200, viewAPI(api))
			return nil
		case http.MethodPatch:
			return s.patchRestAPI(w, r, apiID)
		case http.MethodDelete:
			if err := s.store.Delete(apiID); err != nil {
				return awshttp.AsAPIError(err)
			}
			w.WriteHeader(202)
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on a REST API")
	}

	switch segs[2] {
	case "resources":
		return s.routeResources(w, r, apiID, segs)
	case "deployments":
		return s.routeDeployments(w, r, apiID, segs)
	case "stages":
		return s.routeStages(w, r, apiID, segs)
	case "models", "requestvalidators", "authorizers", "documentation", "gatewayresponses":
		return awshttp.Errf(501, "NotImplemented",
			"doze-aws does not implement API Gateway %s", segs[2])
	}
	return errNotFound("unknown REST API subresource %s", segs[2])
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
	apis, err := s.store.List()
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
	api, err := s.store.Update(apiID, func(api *RestAPI) error {
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

// ---- resources ----

func (s *Server) routeResources(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	// /restapis/{api}/resources
	if len(segs) == 3 {
		if r.Method != http.MethodGet {
			return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on resources")
		}
		items := make([]any, 0, len(api.Resources))
		for _, res := range api.SortedResources() {
			items = append(items, viewResource(res))
		}
		writeJSON(w, 200, map[string]any{"item": items})
		return nil
	}
	resourceID := segs[3]
	res, ok := api.Resources[resourceID]
	if !ok {
		return errNotFound("Invalid Resource identifier specified")
	}

	// /restapis/{api}/resources/{id}
	if len(segs) == 4 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, viewResource(res))
			return nil
		case http.MethodPost:
			return s.createResource(w, r, apiID, resourceID)
		case http.MethodDelete:
			return s.deleteResource(w, apiID, resourceID)
		case http.MethodPatch:
			return s.patchResource(w, r, apiID, resourceID)
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on a resource")
	}

	// /restapis/{api}/resources/{id}/methods/...
	if segs[4] == "methods" && len(segs) >= 6 {
		return s.routeMethods(w, r, apiID, resourceID, strings.ToUpper(segs[5]), segs)
	}
	return errNotFound("unknown resource subresource")
}

func (s *Server) createResource(w http.ResponseWriter, r *http.Request, apiID, parentID string) *awshttp.APIError {
	var req struct {
		PathPart string `json:"pathPart"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.PathPart == "" {
		return errBadRequest("pathPart is required")
	}
	var created *Resource
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		if _, ok := api.Resources[parentID]; !ok {
			return errNotFound("Invalid Resource identifier specified")
		}
		for _, sib := range api.Children(parentID) {
			if sib.PathPart == req.PathPart {
				return errConflict("Another resource with the same parent already has this name: %s", req.PathPart)
			}
		}
		created = &Resource{ID: s.store.newID(), ParentID: parentID, PathPart: req.PathPart}
		api.Resources[created.ID] = created
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewResource(created))
	return nil
}

func (s *Server) deleteResource(w http.ResponseWriter, apiID, resourceID string) *awshttp.APIError {
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		res, ok := api.Resources[resourceID]
		if !ok {
			return errNotFound("Invalid Resource identifier specified")
		}
		if res.ParentID == "" {
			return errBadRequest("The root resource cannot be deleted")
		}
		// Deleting a resource takes its whole subtree, as in AWS.
		var remove func(id string)
		remove = func(id string) {
			for _, child := range api.Children(id) {
				remove(child.ID)
			}
			delete(api.Resources, id)
		}
		remove(resourceID)
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) patchResource(w http.ResponseWriter, r *http.Request, apiID, resourceID string) *awshttp.APIError {
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	var out *Resource
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		res := api.Resources[resourceID]
		if res == nil {
			return errNotFound("Invalid Resource identifier specified")
		}
		for _, op := range ops {
			switch op.Path {
			case "/pathPart":
				res.PathPart = op.Value
			case "/parentId":
				res.ParentID = op.Value
			}
		}
		out = res
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewResource(out))
	return nil
}

// ---- methods, integrations and responses ----

func (s *Server) routeMethods(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb string, segs []string) *awshttp.APIError {
	// /resources/{id}/methods/{verb}
	if len(segs) == 6 {
		switch r.Method {
		case http.MethodPut:
			return s.putMethod(w, r, apiID, resourceID, verb)
		case http.MethodGet:
			return s.getMethod(w, apiID, resourceID, verb)
		case http.MethodDelete:
			return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *Resource) error {
				delete(res.Methods, verb)
				return nil
			})
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method operation")
	}

	switch segs[6] {
	case "integration":
		return s.routeIntegration(w, r, apiID, resourceID, verb, segs)
	case "responses":
		// /methods/{verb}/responses/{status}
		if len(segs) != 8 {
			return errNotFound("a method response needs a status code")
		}
		return s.routeMethodResponse(w, r, apiID, resourceID, verb, segs[7])
	}
	return errNotFound("unknown method subresource %s", segs[6])
}

// mutateMethod applies fn to the owning resource and writes an empty response.
func (s *Server) mutateMethod(w http.ResponseWriter, apiID, resourceID, verb string, status int, fn func(*Resource) error) *awshttp.APIError {
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		res := api.Resources[resourceID]
		if res == nil {
			return errNotFound("Invalid Resource identifier specified")
		}
		return fn(res)
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(status)
	return nil
}

func (s *Server) putMethod(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb string) *awshttp.APIError {
	var req struct {
		AuthorizationType  string            `json:"authorizationType"`
		AuthorizerID       string            `json:"authorizerId"`
		APIKeyRequired     bool              `json:"apiKeyRequired"`
		OperationName      string            `json:"operationName"`
		RequestParameters  map[string]bool   `json:"requestParameters"`
		RequestModels      map[string]string `json:"requestModels"`
		RequestValidatorID string            `json:"requestValidatorId"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	var out *Method
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		res := api.Resources[resourceID]
		if res == nil {
			return errNotFound("Invalid Resource identifier specified")
		}
		if res.Methods == nil {
			res.Methods = map[string]*Method{}
		}
		m := &Method{
			HTTPMethod: verb, AuthorizationType: req.AuthorizationType,
			AuthorizerID: req.AuthorizerID, APIKeyRequired: req.APIKeyRequired,
			OperationName: req.OperationName, RequestParameters: req.RequestParameters,
			RequestModels: req.RequestModels, RequestValidatorID: req.RequestValidatorID,
		}
		if m.AuthorizationType == "" {
			m.AuthorizationType = "NONE"
		}
		// A re-put keeps whatever integration and responses were attached, so
		// Terraform's update path does not silently unwire the backend.
		if prev, ok := res.Methods[verb]; ok {
			m.Integration, m.Responses = prev.Integration, prev.Responses
		}
		res.Methods[verb] = m
		out = m
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewMethod(out))
	return nil
}

func (s *Server) getMethod(w http.ResponseWriter, apiID, resourceID, verb string) *awshttp.APIError {
	m, aerr := s.lookupMethod(apiID, resourceID, verb)
	if aerr != nil {
		return aerr
	}
	writeJSON(w, 200, viewMethod(m))
	return nil
}

func (s *Server) lookupMethod(apiID, resourceID, verb string) (*Method, *awshttp.APIError) {
	api, err := s.store.Get(apiID)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	res, ok := api.Resources[resourceID]
	if !ok {
		return nil, errNotFound("Invalid Resource identifier specified")
	}
	m, ok := res.Methods[verb]
	if !ok {
		return nil, errNotFound("Invalid Method identifier specified")
	}
	return m, nil
}

func (s *Server) routeIntegration(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb string, segs []string) *awshttp.APIError {
	// /methods/{verb}/integration
	if len(segs) == 7 {
		switch r.Method {
		case http.MethodPut:
			return s.putIntegration(w, r, apiID, resourceID, verb)
		case http.MethodGet:
			m, aerr := s.lookupMethod(apiID, resourceID, verb)
			if aerr != nil {
				return aerr
			}
			if m.Integration == nil {
				return errNotFound("Invalid Integration identifier specified")
			}
			writeJSON(w, 200, viewIntegration(m.Integration))
			return nil
		case http.MethodDelete:
			return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *Resource) error {
				if m := res.Methods[verb]; m != nil {
					m.Integration = nil
				}
				return nil
			})
		}
	}
	// /methods/{verb}/integration/responses/{status}
	if len(segs) == 9 && segs[7] == "responses" {
		return s.routeIntegrationResponse(w, r, apiID, resourceID, verb, segs[8])
	}
	return errNotFound("unknown integration subresource")
}

func (s *Server) putIntegration(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb string) *awshttp.APIError {
	var req struct {
		Type                  string            `json:"type"`
		HTTPMethod            string            `json:"httpMethod"`
		IntegrationHTTPMethod string            `json:"integrationHttpMethod"`
		URI                   string            `json:"uri"`
		ConnectionType        string            `json:"connectionType"`
		Credentials           string            `json:"credentials"`
		PassthroughBehavior   string            `json:"passthroughBehavior"`
		TimeoutInMillis       int               `json:"timeoutInMillis"`
		RequestTemplates      map[string]string `json:"requestTemplates"`
		RequestParameters     map[string]string `json:"requestParameters"`
		ContentHandling       string            `json:"contentHandling"`
		CacheKeyParameters    []string          `json:"cacheKeyParameters"`
		CacheNamespace        string            `json:"cacheNamespace"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.Type == "" {
		return errBadRequest("type is required")
	}
	method := req.IntegrationHTTPMethod
	if method == "" {
		method = req.HTTPMethod
	}
	var out *Integration
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		res := api.Resources[resourceID]
		if res == nil {
			return errNotFound("Invalid Resource identifier specified")
		}
		m := res.Methods[verb]
		if m == nil {
			return errNotFound("Invalid Method identifier specified")
		}
		integ := &Integration{
			Type: strings.ToUpper(req.Type), HTTPMethod: method, URI: req.URI,
			ConnectionType: req.ConnectionType, Credentials: req.Credentials,
			PassthroughBehavior: req.PassthroughBehavior, TimeoutInMillis: req.TimeoutInMillis,
			RequestTemplates: req.RequestTemplates, RequestParameters: req.RequestParameters,
			ContentHandling: req.ContentHandling, CacheKeyParameters: req.CacheKeyParameters,
			CacheNamespace: req.CacheNamespace,
		}
		if m.Integration != nil {
			integ.Responses = m.Integration.Responses
		}
		m.Integration = integ
		out = integ
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewIntegration(out))
	return nil
}

func (s *Server) routeMethodResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb, status string) *awshttp.APIError {
	switch r.Method {
	case http.MethodPut:
		var req struct {
			ResponseModels     map[string]string `json:"responseModels"`
			ResponseParameters map[string]bool   `json:"responseParameters"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		var out *MethodResponse
		_, err := s.store.Update(apiID, func(api *RestAPI) error {
			m, err := methodOf(api, resourceID, verb)
			if err != nil {
				return err
			}
			if m.Responses == nil {
				m.Responses = map[string]*MethodResponse{}
			}
			out = &MethodResponse{StatusCode: status, ResponseModels: req.ResponseModels, ResponseParameters: req.ResponseParameters}
			m.Responses[status] = out
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 201, viewMethodResponse(out))
		return nil
	case http.MethodGet:
		m, aerr := s.lookupMethod(apiID, resourceID, verb)
		if aerr != nil {
			return aerr
		}
		mr, ok := m.Responses[status]
		if !ok {
			return errNotFound("Invalid Response status code specified")
		}
		writeJSON(w, 200, viewMethodResponse(mr))
		return nil
	case http.MethodDelete:
		return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *Resource) error {
			if m := res.Methods[verb]; m != nil {
				delete(m.Responses, status)
			}
			return nil
		})
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method-response operation")
}

func (s *Server) routeIntegrationResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb, status string) *awshttp.APIError {
	switch r.Method {
	case http.MethodPut:
		var req struct {
			SelectionPattern   string            `json:"selectionPattern"`
			ResponseTemplates  map[string]string `json:"responseTemplates"`
			ResponseParameters map[string]string `json:"responseParameters"`
			ContentHandling    string            `json:"contentHandling"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		var out *IntegrationResponse
		_, err := s.store.Update(apiID, func(api *RestAPI) error {
			m, err := methodOf(api, resourceID, verb)
			if err != nil {
				return err
			}
			if m.Integration == nil {
				return errNotFound("Invalid Integration identifier specified")
			}
			if m.Integration.Responses == nil {
				m.Integration.Responses = map[string]*IntegrationResponse{}
			}
			out = &IntegrationResponse{
				StatusCode: status, SelectionPattern: req.SelectionPattern,
				ResponseTemplates: req.ResponseTemplates, ResponseParameters: req.ResponseParameters,
				ContentHandling: req.ContentHandling,
			}
			m.Integration.Responses[status] = out
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 201, viewIntegrationResponse(out))
		return nil
	case http.MethodGet:
		m, aerr := s.lookupMethod(apiID, resourceID, verb)
		if aerr != nil {
			return aerr
		}
		if m.Integration == nil || m.Integration.Responses[status] == nil {
			return errNotFound("Invalid Response status code specified")
		}
		writeJSON(w, 200, viewIntegrationResponse(m.Integration.Responses[status]))
		return nil
	case http.MethodDelete:
		return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *Resource) error {
			if m := res.Methods[verb]; m != nil && m.Integration != nil {
				delete(m.Integration.Responses, status)
			}
			return nil
		})
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported integration-response operation")
}

func methodOf(api *RestAPI, resourceID, verb string) (*Method, error) {
	res, ok := api.Resources[resourceID]
	if !ok {
		return nil, errNotFound("Invalid Resource identifier specified")
	}
	m, ok := res.Methods[verb]
	if !ok {
		return nil, errNotFound("Invalid Method identifier specified")
	}
	return m, nil
}

// ---- deployments and stages ----

func (s *Server) routeDeployments(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 3 {
		switch r.Method {
		case http.MethodPost:
			return s.createDeployment(w, r, apiID)
		case http.MethodGet:
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
	}
	if len(segs) == 4 {
		depID := segs[3]
		switch r.Method {
		case http.MethodGet:
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
		case http.MethodDelete:
			if _, err := s.store.Update(apiID, func(api *RestAPI) error {
				delete(api.Deployments, depID)
				return nil
			}); err != nil {
				return awshttp.AsAPIError(err)
			}
			w.WriteHeader(202)
			return nil
		}
	}
	return errNotFound("unknown deployment path")
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
	var dep *Deployment
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		now := s.now().Unix()
		dep = &Deployment{ID: s.store.newID(), Description: req.Description, Created: now}
		if api.Deployments == nil {
			api.Deployments = map[string]*Deployment{}
		}
		api.Deployments[dep.ID] = dep
		// CreateDeployment with a stageName creates the stage too, which is
		// how most templates and the CLI deploy in one call.
		if req.StageName != "" {
			if api.Stages == nil {
				api.Stages = map[string]*Stage{}
			}
			api.Stages[req.StageName] = &Stage{
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

func (s *Server) routeStages(w http.ResponseWriter, r *http.Request, apiID string, segs []string) *awshttp.APIError {
	if len(segs) == 3 {
		switch r.Method {
		case http.MethodPost:
			return s.createStage(w, r, apiID)
		case http.MethodGet:
			api, err := s.store.Get(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			items := make([]any, 0, len(api.Stages))
			for _, name := range sortedKeys(api.Stages) {
				items = append(items, viewStage(apiID, api.Stages[name]))
			}
			writeJSON(w, 200, map[string]any{"item": items})
			return nil
		}
	}
	if len(segs) == 4 {
		name := segs[3]
		switch r.Method {
		case http.MethodGet:
			api, err := s.store.Get(apiID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			st, ok := api.Stages[name]
			if !ok {
				return errNotFound("Invalid stage identifier specified")
			}
			writeJSON(w, 200, viewStage(apiID, st))
			return nil
		case http.MethodPatch:
			return s.patchStage(w, r, apiID, name)
		case http.MethodDelete:
			if _, err := s.store.Update(apiID, func(api *RestAPI) error {
				delete(api.Stages, name)
				return nil
			}); err != nil {
				return awshttp.AsAPIError(err)
			}
			w.WriteHeader(202)
			return nil
		}
	}
	return errNotFound("unknown stage path")
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
	var out *Stage
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
		if _, exists := api.Stages[req.StageName]; exists {
			return errConflict("Stage already exists: %s", req.StageName)
		}
		now := s.now().Unix()
		out = &Stage{
			Name: req.StageName, DeploymentID: req.DeploymentID, Description: req.Description,
			Variables: req.Variables, Tags: req.Tags, TracingEnabled: req.TracingEnabled,
			Created: now, Updated: now,
		}
		if api.Stages == nil {
			api.Stages = map[string]*Stage{}
		}
		api.Stages[req.StageName] = out
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewStage(apiID, out))
	return nil
}

func (s *Server) patchStage(w http.ResponseWriter, r *http.Request, apiID, name string) *awshttp.APIError {
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	var out *Stage
	_, err := s.store.Update(apiID, func(api *RestAPI) error {
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
			}
		}
		st.Updated = s.now().Unix()
		out = st
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewStage(apiID, out))
	return nil
}

// ---- tags and account ----

func (s *Server) routeTags(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	// Tags are addressed by URL-encoded ARN; only REST API ARNs are tagged.
	if len(segs) < 2 {
		return errNotFound("a tag request needs a resource ARN")
	}
	arn := strings.Join(segs[1:], "/")
	apiID := apiIDFromARN(arn)
	switch r.Method {
	case http.MethodGet:
		api, err := s.store.Get(apiID)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, map[string]any{"tags": orEmptyMap(api.Tags)})
		return nil
	case http.MethodPut:
		var req struct {
			Tags map[string]string `json:"tags"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		if _, err := s.store.Update(apiID, func(api *RestAPI) error {
			if api.Tags == nil {
				api.Tags = map[string]string{}
			}
			for k, v := range req.Tags {
				api.Tags[k] = v
			}
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	case http.MethodDelete:
		keys := r.URL.Query()["tagKeys"]
		if _, err := s.store.Update(apiID, func(api *RestAPI) error {
			for _, k := range keys {
				delete(api.Tags, k)
			}
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported tag operation")
}

// apiIDFromARN pulls the api id out of arn:aws:apigateway:region::/restapis/{id}.
func apiIDFromARN(arn string) string {
	if i := strings.Index(arn, "/restapis/"); i >= 0 {
		rest := arn[i+len("/restapis/"):]
		id, _, _ := strings.Cut(rest, "/")
		return id
	}
	return arn
}

func (s *Server) getAccount(w http.ResponseWriter) *awshttp.APIError {
	writeJSON(w, 200, map[string]any{
		"cloudwatchRoleArn": "",
		"throttleSettings":  map[string]any{"burstLimit": 5000, "rateLimit": 10000},
		"features":          []string{},
		"apiKeyVersion":     "4",
	})
	return nil
}
