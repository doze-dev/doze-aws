package kms

// KMS's model-derived input validation: the constraint tables, walked by
// internal/modelcheck.
//
// Generated with `dzaudit cases kms` rather than transcribed, and replayed case
// by case in kms/rejection_parity_test.go.

import (
	"regexp"

	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

var constraintTables = map[string][]modelcheck.Constraint{
	"CancelKeyDeletion": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"CreateAlias": {
		{Path: "AliasName", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "AliasName", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[a-zA-Z0-9:/_-]+$`)},
		{Path: "AliasName", Kind: modelcheck.KindRequired},
		{Path: "TargetKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "TargetKeyId", Kind: modelcheck.KindRequired},
	},
	"CreateKey": {
		{Path: "CustomKeyStoreId", Kind: modelcheck.KindLength, Min: 1, Max: 64},
		{Path: "CustomerMasterKeySpec", Kind: modelcheck.KindEnum, Enum: []string{"ECC_SECG_P256K1", "SYMMETRIC_DEFAULT", "SM2", "RSA_3072", "RSA_4096", "ECC_NIST_P521", "HMAC_224", "HMAC_256", "HMAC_384", "HMAC_512", "RSA_2048", "ECC_NIST_P256", "ECC_NIST_P384"}},
		{Path: "Description", Kind: modelcheck.KindLength, Min: 0, Max: 8192},
		{Path: "KeySpec", Kind: modelcheck.KindEnum, Enum: []string{"ECC_NIST_P521", "SYMMETRIC_DEFAULT", "HMAC_384", "ECC_NIST_EDWARDS25519", "RSA_4096", "ECC_NIST_P256", "HMAC_512", "ML_DSA_44", "ML_DSA_65", "RSA_2048", "RSA_3072", "ECC_NIST_P384", "ECC_SECG_P256K1", "HMAC_256", "SM2", "ML_DSA_87", "HMAC_224"}},
		{Path: "KeyUsage", Kind: modelcheck.KindEnum, Enum: []string{"SIGN_VERIFY", "ENCRYPT_DECRYPT", "GENERATE_VERIFY_MAC", "KEY_AGREEMENT"}},
		{Path: "Origin", Kind: modelcheck.KindEnum, Enum: []string{"AWS_KMS", "EXTERNAL", "AWS_CLOUDHSM", "EXTERNAL_KEY_STORE"}},
		{Path: "Policy", Kind: modelcheck.KindLength, Min: 1, Max: 131072},
		{Path: "Tags[].TagKey", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].TagKey", Kind: modelcheck.KindRequired},
		{Path: "Tags[].TagValue", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].TagValue", Kind: modelcheck.KindRequired},
		{Path: "XksKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "XksKeyId", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[a-zA-Z0-9-_.]+$`)},
	},
	"Decrypt": {
		{Path: "CiphertextBlob", Kind: modelcheck.KindLength, Min: 1, Max: 6144},
		{Path: "DryRunModifiers[]", Kind: modelcheck.KindEnum, Enum: []string{"IGNORE_CIPHERTEXT"}},
		{Path: "EncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"RSAES_OAEP_SHA_256", "SM2PKE", "SYMMETRIC_DEFAULT", "RSAES_OAEP_SHA_1"}},
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Recipient.AttestationDocument", Kind: modelcheck.KindLength, Min: 1, Max: 262144},
		{Path: "Recipient.KeyEncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"RSAES_OAEP_SHA_256"}},
	},
	"DeleteAlias": {
		{Path: "AliasName", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "AliasName", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[a-zA-Z0-9:/_-]+$`)},
		{Path: "AliasName", Kind: modelcheck.KindRequired},
	},
	"DescribeKey": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"DisableKey": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"DisableKeyRotation": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"EnableKey": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"EnableKeyRotation": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "RotationPeriodInDays", Kind: modelcheck.KindRange, Min: 90, Max: 2560},
	},
	"Encrypt": {
		{Path: "EncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"SYMMETRIC_DEFAULT", "RSAES_OAEP_SHA_1", "RSAES_OAEP_SHA_256", "SM2PKE"}},
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Plaintext", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "Plaintext", Kind: modelcheck.KindRequired},
	},
	"GenerateDataKey": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "KeySpec", Kind: modelcheck.KindEnum, Enum: []string{"AES_256", "AES_128"}},
		{Path: "NumberOfBytes", Kind: modelcheck.KindRange, Min: 1, Max: 1024},
		{Path: "Recipient.AttestationDocument", Kind: modelcheck.KindLength, Min: 1, Max: 262144},
		{Path: "Recipient.KeyEncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"RSAES_OAEP_SHA_256"}},
	},
	"GenerateDataKeyPair": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "KeyPairSpec", Kind: modelcheck.KindEnum, Enum: []string{"SM2", "RSA_4096", "ECC_NIST_P256", "ECC_NIST_P384", "ECC_NIST_P521", "ECC_SECG_P256K1", "ECC_NIST_EDWARDS25519", "RSA_2048", "RSA_3072"}},
		{Path: "KeyPairSpec", Kind: modelcheck.KindRequired},
		{Path: "Recipient.AttestationDocument", Kind: modelcheck.KindLength, Min: 1, Max: 262144},
		{Path: "Recipient.KeyEncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"RSAES_OAEP_SHA_256"}},
	},
	"GenerateDataKeyPairWithoutPlaintext": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "KeyPairSpec", Kind: modelcheck.KindEnum, Enum: []string{"RSA_4096", "ECC_NIST_P256", "ECC_NIST_P384", "ECC_NIST_P521", "ECC_SECG_P256K1", "ECC_NIST_EDWARDS25519", "RSA_2048", "RSA_3072", "SM2"}},
		{Path: "KeyPairSpec", Kind: modelcheck.KindRequired},
	},
	"GenerateDataKeyWithoutPlaintext": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "KeySpec", Kind: modelcheck.KindEnum, Enum: []string{"AES_256", "AES_128"}},
		{Path: "NumberOfBytes", Kind: modelcheck.KindRange, Min: 1, Max: 1024},
	},
	"GenerateMac": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "MacAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"HMAC_SHA_224", "HMAC_SHA_256", "HMAC_SHA_384", "HMAC_SHA_512"}},
		{Path: "MacAlgorithm", Kind: modelcheck.KindRequired},
		{Path: "Message", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "Message", Kind: modelcheck.KindRequired},
	},
	"GenerateRandom": {
		{Path: "CustomKeyStoreId", Kind: modelcheck.KindLength, Min: 1, Max: 64},
		{Path: "NumberOfBytes", Kind: modelcheck.KindRange, Min: 1, Max: 1024},
		{Path: "Recipient.AttestationDocument", Kind: modelcheck.KindLength, Min: 1, Max: 262144},
		{Path: "Recipient.KeyEncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"RSAES_OAEP_SHA_256"}},
	},
	"GetKeyPolicy": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "PolicyName", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "PolicyName", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[\w]+$`)},
	},
	"GetKeyRotationStatus": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"GetPublicKey": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"ListAliases": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Limit", Kind: modelcheck.KindRange, Min: 1, Max: 1000},
		{Path: "Marker", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"ListKeyPolicies": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Limit", Kind: modelcheck.KindRange, Min: 1, Max: 1000},
		{Path: "Marker", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"ListKeyRotations": {
		{Path: "IncludeKeyMaterial", Kind: modelcheck.KindEnum, Enum: []string{"ALL_KEY_MATERIAL", "ROTATIONS_ONLY"}},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Limit", Kind: modelcheck.KindRange, Min: 1, Max: 1000},
		{Path: "Marker", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"ListKeys": {
		{Path: "Limit", Kind: modelcheck.KindRange, Min: 1, Max: 1000},
		{Path: "Marker", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"ListResourceTags": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Limit", Kind: modelcheck.KindRange, Min: 1, Max: 1000},
		{Path: "Marker", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"PutKeyPolicy": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Policy", Kind: modelcheck.KindLength, Min: 1, Max: 131072},
		{Path: "Policy", Kind: modelcheck.KindRequired},
		{Path: "PolicyName", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "PolicyName", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[\w]+$`)},
	},
	"ReEncrypt": {
		{Path: "CiphertextBlob", Kind: modelcheck.KindLength, Min: 1, Max: 6144},
		{Path: "DestinationEncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"SYMMETRIC_DEFAULT", "RSAES_OAEP_SHA_1", "RSAES_OAEP_SHA_256", "SM2PKE"}},
		{Path: "DestinationKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "DestinationKeyId", Kind: modelcheck.KindRequired},
		{Path: "DryRunModifiers[]", Kind: modelcheck.KindEnum, Enum: []string{"IGNORE_CIPHERTEXT"}},
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "SourceEncryptionAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"SYMMETRIC_DEFAULT", "RSAES_OAEP_SHA_1", "RSAES_OAEP_SHA_256", "SM2PKE"}},
		{Path: "SourceKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	},
	"RotateKeyOnDemand": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"ScheduleKeyDeletion": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "PendingWindowInDays", Kind: modelcheck.KindRange, Min: 1, Max: 365},
	},
	"Sign": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Message", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "Message", Kind: modelcheck.KindRequired},
		{Path: "MessageType", Kind: modelcheck.KindEnum, Enum: []string{"RAW", "DIGEST", "EXTERNAL_MU"}},
		{Path: "SigningAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"ED25519_SHA_512", "RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_512", "RSASSA_PKCS1_V1_5_SHA_384", "ECDSA_SHA_384", "ECDSA_SHA_512", "SM2DSA", "ML_DSA_SHAKE_256", "ED25519_PH_SHA_512", "RSASSA_PSS_SHA_384", "RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PKCS1_V1_5_SHA_512", "ECDSA_SHA_256"}},
		{Path: "SigningAlgorithm", Kind: modelcheck.KindRequired},
	},
	"TagResource": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Tags", Kind: modelcheck.KindRequired},
		{Path: "Tags[].TagKey", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].TagKey", Kind: modelcheck.KindRequired},
		{Path: "Tags[].TagValue", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].TagValue", Kind: modelcheck.KindRequired},
	},
	"UntagResource": {
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "TagKeys", Kind: modelcheck.KindRequired},
		{Path: "TagKeys[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
	},
	"UpdateAlias": {
		{Path: "AliasName", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "AliasName", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[a-zA-Z0-9:/_-]+$`)},
		{Path: "AliasName", Kind: modelcheck.KindRequired},
		{Path: "TargetKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "TargetKeyId", Kind: modelcheck.KindRequired},
	},
	"UpdateKeyDescription": {
		{Path: "Description", Kind: modelcheck.KindLength, Min: 0, Max: 8192},
		{Path: "Description", Kind: modelcheck.KindRequired},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
	},
	"Verify": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Message", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "Message", Kind: modelcheck.KindRequired},
		{Path: "MessageType", Kind: modelcheck.KindEnum, Enum: []string{"EXTERNAL_MU", "RAW", "DIGEST"}},
		{Path: "Signature", Kind: modelcheck.KindLength, Min: 1, Max: 6144},
		{Path: "Signature", Kind: modelcheck.KindRequired},
		{Path: "SigningAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_512", "RSASSA_PKCS1_V1_5_SHA_384", "ECDSA_SHA_384", "ECDSA_SHA_512", "SM2DSA", "ML_DSA_SHAKE_256", "ED25519_PH_SHA_512", "RSASSA_PSS_SHA_384", "RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PKCS1_V1_5_SHA_512", "ECDSA_SHA_256", "ED25519_SHA_512"}},
		{Path: "SigningAlgorithm", Kind: modelcheck.KindRequired},
	},
	"VerifyMac": {
		{Path: "GrantTokens[]", Kind: modelcheck.KindLength, Min: 1, Max: 8192},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "KeyId", Kind: modelcheck.KindRequired},
		{Path: "Mac", Kind: modelcheck.KindLength, Min: 1, Max: 6144},
		{Path: "Mac", Kind: modelcheck.KindRequired},
		{Path: "MacAlgorithm", Kind: modelcheck.KindEnum, Enum: []string{"HMAC_SHA_224", "HMAC_SHA_256", "HMAC_SHA_384", "HMAC_SHA_512"}},
		{Path: "MacAlgorithm", Kind: modelcheck.KindRequired},
		{Path: "Message", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "Message", Kind: modelcheck.KindRequired},
	},
}
