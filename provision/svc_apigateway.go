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
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
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

		for _, route := range api.Routes {
			if err := ensureRoute(ctx, c, id, route); err != nil {
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
	}
	return nil
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
func ensureRoute(ctx context.Context, c *client, apiID string, route Route) error {
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
	if _, err := c.do(ctx, "PUT", base,
		map[string]string{"Content-Type": "application/json"},
		mustJSON(map[string]any{"authorizationType": "NONE"})); err != nil {
		return err
	}
	uri := "arn:aws:apigateway:" + awsident.Region + ":lambda:path/2015-03-31/functions/" +
		lambdaARN(route.Lambda) + "/invocations"
	_, err = c.do(ctx, "PUT", base+"/integration",
		map[string]string{"Content-Type": "application/json"},
		mustJSON(map[string]any{
			"type": "AWS_PROXY", "integrationHttpMethod": "POST", "uri": uri,
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
