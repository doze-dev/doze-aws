package cloudformation

// Export of SSM Parameter Store parameters.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitParameters(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Parameters) {
		p := s.Parameters[name]
		props := map[string]any{
			"Name":  name,
			"Type":  orDefault(p.Type, "String"),
			"Value": p.Value,
		}
		putIfStr(props, "Description", p.Description)
		// SecureString values are not exported, matching the secrets rule.
		if p.Type == "SecureString" {
			props["Value"] = ""
		}
		add("Parameter", name, "AWS::SSM::Parameter", props)
	}
}
