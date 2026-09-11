package cloudformation

// Export of Secrets Manager secrets.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitSecrets(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Secrets) {
		sec := s.Secrets[name]
		props := map[string]any{"Name": name}
		putIfStr(props, "Description", sec.Description)
		// Values are deliberately omitted, as they were in the old export.
		putTags(props, sec.Tags)
		add("Secret", name, "AWS::SecretsManager::Secret", props)
	}
}
