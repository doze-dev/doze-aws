package apigateway

// REST API resources: the path tree an API's methods hang from.

import (
	"net/http"
	"regexp"
	"sync"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func (s *Server) getResources(w http.ResponseWriter, apiID string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	items := make([]any, 0, len(api.Resources))
	for _, res := range api.SortedResources() {
		items = append(items, viewResource(res))
	}
	writeJSON(w, 200, map[string]any{"item": items})
	return nil
}

func (s *Server) getResource(w http.ResponseWriter, apiID, resourceID string) *awshttp.APIError {
	api, err := s.store.Get(apiID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	res, ok := api.Resources[resourceID]
	if !ok {
		return errNotFound("Invalid Resource identifier specified")
	}
	writeJSON(w, 200, viewResource(res))
	return nil
}

// pathPart is one segment of a resource path: literal characters, or a path
// variable in braces, greedy with a trailing plus. The model gives it no
// pattern, so "has space" made a resource no request path could ever reach.
var pathPart = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^([a-zA-Z0-9._\-:]+|\{[a-zA-Z0-9._\-]+\+?\})$`) })

// restMethods are the verbs a REST API method may be put under.
var restMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	"HEAD": true, "OPTIONS": true, "ANY": true,
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
	if !pathPart().MatchString(req.PathPart) {
		return errBadRequest("Resource's path part only allow a-zA-Z0-9._-: or a valid greedy path variable " +
			"and curly braces at the beginning and the end and an optional plus sign before the closing brace.")
	}
	var created *resource
	_, err := s.store.Update(apiID, func(api *restAPI) error {
		if _, ok := api.Resources[parentID]; !ok {
			return errNotFound("Invalid Resource identifier specified")
		}
		for _, sib := range api.Children(parentID) {
			if sib.PathPart == req.PathPart {
				return errConflict("Another resource with the same parent already has this name: %s", req.PathPart)
			}
		}
		created = &resource{ID: s.store.newID(), ParentID: parentID, PathPart: req.PathPart}
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
	_, err := s.store.Update(apiID, func(api *restAPI) error {
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
	var out *resource
	_, err := s.store.Update(apiID, func(api *restAPI) error {
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
