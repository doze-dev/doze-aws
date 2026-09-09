package console

import (
	"net/http"
	"strings"
)

// API keys and usage plans live at /apigw/keys: every key, every plan, and
// the wiring between them and the deployed stages.

func (c *Console) apigwKeysData(r *http.Request) (map[string]any, error) {
	keys, err := c.be.ListAPIKeys(r.Context())
	if err != nil {
		return nil, err
	}
	plans, _ := c.be.ListUsagePlans(r.Context())
	apis, _ := c.be.ListRestAPIs(r.Context())
	type apiStage struct{ APIID, APIName, Stage string }
	var stages []apiStage
	for _, a := range apis {
		for _, st := range apiStagesOf(r, c, a.ID) {
			stages = append(stages, apiStage{APIID: a.ID, APIName: a.Name, Stage: st})
		}
	}
	return map[string]any{"Keys": keys, "Plans": plans, "Stages": stages}, nil
}

// apiStagesOf lists an API's stage names.
func apiStagesOf(r *http.Request, c *Console, apiID string) []string {
	stages, _ := c.be.APIStages(r.Context(), apiID, endpointHost(r))
	var names []string
	for _, st := range stages {
		names = append(names, st.Name)
	}
	return names
}

func (c *Console) apigwKeys(w http.ResponseWriter, r *http.Request) {
	data, err := c.apigwKeysData(r)
	if err != nil {
		c.fail(w, err)
		return
	}
	data["List"], _ = c.be.ListRestAPIs(r.Context())
	data["Title"] = "API keys · API Gateway"
	c.render(w, r, "apigw_keys", data)
}

func (c *Console) apigwKeysPartial(w http.ResponseWriter, r *http.Request) {
	data, err := c.apigwKeysData(r)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "apigw_keys_body", data)
}

func (c *Console) apigwCreateKey(w http.ResponseWriter, r *http.Request) {
	if err := c.be.CreateAPIKey(r.Context(), strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("description")), strings.TrimSpace(r.FormValue("value"))); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "API key created — reveal it from the row")
	c.apigwKeysPartial(w, r)
}

func (c *Console) apigwRevealKey(w http.ResponseWriter, r *http.Request) {
	k, err := c.be.RevealAPIKey(r.Context(), r.PathValue("key"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "apigw_key_value", map[string]any{"K": k})
}

func (c *Console) apigwToggleKey(w http.ResponseWriter, r *http.Request) {
	if err := c.be.SetAPIKeyEnabled(r.Context(), r.FormValue("id"), r.FormValue("enable") == "true"); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "API key updated")
	c.apigwKeysPartial(w, r)
}

func (c *Console) apigwDeleteKey(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteAPIKey(r.Context(), r.FormValue("id")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "API key deleted")
	c.apigwKeysPartial(w, r)
}

func (c *Console) apigwCreatePlan(w http.ResponseWriter, r *http.Request) {
	apiID, stage, _ := strings.Cut(r.FormValue("stage"), ":")
	if err := c.be.CreateUsagePlan(r.Context(), strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("description")), apiID, stage); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Usage plan created — attach keys to it")
	c.apigwKeysPartial(w, r)
}

func (c *Console) apigwPlanAddStage(w http.ResponseWriter, r *http.Request) {
	apiID, stage, _ := strings.Cut(r.FormValue("stage"), ":")
	if err := c.be.AddUsagePlanStage(r.Context(), r.PathValue("plan"), apiID, stage); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Stage added to the plan")
	c.apigwKeysPartial(w, r)
}

func (c *Console) apigwDeletePlan(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteUsagePlan(r.Context(), r.FormValue("id")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Usage plan deleted")
	c.apigwKeysPartial(w, r)
}

func (c *Console) apigwPlanAttachKey(w http.ResponseWriter, r *http.Request) {
	if err := c.be.AttachUsagePlanKey(r.Context(), r.PathValue("plan"), r.FormValue("key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Key attached")
	c.apigwKeysPartial(w, r)
}

func (c *Console) apigwPlanDetachKey(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DetachUsagePlanKey(r.Context(), r.PathValue("plan"), r.FormValue("key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Key detached")
	c.apigwKeysPartial(w, r)
}

// apigwPlanDetail is GetUsagePlan plus one key's GetUsagePlanKey row.
func (c *Console) apigwPlanDetail(w http.ResponseWriter, r *http.Request) {
	fields, err := c.be.GetUsagePlan(r.Context(), r.PathValue("plan"))
	if err != nil {
		c.fail(w, err)
		return
	}
	data := map[string]any{"Title": "Usage plan " + fields["name"], "Fields": fields}
	if key := r.URL.Query().Get("key"); key != "" {
		if k, err := c.be.UsagePlanKey(r.Context(), r.PathValue("plan"), key); err == nil {
			fields["key "+k.Name] = k.Value
		}
	}
	c.partial(w, "eb_kv_detail", data)
}
