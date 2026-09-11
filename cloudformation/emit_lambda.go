package cloudformation

// Export of Lambda functions and everything that hangs off one: published versions, aliases, function URLs and event-source mappings.

import (
	"fmt"

	"github.com/doze-dev/doze-aws/provision"
)

func emitFunctions(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Functions) {
		f := s.Functions[name]
		props := map[string]any{"FunctionName": name}
		putIfStr(props, "Runtime", f.Runtime)
		putIfStr(props, "Handler", f.Handler)
		putIfNum(props, "Timeout", f.Timeout)
		putIfNum(props, "MemorySize", f.Memory)
		if f.Code != "" {
			// The _local_ convention is what makes an exported template
			// redeployable against doze-aws without a build step.
			props["Code"] = map[string]any{"S3Bucket": "_local_", "S3Key": f.Code}
		}
		if len(f.Env) > 0 {
			props["Environment"] = map[string]any{"Variables": f.Env}
		}
		if f.DLQ != nil {
			props["DeadLetterConfig"] = map[string]any{"TargetArn": destARN(f.DLQ)}
		}
		if len(f.Layers) > 0 {
			var layers []any
			for _, l := range f.Layers {
				if _, inStack := s.Layers[l]; inStack {
					layers = append(layers, map[string]any{"Ref": logicalID("Layer", l)})
				} else {
					layers = append(layers, l)
				}
			}
			props["Layers"] = layers
		}
		putTags(props, f.Tags)
		add("Function", name, "AWS::Lambda::Function", props)

		if f.Publish || len(f.Aliases) > 0 {
			// One version resource per function; every alias points at it.
			add("Version", name, "AWS::Lambda::Version", map[string]any{
				"FunctionName": map[string]any{"Ref": logicalID("Function", name)},
			})
			for _, alias := range sortedNames(f.Aliases) {
				props := map[string]any{
					"Name":            alias,
					"FunctionName":    map[string]any{"Ref": logicalID("Function", name)},
					"FunctionVersion": map[string]any{"Fn::GetAtt": []any{logicalID("Version", name), "Version"}},
				}
				if v := f.Aliases[alias].Version; v != "" {
					props["FunctionVersion"] = v
				}
				if d := f.Aliases[alias].Description; d != "" {
					props["Description"] = d
				}
				add("Alias", name+alias, "AWS::Lambda::Alias", props)
			}
		}
		if f.URL != nil {
			props := map[string]any{
				"TargetFunctionArn": map[string]any{"Fn::GetAtt": []any{logicalID("Function", name), "Arn"}},
				"AuthType":          orDefault(f.URL.AuthType, "NONE"),
			}
			if !f.URL.CORS.IsZero() {
				props["Cors"] = rawDoc(f.URL.CORS)
			}
			add("Url", name, "AWS::Lambda::Url", props)
		}

		for i, trig := range f.Triggers {
			esm := map[string]any{
				"FunctionName":   name,
				"EventSourceArn": arnSub("sqs", trig.Queue),
			}
			putIfNum(esm, "BatchSize", trig.Batch)
			if trig.Enabled != nil {
				esm["Enabled"] = *trig.Enabled
			}
			// Through add, like everything else: this block used to write the
			// resources map directly, doing by hand exactly what add does.
			add("Trigger", fmt.Sprintf("%s%d", name, i+1),
				"AWS::Lambda::EventSourceMapping", esm)
		}
	}
}
