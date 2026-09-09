package provision

// API Gateway apply and teardown.
//
// The IR carries routes; API Gateway wants a resource tree. Apply rebuilds the
// tree from the routes — creating each path segment once, attaching the method
// and an AWS_PROXY integration, then deploying a stage.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func applyAPIs(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.APIs) {
		api := s.APIs[name]
		id, existing, err := findAPI(ctx, c, name)
		if err != nil {
			return err
		}
		if !existing {
			out, err := c.do(ctx, "POST", "/restapis",
				map[string]string{"Content-Type": "application/json"},
				mustJSON(map[string]any{"name": name}))
			if err != nil {
				return fmt.Errorf("api %q: %w", name, err)
			}
			var created struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(out, &created); err != nil {
				return err
			}
			id = created.ID
			rep.add("created", "api/"+name, "")
		} else {
			rep.add("skipped", "api/"+name, "already in place")
		}

		authIDs, err := ensureAuthorizers(ctx, c, id, api)
		if err != nil {
			return fmt.Errorf("api %q: %w", name, err)
		}
		for _, route := range api.Routes {
			if err := ensureRoute(ctx, c, id, api, route, authIDs); err != nil {
				return fmt.Errorf("api %q route %s %s: %w", name, route.Method, route.Path, err)
			}
		}
		stage := api.Stage
		if stage == "" {
			stage = "prod"
		}
		if _, err := c.do(ctx, "POST", "/restapis/"+id+"/deployments",
			map[string]string{"Content-Type": "application/json"},
			mustJSON(map[string]any{"stageName": stage})); err != nil {
			return fmt.Errorf("api %q: deploy: %w", name, err)
		}
		rep.add("updated", "api/"+name, "deployed to stage "+stage)
		if err := applyStageLogging(ctx, c, id, stage, api); err != nil {
			return fmt.Errorf("api %q stage %s logging: %w", name, stage, err)
		}
	}
	return nil
}

// applyStageLogging patches the stage's access log and method settings the
// way CloudFormation patches them — one UpdateStage with a replace per
// setting — so a repeated apply converges.
func applyStageLogging(ctx context.Context, c *client, apiID, stage string, api API) error {
	var ops []map[string]string
	replace := func(path, value string) {
		ops = append(ops, map[string]string{"op": "replace", "path": path, "value": value})
	}
	if api.AccessLog != nil {
		replace("/accessLogSettings/destinationArn", api.AccessLog.DestinationARN)
		replace("/accessLogSettings/format", api.AccessLog.Format)
	}
	for _, ms := range api.MethodSettings {
		path, method := ms.Path, ms.Method
		if path == "" || path == "/*" {
			path = "*"
		}
		if method == "" {
			method = "*"
		}
		prefix := "/" + strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", "~1") + "/" + method
		if path == "*" {
			prefix = "/*/" + method
		}
		if ms.LoggingLevel != "" {
			replace(prefix+"/logging/loglevel", ms.LoggingLevel)
		}
		replace(prefix+"/logging/dataTrace", strconv.FormatBool(ms.DataTrace))
		replace(prefix+"/metrics/enabled", strconv.FormatBool(ms.Metrics))
	}
	if len(ops) == 0 {
		return nil
	}
	_, err := c.do(ctx, "PATCH", "/restapis/"+apiID+"/stages/"+url.PathEscape(stage),
		map[string]string{"Content-Type": "application/json"}, mustJSON(map[string]any{"patchOperations": ops}))
	return err
}

// findAPI looks an API up by name, since the IR names them and API Gateway
// keys them by generated id.
func findAPI(ctx context.Context, c *client, name string) (id string, found bool, err error) {
	out, err := c.do(ctx, "GET", "/restapis", nil, nil)
	if err != nil {
		return "", false, err
	}
	var listed struct {
		Item []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"item"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return "", false, err
	}
	for _, item := range listed.Item {
		if item.Name == name {
			return item.ID, true, nil
		}
	}
	return "", false, nil
}

// ensureRoute creates the path tree for one route and wires its integration.
func ensureRoute(ctx context.Context, c *client, apiID string, api API, route Route, authIDs map[string]string) error {
	resources, err := listResources(ctx, c, apiID)
	if err != nil {
		return err
	}
	parent := resources["/"]
	if parent == "" {
		return fmt.Errorf("api has no root resource")
	}

	path := "/"
	for _, part := range strings.Split(strings.Trim(route.Path, "/"), "/") {
		if part == "" {
			continue
		}
		if path == "/" {
			path = "/" + part
		} else {
			path = path + "/" + part
		}
		if id, ok := resources[path]; ok {
			parent = id
			continue
		}
		out, err := c.do(ctx, "POST", "/restapis/"+apiID+"/resources/"+parent,
			map[string]string{"Content-Type": "application/json"},
			mustJSON(map[string]any{"pathPart": part}))
		if err != nil {
			return err
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(out, &created); err != nil {
			return err
		}
		resources[path] = created.ID
		parent = created.ID
	}

	verb := strings.ToUpper(route.Method)
	if verb == "" || verb == "ANY" {
		verb = "ANY"
	}
	base := "/restapis/" + apiID + "/resources/" + parent + "/methods/" + url.PathEscape(verb)
	method, err := methodRequest(api, route, authIDs)
	if err != nil {
		return err
	}
	if _, err := c.do(ctx, "PUT", base,
		map[string]string{"Content-Type": "application/json"}, mustJSON(method)); err != nil {
		return err
	}
	if route.Mock != nil {
		return putMockIntegration(ctx, c, base, route.Mock)
	}
	_, err = c.do(ctx, "PUT", base+"/integration",
		map[string]string{"Content-Type": "application/json"},
		mustJSON(map[string]any{
			"type": "AWS_PROXY", "integrationHttpMethod": "POST", "uri": lambdaInvokeURI(route.Lambda),
		}))
	return err
}

// listResources maps an API's resource paths to their ids.
func listResources(ctx context.Context, c *client, apiID string) (map[string]string, error) {
	out, err := c.do(ctx, "GET", "/restapis/"+apiID+"/resources", nil, nil)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Item []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"item"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return nil, err
	}
	byPath := map[string]string{}
	for _, item := range listed.Item {
		byPath[item.Path] = item.ID
	}
	return byPath, nil
}

func destroyAPIs(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.APIs) {
		id, found, err := findAPI(ctx, c, name)
		if err != nil || !found {
			rep.add("absent", "api/"+name, "")
			continue
		}
		_, err = c.do(ctx, "DELETE", "/restapis/"+id, nil, nil)
		record(rep, "api/"+name, err)
	}
	return nil
}
