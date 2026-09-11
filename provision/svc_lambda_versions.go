package provision

// Lambda layers, versions, aliases and function URLs: apply, export, destroy.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ---- layers ----

// applyLayers publishes each layer whose content changed and records, per
// layer, the version ARN this apply settled on, for the functions that
// reference the layer by name. PublishLayerVersion always makes a new
// version, as on AWS; publishing content identical to the latest version is
// undone again so a repeated deploy leaves one version, not one per run.
func applyLayers(ctx context.Context, c *client, s *Stack, rep *Report) (map[string]string, error) {
	published := map[string]string{}
	for _, name := range sortedNames(s.Layers) {
		l := s.Layers[name]
		content, err := codeLocation("layer "+name, l.Code)
		if err != nil {
			return nil, err
		}
		latestARN, latestSHA := latestLayerVersion(ctx, c, name)

		in := map[string]any{"LayerName": name, "Content": content}
		if len(l.Runtimes) > 0 {
			in["CompatibleRuntimes"] = l.Runtimes
		}
		if l.Description != "" {
			in["Description"] = l.Description
		}
		out, err := c.do(ctx, "POST", "/2018-10-31/layers/"+url.PathEscape(name)+"/versions", map[string]string{"Content-Type": "application/json"}, mustJSON(in))
		if err != nil {
			return nil, fmt.Errorf("layer %q: %w", name, err)
		}
		var v struct {
			LayerVersionArn string
			Version         int64
			Content         struct{ CodeSha256 string }
		}
		json.Unmarshal(out, &v)
		if latestARN != "" && latestSHA != "" && v.Content.CodeSha256 == latestSHA {
			// Same bytes as the version already there: keep that one.
			c.do(ctx, "DELETE", "/2018-10-31/layers/"+url.PathEscape(name)+"/versions/"+strconv.FormatInt(v.Version, 10), nil, nil)
			published[name] = latestARN
			continue
		}
		published[name] = v.LayerVersionArn
		rep.add("published", "layer/"+name, v.LayerVersionArn)
	}
	return published, nil
}

// latestLayerVersion reports the newest version's ARN and content hash, or
// empty strings when the layer has none.
func latestLayerVersion(ctx context.Context, c *client, name string) (arn, sha string) {
	out, err := c.do(ctx, "GET", "/2018-10-31/layers/"+url.PathEscape(name)+"/versions", nil, nil)
	if err != nil {
		return "", ""
	}
	var lst struct {
		LayerVersions []struct {
			LayerVersionArn string
			Version         int64
		}
	}
	json.Unmarshal(out, &lst)
	if len(lst.LayerVersions) == 0 {
		return "", ""
	}
	newest := lst.LayerVersions[0]
	detail, err := c.do(ctx, "GET", "/2018-10-31/layers/"+url.PathEscape(name)+"/versions/"+strconv.FormatInt(newest.Version, 10), nil, nil)
	if err != nil {
		return newest.LayerVersionArn, ""
	}
	var v struct {
		Content struct{ CodeSha256 string }
	}
	json.Unmarshal(detail, &v)
	return newest.LayerVersionArn, v.Content.CodeSha256
}

// layerARNs resolves a function's layer references: a stack layer's name
// becomes the version this apply published; anything else is an ARN already.
func layerARNs(fn string, refs []string, published map[string]string) ([]string, error) {
	var arns []string
	for _, ref := range refs {
		if arn, ok := published[ref]; ok {
			arns = append(arns, arn)
			continue
		}
		if !strings.HasPrefix(ref, "arn:") {
			return nil, fmt.Errorf("function %q: layer %q is neither a layer in this stack nor a layer version ARN", fn, ref)
		}
		arns = append(arns, ref)
	}
	return arns, nil
}

// ---- versions, aliases, URL ----

// applyVersionAliasesURL publishes the function when the stack asks for a
// version, points every alias at what was published, and converges the
// function URL. Publishing unchanged code and configuration answers the
// version it already has, and an alias converges through UpdateAlias, so a
// repeated deploy changes nothing.
func applyVersionAliasesURL(ctx context.Context, c *client, name string, f Function, rep *Report) error {
	if f.Publish || len(f.Aliases) > 0 {
		out, err := c.do(ctx, "POST", "/2015-03-31/functions/"+url.PathEscape(name)+"/versions", map[string]string{"Content-Type": "application/json"}, []byte("{}"))
		if err != nil {
			return fmt.Errorf("function %q publish: %w", name, err)
		}
		var v struct{ Version string }
		json.Unmarshal(out, &v)
		rep.add("published", "function/"+name, lambdaARN(name)+":"+v.Version)

		for _, alias := range sortedNames(f.Aliases) {
			target := f.Aliases[alias].Version
			if target == "" || target == "$published" {
				target = v.Version
			}
			in := map[string]any{"Name": alias, "FunctionVersion": target}
			if d := f.Aliases[alias].Description; d != "" {
				in["Description"] = d
			}
			if _, err := c.do(ctx, "POST", "/2015-03-31/functions/"+url.PathEscape(name)+"/aliases", map[string]string{"Content-Type": "application/json"}, mustJSON(in)); err == nil {
				rep.add("created", "function/"+name+"/alias/"+alias, v.Version)
				continue
			} else {
				// Lambda's restJson errors carry the code in a header the
				// client does not keep; a conflict is the 409.
				var ae *apiErr
				if !asAPIErr(err, &ae) || ae.status != 409 {
					return fmt.Errorf("function %q alias %q: %w", name, alias, err)
				}
			}
			delete(in, "Name")
			if _, err := c.do(ctx, "PUT", "/2015-03-31/functions/"+url.PathEscape(name)+"/aliases/"+url.PathEscape(alias), map[string]string{"Content-Type": "application/json"}, mustJSON(in)); err != nil {
				return fmt.Errorf("function %q alias %q update: %w", name, alias, err)
			}
			rep.add("updated", "function/"+name+"/alias/"+alias, v.Version)
		}
	}

	if f.URL != nil {
		in := map[string]any{"AuthType": orDefaultStr(f.URL.AuthType, "NONE")}
		if f.URL.CORS.JSON != "" {
			in["Cors"] = json.RawMessage(f.URL.CORS.JSON)
		}
		path := "/2021-10-31/functions/" + url.PathEscape(name) + "/url"
		method, verb := "POST", "created"
		if _, err := c.do(ctx, "GET", path, nil, nil); err == nil {
			method, verb = "PUT", "updated"
		}
		out, err := c.do(ctx, method, path, map[string]string{"Content-Type": "application/json"}, mustJSON(in))
		if err != nil {
			return fmt.Errorf("function %q url: %w", name, err)
		}
		var u struct{ FunctionUrl string }
		json.Unmarshal(out, &u)
		rep.add(verb, "function/"+name+"/url", u.FunctionUrl)
	}
	return nil
}

// ---- export ----

// exportLayers reads every layer's latest version as the stack's layer.
func exportLayers(ctx context.Context, c *client, s *Stack) error {
	out, err := c.do(ctx, "GET", "/2018-10-31/layers", nil, nil)
	if err != nil {
		return err
	}
	var lst struct {
		Layers []struct {
			LayerName             string
			LatestMatchingVersion struct {
				Version            int64
				Description        string
				CompatibleRuntimes []string
			}
		}
	}
	json.Unmarshal(out, &lst)
	if len(lst.Layers) > 0 {
		s.Layers = map[string]Layer{}
	}
	for _, l := range lst.Layers {
		layer := Layer{
			Code:        "<local layer path — set me>",
			Runtimes:    l.LatestMatchingVersion.CompatibleRuntimes,
			Description: l.LatestMatchingVersion.Description,
		}
		if detail, err := c.do(ctx, "GET", "/2018-10-31/layers/"+url.PathEscape(l.LayerName)+"/versions/"+strconv.FormatInt(l.LatestMatchingVersion.Version, 10), nil, nil); err == nil {
			var v struct {
				Content struct{ Location string }
			}
			json.Unmarshal(detail, &v)
			if v.Content.Location != "" {
				layer.Code = v.Content.Location
			}
		}
		s.Layers[l.LayerName] = layer
	}
	return nil
}

// exportFunctionVersioning fills a function's layers, publish flag, aliases
// and URL from the live service. A layer in the stack is named; any other
// layer keeps its ARN.
func exportFunctionVersioning(ctx context.Context, c *client, s *Stack, name string, f *Function, layers []struct{ Arn string }) {
	for _, l := range layers {
		if i := strings.Index(l.Arn, ":layer:"); i >= 0 {
			if layerName, _, ok := strings.Cut(l.Arn[i+len(":layer:"):], ":"); ok {
				if _, inStack := s.Layers[layerName]; inStack {
					f.Layers = append(f.Layers, layerName)
					continue
				}
			}
		}
		f.Layers = append(f.Layers, l.Arn)
	}
	newest := ""
	if out, err := c.do(ctx, "GET", "/2015-03-31/functions/"+url.PathEscape(name)+"/versions", nil, nil); err == nil {
		var lst struct {
			Versions []struct{ Version string }
		}
		json.Unmarshal(out, &lst)
		for _, v := range lst.Versions {
			if v.Version != "$LATEST" {
				f.Publish = true
				newest = v.Version // ascending, so the last one wins
			}
		}
	}
	if out, err := c.do(ctx, "GET", "/2015-03-31/functions/"+url.PathEscape(name)+"/aliases", nil, nil); err == nil {
		var lst struct {
			Aliases []struct{ Name, FunctionVersion, Description string }
		}
		json.Unmarshal(out, &lst)
		for _, a := range lst.Aliases {
			if f.Aliases == nil {
				f.Aliases = map[string]FunctionAlias{}
			}
			alias := FunctionAlias{Description: a.Description}
			// An alias at the newest version follows the deploy; one pinned
			// elsewhere keeps its number.
			if a.FunctionVersion != newest && a.FunctionVersion != "$LATEST" {
				alias.Version = a.FunctionVersion
			}
			f.Aliases[a.Name] = alias
		}
	}
	if out, err := c.do(ctx, "GET", "/2021-10-31/functions/"+url.PathEscape(name)+"/url", nil, nil); err == nil {
		var u struct {
			AuthType string
			Cors     json.RawMessage
		}
		json.Unmarshal(out, &u)
		f.URL = &FunctionURL{AuthType: u.AuthType}
		if len(u.Cors) > 0 {
			f.URL.CORS = Doc{JSON: string(u.Cors)}
		}
	}
}

// ---- destroy ----

// destroyLayers deletes every version of each stack layer.
func destroyLayers(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.Layers) {
		out, err := c.do(ctx, "GET", "/2018-10-31/layers/"+url.PathEscape(name)+"/versions", nil, nil)
		if err != nil {
			record(rep, "layer/"+name, err)
			continue
		}
		var lst struct {
			LayerVersions []struct{ Version int64 }
		}
		json.Unmarshal(out, &lst)
		var last error
		for _, v := range lst.LayerVersions {
			if _, err := c.do(ctx, "DELETE", "/2018-10-31/layers/"+url.PathEscape(name)+"/versions/"+strconv.FormatInt(v.Version, 10), nil, nil); err != nil {
				last = err
			}
		}
		record(rep, "layer/"+name, last)
	}
	return nil
}
