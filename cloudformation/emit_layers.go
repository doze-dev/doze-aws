package cloudformation

// Export of Lambda layer versions. Separate from functions because a function REFERENCES a layer, so the two are emitted independently and joined by logical ID.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitLayers(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Layers) {
		l := s.Layers[name]
		props := map[string]any{
			"LayerName": name,
			"Content":   map[string]any{"S3Bucket": "_local_", "S3Key": l.Code},
		}
		if len(l.Runtimes) > 0 {
			props["CompatibleRuntimes"] = l.Runtimes
		}
		putIfStr(props, "Description", l.Description)
		add("Layer", name, "AWS::Lambda::LayerVersion", props)
	}
}
