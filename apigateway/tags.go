package apigateway

// Tags on REST and HTTP APIs, addressed by ARN.

import (
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/restroute"
)

// tagTarget reads the ARN a tags request names and resolves the API it points
// at. Tags are addressed by URL-encoded ARN; only API ARNs are tagged. The
// ARN's own shape says which kind of API it names, and the two id spaces do
// not see each other: a REST ARN for an HTTP API (or the reverse) is an ARN of
// nothing.
func (s *Server) tagTarget(r *http.Request) (apiID string, aerr *awshttp.APIError) {
	arn := restroute.Param(r, "resourceArn")
	if arn == "" {
		arn = restroute.Wildcard(r)
	}
	if arn == "" {
		return "", errNotFound("a tag request needs a resource ARN")
	}
	apiID = apiIDFromARN(arn)
	if api, err := s.store.Get(apiID); err == nil {
		isHTTP := api.Protocol == "HTTP"
		if isHTTP != strings.Contains(arn, "/apis/") {
			return "", errNotFound("Invalid resource ARN specified %s", arn)
		}
	}
	return apiID, nil
}

func (s *Server) getTags(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	apiID, aerr := s.tagTarget(r)
	if aerr != nil {
		return aerr
	}
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, map[string]any{"tags": orEmptyMap(api.Tags)})
	return nil
}

// tagResource serves TagResource: PUT is the v1 spelling, POST the v2 one.
func (s *Server) tagResource(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	apiID, aerr := s.tagTarget(r)
	if aerr != nil {
		return aerr
	}
	var req struct {
		Tags map[string]string `json:"tags"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if _, err := s.store.Update(apiID, func(api *restAPI) error {
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
}

func (s *Server) untagResource(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	apiID, aerr := s.tagTarget(r)
	if aerr != nil {
		return aerr
	}
	keys := r.URL.Query()["tagKeys"]
	if len(keys) == 0 {
		return errBadRequest("tagKeys is required")
	}
	if _, err := s.store.Update(apiID, func(api *restAPI) error {
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

// apiIDFromARN pulls the api id out of arn:aws:apigateway:region::/restapis/{id}
// or, for an HTTP API, arn:aws:apigateway:region::/apis/{id}.
func apiIDFromARN(arn string) string {
	for _, marker := range []string{"/restapis/", "/apis/"} {
		if i := strings.Index(arn, marker); i >= 0 {
			id, _, _ := strings.Cut(arn[i+len(marker):], "/")
			return id
		}
	}
	return arn
}
