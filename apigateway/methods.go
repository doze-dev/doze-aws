package apigateway

// REST API methods, their integrations, and the method and integration responses.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func (s *Server) deleteMethod(w http.ResponseWriter, apiID, resourceID, verb string) *awshttp.APIError {
	return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *resource) error {
		delete(res.Methods, verb)
		return nil
	})
}

// mutateMethod applies fn to the owning resource and writes an empty response.
func (s *Server) mutateMethod(w http.ResponseWriter, apiID, resourceID, verb string, status int, fn func(*resource) error) *awshttp.APIError {
	_, err := s.store.Update(apiID, func(api *restAPI) error {
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
	// "FETCH" was stored as a method, and no request could ever match it.
	if !restMethods[strings.ToUpper(verb)] {
		return errBadRequest("Invalid HTTP method specified")
	}
	var out *method
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		res := api.Resources[resourceID]
		if res == nil {
			return errNotFound("Invalid Resource identifier specified")
		}
		if res.Methods == nil {
			res.Methods = map[string]*method{}
		}
		m := &method{
			HTTPMethod: verb, AuthorizationType: req.AuthorizationType,
			AuthorizerID: req.AuthorizerID, APIKeyRequired: req.APIKeyRequired,
			OperationName: req.OperationName, RequestParameters: req.RequestParameters,
			RequestModels: req.RequestModels, RequestValidatorID: req.RequestValidatorID,
		}
		if m.AuthorizationType == "" {
			m.AuthorizationType = "NONE"
		}
		if m.AuthorizationType == "CUSTOM" {
			if _, ok := api.Authorizers[m.AuthorizerID]; !ok {
				return errBadRequest("Invalid Authorizer identifier specified")
			}
		}
		if m.AuthorizationType == "COGNITO_USER_POOLS" {
			return errBadRequest("COGNITO_USER_POOLS authorization needs a Cognito user pool, which does not exist locally")
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

func (s *Server) lookupMethod(apiID, resourceID, verb string) (*method, *awshttp.APIError) {
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

func (s *Server) getIntegration(w http.ResponseWriter, apiID, resourceID, verb string) *awshttp.APIError {
	m, aerr := s.lookupMethod(apiID, resourceID, verb)
	if aerr != nil {
		return aerr
	}
	if m.Integration == nil {
		return errNotFound("Invalid Integration identifier specified")
	}
	writeJSON(w, 200, viewIntegration(m.Integration))
	return nil
}

func (s *Server) deleteIntegration(w http.ResponseWriter, apiID, resourceID, verb string) *awshttp.APIError {
	return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *resource) error {
		if m := res.Methods[verb]; m != nil {
			m.Integration = nil
		}
		return nil
	})
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
	var out *integration
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		res := api.Resources[resourceID]
		if res == nil {
			return errNotFound("Invalid Resource identifier specified")
		}
		m := res.Methods[verb]
		if m == nil {
			return errNotFound("Invalid Method identifier specified")
		}
		integ := &integration{
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

func (s *Server) putMethodResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb, status string) *awshttp.APIError {
	var req struct {
		ResponseModels     map[string]string `json:"responseModels"`
		ResponseParameters map[string]bool   `json:"responseParameters"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	var out *methodResponse
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		m, err := methodOf(api, resourceID, verb)
		if err != nil {
			return err
		}
		if m.Responses == nil {
			m.Responses = map[string]*methodResponse{}
		}
		out = &methodResponse{StatusCode: status, ResponseModels: req.ResponseModels, ResponseParameters: req.ResponseParameters}
		m.Responses[status] = out
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewMethodResponse(out))
	return nil
}

func (s *Server) getMethodResponse(w http.ResponseWriter, apiID, resourceID, verb, status string) *awshttp.APIError {
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
}

func (s *Server) deleteMethodResponse(w http.ResponseWriter, apiID, resourceID, verb, status string) *awshttp.APIError {
	return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *resource) error {
		if m := res.Methods[verb]; m != nil {
			delete(m.Responses, status)
		}
		return nil
	})
}

func (s *Server) putIntegrationResponse(w http.ResponseWriter, r *http.Request, apiID, resourceID, verb, status string) *awshttp.APIError {
	var req struct {
		SelectionPattern   string            `json:"selectionPattern"`
		ResponseTemplates  map[string]string `json:"responseTemplates"`
		ResponseParameters map[string]string `json:"responseParameters"`
		ContentHandling    string            `json:"contentHandling"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	var out *integrationResponse
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		m, err := methodOf(api, resourceID, verb)
		if err != nil {
			return err
		}
		if m.Integration == nil {
			return errNotFound("Invalid Integration identifier specified")
		}
		if m.Integration.Responses == nil {
			m.Integration.Responses = map[string]*integrationResponse{}
		}
		out = &integrationResponse{
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
}

func (s *Server) getIntegrationResponse(w http.ResponseWriter, apiID, resourceID, verb, status string) *awshttp.APIError {
	m, aerr := s.lookupMethod(apiID, resourceID, verb)
	if aerr != nil {
		return aerr
	}
	if m.Integration == nil || m.Integration.Responses[status] == nil {
		return errNotFound("Invalid Response status code specified")
	}
	writeJSON(w, 200, viewIntegrationResponse(m.Integration.Responses[status]))
	return nil
}

func (s *Server) deleteIntegrationResponse(w http.ResponseWriter, apiID, resourceID, verb, status string) *awshttp.APIError {
	return s.mutateMethod(w, apiID, resourceID, verb, 204, func(res *resource) error {
		if m := res.Methods[verb]; m != nil && m.Integration != nil {
			delete(m.Integration.Responses, status)
		}
		return nil
	})
}

func methodOf(api *restAPI, resourceID, verb string) (*method, error) {
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
