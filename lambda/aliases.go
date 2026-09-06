package lambda

import (
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Aliases: a named pointer at a version. Create, Get, List, Update, Delete.
//
// An alias is the version it points at plus a description. RoutingConfig
// (weighted traffic between two versions) is accepted by validation and not
// stored: locally a deploy converges instantly to its target version, which
// is where a weighted shift ends up anyway.

func (s *Server) routeAliases(w http.ResponseWriter, r *http.Request, name string, segs []string) *awshttp.APIError {
	if len(segs) == 4 { // /functions/{name}/aliases
		switch r.Method {
		case http.MethodPost:
			return s.createAlias(w, r, name)
		case http.MethodGet:
			f, err := s.store.GetFunction(name)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			views := []any{}
			for _, alias := range slices.Sorted(maps.Keys(f.Aliases)) {
				if strings.HasPrefix(alias, "$") {
					continue
				}
				views = append(views, aliasView(f, alias))
			}
			writeJSON(w, 200, map[string]any{"Aliases": views})
			return nil
		}
	}
	if len(segs) == 5 { // /functions/{name}/aliases/{alias}
		alias := segs[4]
		switch r.Method {
		case http.MethodGet:
			f, err := s.store.GetFunction(name)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			if _, ok := f.Aliases[alias]; !ok {
				return awshttp.Errf(404, "ResourceNotFoundException", "Cannot find alias arn: %s:%s", f.ARN(), alias)
			}
			writeJSON(w, 200, aliasView(f, alias))
			return nil
		case http.MethodPut:
			return s.updateAlias(w, r, name, alias)
		case http.MethodDelete:
			s.store.Update(name, func(f *Function) error {
				delete(f.Aliases, alias)
				delete(f.AliasDescriptions, alias)
				return nil
			})
			w.WriteHeader(204)
			return nil
		}
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported alias request")
}

// createAlias is CreateAlias: a second create of the same name is a
// conflict, as on AWS, so a deploy tool's create-then-update converges.
func (s *Server) createAlias(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	var req struct {
		Name            string `json:"Name"`
		FunctionVersion string `json:"FunctionVersion"`
		Description     string `json:"Description"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if aerr := s.checkAliasTarget(name, req.FunctionVersion); aerr != nil {
		return aerr
	}
	var conflict *awshttp.APIError
	f, err := s.store.Update(name, func(f *Function) error {
		if _, exists := f.Aliases[req.Name]; exists {
			conflict = awshttp.Errf(409, "ResourceConflictException", "Alias already exists: %s:%s", f.ARN(), req.Name)
			return nil
		}
		if f.Aliases == nil {
			f.Aliases = map[string]string{}
		}
		f.Aliases[req.Name] = req.FunctionVersion
		setAliasDescription(f, req.Name, req.Description)
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if conflict != nil {
		return conflict
	}
	writeJSON(w, 201, aliasView(f, req.Name))
	return nil
}

// updateAlias is UpdateAlias: FunctionVersion and Description, each only
// when sent.
func (s *Server) updateAlias(w http.ResponseWriter, r *http.Request, name, alias string) *awshttp.APIError {
	var req struct {
		FunctionVersion string  `json:"FunctionVersion"`
		Description     *string `json:"Description"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.FunctionVersion != "" {
		if aerr := s.checkAliasTarget(name, req.FunctionVersion); aerr != nil {
			return aerr
		}
	}
	var missing *awshttp.APIError
	f, err := s.store.Update(name, func(f *Function) error {
		if _, ok := f.Aliases[alias]; !ok {
			missing = awshttp.Errf(404, "ResourceNotFoundException", "Cannot find alias arn: %s:%s", f.ARN(), alias)
			return nil
		}
		if req.FunctionVersion != "" {
			f.Aliases[alias] = req.FunctionVersion
		}
		if req.Description != nil {
			setAliasDescription(f, alias, *req.Description)
		}
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if missing != nil {
		return missing
	}
	writeJSON(w, 200, aliasView(f, alias))
	return nil
}

// checkAliasTarget refuses an alias at a version that does not exist, which
// is what AWS does — an alias at nothing would 404 on every invoke later.
func (s *Server) checkAliasTarget(name, version string) *awshttp.APIError {
	if version == "" || version == "$LATEST" {
		return nil
	}
	if _, _, aerr := s.resolve(name, version); aerr != nil {
		return awshttp.Errf(400, "InvalidParameterValueException", "Function version %s does not exist", version)
	}
	return nil
}

func setAliasDescription(f *Function, alias, description string) {
	if description == "" {
		delete(f.AliasDescriptions, alias)
		return
	}
	if f.AliasDescriptions == nil {
		f.AliasDescriptions = map[string]string{}
	}
	f.AliasDescriptions[alias] = description
}

func aliasView(f *Function, alias string) map[string]any {
	version := f.Aliases[alias]
	if version == "" {
		version = "$LATEST"
	}
	v := map[string]any{
		"AliasArn":        f.ARN() + ":" + alias,
		"Name":            alias,
		"FunctionVersion": version,
		"RevisionId":      f.Revision,
	}
	if d := f.AliasDescriptions[alias]; d != "" {
		v["Description"] = d
	}
	return v
}
