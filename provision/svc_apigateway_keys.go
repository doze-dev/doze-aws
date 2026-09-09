package provision

// API keys and usage plans: applied after the APIs so a plan's stages
// resolve, keyed by name since the service mints ids.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func applyAPIKeys(ctx context.Context, c *client, s *Stack, rep *Report) error {
	keyIDs := map[string]string{}
	existing, err := listAPIKeys(ctx, c)
	if err != nil {
		return err
	}
	for _, name := range sortedNames(s.APIKeys) {
		k := s.APIKeys[name]
		enabled := k.Enabled == nil || *k.Enabled
		if id, ok := existing[name]; ok {
			ops := []map[string]string{{"op": "replace", "path": "/enabled", "value": fmt.Sprint(enabled)}}
			if k.Description != "" {
				ops = append(ops, map[string]string{"op": "replace", "path": "/description", "value": k.Description})
			}
			if _, err := c.do(ctx, "PATCH", "/apikeys/"+id, jsonHeader, mustJSON(map[string]any{"patchOperations": ops})); err != nil {
				return fmt.Errorf("api key %q: %w", name, err)
			}
			keyIDs[name] = id
			rep.add("updated", "apikey/"+name, "")
			continue
		}
		in := map[string]any{"name": name, "enabled": enabled}
		if k.Description != "" {
			in["description"] = k.Description
		}
		if k.Value != "" {
			in["value"] = k.Value
		}
		out, err := c.do(ctx, "POST", "/apikeys", jsonHeader, mustJSON(in))
		if err != nil {
			return fmt.Errorf("api key %q: %w", name, err)
		}
		var created struct{ ID string }
		json.Unmarshal(out, &created)
		keyIDs[name] = created.ID
		rep.add("created", "apikey/"+name, "")
	}

	plans, err := listUsagePlans(ctx, c)
	if err != nil {
		return err
	}
	for _, name := range sortedNames(s.UsagePlans) {
		p := s.UsagePlans[name]
		var stages []map[string]any
		for _, st := range p.Stages {
			id, found, err := findAPI(ctx, c, st.API)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("usage plan %q: API %q is not deployed", name, st.API)
			}
			stage := st.Stage
			if stage == "" {
				stage = orDefaultStr(s.APIs[st.API].Stage, "prod")
			}
			stages = append(stages, map[string]any{"apiId": id, "stage": stage})
		}
		id, exists := plans[name]
		if !exists {
			in := map[string]any{"name": name, "apiStages": stages}
			if p.Description != "" {
				in["description"] = p.Description
			}
			if p.Throttle != nil {
				in["throttle"] = map[string]any{"rateLimit": p.Throttle.Rate, "burstLimit": p.Throttle.Burst}
			}
			if p.Quota != nil {
				in["quota"] = map[string]any{"limit": p.Quota.Limit, "offset": p.Quota.Offset, "period": p.Quota.Period}
			}
			out, err := c.do(ctx, "POST", "/usageplans", jsonHeader, mustJSON(in))
			if err != nil {
				return fmt.Errorf("usage plan %q: %w", name, err)
			}
			var created struct{ ID string }
			json.Unmarshal(out, &created)
			id = created.ID
			rep.add("created", "usageplan/"+name, "")
		} else {
			var ops []map[string]string
			for _, st := range stages {
				ops = append(ops, map[string]string{"op": "add", "path": "/apiStages", "value": fmt.Sprint(st["apiId"]) + ":" + fmt.Sprint(st["stage"])})
			}
			if len(ops) > 0 {
				if _, err := c.do(ctx, "PATCH", "/usageplans/"+id, jsonHeader, mustJSON(map[string]any{"patchOperations": ops})); err != nil {
					return fmt.Errorf("usage plan %q: %w", name, err)
				}
			}
			rep.add("updated", "usageplan/"+name, "")
		}
		for _, keyName := range p.Keys {
			keyID, ok := keyIDs[keyName]
			if !ok {
				if keyID, ok = existing[keyName]; !ok {
					return fmt.Errorf("usage plan %q: key %q is not in the stack", name, keyName)
				}
			}
			_, err := c.do(ctx, "POST", "/usageplans/"+id+"/keys", jsonHeader, mustJSON(map[string]any{"keyId": keyID, "keyType": "API_KEY"}))
			if err != nil {
				var ae *apiErr
				if asAPIErr(err, &ae) && ae.status == 409 {
					continue // already attached
				}
				return fmt.Errorf("usage plan %q key %q: %w", name, keyName, err)
			}
		}
	}
	return nil
}

var jsonHeader = map[string]string{"Content-Type": "application/json"}

// listAPIKeys maps key names to ids.
func listAPIKeys(ctx context.Context, c *client) (map[string]string, error) {
	out, err := c.do(ctx, "GET", "/apikeys", nil, nil)
	if err != nil {
		return nil, err
	}
	var listed struct{ Items []struct{ ID, Name string } }
	json.Unmarshal(out, &listed)
	byName := map[string]string{}
	for _, k := range listed.Items {
		byName[k.Name] = k.ID
	}
	return byName, nil
}

// listUsagePlans maps plan names to ids.
func listUsagePlans(ctx context.Context, c *client) (map[string]string, error) {
	out, err := c.do(ctx, "GET", "/usageplans", nil, nil)
	if err != nil {
		return nil, err
	}
	var listed struct{ Items []struct{ ID, Name string } }
	json.Unmarshal(out, &listed)
	byName := map[string]string{}
	for _, p := range listed.Items {
		byName[p.Name] = p.ID
	}
	return byName, nil
}

func destroyAPIKeys(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	plans, _ := listUsagePlans(ctx, c)
	for _, name := range sortedNames(s.UsagePlans) {
		id, ok := plans[name]
		if !ok {
			rep.add("absent", "usageplan/"+name, "")
			continue
		}
		_, err := c.do(ctx, "DELETE", "/usageplans/"+url.PathEscape(id), nil, nil)
		record(rep, "usageplan/"+name, err)
	}
	keys, _ := listAPIKeys(ctx, c)
	for _, name := range sortedNames(s.APIKeys) {
		id, ok := keys[name]
		if !ok {
			rep.add("absent", "apikey/"+name, "")
			continue
		}
		_, err := c.do(ctx, "DELETE", "/apikeys/"+url.PathEscape(id), nil, nil)
		record(rep, "apikey/"+name, err)
	}
	return nil
}
