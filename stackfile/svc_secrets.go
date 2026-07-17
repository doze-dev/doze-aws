package stackfile

// Secrets Manager apply + export: secrets, force-overwrite semantics, and the
// deliberate refusal to export secret values.

import (
	"context"
	"encoding/json"
	"fmt"
)

// ---- secrets ----

func applySecrets(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Secrets) {
		sec := s.Secrets[name]
		value := func(in map[string]any) map[string]any {
			if sec.Binary != "" {
				in["SecretBinary"] = sec.Binary // already base64, as the wire wants
			} else {
				in["SecretString"] = sec.Value
			}
			return in
		}
		_, err := c.json11(ctx, "secretsmanager", "DescribeSecret", map[string]any{"SecretId": name})
		switch {
		case err == nil && !sec.Force:
			rep.add("skipped", "secret/"+name, "exists — value untouched (set force: true to overwrite)")
		case err == nil && sec.Force:
			if _, err := c.json11(ctx, "secretsmanager", "PutSecretValue", value(map[string]any{
				"SecretId": name,
			})); err != nil {
				return fmt.Errorf("secret %q: %w", name, err)
			}
			rep.add("updated", "secret/"+name, "value (force)")
		case notFound(err):
			in := value(map[string]any{"Name": name})
			if sec.Description != "" {
				in["Description"] = sec.Description
			}
			if len(sec.Tags) > 0 {
				in["Tags"] = tagList(sec.Tags, "Key", "Value")
			}
			if _, err := c.json11(ctx, "secretsmanager", "CreateSecret", in); err != nil {
				return fmt.Errorf("secret %q: %w", name, err)
			}
			rep.add("created", "secret/"+name, "")
		default:
			return fmt.Errorf("secret %q: %w", name, err)
		}
	}
	return nil
}

func exportSecrets(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "secretsmanager", "ListSecrets", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		SecretList []struct {
			Name        string
			Description string
			Tags        []struct{ Key, Value string }
		}
	}
	json.Unmarshal(out, &lst)
	if len(lst.SecretList) > 0 {
		s.Secrets = map[string]Secret{}
	}
	for _, sec := range lst.SecretList {
		out := Secret{Description: sec.Description} // values intentionally not exported
		for _, tag := range sec.Tags {
			if out.Tags == nil {
				out.Tags = map[string]string{}
			}
			out.Tags[tag.Key] = tag.Value
		}
		s.Secrets[sec.Name] = out
	}
	return nil
}
