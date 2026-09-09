package console

import (
	"context"
	"encoding/json"
	"strconv"
)

// ---- API keys and usage plans ----

// APIKeyRow is one key as the keys tab shows it. Value is set only by
// RevealAPIKey; the list never carries it.
type APIKeyRow struct {
	ID, Name, Description string
	Enabled               bool
	Value                 string
	Created               string
}

// UsagePlanRow is one plan with the stages it covers and the keys it admits.
type UsagePlanRow struct {
	ID, Name, Description string
	Stages                []PlanStageRow
	Keys                  []APIKeyRow
	Throttle              string // "10 rps, burst 5" or ""
	Quota                 string // "1000 per MONTH" or ""
}

type PlanStageRow struct {
	APIID, APIName, Stage string
}

func (b *backend) ListAPIKeys(ctx context.Context) ([]APIKeyRow, error) {
	body, err := b.apigwJSON(ctx, "GET", "/apikeys", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []struct {
			ID, Name, Description string
			Enabled               bool
			CreatedDate           float64
		} `json:"items"`
	}
	json.Unmarshal(body, &out)
	rows := make([]APIKeyRow, 0, len(out.Items))
	for _, k := range out.Items {
		rows = append(rows, APIKeyRow{ID: k.ID, Name: k.Name, Description: k.Description, Enabled: k.Enabled, Created: epochToTime(k.CreatedDate)})
	}
	return rows, nil
}

// RevealAPIKey reads one key with its value (GetApiKey includeValue).
func (b *backend) RevealAPIKey(ctx context.Context, id string) (APIKeyRow, error) {
	body, err := b.apigwJSON(ctx, "GET", "/apikeys/"+id+"?includeValue=true", nil)
	if err != nil {
		return APIKeyRow{}, err
	}
	var k struct {
		ID, Name, Description, Value string
		Enabled                      bool
		CreatedDate                  float64
	}
	json.Unmarshal(body, &k)
	return APIKeyRow{ID: k.ID, Name: k.Name, Description: k.Description, Enabled: k.Enabled, Value: k.Value, Created: epochToTime(k.CreatedDate)}, nil
}

func (b *backend) CreateAPIKey(ctx context.Context, name, description, value string) error {
	in := map[string]any{"name": name, "enabled": true}
	if description != "" {
		in["description"] = description
	}
	if value != "" {
		in["value"] = value
	}
	_, err := b.apigwJSON(ctx, "POST", "/apikeys", in)
	return err
}

// SetAPIKeyEnabled flips a key (UpdateApiKey).
func (b *backend) SetAPIKeyEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := b.apigwJSON(ctx, "PATCH", "/apikeys/"+id, map[string]any{
		"patchOperations": []map[string]string{{"op": "replace", "path": "/enabled", "value": strconv.FormatBool(enabled)}},
	})
	return err
}

func (b *backend) DeleteAPIKey(ctx context.Context, id string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/apikeys/"+id, nil)
	return err
}

func (b *backend) ListUsagePlans(ctx context.Context) ([]UsagePlanRow, error) {
	body, err := b.apigwJSON(ctx, "GET", "/usageplans", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []struct {
			ID, Name, Description string
			APIStages             []struct{ APIID, Stage string } `json:"apiStages"`
			Throttle              *struct {
				RateLimit  float64
				BurstLimit int
			}
			Quota *struct {
				Limit  int
				Period string
			}
		} `json:"items"`
	}
	json.Unmarshal(body, &out)
	apis, _ := b.ListRestAPIs(ctx)
	names := map[string]string{}
	for _, a := range apis {
		names[a.ID] = a.Name
	}
	rows := make([]UsagePlanRow, 0, len(out.Items))
	for _, p := range out.Items {
		row := UsagePlanRow{ID: p.ID, Name: p.Name, Description: p.Description}
		for _, st := range p.APIStages {
			row.Stages = append(row.Stages, PlanStageRow{APIID: st.APIID, APIName: names[st.APIID], Stage: st.Stage})
		}
		if p.Throttle != nil {
			row.Throttle = strconv.FormatFloat(p.Throttle.RateLimit, 'f', -1, 64) + " rps, burst " + strconv.Itoa(p.Throttle.BurstLimit)
		}
		if p.Quota != nil {
			row.Quota = strconv.Itoa(p.Quota.Limit) + " per " + p.Quota.Period
		}
		row.Keys, _ = b.UsagePlanKeys(ctx, p.ID)
		rows = append(rows, row)
	}
	return rows, nil
}

// GetUsagePlan reads one plan's fields (the list carries the same).
func (b *backend) GetUsagePlan(ctx context.Context, id string) (map[string]string, error) {
	body, err := b.apigwJSON(ctx, "GET", "/usageplans/"+id, nil)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	json.Unmarshal(body, &raw)
	fields := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			fields[k] = s
		}
	}
	return fields, nil
}

// UsagePlanKeys lists the keys a plan admits (GetUsagePlanKeys).
func (b *backend) UsagePlanKeys(ctx context.Context, planID string) ([]APIKeyRow, error) {
	body, err := b.apigwJSON(ctx, "GET", "/usageplans/"+planID+"/keys", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []struct{ ID, Name string } `json:"items"`
	}
	json.Unmarshal(body, &out)
	rows := make([]APIKeyRow, 0, len(out.Items))
	for _, k := range out.Items {
		rows = append(rows, APIKeyRow{ID: k.ID, Name: k.Name})
	}
	return rows, nil
}

// UsagePlanKey reads one key of a plan (GetUsagePlanKey).
func (b *backend) UsagePlanKey(ctx context.Context, planID, keyID string) (APIKeyRow, error) {
	body, err := b.apigwJSON(ctx, "GET", "/usageplans/"+planID+"/keys/"+keyID, nil)
	if err != nil {
		return APIKeyRow{}, err
	}
	var k struct{ ID, Name, Value string }
	json.Unmarshal(body, &k)
	return APIKeyRow{ID: k.ID, Name: k.Name, Value: k.Value}, nil
}

func (b *backend) CreateUsagePlan(ctx context.Context, name, description, apiID, stage string) error {
	in := map[string]any{"name": name}
	if description != "" {
		in["description"] = description
	}
	if apiID != "" {
		in["apiStages"] = []map[string]string{{"apiId": apiID, "stage": stage}}
	}
	_, err := b.apigwJSON(ctx, "POST", "/usageplans", in)
	return err
}

// AddUsagePlanStage covers one more API stage (UpdateUsagePlan).
func (b *backend) AddUsagePlanStage(ctx context.Context, planID, apiID, stage string) error {
	_, err := b.apigwJSON(ctx, "PATCH", "/usageplans/"+planID, map[string]any{
		"patchOperations": []map[string]string{{"op": "add", "path": "/apiStages", "value": apiID + ":" + stage}},
	})
	return err
}

func (b *backend) DeleteUsagePlan(ctx context.Context, id string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/usageplans/"+id, nil)
	return err
}

// AttachUsagePlanKey admits a key (CreateUsagePlanKey).
func (b *backend) AttachUsagePlanKey(ctx context.Context, planID, keyID string) error {
	_, err := b.apigwJSON(ctx, "POST", "/usageplans/"+planID+"/keys", map[string]any{"keyId": keyID, "keyType": "API_KEY"})
	return err
}

// DetachUsagePlanKey drops a key (DeleteUsagePlanKey).
func (b *backend) DetachUsagePlanKey(ctx context.Context, planID, keyID string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/usageplans/"+planID+"/keys/"+keyID, nil)
	return err
}
