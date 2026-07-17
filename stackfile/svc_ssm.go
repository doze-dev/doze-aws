package stackfile

// SSM apply + export: parameters, force-overwrite semantics, and the
// deliberate refusal to export SecureString values.

import (
	"context"
	"encoding/json"
	"fmt"
)

// ---- parameters ----

func applyParameters(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Parameters) {
		p := s.Parameters[name]
		typ := p.Type
		if typ == "" {
			typ = "String"
		}
		_, err := c.json11(ctx, "AmazonSSM", "GetParameter", map[string]any{"Name": name})
		switch {
		case err == nil && !p.Force:
			rep.add("skipped", "parameter/"+name, "exists — value untouched (set force: true to overwrite)")
		case err == nil && p.Force:
			in := map[string]any{"Name": name, "Value": p.Value, "Type": typ, "Overwrite": true}
			if p.Description != "" {
				in["Description"] = p.Description
			}
			if _, err := c.json11(ctx, "AmazonSSM", "PutParameter", in); err != nil {
				return fmt.Errorf("parameter %q: %w", name, err)
			}
			rep.add("updated", "parameter/"+name, "value (force)")
		case notFound(err):
			in := map[string]any{"Name": name, "Value": p.Value, "Type": typ}
			if p.Description != "" {
				in["Description"] = p.Description
			}
			if len(p.Tags) > 0 {
				in["Tags"] = tagList(p.Tags, "Key", "Value")
			}
			if _, err := c.json11(ctx, "AmazonSSM", "PutParameter", in); err != nil {
				return fmt.Errorf("parameter %q: %w", name, err)
			}
			rep.add("created", "parameter/"+name, "")
		default:
			return fmt.Errorf("parameter %q: %w", name, err)
		}
	}
	return nil
}

func exportParameters(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "AmazonSSM", "DescribeParameters", map[string]any{"MaxResults": 50})
	if err != nil {
		return err
	}
	var lst struct {
		Parameters []struct{ Name, Type, Description string }
	}
	json.Unmarshal(out, &lst)
	if len(lst.Parameters) > 0 {
		s.Parameters = map[string]Parameter{}
	}
	for _, p := range lst.Parameters {
		param := Parameter{Type: p.Type, Description: p.Description}
		if p.Type == "String" || p.Type == "StringList" {
			if out, err := c.json11(ctx, "AmazonSSM", "GetParameter", map[string]any{"Name": p.Name}); err == nil {
				var g struct {
					Parameter struct{ Value string }
				}
				json.Unmarshal(out, &g)
				param.Value = g.Parameter.Value
			}
			if param.Type == "String" {
				param.Type = "" // the default; keep exports minimal
			}
		}
		if out, err := c.json11(ctx, "AmazonSSM", "ListTagsForResource", map[string]any{
			"ResourceType": "Parameter", "ResourceId": p.Name,
		}); err == nil {
			var lt struct {
				TagList []struct{ Key, Value string }
			}
			json.Unmarshal(out, &lt)
			for _, tag := range lt.TagList {
				if param.Tags == nil {
					param.Tags = map[string]string{}
				}
				param.Tags[tag.Key] = tag.Value
			}
		}
		s.Parameters[p.Name] = param
	}
	return nil
}
