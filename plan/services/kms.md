# KMS

Stage 3. Size M. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds KMS's own.

## 1. Summary

Keys with real standard-library cryptography over one bbolt store (`kms.bolt`),
on one wire protocol (JSON 1.1, target prefix `TrentService`). Symmetric keys
are AES-256-GCM with the encryption context as authenticated data; RSA and ECC
keys sign, verify and (RSA) encrypt; HMAC keys MAC. A janitor finalises
scheduled deletions once a minute.

KMS calls no sibling. Its peers call it: Kinesis checks a stream's key exists
and is usable (`kinesis/kms_test.go`), `internal/provision/svc_kms.go` and
CloudFormation create keys by alias. Secrets Manager, Parameter Store, SQS, SNS
and S3 record a `KmsKeyId` and do not call KMS at all; that is their cosmetic
row, not KMS's.

KMS is the one service whose resource policy gates the identity policies, so
its guard (`kms/guard.go`, `iamguard.Guard.KeyPolicyGates`) is part of this
stage even though IAM is stage 15.

**Estimate:** M, three to four focused days. The grounding below found more
than the plan assumed (three P0s in the service, one of them in shared
validation), so treat M as the floor. **Actual:** _fill in when done._

## 2. Contract

The model (`kms/testdata/ops_kms.json`, checked by
`TestEveryModelOperationIsAccountedFor`) lists **54 operations**. The dispatch
table in `kms/actions.go` holds 36 handlers and 18 stubs; 17 of the stubs are
model operations, one (`VerifyMacForImport`) is no longer in the model, and one
model operation (`GetKeyLastUsage`) is not dispatched at all.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateKey | F | F | Per-setting tiers below. Stores any `Policy` string (KMS-3). Ignores `Origin`, `MultiRegion`, `BypassPolicyLockoutSafetyCheck` (KMS-16). |
| DescribeKey | F | F | `AWSAccountId` is a literal (KMS-1). |
| ListKeys | F | F | Ignores `Limit` and `Marker` (KMS-7). |
| EnableKey, DisableKey | F | F | |
| ScheduleKeyDeletion, CancelKeyDeletion | F | F | Scheduling an already-pending key succeeds (KMS-8). |
| Encrypt, Decrypt, ReEncrypt | F | F | Blob length counted in base64 (KMS-2). `KeyId` on a symmetric Decrypt is not compared with the ciphertext's key (KMS-10). `DryRun` ignored (KMS-17). |
| GenerateDataKey, GenerateDataKeyWithoutPlaintext | F | F | Both `KeySpec` and `NumberOfBytes`, or neither, is accepted (KMS-11). |
| GenerateDataKeyPair, GenerateDataKeyPairWithoutPlaintext | F | F | Per-spec tiers below. |
| GenerateRandom | F | F | |
| Sign, Verify | F | F | Per-algorithm tiers below. |
| GetPublicKey | F | F | |
| GenerateMac, VerifyMac | F | F | |
| CreateAlias, UpdateAlias, DeleteAlias | F | F | UpdateAlias moves an alias across key types (KMS-9). |
| ListAliases | F | F | Ignores `Limit` and `Marker` (KMS-7). |
| TagResource, UntagResource, ListResourceTags | F | F | No `aws:` rule and no count limit (KMS-12). List ignores paging (KMS-7). |
| UpdateKeyDescription | F | F | Succeeds on a pending key (KMS-8). |
| GetKeyPolicy, PutKeyPolicy, ListKeyPolicies | F | F | Only JSON validity is checked (KMS-3). No lockout safety check (KMS-4). `PolicyName` is not checked (KMS-18). |
| EnableKeyRotation, DisableKeyRotation, GetKeyRotationStatus | F | F, with the schedule C | The flag and period are stored and reported; nothing runs on the schedule. The ledger says so; the console says "yearly" (KMS-31). |
| RotateKeyOnDemand, ListKeyRotations | F | F | No per-key limit (KMS-13). |
| GetKeyLastUsage | absent: answers `InvalidAction` | F or S, to decide | KMS-14. Nothing may be absent. |
| CreateGrant, RetireGrant, RevokeGrant, ListGrants, ListRetirableGrants | S | S | `UnsupportedOperationException`. In `not-built.md` ("grants are IAM machinery"). |
| CreateCustomKeyStore, ConnectCustomKeyStore, DisconnectCustomKeyStore, DeleteCustomKeyStore, DescribeCustomKeyStores, UpdateCustomKeyStore | S | S | In `not-built.md`. |
| ImportKeyMaterial, DeleteImportedKeyMaterial, GetParametersForImport | S | S | In `not-built.md`. |
| ReplicateKey, UpdatePrimaryRegion | S | S | In `not-built.md`. |
| DeriveSharedSecret | S | S | In `not-built.md`. |
| VerifyMacForImport | stub for an operation not in the model | remove, or keep and say why in the ledger | KMS-34. |

That is 36 F, 17 S, 1 absent, 54 in all.

"CreateKey is F" and "Sign is F" hide which values do anything, so the settings
get their own tiers. "Now" is what the binary answered on 2026-10-10 unless
marked read.

**Key spec (`KeySpec`, 17 values in the model)**

| Value | Now | Target | To confirm |
|---|---|---|---|
| SYMMETRIC_DEFAULT | F | F | |
| RSA_2048, RSA_3072, RSA_4096 | F | F | Usage defaults to ENCRYPT_DECRYPT in the service and to SIGN_VERIFY in `provision` (KMS-6). |
| ECC_NIST_P256, ECC_NIST_P384, ECC_NIST_P521 | F | F | |
| HMAC_224, HMAC_256, HMAC_384, HMAC_512 | F | F | |
| ECC_SECG_P256K1 | S, `UnsupportedOperationException` | S | Listed in the ledger. |
| ECC_NIST_EDWARDS25519 | refused as `ValidationException` "unsupported KeySpec" | S with the stub code, or F (Go has ed25519) | KMS-15. Not in the ledger. |
| ML_DSA_44, ML_DSA_65, ML_DSA_87 | same | S with the stub code | KMS-15. Not in the ledger. |
| SM2 | same | S with the stub code | KMS-15. AWS serves it in China regions only (memory). |

**Key usage (`KeyUsage`, 4 values)**

| Value | Now | Target |
|---|---|---|
| ENCRYPT_DECRYPT | F for SYMMETRIC_DEFAULT and RSA | F |
| SIGN_VERIFY | F for RSA and ECC_NIST | F |
| GENERATE_VERIFY_MAC | F for HMAC | F |
| KEY_AGREEMENT | refused as `ValidationException` "not valid for KeySpec" | S with the stub code, with DeriveSharedSecret (KMS-15) |

**Algorithms**

| Setting | Values that work | Values in the model that do not | Now for those |
|---|---|---|---|
| `EncryptionAlgorithm` | SYMMETRIC_DEFAULT, RSAES_OAEP_SHA_1, RSAES_OAEP_SHA_256 | SM2PKE | `ValidationException` "unsupported EncryptionAlgorithm" (read) |
| `SigningAlgorithm` | RSASSA_PSS_SHA_256/384/512, RSASSA_PKCS1_V1_5_SHA_256/384/512, ECDSA_SHA_256/384/512 | SM2DSA, ML_DSA_SHAKE_256, ED25519_SHA_512, ED25519_PH_SHA_512 | `ValidationException` "not valid for key spec" (read) |
| `MacAlgorithm` | HMAC_SHA_224/256/384/512, one per key spec | none | |
| `MessageType` | RAW, DIGEST | EXTERNAL_MU | `ValidationException` "unsupported MessageType" (read) |
| Data key `KeySpec` | AES_256, AES_128 | none | |
| `KeyPairSpec` | RSA_2048/3072/4096, ECC_NIST_P256/P384/P521 | ECC_SECG_P256K1, ECC_NIST_EDWARDS25519, SM2 | not established: P256K1 should answer the stub code, the other two "unsupported KeySpec" (read, not run) |

Whether an ECDSA algorithm may be used with a curve other than its own
(ECDSA_SHA_256 on P384) is refused here; what AWS does is not established.

**Other request settings**

| Setting | Now | Target | To confirm |
|---|---|---|---|
| `EncryptionContext` | F: bound as authenticated data, on Encrypt, Decrypt, ReEncrypt, data keys and key pairs | F | |
| `Policy` on CreateKey and PutKeyPolicy | Stored as given; evaluated under soft and enforce; ignored when it does not parse | F, validated when stored | KMS-3 |
| `BypassPolicyLockoutSafetyCheck` | Ignored; every lockout is accepted | F | KMS-4 |
| `Origin` | Ignored; EXTERNAL returns an enabled AWS_KMS key | AWS_KMS F, others S | KMS-16 |
| `MultiRegion` | Accepted and reported back; the key id has no `mrk-` prefix | S or C, to decide | KMS-16. Not established whether anything else changes. |
| `CustomKeyStoreId`, `XksKeyId` | Length and pattern checked, then ignored (read) | S | KMS-16 |
| `RotationPeriodInDays` | Range 90 to 2560 enforced; stored and reported | C, says so | |
| `PendingWindowInDays` | 7 to 30 enforced by the handler; default 30 | F | |
| `GrantTokens` | Length checked, ignored | C | Grants are S, so a token can do nothing. |
| `DryRun` | Ignored: the operation runs | F (`DryRunOperationException`) or refused | KMS-17 |
| `DryRunModifiers`, `Recipient` (attestation) | Enum and length checked, then ignored (read) | not established | Set a tier. |
| `PolicyName` | Pattern checked; any name returns the one policy | F: only `default` exists | KMS-18 |
| `Limit`, `Marker` | Range checked, ignored | F | KMS-7 |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `kms/`: 17 tests and one fuzz target. SDK v2 (8, `sdk_test.go`), SDK v1 (2), admin and data-key pairs (3, `sdk_coverage_test.go`), rotation period (1), managed alias namespace (1), dispatch coverage (1), rejection parity (1), `FuzzOpenBlob`. There is no store-level or handler-level unit test: everything goes through an SDK. |
| Parity | `rejection_parity_test.go`, driven by `testdata/cases_kms.json`; `coverage_model_test.go`. The ledger says 263 of 263 model constraints enforced across the 36 handlers, `knownGaps` empty, and 209 cases on undispatched or stubbed operations unaudited. The 263 include every blob length, which is enforced against the wrong quantity (KMS-2). |
| Boundary | `kinesis/kms_test.go`: 4 tests of Kinesis against a key's existence and state. |
| Conformance | `conformance/tests/test_kms.py`: 5 scenarios (symmetric round trip, aliases, key states, asymmetric sign and HMAC, refusals). All **unverified**: `conformance/snapshots/` is empty. |
| Browser | `e2e/tests/kms.spec.ts`: 9 tests. `e2e/route-gaps.txt` has no KMS entry. |
| Console (Go) | KMS cases inside `internal/console/console_test.go` and the mutation-route sweep; no KMS-only file. |
| Provision | `internal/provision/svc_kms.go` (apply and export), `internal/cfn/emit_kms.go`. Tests not counted here: not established how many touch KMS. |
| Restart | The upgrade fixture `testdata/datadir-1.0.0/us-east-1/kms/kms.bolt` exists. A KMS write-restart-read test does not. |
| Mutation | No per-package baseline (foundation B3). |

Unlike SQS, KMS's model carries its ranges, lengths, patterns and enums, so
most constraints are `model`. What the model does not carry is state: which
operations a Disabled or PendingDeletion key refuses, alias rules across key
types, the policy lockout check, limits. Those are `doc` or `memory` and fill
the recording queue.

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree on
2026-10-10, over the API with curl or through the console routes.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| KMS-1 | P0 | The default key policy and `KeyMetadata.AWSAccountId` are written with the literal `000000000000`. Under `--account-id 111122223333 --iam-mode enforce` every new key is locked at birth: CreateKey succeeds, then Encrypt, DescribeKey and GetKeyPolicy answer `AccessDeniedException` for the root, and the console's key page is a 400. Under soft every call is logged as a would-deny. | `kms/actions_policy.go` `defaultKeyPolicy`; `kms/actions_keys.go` `metadataFor` | run | n/a (ours); AWS's default policy names the key's own account (doc) | SDK test under a non-default identity and enforce: create, encrypt, describe. Same class as foundation A8, which does not list KMS: add it there. |
| KMS-2 | P0 | Length constraints on blob members are applied to the base64 text, not the bytes. Encrypt refuses a plaintext of 3 073 to 4 096 bytes with "Member must have length less than or equal to 4096". The same check covers Sign, Verify, GenerateMac and VerifyMac `Message` (4 096), and `CiphertextBlob`, `Signature`, `Mac` (6 144). | `internal/modelcheck/modelcheck.go` `KindLength` / `lengthOf`; `kms/validate.go` | run for Encrypt (3 072 accepted, 3 073 refused); read for the others | model: a length on a blob shape counts bytes | Parity case per blob member at the byte limit and one over. The fix is in shared code: check every service whose tables put a length on a blob (foundation candidate). |
| KMS-3 | P0 | A key policy is stored without being parsed. `PutKeyPolicy` accepts `{"hello":1}` and returns it from GetKeyPolicy; `CreateKey` accepts `Policy: "not json"`. `guardRequest` drops a document that fails `iampolicy.Parse`, so under enforce such a key denies everyone, and under off nothing ever notices. | `kms/actions_policy.go` `putKeyPolicy`; `kms/actions_keys.go` `createKey`; `kms/guard.go` `guardRequest` | run (store and read back); read (the guard) | model has `MalformedPolicyDocumentException` on both operations; which documents raise it is doc | Handler tests: not JSON on CreateKey; JSON that is not a policy; a statement with no `Effect`; a statement with no `Principal`. |
| KMS-4 | P1 | `PutKeyPolicy` and `CreateKey` accept a policy that locks the caller out, with or without `BypassPolicyLockoutSafetyCheck`. AWS refuses unless the flag is set. The ledger presents the lockout as deliberate; the safety check that normally prevents it is missing. | `kms/actions_policy.go` `putKeyPolicy` | run | doc (PutKeyPolicy, `BypassPolicyLockoutSafetyCheck`) | SDK test under enforce: a policy without the caller is refused, and accepted with the flag. |
| KMS-5 | P2 | The console creates the key, then the alias. A refused alias (in use, bad characters, `alias/aws/…`) shows a refusal on the form while the key already exists with no alias. Three refused submits leave three orphan keys. | `internal/console/client_sec.go` `CreateKey` | run | n/a | Console handler test: create with an alias that exists, then count keys. |
| KMS-6 | P0 | `provision` gives an RSA key with no stated usage `SIGN_VERIFY`. The service itself, given the same spec and no usage, gives `ENCRYPT_DECRYPT`. A template that relies on the default gets a different key through CloudFormation than through the API. | `internal/provision/svc_kms.go`, the `KeyUsage` default at about line 41 | read | memory: `AWS::KMS::Key` documents `KeyUsage` default `ENCRYPT_DECRYPT`. Confirm before fixing. | Provision test: `KeySpec: RSA_2048`, no usage, then DescribeKey. |
| KMS-7 | P1 | `ListKeys`, `ListAliases`, `ListResourceTags`, `ListKeyPolicies` and `ListKeyRotations` ignore `Limit` and `Marker` and always answer `Truncated: false`. | `kms/actions_keys.go` `listKeys`, `listKeyRotations`; `kms/actions_aliases.go` `listAliases`, `listResourceTags`; `kms/actions_policy.go` | run for ListKeys and ListAliases with `Limit: 1`; read for the rest | model (the members exist); doc for the default page size | SDK test: `Limit: 1` over three keys, follow `NextMarker` to the end, for each list. |
| KMS-8 | P1 | A key pending deletion accepts what AWS refuses for that state: ScheduleKeyDeletion again (the date is silently reset), EnableKeyRotation, CreateAlias, UpdateKeyDescription. TagResource, PutKeyPolicy, UpdateAlias and DisableKeyRotation have no state check either. | `kms/actions_keys.go` `scheduleKeyDeletion`, `setRotation`, `updateKeyDescription`; `kms/store.go` `SetAlias` | run for the first four; read for the rest | doc: the "Key states of AWS KMS keys" table. Which rows apply is from memory. | One table test: every operation against Enabled, Disabled and PendingDeletion, asserting the code. Rows whose AWS answer is memory are written and skipped pending the recording. |
| KMS-9 | P1 | `UpdateAlias` moves an alias from a symmetric key to an RSA signing key. | `kms/store.go` `SetAlias` | run | memory of doc: UpdateAlias requires the same key type and usage | SDK test; skipped until the claim is `doc`. |
| KMS-10 | P1 | A symmetric `Decrypt` or `ReEncrypt` that names a `KeyId` other than the key the ciphertext was sealed under succeeds. The `KeyId` is used for the policy check only. | `kms/actions_crypto.go` `decrypt`; `kms/guard.go` `decryptKeyIdent` | run (decrypted naming an HMAC key) | model has `IncorrectKeyException`; when it is raised is doc | SDK test: encrypt under A, decrypt naming B. |
| KMS-11 | P1 | `GenerateDataKey` with both `KeySpec` and `NumberOfBytes`, or with neither, succeeds. The "specify KeySpec or NumberOfBytes" branch cannot be reached because an empty `KeySpec` is treated as AES_256. | `kms/actions_crypto.go` `dataKey` | run | memory: AWS requires exactly one | Handler test for both and for neither; skipped until `doc`. |
| KMS-12 | P1 | Tags: a key beginning `aws:` is accepted, and 51 tags in one call are accepted. | `kms/actions_aliases.go` `tagResource` | run | memory: reserved prefix raises `TagException`; 50 per key | Handler tests for both; skipped until `doc`. |
| KMS-13 | P1 | `RotateKeyOnDemand` has no limit: twelve in a row all succeed. `ListKeyRotations` entries carry `KeyId`, `RotationDate`, `RotationType` only. | `kms/actions_keys.go` `rotateKeyOnDemand`, `listKeyRotations` | run | memory: 10 on-demand rotations per key, `LimitExceededException`; the full entry shape is model | SDK test for the eleventh; shape test against the model. |
| KMS-14 | P1 | `GetKeyLastUsage` is in the model and answers `InvalidAction`, which no SDK expects from KMS. The coverage test allows it as "Unreached". | `kms/actions.go` `handlers`; `kms/coverage_model_test.go` | run | model | Coverage test with `Unreached` empty. |
| KMS-15 | P1 | Unsupported choices are refused with three different codes. `ECC_SECG_P256K1` answers `UnsupportedOperationException`. `ML_DSA_*`, `SM2`, `ECC_NIST_EDWARDS25519` answer `ValidationException` "unsupported KeySpec", which reads as the caller's mistake for a spec AWS accepts. `KEY_AGREEMENT` answers "not valid for KeySpec". None but P256K1 is in the ledger. | `kms/crypto.go` `validateSpecUsage` | run | model (the enum values exist) | Table test: every enum value of every spec, usage and algorithm either works or answers the stub code and has a ledger row. |
| KMS-16 | P0 | `CreateKey` with `Origin: EXTERNAL` returns an enabled key with generated material and `Origin: AWS_KMS`. On AWS that key is `PendingImport` and cannot encrypt. `MultiRegion: true` is echoed back but the key id is an ordinary UUID. `CustomKeyStoreId` and `XksKeyId` are ignored. The import, replica and custom-store operations are honest stubs; the create-time switches for the same features are silent. | `kms/actions_keys.go` `createKey`; `kms/store.go` `CreateKey` | run for `Origin` and `MultiRegion`; read for the other two | doc | Handler test per switch: refused with the stub code. |
| KMS-17 | P1 | `DryRun: true` is ignored: Encrypt returns ciphertext. | `kms/kms.go` `ServeHTTP` (no handling anywhere) | run | model has `DryRunOperationException` | Handler test on Encrypt and ScheduleKeyDeletion. Decide F or refuse. |
| KMS-18 | P1 | `GetKeyPolicy` with `PolicyName: "other"` returns the default policy. | `kms/actions_policy.go` `getKeyPolicy` | run | memory: only `default` exists, anything else is `NotFoundException` | Handler test; skipped until `doc`. |
| KMS-19 | P1 | `GetKeyRotationStatus` on an RSA key answers `KeyRotationEnabled: false`. Whether AWS answers that or refuses is not established. | `kms/actions_keys.go` `getKeyRotationStatus` | run | memory | Recording first. |
| KMS-20 | P2 | The detail header never shows the alias. `DescribeKey` in the console client does not set `Key.Alias`; only `ListKeys` does, and the header reads `.Key.Alias`. | `internal/console/client_sec.go` `DescribeKey`; `templates/kms.html` `kms_key_detail` | run | n/a | Console test: a key with an alias, assert the heading. |
| KMS-21 | P2 | After Cancel deletion the toast says "the key is usable again". The key lands Disabled and refuses every use. The pending banner says "until you cancel", which is one step short. | `internal/console/handlers_sec.go` `kmsCancelDeletion`; `templates/kms.html` | run | doc (the ledger row agrees) | Browser test reads the toast and the state pill. See also foundation A12. |
| KMS-22 | P2 | The page chooses its controls by usage, not spec. An RSA key with ENCRYPT_DECRYPT gets Rotate now and the rotation switch, both always refused. Its Encrypt sends no algorithm and always fails ("SYMMETRIC_DEFAULT is not valid for key spec RSA_2048"); Decrypt sends no `KeyId`; Re-encrypt to it fails the same way. The console cannot use an RSA encryption key at all. | `templates/kms.html` (the `eq .Key.Usage "ENCRYPT_DECRYPT"` tests); `client_sec.go` `KMSEncrypt`, `KMSDecrypt` | run | n/a | Browser test: create RSA_2048 with the default usage, encrypt, decrypt. |
| KMS-23 | P2 | Verify and Verify MAC report every error as "does NOT verify". With the key disabled after signing, the verdict is the same as for a forged signature. | `handlers_sec.go` `kmsVerify`, `kmsVerifyMac` | run | n/a | Handler test: disable, verify, expect the refusal. |
| KMS-24 | P2 | Saving a policy that excludes the root under enforce: the toast says "Key policy saved", the page's own DescribeKey is denied inline, and from then on the key is missing from the list (the list skips any key whose DescribeKey fails) and its count. Nothing says a key is hidden. | `handlers_sec.go` `kmsSavePolicy`, `kmsKeyPartial`; `client_sec.go` `ListKeys` (the `continue`) | run | n/a | Console test under enforce: save, then assert the list still shows a row for the key, marked unreadable. |
| KMS-25 | P2 | Re-encrypt's destination list holds every key: the source itself, signing keys, HMAC keys, disabled and pending keys. Most choices can only be refused. | `templates/kms.html`, the `dest` select | run | n/a | Browser test asserts the options. |
| KMS-26 | P2 | The console sends no encryption context, so ciphertext an application sealed with one cannot be decrypted from the console ("decryption failed (wrong key or encryption context)"), and the console cannot produce ciphertext the application expects. | `client_sec.go` `KMSEncrypt`, `KMSDecrypt` | run | n/a | Browser test: encrypt over the API with a context, decrypt in the console with it. |
| KMS-27 | P2 | Schedule deletion always sends 7 days and then lands on the list's first key, not the scheduled one. The pending page shows no date, though DescribeKey returns `DeletionDate`. The rotation switch sends no period. | `client_sec.go` `ScheduleKeyDeletion`, `DescribeKey`; `handlers_sec.go` `kmsScheduleDeletion` | run | n/a | Browser test for the landing page and the date. |
| KMS-28 | P2 | The list shows one alias per key (the last in name order) and no state, so a disabled or pending key looks like any other. | `client_sec.go` `ListKeys`; `templates/kms.html` `kms_lp` | run | n/a | Console test with a disabled key. |
| KMS-29 | P3 | "Automatic rotation: New backing material yearly". Nothing rotates on a schedule here. | `templates/kms.html` | read | n/a (the ledger says the opposite) | Covered by the common "no console text contradicts the ledger" box. |
| KMS-30 | P3 | The create page says "RSA/ECC sign/verify". An RSA key created there with "Default for spec" encrypts. | `templates/create.html` `kms_create` | read | n/a | Same box. |
| KMS-31 | P3 | Random bytes outside 1 to 1024 are silently replaced by 32. | `handlers_sec.go` `kmsRandom` | run | n/a | Handler test: 0 is refused with the service's message. |
| KMS-32 | P3 | The create form has no CLI preview; the Encrypt form is the only KMS form with one. | `templates/create.html` `kms_create` | read | n/a | Browser test once built (foundation A11 gives the rule). |
| KMS-33 | P3 | The store's comment calls `Policy` "round-trip only"; the guard evaluates it. | `kms/store.go`, the `key` struct | read | n/a | none |
| KMS-34 | P3 | Ledger: `VerifyMacForImport` is stubbed and is not an AWS operation; `GetKeyLastUsage` is missing; the unsupported specs and algorithms other than P256K1 are missing; "Stored and returned only under the default `off`" (the default is `soft`; the foundation's acceptance criteria own that sentence). | `docs/SUPPORT.md`, `kms/actions.go` | read | n/a | The ledger tests after the contract table above is checked in. |

Owned by the foundation, seen here: the reserved-alias refusal
(`NotAuthorizedException`) is shown as Denied with "a policy decided this", and
the "because AWS does" line sits under hand-derived messages (A2); the colours
(A3); tags are written one call per tag though KMS takes a batch (A5); a refusal
from Rotate now in the header has no inline slot (A6, read); the "Select a key"
line is unreachable because `/kms` always opens the first key (A7); hard-coded
identity (A8, extended by KMS-1).

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once, through
SDK v2 unless it says otherwise.

**Create and describe**
- [ ] Every `KeySpec` and `KeyUsage` pair: works, or is refused with one code, per the tables in section 2.
- [ ] The default usage per spec, through the API and through `provision`, is the same and is AWS's.
- [ ] `Description` 0 to 8192; `Tags` at create; `Policy` at create is validated like PutKeyPolicy.
- [ ] `Origin`, `MultiRegion`, `CustomKeyStoreId`, `XksKeyId`: each is honoured or refused, never ignored.
- [ ] `KeyMetadata`: every member the model lists is present or deliberately absent; `AWSAccountId` and `Arn` follow the instance's identity; `DeletionDate` only while pending; `EncryptionAlgorithms`, `SigningAlgorithms`, `MacAlgorithms` per usage.
- [ ] A key is found by id, key ARN, alias name and alias ARN on every operation the ledger says accepts them. Establish which operations AWS refuses an alias for (ScheduleKeyDeletion, PutKeyPolicy and others take id or ARN only: memory).
- [ ] An ARN naming another region or account is refused, not resolved by its suffix. Not established today: `resolve` cuts at `:key/`.
- [ ] `alias/aws/<service>` materialises a key on first use. Its `KeyManager` is `CUSTOMER` today; AWS's managed keys say `AWS` (memory). Establish whether such a key can be disabled, scheduled or re-policied here.

**States**
- [ ] The state table: every dispatched operation against Enabled, Disabled and PendingDeletion, with the exact code.
- [ ] Disable then Enable keeps material: ciphertext from before decrypts after.
- [ ] Schedule: 7 to 30, default 30, the response's `DeletionDate` and `PendingWindowInDays`; scheduling twice.
- [ ] Cancel: lands Disabled; cancelling a key that is not pending.
- [ ] The janitor: at the deletion time the key and every alias pointing at it are gone; a Decrypt of its ciphertext answers `InvalidCiphertextException`; a restart during the window keeps the date. Uses `Options.Clock`.

**Symmetric encryption**
- [ ] Plaintext of exactly 1, 4096 and 4097 bytes.
- [ ] Encryption context: same pairs in another order decrypts; a missing, extra or changed pair is refused; an empty context equals none.
- [ ] Decrypt without `KeyId`; with the right `KeyId`; with the wrong one; with an alias.
- [ ] A truncated, extended or bit-flipped blob is refused (`FuzzOpenBlob` exists; keep it and add fixed cases).
- [ ] ReEncrypt: source and destination contexts; destination disabled, of another usage, missing; `ReEncryptFrom` and `ReEncryptTo` are the actions checked.
- [ ] Ciphertext made before one, two and many rotations still decrypts; ciphertext made after uses the new material.

**RSA encryption**
- [ ] RSAES_OAEP_SHA_1 and SHA_256 round trip on each RSA size; the largest plaintext each accepts and one byte more; Decrypt requires `KeyId` and the algorithm.

**Data keys**
- [ ] AES_128, AES_256, `NumberOfBytes` 1 and 1024; both; neither.
- [ ] The ciphertext of a data key decrypts to the plaintext returned; `WithoutPlaintext` returns none.
- [ ] Key pairs: each `KeyPairSpec` works or is refused with the stub code; the public key is SPKI DER and matches the private key; the wrapped private key decrypts.
- [ ] A data key or key pair requested from a non-symmetric key answers the code AWS uses (not established; `ValidationException` today).

**Sign, verify, MAC**
- [ ] Every signing algorithm on every key spec it belongs to, RAW and DIGEST; a DIGEST of the wrong length; an algorithm that does not belong to the spec.
- [ ] A signature made here verifies with the exported public key outside KMS (Go `crypto` in the test), and one made outside verifies here.
- [ ] A wrong signature is `KMSInvalidSignatureException`, not `SignatureValid: false`.
- [ ] Each HMAC spec with its one algorithm; a wrong algorithm; a wrong MAC is `KMSInvalidMacException`.
- [ ] `GetPublicKey` on a symmetric and an HMAC key: establish the code AWS uses (`ValidationException` "has no public key" today).
- [ ] Message of exactly 4096 and 4097 bytes on Sign and GenerateMac.

**Aliases**
- [ ] Name: `alias/` prefix required, the pattern, 256 in all; `alias/aws/` cannot be created, repointed or deleted.
- [ ] Create on a name in use; update and delete on a name that is not.
- [ ] UpdateAlias across key types (KMS-9).
- [ ] ListAliases with and without `KeyId`, paged.

**Policy**
- [ ] Every malformed document in KMS-3 is refused at CreateKey and PutKeyPolicy.
- [ ] The lockout check and its bypass flag (KMS-4).
- [ ] Under soft: a would-deny is logged and nothing is refused. Under enforce: an explicit Deny refuses the root for exactly the actions it names; a policy without the root closes the gate; with IAM off the policy is stored and returned.
- [ ] An identity policy grants only while the key policy admits the account root.
- [ ] The action checked for each operation, including Decrypt by ciphertext, ReEncrypt's two, and the alias operations.
- [ ] `PolicyName` other than `default`.

**Rotation**
- [ ] Enable, disable, status, the period's range and default, on symmetric keys; the code on asymmetric and HMAC keys for each of the four operations.
- [ ] On-demand rotation: the list entry's full shape, the per-key limit, a disabled key.
- [ ] The ledger and the console both say the schedule is not run.

**Tags and lists**
- [ ] Key 1 to 128, value 0 to 256, the reserved prefix, the per-key count, a duplicate key in one call.
- [ ] Every list operation pages with `Limit` and `Marker`.

**Stubs**
- [ ] All 17 stubbed model operations answer `UnsupportedOperationException` and appear in `not-built.md`. Establish that this is the code an SDK expects for each (the model lists `UnsupportedOperationException` on some of them only).
- [ ] `GetKeyLastUsage` and `VerifyMacForImport` are decided.

**Lifecycle**
- [ ] Everything above survives a restart: material, old material, rotations, state, deletion date, policy, tags, aliases.
- [ ] The service's tests pass under a non-default account and region, with enforce.

## 6. Console

The design's "KMS" page. 20 boards. Templates: `templates/kms.html`
(`kms_lp`, `kms_home`, `kms_key`, `kms_key_detail`, `kms_sign_result`,
`kms_mac_result`, `kms_verdict`, `kms_crypto_result`, `kms_out`), the
`kms_create` block in `create.html`, shared pieces in `panes.html`
(`fail_inline`, `tags_panel`, `tag_editor`, `cli`, `info_panel`), `layout.html`
(`empty_detail`, the confirm dialog) and `iam.html` (`policy_builder`).
Handlers: the `kms*` functions in `handlers_sec.go`, `tagsView` and `tagsSave`
in `handlers_tags.go`, `svcInfo` in `info.go`; routes in `routes_config.go`
(`kmsRoutes`); the client in `client_sec.go`.

Today's page has no tabs: one scroll of stacked sections in two columns. The
design groups the same sections under five tabs (Operations, Configuration,
Aliases, Key policy, Tags). That grouping is **new** and needs either tab
routes or in-page tabs.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Kms-01-None | No keys yet | `kms_home`, `empty_detail`, `lp_empty` | exists |
| Kms-Home | `/kms` with keys: the first key, Operations | `kmsKeys` to `kmsKey`, `kms_key_detail` | exists; alias title, row state and tabs are **new** |
| Kms-02-Create | Create, symmetric | `kms_create`, `kmsCreateKey` | form exists; teaching lines, the "what this key will be" strip, the usage table and the cli are **new** |
| Kms-03-Create-Signing | Create, RSA_2048 SIGN_VERIFY | `kms_create` | same |
| Kms-04-Create-Refused | Create refused | `fail_inline`, appended to the form | exists |
| Kms-05-Encrypt | Encrypt result, Decrypt this, Re-encrypt result | `kmsEncrypt`, `kmsDecrypt`, `kmsReEncrypt`; `kms_crypto_result`, `kms_out` | exists |
| Kms-06-Refused | Decrypt and Re-encrypt refused | `fail_inline` | exists; the encryption-context field is **new** |
| Kms-07-Sign-Verify | Sign, one-click verify, public key | `kmsSign`, `kmsVerify`, `kmsPublicKey`; `kms_sign_result`, `kms_verdict`, `kms_out` | exists |
| Kms-08-Mac-Random | MAC, verify MAC, random bytes | `kmsMac`, `kmsVerifyMac`, `kmsRandom` | exists |
| Kms-09-Config | Enabled, rotation, details, description | `kms_key_detail` (Key settings, Details, Description); `kmsToggleEnabled`, `kmsToggleRotation`, `kmsRotateNow`, `kmsDescription` | exists; the period field and rotation list are **new** |
| Kms-10-Aliases | Aliases: list, add, repoint, refusals | `kms_key_detail` (Aliases); `kmsAddAlias`, `kmsUpdateAlias`, `kmsDeleteAlias` | exists |
| Kms-11-Key-Policy | The policy builder | `policy_builder`, `kmsSavePolicy` | exists; the numbered explanation is **new** |
| Kms-12-Tags | Tags as a draft | `tag_editor`, `tagsSave` | exists (foundation A5) |
| Kms-13-Confirms | Schedule deletion, remove alias, disable | `hx-confirm` on two; the Enabled switch | two exist; the Disable confirm, the waiting-period field and the date are **new** |
| Kms-14-Disabled | A disabled key | `kms_key_detail`, `fail_inline` | exists |
| Kms-15-Pending-Deletion | A key pending deletion | `kms_key_detail` (the banner), `kmsCancelDeletion` | exists; the date is **new** |
| Kms-16-Rsa-Encrypt | An RSA key with ENCRYPT_DECRYPT | `kms_key_detail` | the refusals exist (KMS-22); the algorithm select is **new** |
| Kms-17-Denied | Encrypt denied under enforce | `fail_inline` | exists |
| Kms-18-Lockout | Locked out by a saved policy | `kmsSavePolicy`, `fail_inline` in the builder's slot | exists as a bad day (KMS-24) |
| Kms-19-Fidelity | The fidelity dialog | `info_panel`, `svcInfo` | table exists; "What is real" is **new** (foundation D) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Tabs | The existing sections; no new data |
| Alias as the page title | `ListAliases`, already called by the console's `DescribeKey` (fixes KMS-20) |
| State in a list row | `KeyState`, already fetched per key by `ListKeys` (KMS-28) |
| Teaching lines on the create form and settings | The settings table (foundation B4): enums and ranges from `kms/validate.go`, tiers from the ledger. The spec-to-usage pairs come from `kms/crypto.go` `specs`, which B4's accessor must expose. |
| "What this key will be" and the usage table | The form's two selects and the same `specs` table. A pure function of the form. |
| CLI preview on create | The form's fields in the existing `cli` microformat: `kms create-key --key-spec $spec [--key-usage $usage] [--description $description]`, with the alias as a second command |
| Byte count under a plaintext or message | The field's own value |
| "Of the 7 keys, 2 can receive it" | Spec, usage and state of each key in the list |
| Encryption context on Encrypt and Decrypt | The `EncryptionContext` member both operations already take (KMS-26) |
| Algorithm select on an RSA encryption key | `EncryptionAlgorithms` in `KeyMetadata`; Decrypt then also sends `KeyId` (KMS-22) |
| Rotation period | `RotationPeriodInDays` on EnableKeyRotation and GetKeyRotationStatus |
| Rotation list | `ListKeyRotations`, already read into `Key.Rotations` |
| Deletion date and time left | `DeletionDate` in `KeyMetadata` |
| Waiting period on the confirm | `PendingWindowInDays` on ScheduleKeyDeletion |
| Disable confirm | None; a question in front of the existing switch |
| "What this policy does", five lines | The stored document (statement count), the ledger row, the IAM mode the console already shows |
| "What is real" in the fidelity dialog | "Differences from AWS" in `SUPPORT.md` (foundation D) |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Spec and usage are fixed at creation | doc (CreateKey). Recording queue. |
| Which usage each spec allows; the default for RSA | `kms/crypto.go` `specs`, `defaultUsageFor`; held by KMS-6's test |
| "7 offered; also over the API: P521, HMAC_224/384/512" | `kms_create`'s options against `specs` |
| Wrong usage is `InvalidKeyUsageException` | `requireUsage`, run |
| Alias: characters, 250 after the prefix, `alias/aws/` reserved | model (`AliasName` pattern and length), `ownAliasName` |
| Description 0 to 8192; tags 1 to 128 and 0 to 256 | model |
| "The ciphertext carries the key id, so Decrypt needs none" | `seal`, `openBlob`; ledger |
| "1 to 4 096 on AWS, refused above 3 072 here" | KMS-2. The line is deleted when KMS-2 is fixed; it must not ship. |
| "Opens only ciphertext this doze-aws sealed" | `openBlob`'s magic check, run |
| Re-encrypt: "the plaintext never leaves the service" | the handler's own note; true of the wire, since `reEncrypt` decrypts and encrypts in process |
| Signed as RAW; six algorithms on RSA, one per curve on ECC | `client_sec.go` `KMSSign`; `signingAlgorithms` |
| "A MAC can only be verified by calling this key"; constant-time | the template's own reasoning; ledger |
| Random: 1 to 1024, else 32 | `kmsRandom`, run (KMS-31 changes it) |
| Rotation: 90 to 2560, default 365, symmetric only, stored and not run | model, `defaultRotationDays`, `setRotation`, ledger |
| Deletion window 7 to 30 | `scheduleKeyDeletion`, run |
| Cancelled keys land Disabled | `cancelKeyDeletion`, run, ledger |
| Key policy: one named `default`, gates identity policies, soft and enforce, 131 072 characters | `listKeyPolicies`, `iamguard`, ledger, model |
| RSA encryption needs `KeyId` on Decrypt | ledger, `decrypt` |

**Statements on the boards not yet backed, or now known wrong.** Fix or remove
each before the board ships.

- Kms-13: "every operation on it is refused as KMSInvalidStateException".
  False today: description, aliases, tags, policy and the rotation flag still
  succeed on a pending key (KMS-8). True of the cryptographic operations.
- Kms-16: Rotate now's refusal is drawn inline. Read from `shell.js`, a header
  button has no slot, so today it would be a toast (foundation A6). Not checked
  in a browser.
- Kms-15: the Enabled switch's refusal is drawn at the top of its card. Read
  from the same ladder, not checked in a browser.
- Kms-18: "restart with `--iam-mode soft` and Save key policy works again". The
  key's page under soft was run; PutKeyPolicy under soft was read, not run.
- Kms-06 and Kms-10: "the key this ciphertext was encrypted under no longer
  exists" and "the aliases are removed with the key" are read from
  `SweepDeletions` and `decrypt`; the janitor test in section 5 holds them.
- Kms-13: the "got 3" refusal was run over the API; the field it sits beside is new.
- Every board carries the "doze-aws refuses this because AWS does" line under
  hand-derived messages. That line is foundation A2's to replace.
- Kms-02, Kms-03: "it cannot change after creation" is `doc`, not recorded.
- Kms-07: "the service hashes the message with the algorithm's SHA" is true of
  doze-aws (`digest`); that AWS does the same for RAW is `doc`.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Shared validation | Blob length at the byte limit, in `internal/modelcheck` and per KMS blob member (KMS-2) |
| Store, handler | KMS has none today. Add `kms/store_test.go` and `kms/actions_test.go`: policy validation (KMS-3, KMS-4), the state table (KMS-8), `IncorrectKeyException` (KMS-10), data-key arguments (KMS-11), tags (KMS-12), rotation limit and entry shape (KMS-13), every enum value (KMS-15), create switches (KMS-16), DryRun (KMS-17), `PolicyName` (KMS-18), the janitor under a fake clock |
| SDK v2 | Paging on five lists (KMS-7); UpdateAlias across types (KMS-9); sign here and verify outside, and the reverse; every algorithm on every spec; the identity run (KMS-1) |
| Guard | Soft and enforce, default policy, explicit Deny, no root statement, an identity policy with and without the gate, the action per operation |
| Rejection parity | Regenerate `cases_kms.json` after KMS-2; `GetKeyLastUsage` (KMS-14) |
| Restart | Write, restart, read: material, old material, rotations, state, deletion date, policy, tags, aliases (foundation B5) |
| Provision | RSA default usage (KMS-6); a key with a policy, disabled, with a rotation period |
| Conformance (boto3) | One scenario per row of section 8, added to the 5 that exist |
| Console (Go) | Orphan key (KMS-5); heading (KMS-20); verdicts (KMS-23); the list under a locked key (KMS-24); random (KMS-31) |
| Browser | RSA encryption key end to end (KMS-22); encryption context (KMS-26); schedule with a window and the landing page (KMS-27); cancel then enable (KMS-21); each `new` item; each confirm's sentence; the five tabs by keyboard |
| Mutation | `kms/` baseline, then survivors in `crypto.go`, `store.go`, `guard.go` |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| Key spec and key usage cannot be changed after creation | doc | `test_kms::spec_and_usage_are_fixed` |
| Default `KeyUsage` per spec, through the API and through CloudFormation | memory | `default_usage_per_spec` |
| Which operations each key state refuses, and the code | doc for the table, memory for the rows | `state_table` |
| ScheduleKeyDeletion on a pending key | memory | `schedule_twice` |
| A cancelled key is Disabled | doc | `cancel_lands_disabled` (in `test_key_states` today, unverified) |
| UpdateAlias needs the same key type and usage; its code | memory | `update_alias_across_types` |
| Creating `alias/aws/…` answers `NotAuthorizedException` | memory | `reserved_alias_prefix` |
| Operations that refuse an alias as `KeyId` | memory | `alias_not_accepted` |
| Decrypt with a `KeyId` that is not the ciphertext's key: `IncorrectKeyException` | doc | `decrypt_incorrect_key` |
| Wrong encryption context: `InvalidCiphertextException` | doc | `wrong_context` |
| GenerateDataKey with both or neither of `KeySpec` and `NumberOfBytes` | memory | `data_key_arguments` |
| A malformed key policy: which documents, which code | doc | `malformed_policy` |
| The lockout safety check and `BypassPolicyLockoutSafetyCheck` | doc | `policy_lockout_check` |
| `GetKeyPolicy` with a name other than `default` | memory | `policy_name` |
| Tag key beginning `aws:`; more than 50 tags | memory | `tag_rules` |
| On-demand rotation limit of 10 and its code; the list entry's shape | memory, model | `on_demand_rotation_limit` |
| Rotation operations on an asymmetric or HMAC key | memory | `rotation_on_asymmetric` |
| `GetPublicKey` on a symmetric key | memory | `public_key_of_symmetric` |
| Data key from a non-symmetric key | memory | `data_key_from_rsa` |
| ECDSA algorithm on a curve other than its own | memory | `ecdsa_curve_mismatch` |
| RAW messages are hashed by KMS; DIGEST length rule | doc | `message_types` |
| `Origin: EXTERNAL` gives `PendingImport` | doc | `origin_external` (record only; costs nothing extra) |
| `DryRun` answers `DryRunOperationException` | doc | `dry_run` |
| An AWS-managed key's `KeyManager`, and what may be done to it | memory | `managed_key` |
| The code an SDK sees for an operation a region does not serve (for the stub code) | memory | `unsupported_operation_code` |
| Default page size and marker shape for ListKeys and ListAliases | doc | `list_paging` |

Keys cost a prorated dollar a month and cannot be deleted in under seven days;
`test_kms.py`'s header already accounts for it. Recordings leave keys pending.

## 9. Owed to integration

| What | With |
|---|---|
| A key and alias declared in a template: `KeyPolicy`, `Enabled`, `EnableKeyRotation`, `RotationPeriodInDays`, `PendingWindowInDays`, `MultiRegion`. What the mapper reads beyond spec, usage, description, rotation and tags is not established here. | CloudFormation |
| A key with no alias cannot be declared (`AWS::KMS::Key` is "addressed by alias") | CloudFormation |
| Export and re-apply round trip of a key with a policy | CloudFormation |
| A stream whose key is disabled or pending (4 tests exist) | Kinesis |
| Secrets, parameters, queues, topics, buckets and log groups record a `KmsKeyId` and never call KMS. Decide per service whether an unknown or disabled key is refused, as Kinesis does. | Secrets Manager, Parameter Store, SQS, SNS, S3, Logs |
| `kms:ViaService` and the other KMS condition keys in a key policy | IAM |
| An identity policy granting `kms:Decrypt` to a user, with the key policy's root statement present and absent | IAM |
| The access log's two rows (identity, resource) for a KMS call, and the decision headers | IAM |
| Envelope encryption end to end: a function generates a data key, stores ciphertext, another decrypts | Lambda |
| The wire showing a Decrypt with the key resolved from the ciphertext | console, wire |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] KMS-1 to KMS-34 each closed, with the test that failed first named beside it.
- [ ] KMS-2 is fixed in `internal/modelcheck`, and every other service's blob lengths were checked in the same commit.
- [ ] Decided and written into `SUPPORT.md` and `not-built.md`: `GetKeyLastUsage`; `VerifyMacForImport`; `ECC_NIST_EDWARDS25519` (build or decline); `MultiRegion`; `DryRun`; `DryRunModifiers` and `Recipient`; whether the lockout stays reproducible only with the bypass flag.
- [ ] The tables in section 2 have no "not established" and no "to decide" left: `KeyPairSpec` values run, the ECDSA curve rule, `MultiRegion`, `DryRunModifiers`, `Recipient`.
- [ ] Every enum value in the model for spec, usage, algorithm, message type and origin either works or answers the stub code and has a ledger row. One table test holds it.
- [ ] The state table test exists with every row asserting a code; rows resting on memory are skipped with the scenario named.
- [ ] Section 5: an ARN for another region or account; an alias as `KeyId` per operation; AWS-managed keys; `GetPublicKey` on a symmetric key; a data key from an RSA key; the stub code. Each is established and ticked, or on the recording queue with its test written and skipped.
- [ ] Every box in section 5 ticked, or moved the same way.
- [ ] `kms/` has store and handler tests, not only SDK tests.
- [ ] The package's tests and the KMS browser spec pass under `--account-id 111122223333 --region eu-west-1 --iam-mode enforce`.
- [ ] A write-restart-read test covers every field of the stored key and the alias bucket.
- [ ] Section 9's "not established" (what the CloudFormation mapper reads) is written down as a table in this document, even though the fix is CloudFormation's.
- [ ] All 20 boards implemented from the foundation's shell, or the dropped ones named here. Each `new` item reads only the data named for it.
- [ ] No statement on a KMS screen is in the "not yet backed" list in section 6. The "refused above 3 072 here" line does not exist.
- [ ] The console can use every kind of key it can create: an RSA encryption key encrypts and decrypts; an encryption context can be given.
- [ ] A refused alias on the create form leaves no key behind, or the page says a key was made.
- [ ] A key the console cannot describe is still listed, and says why.
- [ ] The confirms (schedule deletion, remove alias, disable) say only what the code does, and the cancel toast names the state the key is in.
- [ ] `e2e/tests/kms.spec.ts` covers every route under `/kms` with the route gate on, and the accessibility sweep is clean on all five tabs and create.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: the tables in section 2, the decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for KMS-1 to KMS-19 | `kms/store_test.go` and `kms/actions_test.go` (new), `kms/sdk_test.go`, `kms/rejection_parity_test.go`, `internal/modelcheck/*_test.go`, `internal/provision/*_test.go` |
| 3 | Blob lengths | `internal/modelcheck/modelcheck.go`, `kms/validate.go`, `kms/testdata/cases_kms.json` (regenerated with `dzaudit cases kms`) |
| 4 | Identity | `kms/actions_policy.go`, `kms/actions_keys.go` |
| 5 | Policy validation and the lockout check | `kms/actions_policy.go`, `kms/actions_keys.go`, `kms/guard.go` |
| 6 | Create switches, enum values, stubs, `GetKeyLastUsage` | `kms/crypto.go`, `kms/store.go`, `kms/actions.go`, `kms/coverage_model_test.go` |
| 7 | States, incorrect key, data-key arguments, aliases, tags, rotation limit, DryRun, policy name | `kms/actions_keys.go`, `kms/actions_crypto.go`, `kms/actions_aliases.go`, `kms/store.go`, `kms/kms.go` |
| 8 | Paging | `kms/actions_keys.go`, `kms/actions_aliases.go`, `kms/actions_policy.go` |
| 9 | Provision default usage | `internal/provision/svc_kms.go`, `internal/cfn/emit_kms.go` |
| 10 | Section 5 sweep, restart test, guard tests | `kms/*_test.go` |
| 11 | Conformance scenarios and claims table | `conformance/tests/test_kms.py`, the claims file from foundation B1 |
| 12 | Console | `internal/console/templates/kms.html`, `create.html` (`kms_create`), `handlers_sec.go` (`kms*`), `client_sec.go`, `routes_config.go`, `testdata/routes.txt` |
| 13 | Browser tests | `e2e/tests/kms.spec.ts` |
| 14 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 and 4 touch shared ground (the validator, and the identity rule in
foundation A8). If the foundation has not already taken them, they are done
first here and noted in the foundation's document.

**Prior art.** AWS's "Key states of AWS KMS keys" page is a table of operation
against state, which is the shape of the state-table test in section 5; each
row's test names that page. The KMS API reference for PutKeyPolicy describes
the lockout safety check and its bypass flag (KMS-4). Nothing is claimed here
about how other emulators handle these.
