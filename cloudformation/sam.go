package cloudformation

// The SAM transform.
//
// SAM is a macro layer: `AWS::Serverless::Function` expands to a Lambda
// function plus a role plus event wiring. doze-aws does not need the role, so
// the transform here is deliberately thin — it normalises SAM's shorthand onto
// the properties the Lambda and DynamoDB mappers already understand, and lets
// the Events block through to be expanded once every resource exists.
//
// The honest limit: SAM's `Api` and `HttpApi` events need API Gateway, which
// doze-aws does not serve yet. Those are refused by name during mapping rather
// than quietly producing a function nothing can reach.

import "fmt"

// applySAMTransform normalises serverless resources in place. It runs before
// classification, so afterwards the ordinary registry handles everything.
//
// The Globals section supplies defaults for every function, and is applied
// first so an explicit property on a function always wins.
func applySAMTransform(t *Template) error {
	fnGlobals, _ := t.Globals["Function"].(map[string]any)
	for _, id := range t.Order() {
		r := t.Resources[id]
		if r.Type != "AWS::Serverless::Function" {
			continue
		}
		for k, v := range fnGlobals {
			if _, set := r.Properties[k]; !set {
				r.Properties[k] = v
			}
		}
		// SAM's InlineCode is a source body, and there is no build step
		// locally. Dropping it means apply reports a missing code path rather
		// than creating a function that cannot run.
		delete(r.Properties, "InlineCode")
		t.samVersioning(id, r)
	}
	for _, id := range t.Order() {
		r := t.Resources[id]
		if r.Type != "AWS::Serverless::LayerVersion" {
			continue
		}
		// SAM's LayerVersion is the plain resource with ContentUri in place
		// of Content; the layer mapper reads either. RetentionPolicy governs
		// what CloudFormation keeps on delete, which does not apply here.
		r.Type = "AWS::Lambda::LayerVersion"
		delete(r.Properties, "RetentionPolicy")
	}
	for _, id := range t.Order() {
		r := t.Resources[id]
		if r.Type != "AWS::Serverless::StateMachine" {
			continue
		}
		// SAM's StateMachine is the plain resource under different property
		// names. Events would need EventBridge/API wiring into executions,
		// which does not exist locally — refused by name, not dropped.
		if _, has := r.Properties["Events"]; has {
			return fmt.Errorf("resource %s: Events on a Serverless StateMachine are not supported locally; start executions directly or from a Lambda", id)
		}
		r.Type = "AWS::StepFunctions::StateMachine"
		renameProp(r.Properties, "Name", "StateMachineName")
		renameProp(r.Properties, "Role", "RoleArn")
		renameProp(r.Properties, "Type", "StateMachineType")
		// Policies build IAM the local stack does not enforce on deploys.
		delete(r.Properties, "Policies")
		delete(r.Properties, "Logging")
		delete(r.Properties, "Tracing")
	}
	return nil
}

// samVersioning expands the shorthands SAM puts on a function into the
// resources SAM itself generates, under the logical ids SAM gives them, so a
// template's `!GetAtt MyFnUrl.FunctionUrl` resolves: AutoPublishAlias into
// a Version and an Alias at it, FunctionUrlConfig into a Url.
func (t *Template) samVersioning(id string, r *Resource) {
	add := func(logical, typ string, props map[string]any) {
		if _, taken := t.Resources[logical]; taken {
			return
		}
		t.Resources[logical] = &Resource{LogicalID: logical, Type: typ, Properties: props}
		t.order = append(t.order, logical)
	}
	if alias, ok := r.Properties["AutoPublishAlias"].(string); ok && alias != "" {
		add(id+"Version", "AWS::Lambda::Version", map[string]any{
			"FunctionName": map[string]any{"Ref": id},
		})
		add(id+"Alias"+alias, "AWS::Lambda::Alias", map[string]any{
			"Name":            alias,
			"FunctionName":    map[string]any{"Ref": id},
			"FunctionVersion": map[string]any{"Fn::GetAtt": []any{id + "Version", "Version"}},
		})
	}
	delete(r.Properties, "AutoPublishAlias")
	delete(r.Properties, "DeploymentPreference")
	if cfg, ok := r.Properties["FunctionUrlConfig"].(map[string]any); ok {
		props := map[string]any{
			"TargetFunctionArn": map[string]any{"Fn::GetAtt": []any{id, "Arn"}},
			"AuthType":          cfg["AuthType"],
		}
		if cors, ok := cfg["Cors"]; ok {
			props["Cors"] = cors
		}
		add(id+"Url", "AWS::Lambda::Url", props)
	}
	delete(r.Properties, "FunctionUrlConfig")
}

func renameProp(props map[string]any, from, to string) {
	if v, ok := props[from]; ok {
		if _, taken := props[to]; !taken {
			props[to] = v
		}
		delete(props, from)
	}
}

// unsupportedSAM names serverless resource types doze-aws cannot model, with
// the reason, so the registry can refuse them precisely.
var unsupportedSAM = map[string]string{
	"AWS::Serverless::Application": "nested applications need the Serverless Application Repository",
}

func samReason(typ string) (string, bool) {
	reason, ok := unsupportedSAM[typ]
	return reason, ok
}
