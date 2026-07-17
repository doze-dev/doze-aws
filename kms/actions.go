package kms

// Action routing: the handler dispatch table, honest stubs for unsupported
// operations, and the shared param-extraction helpers.

import (
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// handlers maps every supported KMS action. Actions absent here (grants
// management, custom key stores, key material import, multi-region replicas)
// answer via stubs registered at the bottom.
var handlers = map[string]handler{
	"CreateKey":                           (*Server).createKey,
	"DescribeKey":                         (*Server).describeKey,
	"ListKeys":                            (*Server).listKeys,
	"EnableKey":                           (*Server).enableKey,
	"DisableKey":                          (*Server).disableKey,
	"ScheduleKeyDeletion":                 (*Server).scheduleKeyDeletion,
	"CancelKeyDeletion":                   (*Server).cancelKeyDeletion,
	"Encrypt":                             (*Server).encrypt,
	"Decrypt":                             (*Server).decrypt,
	"ReEncrypt":                           (*Server).reEncrypt,
	"GenerateDataKey":                     (*Server).generateDataKey,
	"GenerateDataKeyWithoutPlaintext":     (*Server).generateDataKeyWithoutPlaintext,
	"GenerateDataKeyPair":                 (*Server).generateDataKeyPair,
	"GenerateDataKeyPairWithoutPlaintext": (*Server).generateDataKeyPairWithoutPlaintext,
	"GenerateRandom":                      (*Server).generateRandom,
	"Sign":                                (*Server).sign,
	"Verify":                              (*Server).verify,
	"GetPublicKey":                        (*Server).getPublicKey,
	"GenerateMac":                         (*Server).generateMac,
	"VerifyMac":                           (*Server).verifyMac,
	"CreateAlias":                         (*Server).createAlias,
	"UpdateAlias":                         (*Server).updateAlias,
	"DeleteAlias":                         (*Server).deleteAlias,
	"ListAliases":                         (*Server).listAliases,
	"TagResource":                         (*Server).tagResource,
	"UntagResource":                       (*Server).untagResource,
	"ListResourceTags":                    (*Server).listResourceTags,
	"GetKeyPolicy":                        (*Server).getKeyPolicy,
	"PutKeyPolicy":                        (*Server).putKeyPolicy,
	"ListKeyPolicies":                     (*Server).listKeyPolicies,
	"GetKeyRotationStatus":                (*Server).getKeyRotationStatus,
	"EnableKeyRotation":                   (*Server).enableKeyRotation,
	"DisableKeyRotation":                  (*Server).disableKeyRotation,
	"RotateKeyOnDemand":                   (*Server).rotateKeyOnDemand,
	"ListKeyRotations":                    (*Server).listKeyRotations,
	"UpdateKeyDescription":                (*Server).updateKeyDescription,
}

func init() {
	// Tier S: infrastructure that cannot exist locally answers honestly.
	for _, name := range []string{
		"CreateCustomKeyStore", "ConnectCustomKeyStore", "DisconnectCustomKeyStore",
		"DeleteCustomKeyStore", "DescribeCustomKeyStores", "UpdateCustomKeyStore",
		"ImportKeyMaterial", "DeleteImportedKeyMaterial", "GetParametersForImport",
		"ReplicateKey", "UpdatePrimaryRegion",
		"CreateGrant", "RetireGrant", "RevokeGrant", "ListGrants", "ListRetirableGrants",
		"DeriveSharedSecret", "VerifyMacForImport",
	} {
		handlers[name] = stub(name)
	}
}

func stub(name string) handler {
	return func(*Server, map[string]any) (any, *awshttp.APIError) {
		return nil, awshttp.Errf(400, "UnsupportedOperationException",
			"%s is not supported by doze-aws (see docs/api-support/kms.md)", name)
	}
}

// ---- param helpers ----
//
// Scalar/blob/map accessors come from internal/awsjson (Str, Int, Blob,
// StrMap); only the KMS-specific tag shape lives here.

// ptags reads the KMS tag list shape [{TagKey, TagValue}].
func ptags(p map[string]any, key string) map[string]string {
	list, ok := p[key].([]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			k, _ := m["TagKey"].(string)
			v, _ := m["TagValue"].(string)
			if k != "" {
				out[k] = v
			}
		}
	}
	return out
}
