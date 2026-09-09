package cloudformation

// AWS::ApiGateway::ApiKey, ::UsagePlan and ::UsagePlanKey, and SAM's
// Auth.UsagePlan on a Serverless::Api. Keys and plans are named; a
// UsagePlanKey joins them by Ref, which yields the name.

import (
	"fmt"

	"github.com/doze-dev/doze-aws/provision"
)

func (m *mapper) apiKey(name string, props map[string]any) error {
	k := provision.APIKey{Description: propStr(props, "Description"), Value: propStr(props, "Value")}
	if _, ok := props["Enabled"]; ok {
		enabled := propBool(props, "Enabled")
		k.Enabled = &enabled
	}
	if m.stack.APIKeys == nil {
		m.stack.APIKeys = map[string]provision.APIKey{}
	}
	m.stack.APIKeys[name] = k
	return nil
}

func (m *mapper) usagePlan(name string, props map[string]any) error {
	p := provision.UsagePlan{Description: propStr(props, "Description")}
	for _, item := range propList(props, "ApiStages") {
		st, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p.Stages = append(p.Stages, provision.UsagePlanStage{API: propStr(st, "ApiId"), Stage: propStr(st, "Stage")})
	}
	if th := propMap(props, "Throttle"); th != nil {
		p.Throttle = &provision.UsagePlanThrottle{Rate: floatOf(th["RateLimit"]), Burst: propInt(th, "BurstLimit")}
	}
	if q := propMap(props, "Quota"); q != nil {
		p.Quota = &provision.UsagePlanQuota{Limit: propInt(q, "Limit"), Offset: propInt(q, "Offset"), Period: propStr(q, "Period")}
	}
	if m.stack.UsagePlans == nil {
		m.stack.UsagePlans = map[string]provision.UsagePlan{}
	}
	m.stack.UsagePlans[name] = p
	return nil
}

func (m *mapper) usagePlanKey(props map[string]any) error {
	key, plan := propStr(props, "KeyId"), propStr(props, "UsagePlanId")
	if key == "" || plan == "" {
		return fmt.Errorf("KeyId and UsagePlanId are required")
	}
	if typ := propStr(props, "KeyType"); typ != "" && typ != "API_KEY" {
		return fmt.Errorf("KeyType %q: API_KEY is the only key type", typ)
	}
	m.deferred = append(m.deferred, func() error {
		p, ok := m.stack.UsagePlans[plan]
		if !ok {
			return fmt.Errorf("usage plan key: plan %q is not declared in the template", plan)
		}
		if _, ok := m.stack.APIKeys[key]; !ok {
			return fmt.Errorf("usage plan key: key %q is not declared in the template", key)
		}
		p.Keys = append(p.Keys, key)
		m.stack.UsagePlans[plan] = p
		return nil
	})
	return nil
}

// samUsagePlan reads Auth.UsagePlan on a Serverless::Api: SAM creates a
// plan on the API's stage and a key in it. PER_API makes both per API;
// SHARED shares one plan across every API that says so.
func (m *mapper) samUsagePlan(apiName string, auth map[string]any) {
	up := propMap(auth, "UsagePlan")
	if up == nil {
		return
	}
	mode := propStr(up, "CreateUsagePlan")
	if mode == "" || mode == "NONE" {
		return
	}
	planName := propStr(up, "UsagePlanName")
	keyName := apiName + "-key"
	if planName == "" {
		planName = apiName + "-plan"
		if mode == "SHARED" {
			planName, keyName = "shared-plan", "shared-key"
		}
	}
	if m.stack.APIKeys == nil {
		m.stack.APIKeys = map[string]provision.APIKey{}
	}
	if m.stack.UsagePlans == nil {
		m.stack.UsagePlans = map[string]provision.UsagePlan{}
	}
	if _, ok := m.stack.APIKeys[keyName]; !ok {
		m.stack.APIKeys[keyName] = provision.APIKey{}
	}
	p := m.stack.UsagePlans[planName]
	p.Description = propStr(up, "Description")
	p.Stages = append(p.Stages, provision.UsagePlanStage{API: apiName})
	if th := propMap(up, "Throttle"); th != nil {
		p.Throttle = &provision.UsagePlanThrottle{Rate: floatOf(th["RateLimit"]), Burst: propInt(th, "BurstLimit")}
	}
	if q := propMap(up, "Quota"); q != nil {
		p.Quota = &provision.UsagePlanQuota{Limit: propInt(q, "Limit"), Offset: propInt(q, "Offset"), Period: propStr(q, "Period")}
	}
	hasKey := false
	for _, k := range p.Keys {
		hasKey = hasKey || k == keyName
	}
	if !hasKey {
		p.Keys = append(p.Keys, keyName)
	}
	m.stack.UsagePlans[planName] = p
}

func floatOf(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		var f float64
		fmt.Sscan(t, &f)
		return f
	}
	return 0
}
