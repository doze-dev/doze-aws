package apigateway

// HTTP API integrations: what a route forwards to.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

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

func (s *Server) v2CreateIntegration(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError {
	var req v2IntegrationInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.IntegrationType == nil {
		return errBadRequest("integrationType is required")
	}
	var out *v2Integration
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
		integ := &v2Integration{ID: s.store.newID(), TimeoutInMillis: 30000, ConnectionType: "INTERNET"}
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
}

func (s *Server) v2GetIntegrations(w http.ResponseWriter, apiID string) *awshttp.APIError {
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

func (s *Server) v2GetIntegration(w http.ResponseWriter, apiID, integID string) *awshttp.APIError {
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
}

func (s *Server) v2UpdateIntegration(w http.ResponseWriter, r *http.Request, apiID, integID string) *awshttp.APIError {
	var req v2IntegrationInput
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	var out *v2Integration
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
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
}

func (s *Server) v2DeleteIntegration(w http.ResponseWriter, apiID, integID string) *awshttp.APIError {
	_, err := s.store.UpdateHTTP(apiID, func(api *restAPI) error {
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

// applyV2IntegrationInput validates and applies an integration's fields.
// HTTP APIs run AWS_PROXY (a Lambda function, payload 1.0 or 2.0) and
// HTTP_PROXY (a URL); the WebSocket kinds and AWS service integrations are
// refused by name rather than stored and then failing on the data plane.
func applyV2IntegrationInput(integ *v2Integration, in *v2IntegrationInput) error {
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

func viewV2Integration(integ *v2Integration) map[string]any {
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
