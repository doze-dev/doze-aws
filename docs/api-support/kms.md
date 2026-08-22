# KMS — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

All three key families carry real standard-library crypto: symmetric keys are
AES-256-GCM with the encryption context as authenticated data; RSA/ECC keys
really sign, verify, and (RSA) encrypt; HMAC keys really MAC. GetPublicKey
returns genuine SPKI DER — signatures verify outside KMS.

| Operation | Tier | Notes |
|---|---|---|
| CreateKey | F | SYMMETRIC_DEFAULT, RSA_2048/3072/4096, ECC_NIST_P256/P384/P521, HMAC_224/256/384/512. ECC_SECG_P256K1 → honest error (no stdlib secp256k1) |
| DescribeKey / ListKeys | F | by id, ARN, alias, alias ARN |
| Encrypt / Decrypt | F | SYMMETRIC_DEFAULT (blob embeds key id; Decrypt needs no KeyId) and RSAES_OAEP_SHA_1/SHA_256 (KeyId required, like real KMS) |
| ReEncrypt | F | |
| GenerateDataKey(WithoutPlaintext) | F | AES_128/AES_256/NumberOfBytes |
| GenerateDataKeyPair(WithoutPlaintext) | F | RSA/ECC pairs, private key wrapped by the symmetric key |
| GenerateRandom | F | |
| Sign / Verify | F | RSASSA_PKCS1_V1_5_SHA_256/384/512, RSASSA_PSS_SHA_256/384/512, ECDSA_SHA_256/384/512; RAW and DIGEST message types |
| GetPublicKey | F | real SPKI DER |
| GenerateMac / VerifyMac | F | HMAC_SHA_224/256/384/512, constant-time compare |
| EnableKey / DisableKey | F | DisabledException on use |
| ScheduleKeyDeletion / CancelKeyDeletion | F | 7–30 day window, janitor finalizes; cancelled keys land Disabled |
| CreateAlias / UpdateAlias / DeleteAlias / ListAliases | F | |
| TagResource / UntagResource / ListResourceTags | F | |
| UpdateKeyDescription | F | |
| GetKeyPolicy / PutKeyPolicy / ListKeyPolicies | C | stored and returned; not evaluated (no IAM locally) |
| EnableKeyRotation / DisableKeyRotation / GetKeyRotationStatus | C | flag stored; actual rotation mechanics arrive in Phase 8 |
| RotateKeyOnDemand / ListKeyRotations | S | Phase 8 |
| Grants (Create/Retire/Revoke/List) | S | grants are IAM machinery |
| Custom key stores, ImportKeyMaterial, multi-region replication, DeriveSharedSecret | S | cloud-infrastructure-only |

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what KMS refuses**.

**263/263 model-derived constraints enforced across all 36 dispatched
operations, with `knownGaps` empty.** Removing the table makes the audit fail,
so it is doing work the hand-written checks were not.

Generated with `dzaudit cases kms`, committed to `testdata/cases_kms.json`, and
replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts. 209 further cases fall on operations with no
handler — custom key stores, multi-region replication, imported key material —
which cannot be audited at all.

### The fixtures are real cryptographic material

KMS needs more setup than any other service here, and none of it can be faked.
`Decrypt` needs ciphertext this key actually produced, `Verify` needs a
signature over the message it is given, `VerifyMac` needs a real MAC. An
invented blob is refused as invalid — a refusal for the wrong reason, which
reads exactly like a pass. So the fixture creates three keys (symmetric,
RSA_2048 for sign/verify, HMAC_256 for MACs) and then performs the operations
whose *output* the later baselines consume.

Three operations also get a throwaway key rather than the fixture's:
`ScheduleKeyDeletion`, `CancelKeyDeletion` and `DisableKey` would otherwise
disable the key every cryptographic baseline after them alphabetically depends
on.
