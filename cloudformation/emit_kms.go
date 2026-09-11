package cloudformation

// Export of KMS keys and their aliases. A key is addressed by its alias, which is how nameProperty declares it.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitKeys(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Keys) {
		k := s.Keys[name]
		props := map[string]any{}
		putIfStr(props, "Description", k.Description)
		putIf(props, "EnableKeyRotation", k.Rotation)
		putIfStr(props, "KeySpec", k.Spec)
		putIfStr(props, "KeyUsage", k.Usage)
		putTags(props, k.Tags)
		add("Key", name, "AWS::KMS::Key", props)
		// The alias is what addresses the key, so it must be exported too.
		// Through add, like everything else: this block used to write the
		// resources map directly, doing by hand exactly what add does.
		add("KeyAlias", name, "AWS::KMS::Alias", map[string]any{
			"AliasName":   "alias/" + name,
			"TargetKeyId": map[string]any{"Ref": logicalID("Key", name)},
		})
	}
}
