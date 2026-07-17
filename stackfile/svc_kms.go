package stackfile

// KMS apply + export: keys addressed by alias, rotation, and key metadata.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ---- keys (KMS, keyed by alias) ----

func applyKeys(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Keys) {
		k := s.Keys[name]
		alias := "alias/" + name
		// Existing alias → converge rotation only.
		body, err := c.json11(ctx, "TrentService", "ListAliases", map[string]any{})
		if err != nil {
			return fmt.Errorf("key %q: %w", name, err)
		}
		var aliases struct {
			Aliases []struct {
				AliasName   string `json:"AliasName"`
				TargetKeyId string `json:"TargetKeyId"`
			} `json:"Aliases"`
		}
		json.Unmarshal(body, &aliases)
		keyID := ""
		for _, a := range aliases.Aliases {
			if a.AliasName == alias {
				keyID = a.TargetKeyId
			}
		}
		created := false
		if keyID == "" {
			in := map[string]any{}
			if k.Spec != "" && k.Spec != "SYMMETRIC_DEFAULT" {
				in["KeySpec"] = k.Spec
				if strings.HasPrefix(k.Spec, "RSA") || strings.HasPrefix(k.Spec, "ECC") {
					in["KeyUsage"] = "SIGN_VERIFY"
				}
				if strings.HasPrefix(k.Spec, "HMAC") {
					in["KeyUsage"] = "GENERATE_VERIFY_MAC"
				}
			}
			if k.Usage != "" {
				in["KeyUsage"] = k.Usage // an explicit usage beats the spec default
			}
			if k.Description != "" {
				in["Description"] = k.Description
			}
			if len(k.Tags) > 0 {
				in["Tags"] = tagList(k.Tags, "TagKey", "TagValue")
			}
			out, err := c.json11(ctx, "TrentService", "CreateKey", in)
			if err != nil {
				return fmt.Errorf("key %q: %w", name, err)
			}
			var ck struct {
				KeyMetadata struct {
					KeyId string `json:"KeyId"`
				} `json:"KeyMetadata"`
			}
			json.Unmarshal(out, &ck)
			keyID = ck.KeyMetadata.KeyId
			if _, err := c.json11(ctx, "TrentService", "CreateAlias", map[string]any{
				"AliasName": alias, "TargetKeyId": keyID,
			}); err != nil {
				return fmt.Errorf("key %q alias: %w", name, err)
			}
			created = true
		}
		if k.Rotation {
			if _, err := c.json11(ctx, "TrentService", "EnableKeyRotation", map[string]any{"KeyId": keyID}); err != nil {
				return fmt.Errorf("key %q rotation: %w", name, err)
			}
		}
		if !created {
			// Converge the cheap metadata on an existing key.
			if k.Description != "" {
				if _, err := c.json11(ctx, "TrentService", "UpdateKeyDescription", map[string]any{
					"KeyId": keyID, "Description": k.Description,
				}); err != nil {
					return fmt.Errorf("key %q description: %w", name, err)
				}
			}
			if len(k.Tags) > 0 {
				if _, err := c.json11(ctx, "TrentService", "TagResource", map[string]any{
					"KeyId": keyID, "Tags": tagList(k.Tags, "TagKey", "TagValue"),
				}); err != nil {
					return fmt.Errorf("key %q tags: %w", name, err)
				}
			}
		}
		if created {
			rep.add("created", "key/"+name, keyID)
		} else {
			rep.add("skipped", "key/"+name, "exists")
		}
	}
	return nil
}

func exportKeys(ctx context.Context, c *client, s *Stack) error {
	out, err := c.json11(ctx, "TrentService", "ListAliases", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		Aliases []struct{ AliasName, TargetKeyId string }
	}
	json.Unmarshal(out, &lst)
	for _, a := range lst.Aliases {
		name := strings.TrimPrefix(a.AliasName, "alias/")
		if strings.HasPrefix(name, "aws/") { // AWS-managed style aliases stay out
			continue
		}
		if s.Keys == nil {
			s.Keys = map[string]Key{}
		}
		k := Key{}
		if out, err := c.json11(ctx, "TrentService", "DescribeKey", map[string]any{"KeyId": a.TargetKeyId}); err == nil {
			var d struct {
				KeyMetadata struct{ KeySpec, KeyUsage, Description string }
			}
			json.Unmarshal(out, &d)
			if d.KeyMetadata.KeySpec != "" && d.KeyMetadata.KeySpec != "SYMMETRIC_DEFAULT" {
				k.Spec = d.KeyMetadata.KeySpec
			}
			// Only export a usage apply wouldn't infer from the spec.
			if u := d.KeyMetadata.KeyUsage; u != "" && u != inferredUsage(k.Spec) {
				k.Usage = u
			}
			k.Description = d.KeyMetadata.Description
		}
		if out, err := c.json11(ctx, "TrentService", "GetKeyRotationStatus", map[string]any{"KeyId": a.TargetKeyId}); err == nil {
			var rs struct{ KeyRotationEnabled bool }
			json.Unmarshal(out, &rs)
			k.Rotation = rs.KeyRotationEnabled
		}
		if out, err := c.json11(ctx, "TrentService", "ListResourceTags", map[string]any{"KeyId": a.TargetKeyId}); err == nil {
			var lt struct {
				Tags []struct{ TagKey, TagValue string }
			}
			json.Unmarshal(out, &lt)
			for _, tag := range lt.Tags {
				if k.Tags == nil {
					k.Tags = map[string]string{}
				}
				k.Tags[tag.TagKey] = tag.TagValue
			}
		}
		s.Keys[name] = k
	}
	return nil
}

// inferredUsage mirrors apply's spec → default-usage mapping.
func inferredUsage(spec string) string {
	switch {
	case strings.HasPrefix(spec, "RSA"), strings.HasPrefix(spec, "ECC"):
		return "SIGN_VERIFY"
	case strings.HasPrefix(spec, "HMAC"):
		return "GENERATE_VERIFY_MAC"
	default:
		return "ENCRYPT_DECRYPT"
	}
}
