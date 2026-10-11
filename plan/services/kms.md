# KMS

Stage 3. Size **L** (was M; re-estimated 2026-10-11, see section 1). Follows
[../01-protocol.md](../01-protocol.md); the common acceptance criteria there
apply in full, and this page adds KMS's own.

**Pre-stage review, 2026-10-11.** This page was checked claim by claim against
`.audit-models/kms.json` (the documentation of all 54 operations, every member
of every input and output shape, the 48 error shapes with their codes and
statuses, the 30 enums and the `examples` traits), against the code, and against a
running binary, and the console was rendered board by board beside the dream
design. Corrections are marked *Corrected 2026-10-11* with the model's words;
new findings are KMS-35 to KMS-55; the answers to the twelve questions of
[../02-lessons-from-sqs.md](../02-lessons-from-sqs.md) are in section 12. Source
tags: `model` (the model's text or traits), `doc` (a named page actually read),
`memory`, `run` (reproduced against a binary built from this tree on
2026-10-11), `read` (from the code).

## 1. Summary

Keys with real standard-library cryptography over one bbolt store (`kms.bolt`),
on one wire protocol (JSON 1.1, target prefix `TrentService`; the model
declares `awsJson1_1` and nothing else, `model`). Symmetric keys
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

**Estimate:** **L, eight to ten focused days** (was "M, three to four"). The
floor is six if the three crypto additions (ML-DSA, Ed25519, ECDH; KMS-15) and
the asynchronous rotation (KMS-45) are declined. Why it moved: the findings
table went from 34 rows to 55 (KMS-2 had already been fixed by the foundation's
B7, commit bed7d64, after this page was written; 21 are new, five of them P1
and none of them found by the first pass); seventeen of the twenty boards differ
from the page in shape and a further one cannot match yet (6a), which is the
largest single piece (five tabs, a create
page with teaching lines and a live strip, a two-column Configuration, confirms
with fields, a result slot per operation); and the state-by-operation table
alone is 40 operations by three states. The SQS lesson applies in full: the
first pass of that service was sized S and was several times that. **Actual:**
_fill in when done._

**Decisions this stage needs from a person** are collected at the end of
section 12 (eleven of them; none blocks the red tests).

## 2. Contract

The model (`kms/testdata/ops_kms.json`, checked by
`TestEveryModelOperationIsAccountedFor`) lists **54 operations** (`model`,
counted again from `.audit-models/kms.json`). The dispatch
table in `kms/actions.go` holds 36 handlers and 18 stubs; 17 of the stubs are
model operations, one (`VerifyMacForImport`) is no longer in the model, and one
model operation (`GetKeyLastUsage`) is not dispatched at all.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateKey | F | F | Per-setting tiers below. Stores any `Policy` string (KMS-3). Ignores `Origin`, `MultiRegion`, `BypassPolicyLockoutSafetyCheck`, `CustomKeyStoreId`, and `XksKeyId` after its pattern (KMS-16). `KeyUsage` omitted on RSA/ECC/HMAC is defaulted where the model requires it (KMS-6). |
| DescribeKey | F | F | `AWSAccountId` is a literal (KMS-1). No `CurrentKeyMaterialId` (KMS-41). A malformed ARN is `NotFoundException`, not `InvalidArnException` (KMS-39). |
| ListKeys | F | F | Ignores `Limit` and `Marker`; never truncates (KMS-7). |
| EnableKey, DisableKey | F | F | |
| ScheduleKeyDeletion, CancelKeyDeletion | F | F | Scheduling an already-pending key succeeds and resets the date (KMS-8). *Corrected 2026-10-11:* the window's trait is 1 to 365; the 7 to 30 is the model's prose ("it must be between 7 and 30, inclusive. If you do not include a value, it defaults to 30"). The handler enforces the prose. |
| Encrypt, Decrypt, ReEncrypt | F | F | *Corrected 2026-10-11:* the plan said "Blob length counted in base64 (KMS-2)". That was fixed by the foundation (B7, `bed7d64`, `TestEncryptAcceptsTheFullFourKilobytes`); 4 096 bytes round-trip (`run`). `KeyId` on a symmetric Decrypt is not compared with the ciphertext's key, an alias that does not exist is not even resolved (KMS-10). `DryRun` ignored (KMS-17). Algorithm mismatches answer `ValidationException`, and an RSAES algorithm on a symmetric key answers 500 (KMS-36, KMS-37). No `KeyMaterialId` (KMS-41). |
| GenerateDataKey, GenerateDataKeyWithoutPlaintext | F | F | Both `KeySpec` and `NumberOfBytes` (the larger wins) or neither (AES_256) is accepted (KMS-11). An asymmetric key answers `ValidationException` (KMS-37). |
| GenerateDataKeyPair, GenerateDataKeyPairWithoutPlaintext | F | F | Per-spec tiers below. `ECC_NIST_EDWARDS25519` and `ECC_SECG_P256K1` are refused although the model's `DataKeyPairSpec` lists them (KMS-15). |
| GenerateRandom | F | F | `CustomKeyStoreId` and `Recipient` accepted and ignored (KMS-16, KMS-44). |
| Sign, Verify | F | F | Per-algorithm tiers below. |
| GetPublicKey | F | F | *Corrected 2026-10-11:* on a symmetric or HMAC key it answers **500** `KMSInternalException` "stored key material is corrupt" (`run`); the plan said `ValidationException` "has no public key" (KMS-36). |
| GenerateMac, VerifyMac | F | F | |
| CreateAlias, UpdateAlias, DeleteAlias | F | F | UpdateAlias moves an alias across key types (KMS-9). No `CreationDate` or `LastUpdatedDate` is kept (KMS-41). `alias/aws/` is `NotAuthorizedException`, which is not a code of the model (KMS-53). |
| ListAliases | F | F | Ignores `Limit` and `Marker` (KMS-7). |
| TagResource, UntagResource, ListResourceTags | F | F | No `aws:` rule and no count limit (KMS-12). List ignores paging (KMS-7). An AWS-managed key can be tagged (KMS-43). |
| UpdateKeyDescription | F | F | Succeeds on a pending key (KMS-8). |
| GetKeyPolicy, PutKeyPolicy, ListKeyPolicies | F | F | Only JSON validity is checked (KMS-3), and the shared validator must not be wired as it stands (KMS-35). No lockout safety check (KMS-4). `PolicyName` is not checked (KMS-18). |
| EnableKeyRotation, DisableKeyRotation, GetKeyRotationStatus | F | F, with the schedule C or F (decision) | The flag and period are stored and reported; nothing runs on the schedule. The ledger says so; the console says "yearly" (KMS-29). *Corrected 2026-10-11:* the plan cited KMS-31 here; KMS-31 is the random-bytes field. No `NextRotationDate` (KMS-46); key state and key type are not consulted as the model lists (KMS-38). |
| RotateKeyOnDemand, ListKeyRotations | F | F | *Corrected 2026-10-11:* the limit is 25, not 10 (KMS-13). Synchronous where AWS is a background process (KMS-45). `IncludeKeyMaterial` ignored; entries carry three of the nine members (KMS-41). |
| GetKeyLastUsage | absent: answers `InvalidAction` | F (decision, KMS-14) | Nothing may be absent. The model gives it a full shape; see KMS-14. |
| CreateGrant, RetireGrant, RevokeGrant, ListGrants, ListRetirableGrants | S | S | `UnsupportedOperationException`, HTTP 400, no `X-Doze-Not-Built` header: not decision D5's 501 `NotImplemented` (KMS-54). In `not-built.md` ("grants are IAM machinery"). The model lists `UnsupportedOperationException` on none of the five. |
| CreateCustomKeyStore, ConnectCustomKeyStore, DisconnectCustomKeyStore, DeleteCustomKeyStore, DescribeCustomKeyStores, UpdateCustomKeyStore | S | S | In `not-built.md`. The model lists `UnsupportedOperationException` on none of the six. |
| ImportKeyMaterial, DeleteImportedKeyMaterial, GetParametersForImport | S | S | In `not-built.md`. The model lists `UnsupportedOperationException` on all three. |
| ReplicateKey, UpdatePrimaryRegion | S | S | In `not-built.md`. Listed on both. |
| DeriveSharedSecret | S | **F, decision (KMS-15)** | *Corrected 2026-10-11:* `not-built.md` says "cloud-infrastructure-only". The model describes it as ECDH over a KMS key pair and a caller-supplied public key on the same curve, which is `crypto/ecdh` (P-256, P-384, P-521) in the standard library (`read`, `go doc crypto/ecdh`). Not listed `UnsupportedOperationException`. |
| VerifyMacForImport | stub for an operation not in the model | remove, or keep and say why in the ledger | KMS-34. |

That is 36 F, 17 S, 1 absent, 54 in all.

"CreateKey is F" and "Sign is F" hide which values do anything, so the settings
get their own tiers. "Now" is what the binary answered on 2026-10-10 unless
marked read or run (2026-10-11).

**Key spec (`KeySpec`, 17 values in the model)**

| Value | Now | Target | To confirm |
|---|---|---|---|
| SYMMETRIC_DEFAULT | F | F | Model: "a 256-bit AES-GCM key". |
| RSA_2048, RSA_3072, RSA_4096 | F | F | Usage defaults to ENCRYPT_DECRYPT in the service and to SIGN_VERIFY in `provision` (KMS-6). *Corrected 2026-10-11:* the model says KeyUsage "is optional when you are creating a symmetric encryption KMS key; otherwise, it is required", so AWS has no default for RSA at all. |
| ECC_NIST_P256, ECC_NIST_P384, ECC_NIST_P521 | F for SIGN_VERIFY | F, plus KEY_AGREEMENT (KMS-15) | Model: "NIST-standard elliptic curve key pairs, specify SIGN_VERIFY or KEY_AGREEMENT". |
| HMAC_224, HMAC_256, HMAC_384, HMAC_512 | F | F | Model: "You must set the key usage even though GENERATE_VERIFY_MAC is the only valid key usage". |
| ECC_SECG_P256K1 | S, `UnsupportedOperationException` | S | Listed in the ledger. No curve in the standard library (`read`); SIGN_VERIFY only (`model`). |
| ECC_NIST_EDWARDS25519 | refused as `ValidationException` "unsupported KeySpec" (`run`) | **F, decision** | KMS-15. `crypto/ed25519` is in the standard library; the model's rules: ED25519_SHA_512 needs `MessageType:RAW`, ED25519_PH_SHA_512 needs `DIGEST`. Not in the ledger. |
| ML_DSA_44, ML_DSA_65, ML_DSA_87 | same (`run`) | **F, decision (weight)** | KMS-15. *Corrected 2026-10-11:* the plan said "S with the stub code". This toolchain (Go 1.27) has `crypto/mldsa` (`read`, `go doc crypto/mldsa`: FIPS 204, `MLDSA44/65/87`). Whether it moves the binary is a `task lightness` question. Algorithm `ML_DSA_SHAKE_256`, `MessageType` RAW or EXTERNAL_MU (64 bytes). |
| SM2 | same (`run`) | S with the stub code | KMS-15. Model: "SM2 (China Regions only)". |

**Key usage (`KeyUsage`, 4 values)**

| Value | Now | Target |
|---|---|---|
| ENCRYPT_DECRYPT | F for SYMMETRIC_DEFAULT and RSA | F |
| SIGN_VERIFY | F for RSA and ECC_NIST | F |
| GENERATE_VERIFY_MAC | F for HMAC | F |
| KEY_AGREEMENT | refused as `ValidationException` "not valid for KeySpec" (`run`) | F with DeriveSharedSecret for the NIST curves (KMS-15); `KeyAgreementAlgorithms: ["ECDH"]` in KeyMetadata and GetPublicKey (`model`) |

**Algorithms**

| Setting | Values that work | Values in the model that do not | Now for those |
|---|---|---|---|
| `EncryptionAlgorithm` | SYMMETRIC_DEFAULT, RSAES_OAEP_SHA_1, RSAES_OAEP_SHA_256 | SM2PKE | `ValidationException` "unsupported EncryptionAlgorithm ... for an RSA key" (`run`). *Corrected 2026-10-11:* the model's `InvalidKeyUsageException` is "The encryption algorithm or signing algorithm specified for the operation is incompatible with the type of key material in the KMS key (KeySpec)" (KMS-37). |
| `SigningAlgorithm` | RSASSA_PSS_SHA_256/384/512, RSASSA_PKCS1_V1_5_SHA_256/384/512, ECDSA_SHA_256/384/512 | SM2DSA, ML_DSA_SHAKE_256, ED25519_SHA_512, ED25519_PH_SHA_512 | `ValidationException` "algorithm ... is not valid for key spec" (`run`); model code `InvalidKeyUsageException` (KMS-37). |
| `MacAlgorithm` | HMAC_SHA_224/256/384/512, one per key spec | none | A wrong one is `ValidationException` (`run`); the model's `InvalidKeyUsageException` is listed on GenerateMac and VerifyMac, its text names encryption and signing only (by analogy: memory). |
| `MessageType` | RAW, DIGEST | EXTERNAL_MU | `ValidationException` "unsupported MessageType" (`run`). |
| Data key `KeySpec` | AES_256, AES_128 | none | |
| `KeyPairSpec` | RSA_2048/3072/4096, ECC_NIST_P256/P384/P521 | ECC_SECG_P256K1, ECC_NIST_EDWARDS25519, SM2 | `run`: P256K1 answers the stub code, EDWARDS25519 and SM2 answer `ValidationException` "unsupported KeySpec". The model's `DataKeyPairSpec` lists all nine and says the SM2 spec "is only available in China Regions", so **AWS accepts the first two**. |
| Largest plaintext (`model`, Encrypt) | SYMMETRIC_DEFAULT 4 096; RSA_2048 OAEP_SHA_1 214, SHA_256 190; RSA_3072 342, 318; RSA_4096 470, 446 | | `run`: RSA_2048 accepts 214 and 190, refuses 215 and 191 with the Go text "RSA encrypt: crypto/rsa: message too long for RSA key size" (KMS-37). |

Whether an ECDSA algorithm may be used with a curve other than its own
(ECDSA_SHA_256 on P384) is refused here (`run`: `ValidationException`); the
model does not say. `memory`: AWS lists exactly one per curve.

**Other request settings**

| Setting | Now | Target | To confirm |
|---|---|---|---|
| `EncryptionContext` | F: bound as authenticated data, on Encrypt, Decrypt, ReEncrypt, data keys and key pairs | F | Model: "supported only on operations with symmetric encryption KMS keys"; what an asymmetric Encrypt does with one is not established (here: ignored, `read`). |
| `Policy` on CreateKey and PutKeyPolicy | Stored as given; evaluated under soft and enforce; ignored when it does not parse (`run`: a key whose policy is `{"hello":1}` denies everyone under enforce) | F, validated when stored | KMS-3, KMS-35. Characters outside `\u0009 \u000A \u000D  -ÿ` are accepted (`run`, KMS-42). |
| `BypassPolicyLockoutSafetyCheck` | Ignored; every lockout is accepted (`run`) | F | KMS-4 |
| `Origin` | Ignored; EXTERNAL returns an enabled AWS_KMS key with generated material (`run`); AWS_CLOUDHSM with a `CustomKeyStoreId` too | AWS_KMS F, others S | KMS-16. Model: EXTERNAL "is valid only for symmetric KMS keys". |
| `MultiRegion` | *Corrected 2026-10-11:* ignored; the key reports `MultiRegion: false` and an ordinary UUID (`run`). The plan said "accepted and reported back". | S or C, to decide | KMS-16. Model: a multi-Region key id is `mrk-` plus 32 hex (its examples). |
| `CustomKeyStoreId`, `XksKeyId` | Length and pattern checked, then ignored (`run`) | S | KMS-16 |
| `RotationPeriodInDays` | Range 90 to 2560 enforced; stored and reported; default 365 | C or F (KMS-46), says so | `model`: "If no value is specified, the default value is 365 days". |
| `PendingWindowInDays` | 7 to 30 enforced by the handler; default 30; the console sends 7 | F | Trait 1 to 365, prose 7 to 30 (`model`, both). |
| `GrantTokens` | Each token's length checked (1 to 8 192); the list's length (0 to 10) **is not** (`run`: 11 accepted) | C | KMS-42. Grants are S, so a token can do nothing; the model lists `InvalidGrantTokenException` on 15 operations. |
| `DryRun` | Ignored: the operation runs (`run`) | F (`DryRunOperationException`, 412) or refused | KMS-17 |
| `DryRunModifiers`, `Recipient` (attestation) | Enum and length checked, then ignored (`run`: `Recipient` is accepted and the plaintext is returned in clear) | `DryRunModifiers` with DryRun; `Recipient` S | KMS-17, KMS-44. |
| `PolicyName` | Pattern checked; any name returns the one policy (`run`) | F: only `default` exists | KMS-18. Model: "The only valid name is `default`". |
| `Limit`, `Marker` | Range checked, otherwise ignored; any marker accepted (`run`) | F | KMS-7. |
| `KeyId` form per operation | Every operation resolves an alias name or alias ARN | The model's text lists which do | KMS-40. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `kms/`: 18 tests, `TestMain` and one fuzz target (*Corrected 2026-10-11:* 17; `blob_length_test.go` landed with B7). SDK v2 (8, `sdk_test.go`), SDK v1 (2), admin and data-key pairs (3, `sdk_coverage_test.go`), rotation period (1), managed alias namespace (1), dispatch coverage (1), rejection parity (1), blob length (1), `FuzzOpenBlob`. There is no store-level or handler-level unit test: everything goes through an SDK. |
| Parity | `rejection_parity_test.go`, driven by `testdata/cases_kms.json`; `coverage_model_test.go`. The ledger says 263 of 263 model constraints enforced across the 36 handlers, `knownGaps` empty, and 209 cases on undispatched or stubbed operations unaudited. *Corrected 2026-10-11:* **20 constraints of the model are not in `validate.go`** (13 `GrantTokens` list lengths, 2 `Policy` patterns, 5 `Marker` patterns; KMS-42), and the committed cases are 263 against 472 from a fresh `dzaudit cases kms` (foundation B8). |
| Boundary | `kinesis/kms_test.go`: 4 tests of Kinesis against a key's existence and state. |
| Conformance | `conformance/tests/test_kms.py`: 5 scenarios (symmetric round trip, aliases, key states, asymmetric sign and HMAC, refusals). All **unverified**: `conformance/snapshots/` is empty. |
| Browser | `e2e/tests/kms.spec.ts`: 9 tests. `e2e/route-gaps.txt` has no KMS entry. |
| Console (Go) | KMS cases inside `internal/console/console_test.go` (24 mentions) and the mutation-route sweep; no KMS-only file. |
| Provision | `internal/provision/svc_kms.go` (apply and export), `internal/cfn/emit_kms.go`, `internal/cfn/mapper.go` `key` and `keyAlias`. The tests that touch KMS: `internal/provision/destroy_test.go` and `internal/cfn/transpile_test.go`; no test of the default usage. |
| Restart | The upgrade fixture `testdata/datadir-1.0.0/us-east-1/kms/kms.bolt` exists. A KMS write-restart-read test does not; reproduced by hand 2026-10-11 (`run`: key, alias, tag, rotation, deletion date and an old ciphertext all survived). |
| Mutation | No per-package baseline (foundation B3). |

Unlike SQS, KMS's model carries its ranges, lengths, patterns and enums, so
most constraints are `model`. What the model does not carry as a trait is
state: which operations a Disabled or PendingDeletion key refuses, alias rules
across key types, the policy lockout check, limits. *Corrected 2026-10-11:* the
model **does** carry them in prose and in each operation's `errors` list, which
turns most of that from `doc` or `memory` into `model` (section 5, States).

## 4. Findings

Evidence `run` means reproduced against a binary built from this tree, over the
API with curl or through the console routes (2026-10-10 for KMS-1 to KMS-34,
2026-10-11 for the corrections and KMS-35 onward).

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| KMS-1 | P0 | The default key policy and `KeyMetadata.AWSAccountId` are written with the literal `000000000000`. Under `--account-id 111122223333 --iam-mode enforce` every new key is locked at birth: CreateKey succeeds, then Encrypt, DescribeKey and GetKeyPolicy answer `AccessDeniedException` for the root, and the console's key page is a 400. Under soft every call is logged as a would-deny. | `kms/actions_policy.go` `defaultKeyPolicy`; `kms/actions_keys.go` `metadataFor` | run (again 2026-10-11: `AWSAccountId` "000000000000" beside an ARN with 111122223333; DescribeKey denied) | n/a (ours); AWS's default policy names the key's own account (model: the GetKeyPolicy example names `arn:aws:iam::111122223333:root`) | SDK test under a non-default identity and enforce: create, encrypt, describe. Same class as foundation A8, which does not list KMS: add it there. |
| KMS-2 | P0 | **Closed** (foundation B7, `bed7d64`; `kms/blob_length_test.go`, `internal/modelcheck` `Blob`). Length constraints on blob members were applied to the base64 text. 3 073 to 4 096 bytes were refused. | `internal/modelcheck/modelcheck.go` | run: 3 072, 4 096 accepted, 4 097 refused (2026-10-11) | model: a length on a blob shape counts bytes | Remaining: a parity case per blob member at the byte limit (`Message`, `CiphertextBlob`, `Signature`, `Mac`) and regenerating `cases_kms.json` (B8). |
| KMS-3 | P0 | A key policy is stored without being parsed. `PutKeyPolicy` accepts `{"hello":1}` and `[]` and returns them from GetKeyPolicy; `CreateKey` accepts `Policy: "not json"`. `guardRequest` drops a document that fails `iampolicy.Parse`, so under enforce such a key denies everyone, and under off nothing ever notices. | `kms/actions_policy.go` `putKeyPolicy`; `kms/actions_keys.go` `createKey`; `kms/guard.go` `guardRequest` | run (store and read back; under enforce `{"hello":1}` answers `AccessDeniedException` on DescribeKey) | model: `MalformedPolicyDocumentException` ("not syntactically or semantically correct") on both operations; *Corrected 2026-10-11:* "Each statement in the key policy must contain one or more principals", and "If either of the required `Resource` or `Action` elements are missing ... the policy statement has no effect ... the `CreateKey` and `PutKeyPolicy` API requests succeed" | Handler tests: not JSON; JSON that is not a policy; no `Statement`; a statement with no `Principal` (**refused**). A statement with no `Action` or no `Resource` is **accepted**, and does nothing (KMS-35). *Corrected: the plan listed "no Effect" and a missing Action or Resource as refusals.* |
| KMS-4 | P1 | `PutKeyPolicy` and `CreateKey` accept a policy that locks the caller out, with or without `BypassPolicyLockoutSafetyCheck`. AWS refuses unless the flag is set. The ledger presents the lockout as deliberate; the safety check that normally prevents it is missing. | `kms/actions_policy.go` `putKeyPolicy` | run | model: "The key policy must allow the calling principal to make a subsequent `PutKeyPolicy` request ... (To omit this condition, set `BypassPolicyLockoutSafetyCheck` to true.)" The code is probably `MalformedPolicyDocumentException` (the only listed code that fits; memory). | SDK test under enforce: a policy without the caller is refused, and accepted with the flag. The console's Save key policy then needs the flag as a visible choice (board Kms-18). |
| KMS-5 | P2 | The console creates the key, then the alias. A refused alias (in use, bad characters, `alias/aws/…`) shows a refusal on the form while the key already exists with no alias. Three refused submits leave three orphan keys. | `internal/console/client_sec.go` `CreateKey` | run (again 2026-10-11: a refused `alias/harbour-checkout` left a key with no alias at the top of the list) | n/a | Console handler test: create with an alias that exists, then count keys. |
| KMS-6 | P0 | `provision` gives an RSA key with no stated usage `SIGN_VERIFY`. The service itself, given the same spec and no usage, gives `ENCRYPT_DECRYPT`. A template that relies on the default gets a different key through CloudFormation than through the API. *Corrected 2026-10-11:* neither is AWS's. The model says `KeyUsage` "is optional when you are creating a symmetric encryption KMS key; otherwise, it is required", and "You must set the key usage even though GENERATE_VERIFY_MAC is the only valid key usage value for HMAC KMS keys". So the service defaults (RSA to ENCRYPT_DECRYPT, ECC to SIGN_VERIFY, HMAC to GENERATE_VERIFY_MAC) and the provision defaults are inventions; the service is more forgiving than AWS. | `internal/provision/svc_kms.go`, the `KeyUsage` default at about line 41; `kms/store.go` `CreateKey`; `kms/crypto.go` `defaultUsageFor` | read; run (RSA_2048, no usage, answers ENCRYPT_DECRYPT) | model (the requirement); memory: `AWS::KMS::Key` documents `KeyUsage` default `ENCRYPT_DECRYPT`, which a CloudFormation-driven create then sends explicitly. Confirm before fixing the provision side. | Provision test: `KeySpec: RSA_2048`, no usage, then DescribeKey. Decide the service side (decision 1, section 12): refuse as AWS does, or keep a listed forgiveness. |
| KMS-7 | P1 | `ListKeys`, `ListAliases`, `ListResourceTags`, `ListKeyPolicies` and `ListKeyRotations` ignore `Limit` and `Marker` and always answer `Truncated: false`; a `Marker` of "garbage" is accepted. | `kms/actions_keys.go` `listKeys`, `listKeyRotations`; `kms/actions_aliases.go` `listAliases`, `listResourceTags`; `kms/actions_policy.go` | run for all five (2026-10-11: `Limit: 1` over 41 keys returns 41) | model. *Corrected 2026-10-11 (was "doc for the default page size"):* defaults are ListKeys 100, ListKeyPolicies 100, ListKeyRotations 100, ListAliases 50, ListResourceTags 50. **Conflict inside the model:** `Limit`'s trait is 1 to 1 000 everywhere, but ListAliases, ListGrants and ListRetirableGrants say "between 1 and 100", ListResourceTags "between 1 and 50". Which one AWS enforces is not established (recording queue); the trait is what the SDKs and the table enforce. `Marker`: 1 to 1 024 characters, `^[ -ÿ]*$`, "Do not attempt to construct this value", `InvalidMarkerException` (400) for a bad one. | SDK test: `Limit: 1` over three keys, follow `NextMarker` to the end, for each list; a marker nobody issued answers `InvalidMarkerException`. |
| KMS-8 | P1 | A key pending deletion accepts what AWS refuses for that state: ScheduleKeyDeletion again (the date is silently reset), EnableKeyRotation, CreateAlias, UpdateKeyDescription. TagResource, PutKeyPolicy, UpdateAlias and DisableKeyRotation have no state check either. | `kms/actions_keys.go` `scheduleKeyDeletion`, `setRotation`, `updateKeyDescription`; `kms/store.go` `SetAlias` | run for all (2026-10-11: EnableKeyRotation, DisableKeyRotation, UpdateKeyDescription, TagResource, UntagResource, PutKeyPolicy, CreateAlias, UpdateAlias onto it, GetKeyRotationStatus all 200) | model for **which operations can refuse at all**: section 5, States, is the table from each operation's `errors`. Which state refuses which operation is the "Key states of KMS keys" page, not read (memory). | One table test: every operation against Enabled, Disabled and PendingDeletion, asserting the code. Rows whose AWS answer is memory are written and skipped pending the recording. |
| KMS-9 | P1 | `UpdateAlias` moves an alias from a symmetric key to an RSA signing key, and from a signing key to an encryption key. | `kms/store.go` `SetAlias` | run (2026-10-11, three moves) | *Corrected 2026-10-11 (was memory):* model: "The current and new KMS key must be the same type (both symmetric or both asymmetric or both HMAC), and they must have the same key usage." **The code is not named:** `UpdateAlias` lists `NotFoundException`, `LimitExceededException`, `KMSInvalidStateException` and `DependencyTimeoutException`, none of which fits (memory). RSA signing to ECC signing is *allowed* (both asymmetric, same usage). | SDK test for the three moves; the code skipped until recorded. |
| KMS-10 | P1 | A symmetric `Decrypt` or `ReEncrypt` that names a `KeyId`/`SourceKeyId` other than the key the ciphertext was sealed under succeeds, including an HMAC key and an alias that does not exist. The `KeyId` is used for the policy check only. | `kms/actions_crypto.go` `decrypt`; `kms/guard.go` `decryptKeyIdent` | run (2026-10-11, all three, and ReEncrypt) | *Corrected 2026-10-11 (was doc):* model: "If you identify a different KMS key, the `Decrypt` operation throws an `IncorrectKeyException`" (400, shape code `IncorrectKeyException`); the same text for `ReEncrypt`'s `SourceKeyId`. | SDK test: encrypt under A, decrypt naming B, naming an HMAC key, naming an alias that does not exist; same for ReEncrypt. |
| KMS-11 | P1 | `GenerateDataKey` with both `KeySpec` and `NumberOfBytes` (the larger wins, 64), or with neither (32), succeeds. The "specify KeySpec or NumberOfBytes" branch cannot be reached because an empty `KeySpec` is treated as AES_256. | `kms/actions_crypto.go` `dataKey` | run | *Corrected 2026-10-11 (was memory):* model: "You must specify either the `KeySpec` or the `NumberOfBytes` parameter (but not both) in every `GenerateDataKey` request". The code is not named (probably `ValidationException`; memory). | Handler test for both and for neither; the code skipped until recorded. |
| KMS-12 | P1 | Tags: a key beginning `aws:` is accepted, 51 and 60 tags in one call are accepted, a duplicate key in one call is accepted (the last wins). | `kms/actions_aliases.go` `tagResource` | run (2026-10-11, all three) | memory for the reserved prefix and for 50 per key (the model has no number: `TagResource` lists `LimitExceededException` and `TagException`). model: "You cannot have more than one tag on a KMS key with the same tag key. If you specify an existing tag key with a different tag value, KMS replaces the current tag value", so a repeat *across* calls replaces, and what a duplicate *within* one call does is not established; "you cannot tag an AWS managed key, an AWS owned key, a custom key store, or an alias" (KMS-43). | Handler tests for each; skipped until `doc`. |
| KMS-13 | P1 | `RotateKeyOnDemand` has no limit: twenty-seven in a row all succeed. `ListKeyRotations` entries carry `KeyId`, `RotationDate`, `RotationType` only. *Corrected 2026-10-11:* the limit is **25**, not 10. | `kms/actions_keys.go` `rotateKeyOnDemand`, `listKeyRotations` | run (27 of 27 succeeded) | model: "You can perform on-demand key rotation a maximum of 25 times per KMS key"; `LimitExceededException` is on its error list. The entry has nine members (KMS-41). | SDK test for the twenty-sixth; shape test against the model. |
| KMS-14 | P1 | `GetKeyLastUsage` is in the model and answers `InvalidAction` (HTTP 400), which no SDK expects from KMS. The coverage test allows it as "Unreached". | `kms/actions.go` `handlers`; `kms/coverage_model_test.go` | run | model. *Added 2026-10-11:* request `KeyId` "key ID or key ARN ... Alias names are not supported"; response `KeyId`, `KeyLastUsage` (`Operation`, `Timestamp`, `CloudTrailEventId`, `KmsRequestId`, each "Absent if the key has not been used since KMS began tracking"), `TrackingStartDate` ("the date from which KMS began recording cryptographic activity for this key, or the date the KMS key was created, whichever is later"), `KeyCreationDate`. Errors: `InvalidArnException`, `NotFoundException`. **Not** `KMSInvalidStateException`: it answers for a key in any state. Tracking in memory only is honest by that definition: after a restart `TrackingStartDate` is the restart time and "empty" means "no record". | Coverage test with `Unreached` empty; one test: encrypt, then the entry names `Encrypt`; a restart leaves it empty with a later `TrackingStartDate`. |
| KMS-15 | P1 | Unsupported choices are refused with three different codes. `ECC_SECG_P256K1` answers `UnsupportedOperationException`. `ML_DSA_*`, `SM2`, `ECC_NIST_EDWARDS25519` answer `ValidationException` "unsupported KeySpec", which reads as the caller's mistake for a spec AWS accepts. `KEY_AGREEMENT` answers "not valid for KeySpec". None but P256K1 is in the ledger. *Added 2026-10-11:* three of those refusals are not needed: `crypto/ed25519`, `crypto/mldsa` and `crypto/ecdh` are in this toolchain (`read`), so ECC_NIST_EDWARDS25519, ML_DSA_44/65/87 and KEY_AGREEMENT with DeriveSharedSecret can be F with no new module; the weight question is `task lightness`. The data-key-pair specs EDWARDS25519 and SECG_P256K1 are accepted by AWS (`model`). Only SM2 (China Regions only) and P256K1 (no curve in the standard library) stay S. | `kms/crypto.go` `validateSpecUsage`; `docs/not-built.md` (DeriveSharedSecret "cloud-infrastructure-only") | run | model (the enum values, the algorithms' rules, DeriveSharedSecret's description) | Table test: every enum value of every spec, usage and algorithm either works or answers the stub code and has a ledger row. |
| KMS-16 | P0 | `CreateKey` with `Origin: EXTERNAL` returns an enabled key with generated material and `Origin: AWS_KMS`. On AWS that key has no material and cannot encrypt. `MultiRegion: true` is ignored (*Corrected 2026-10-11:* the plan said "echoed back"; the key reports `MultiRegion: false`). `Origin: AWS_CLOUDHSM` with a `CustomKeyStoreId` creates an ordinary AWS_KMS key. `CustomKeyStoreId` and `XksKeyId` are ignored. The import, replica and custom-store operations are honest stubs; the create-time switches for the same features are silent. | `kms/actions_keys.go` `createKey`; `kms/store.go` `CreateKey` | run for all four (2026-10-11) | model: "To create a KMS key with no key material ... set this value to `EXTERNAL`" (state `PendingImport`: "its `KeyState` must be `PendingImport`", ImportKeyMaterial); "multi-Region ... The default value is `False`" and a primary's id is `mrk-` + 32 hex (the DescribeKey example) | Handler test per switch: refused with the stub code (decision 11). |
| KMS-17 | P1 | `DryRun: true` is ignored: Encrypt returns ciphertext. | `kms/kms.go` `ServeHTTP` (no handling anywhere) | run (2026-10-11) | model: `DryRunOperationException` (HTTP 412, "The request was rejected because the DryRun parameter was specified") is on 15 operations (Decrypt, ReEncrypt, Encrypt, GenerateDataKey and its three siblings, GenerateMac, VerifyMac, Sign, Verify, DeriveSharedSecret, CreateGrant, RetireGrant, RevokeGrant). `DryRunModifiers: IGNORE_CIPHERTEXT` makes `CiphertextBlob` and `KeyId` optional on Decrypt and ReEncrypt ("performs only authorization validation without ciphertext validation"). | Handler test on Encrypt and Decrypt. Decide F or refuse (decision 11). |
| KMS-18 | P1 | `GetKeyPolicy` with `PolicyName: "other"` returns the default policy; `PutKeyPolicy` with `PolicyName: "other"` replaces it. | `kms/actions_policy.go` `getKeyPolicy`, `putKeyPolicy` | run (both, 2026-10-11) | *Corrected 2026-10-11 (was memory):* model: "The only valid name is `default`" on GetKeyPolicy, PutKeyPolicy, ListKeyPolicies. The code for another name is not named (GetKeyPolicy lists `NotFoundException`, `KMSInvalidStateException`; probably the first, memory). | Handler test; the code skipped until recorded. |
| KMS-19 | P1 | `GetKeyRotationStatus` on an RSA or HMAC key answers `KeyRotationEnabled: false`. *Corrected 2026-10-11:* AWS refuses. The model lists `UnsupportedOperationException` on GetKeyRotationStatus and says "Automatic key rotation is supported only on symmetric encryption KMS keys". See KMS-38 for the whole family. | `kms/actions_keys.go` `getKeyRotationStatus` | run | model (the code is listed; "when" is the prose) | Handler test; part of KMS-38's table. |
| KMS-20 | P2 | The detail header never shows the alias. `DescribeKey` in the console client does not set `Key.Alias`; only `ListKeys` does, and the header reads `.Key.Alias`. | `internal/console/client_sec.go` `DescribeKey`; `templates/kms.html` `kms_key_detail` | run (2026-10-11: every key page titled by its UUID) | n/a | Console test: a key with an alias, assert the heading. |
| KMS-21 | P2 | After Cancel deletion the toast says "the key is usable again". The key lands Disabled and refuses every use. The pending banner says "until you cancel", which is one step short. | `internal/console/handlers_sec.go` `kmsCancelDeletion`; `templates/kms.html` | read | *Corrected 2026-10-11 (was doc):* model, CancelKeyDeletion: "When this operation succeeds, the key state of the KMS key is `Disabled`." | Browser test reads the toast and the state pill. See also foundation A12. |
| KMS-22 | P2 | The page chooses its controls by usage, not spec. An RSA key with ENCRYPT_DECRYPT gets Rotate now and the rotation switch, both always refused. Its Encrypt sends no algorithm and always fails ("SYMMETRIC_DEFAULT is not valid for key spec RSA_3072"); Decrypt sends no `KeyId`; Re-encrypt to it fails the same way. The console cannot use an RSA encryption key at all. | `templates/kms.html` (the `eq .Key.Usage "ENCRYPT_DECRYPT"` tests); `client_sec.go` `KMSEncrypt`, `KMSDecrypt` | run (2026-10-11, screenshots) | model (rotation is symmetric only; Decrypt needs the key and the algorithm for an asymmetric key) | Browser test: create RSA_2048 with the default usage, encrypt, decrypt. |
| KMS-23 | P2 | Verify and Verify MAC report every error as "does NOT verify". With the key disabled after signing, the verdict is the same as for a forged signature. | `handlers_sec.go` `kmsVerify`, `kmsVerifyMac` | read | n/a | Handler test: disable, verify, expect the refusal. |
| KMS-24 | P2 | Saving a policy that excludes the root under enforce: the toast says "Key policy saved", and from then on the key is missing from the list (the list skips any key whose DescribeKey fails) and its count, and opening it answers the generic "That could not be opened" page with a Denied callout. Nothing says a key is hidden. *Corrected 2026-10-11:* the inline denial the plan described was **not** reproduced: the save answered 200 with a normal re-render and the toast (`run`); the denial appears on the next navigation. | `handlers_sec.go` `kmsSavePolicy`, `kmsKeyPartial`; `client_sec.go` `ListKeys` (the `continue`) | run (2026-10-11: list dropped the key; direct open Denied) | n/a | Console test under enforce: save, then assert the list still shows a row for the key, marked unreadable. |
| KMS-25 | P2 | Re-encrypt's destination list holds every key: the source itself, signing keys, HMAC keys, disabled and pending keys. Most choices can only be refused. | `templates/kms.html`, the `dest` select | run | n/a | Browser test asserts the options. |
| KMS-26 | P2 | The console sends no encryption context, so ciphertext an application sealed with one cannot be decrypted from the console ("decryption failed (wrong key or encryption context)"), and the console cannot produce ciphertext the application expects. | `client_sec.go` `KMSEncrypt`, `KMSDecrypt` | run | n/a | Browser test: encrypt over the API with a context, decrypt in the console with it. |
| KMS-27 | P2 | Schedule deletion always sends 7 days and then lands on the list's first key, not the scheduled one. The pending page shows no date, though DescribeKey returns `DeletionDate`. The rotation switch sends no period. | `client_sec.go` `ScheduleKeyDeletion`, `DescribeKey`; `handlers_sec.go` `kmsScheduleDeletion` | run | n/a | Browser test for the landing page and the date. |
| KMS-28 | P2 | The list shows one alias per key (the last in name order) and no state, so a disabled or pending key looks like any other. A key with no alias is its UUID, truncated. | `client_sec.go` `ListKeys`; `templates/kms.html` `kms_lp` | run | n/a | Console test with a disabled key. |
| KMS-29 | P3 | "Automatic rotation: New backing material yearly". Nothing rotates on a schedule here. | `templates/kms.html` | read | n/a (the ledger says the opposite) | Covered by the common "no console text contradicts the ledger" box. |
| KMS-30 | P3 | The create page says "RSA/ECC sign/verify". An RSA key created there with "Default for spec" encrypts. | `templates/create.html` `kms_create` | run | n/a | Same box. |
| KMS-31 | P3 | Random bytes outside 1 to 1024 are silently replaced by 32. | `handlers_sec.go` `kmsRandom` | read | model: `NumberOfBytes` 1 to 1 024, "This parameter is required" | Handler test: 0 is refused with the service's message. |
| KMS-32 | P3 | The create form has no CLI preview; the Encrypt form is the only KMS form with one. | `templates/create.html` `kms_create` | run | n/a | Browser test once built (foundation A11 gives the rule). |
| KMS-33 | P3 | The store's comment calls `Policy` "round-trip only"; the guard evaluates it. | `kms/store.go`, the `key` struct | read | n/a | none |
| KMS-34 | P3 | Ledger: `VerifyMacForImport` is stubbed and is not an AWS operation; `GetKeyLastUsage` is missing; the unsupported specs and algorithms other than P256K1 are missing; "Stored and returned only under the default `off`" (the default is `soft`; the foundation's acceptance criteria own that sentence). | `docs/SUPPORT.md`, `kms/actions.go` | read | n/a | The ledger tests after the contract table above is checked in. |

**Found by reading the model's own documentation, the running binary and the
rendered boards (2026-10-11).** The direction is named, because "stricter than
AWS" breaks a correct caller exactly as "more forgiving" breaks a deploy.

| Id | Sev | What | Direction | Where | Evidence | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| KMS-35 | P1 | **Do not wire the shared policy validator as it stands.** `iampolicy.ValidateResourcePolicy` (foundation B9, `909f9ed`) refuses a statement with no `Action` and one with no `Resource`. KMS's model says those statements are accepted and do nothing: the policy "has no effect ... the `CreateKey` and `PutKeyPolicy` API requests succeed". Wired as it is, KMS would refuse a document AWS stores. The other half is the guard: `iampolicy.Parse` fails on a statement with no `Action`, and the guard then drops the **whole** document, so a good root statement beside one statement without `Action` locks the key against everyone under enforce (`run`); while a statement with no `Resource`, and one with no `Principal`, are *effective* (a key whose only statement lacks `Resource` admits the root). | stricter if wired; more forgiving in the guard | `internal/iampolicy/validate.go` `NoAction`, `NoResource`; `kms/guard.go` `guardRequest` | run (enforce: no Action denied, and denied the whole document; no Resource allowed; no Principal allowed; no Effect denied) | model: CreateKey.Policy and PutKeyPolicy.Policy | Handler tests: a statement with no `Action` / no `Resource` is stored; under enforce it grants nothing; a statement with no `Principal` is refused with `MalformedPolicyDocumentException`. |
| KMS-36 | P1 | A caller's mistake answers **HTTP 500** `KMSInternalException` "stored key material is corrupt": GetPublicKey on a symmetric or HMAC key, and Encrypt, Decrypt or ReEncrypt with an RSAES algorithm on a symmetric key. SDKs retry 5xx, so the call is made again and again, and the class is wrong. The plan §5 said `ValidationException` "has no public key". | wrong code (and class) | `kms/crypto.go` `privateKey`, `publicKeyDER`, `rsaEncrypt`, `rsaDecrypt` | run (2026-10-11: GetPublicKey on a symmetric and on an HMAC key, Encrypt with RSAES on a symmetric key; the Decrypt and ReEncrypt paths read) | model: GetPublicKey lists `UnsupportedOperationException` ("a specified resource is not valid for this operation") and `InvalidKeyUsageException`; which of the two a symmetric key gets is memory | Handler test: GetPublicKey(symmetric) answers 4xx with a listed code; no operation answers 500 for a request a valid key could not satisfy. |
| KMS-37 | P1 | Key and algorithm mismatches answer `ValidationException`; the model's `InvalidKeyUsageException` is defined as "The encryption algorithm or signing algorithm specified for the operation is incompatible with the type of key material in the KMS key (KeySpec)". Cases (`run`): Encrypt on an RSA key with no algorithm; SM2PKE; Sign with ECDSA_SHA_384 on a P-256 key, RSA algorithms on an EC key, ED25519 and ML-DSA on an RSA key; GenerateMac with the wrong MacAlgorithm (by analogy); **GenerateDataKey and GenerateDataKeyPair on an asymmetric key**, where `InvalidKeyUsageException` is the only listed code that fits. Also: an RSA plaintext over the limit leaks Go's text, "RSA encrypt: crypto/rsa: message too long for RSA key size". | wrong code | `kms/crypto.go` `rsaEncrypt`, `oaepHash`, `sign`, `verify`, `mac`; `kms/actions_crypto.go` `dataKey`, `dataKeyPair` | run | model | Table test: one request per mismatch, asserting `InvalidKeyUsageException`; RSA limits at 190/191, 214/215, 318/319, 342/343, 446/447, 470/471. |
| KMS-38 | P1 | Rotation ignores key state and key type as the model lists. (a) `GetKeyRotationStatus` on a key pending deletion reports the stored flag; the model: "While a KMS key is pending deletion, its key rotation status is `false` ... If you cancel the deletion, the original key rotation status returns". (b) On a Disabled key `EnableKeyRotation` and `DisableKeyRotation` succeed; both list `DisabledException`. (c) `RotateKeyOnDemand` on a Disabled key answers `KMSInvalidStateException` "key is not enabled"; it lists `DisabledException`. (d) `ListKeyRotations` on an asymmetric key answers an empty list; it lists `UnsupportedOperationException`. (e) `GetKeyRotationStatus` on an asymmetric or HMAC key answers false (KMS-19). (f) An AWS-managed key's status is "always `true`" (KMS-43). | more forgiving, and wrong code for (c) | `kms/actions_keys.go` | run (a, b, c, d, e) | model | One table test per operation. |
| KMS-39 | P2 | A key is resolved from any ARN by cutting at `:key/` or `:alias/`: an ARN of another account, another region, or another service resolves to the local key (`run`). A malformed ARN is `NotFoundException`; the model's `InvalidArnException` ("a specified ARN, or an ARN in a key policy, is not valid", 400) is never produced although 31 operations list it. | more forgiving, wrong code | `kms/store.go` `resolve` | run | model | Handler tests: `arn:aws:kms:eu-west-1:...:key/<id>` and another account's are `NotFoundException` (or the foreign-account answer, memory); `arn:aws:kms:us-east-1:000000000000:nothing/x` is `InvalidArnException` on DescribeKey. |
| KMS-40 | P2 | Every operation resolves an alias name or alias ARN for its key. The model's member documentation says "Specify the key ID or key ARN of the KMS key" for 28 members (20 on implemented operations; list in section 5) and "key ID, key ARN, alias name, or alias ARN" for 13 (Decrypt, DeriveSharedSecret, DescribeKey, Encrypt, the four GenerateDataKey operations, GetPublicKey, ReEncrypt twice, Sign, Verify). `CreateAlias.TargetKeyId` and `UpdateAlias.TargetKeyId` take an alias name (`run`); TagResource via an alias worked. What AWS does with an alias in the first group is not established. `GetKeyLastUsage` says "Alias names are not supported". | more forgiving (probable) | `kms/store.go` `Resolve` | run | model (the rule as documented), memory (the behaviour) | One test per member of the 28, written and skipped pending the recording. |
| KMS-41 | P2 | Members of the model's output shapes that are missing: `KeyMaterialId` on Decrypt, GenerateDataKey, GenerateDataKeyWithoutPlaintext, GenerateDataKeyPair(WithoutPlaintext) and ReEncrypt (`SourceKeyMaterialId`, `DestinationKeyMaterialId`); `CurrentKeyMaterialId` on KeyMetadata ("present for symmetric encryption keys with AWS_KMS or EXTERNAL origin"); `NextRotationDate` and `OnDemandRotationStartDate` on GetKeyRotationStatus; the alias entry's `CreationDate` and `LastUpdatedDate`; the rotation entry's `KeyMaterialId`, `KeyMaterialState` (CURRENT, NON_CURRENT), and "not present for the first key material" rules; `IncludeKeyMaterial` (`ROTATIONS_ONLY` default, `ALL_KEY_MATERIAL` "adds the first key material"). The id is 64 hex characters and is derivable from the key id and the material without a stored field. | missing | `kms/actions_keys.go` `metadataFor`, `getKeyRotationStatus`, `listKeyRotations`; `kms/actions_aliases.go`; `kms/actions_crypto.go`; `kms/store.go` (alias timestamps are not stored) | run | model (members, their documentation and the `examples` trait) | Shape test: every response compared with the model's member list for the state it is in. |
| KMS-42 | P2 | Twenty constraints of the model are not in `validate.go`, and the ledger's "263 of 263" is a count of the ones the generator emits: `GrantTokens` list length 0 to 10 on 13 operations (Decrypt, DescribeKey, Encrypt, GenerateDataKey, GenerateDataKeyPair, GenerateDataKeyPairWithoutPlaintext, GenerateDataKeyWithoutPlaintext, GenerateMac, GetPublicKey, ReEncrypt, Sign, Verify, VerifyMac), the `Policy` pattern on CreateKey and PutKeyPolicy, the `Marker` pattern on five list operations. `\u` patterns do not compile in RE2 and are dropped; list lengths are what foundation B8 says the generator drops. | more forgiving | `kms/validate.go`; `cmd/dzaudit/cases.go` | run (11 tokens accepted; an emoji in a policy accepted) | model | Parity cases for 11 tokens, a policy with U+0100, a marker with U+0100; regenerate after B8. |
| KMS-43 | P2 | AWS-managed keys (`alias/aws/<anything>`) are created on first use by **any** operation and for any name; they report `KeyManager: CUSTOMER`; and none of the model's refusals applies: "you cannot tag an AWS managed key", "You cannot enable or disable automatic rotation of AWS managed KMS keys", "You cannot initiate on-demand rotation", status "always `true`", UpdateAlias: "You don't have permission to associate an alias with an AWS managed key". The model creates one only from "a predefined AWS alias with no key ID" through DescribeKey. | more forgiving, wrong shape (deliberate in part: a template that names `alias/aws/s3` must work) | `kms/store.go` `Resolve`, `ensureAWSManaged`, `isAWSManagedAlias` | run (`KeyManager` CUSTOMER; TagResource via `alias/aws/s3` accepted; Encrypt via `alias/aws/ssm` creates it) | model (`KeyManagerType` has `AWS`; prose per operation) | Handler tests: `KeyManager: AWS`; TagResource, EnableKeyRotation, RotateKeyOnDemand refused (codes: memory); status true. Decide whether materialisation stays on every operation (a listed difference). |
| KMS-44 | P2 | `Recipient` (a Nitro attestation) is accepted and ignored on Decrypt, GenerateDataKey, GenerateDataKeyPair, GenerateRandom (and DeriveSharedSecret when built): the plaintext comes back in clear and there is no `CiphertextForRecipient`. On AWS the plaintext is withheld and encrypted to the enclave's key. | more forgiving; wrong shape | `kms/actions_crypto.go` | run (Encrypt-side `Recipient` accepted; Decrypt-side read) | model | Handler test: a request with `Recipient` is refused with the stub code (or answers `CiphertextForRecipient`), never the plaintext. |
| KMS-45 | P1 | `RotateKeyOnDemand` finishes inside the call. On AWS it is a background process: "there might be a slight delay between initiating on-demand key rotation and the rotation's completion", `GetKeyRotationStatus` returns `OnDemandRotationStartDate` while it runs and "KMS removes this field" after, `ListKeyRotations` lists it once complete, and a second call during it, or within 20 minutes of a scheduled automatic rotation, is `ConflictException` (409). A test that asserts the list straight after the call passes here and races there (the SQS lesson, L7). | more forgiving | `kms/actions_keys.go` `rotateKeyOnDemand` | run | model | SDK test: the call returns, status shows `OnDemandRotationStartDate`, the list does not yet have the entry; after the delay it does and the field is gone. Decision 2 (section 12). |
| KMS-46 | P2 | Automatic rotation is a flag with no clock: no `NextRotationDate`, nothing happens when the period passes. The janitor and `Options.Clock` exist, so a rotation at `enable time + period` is a few lines on the same loop; the ledger says C. | n/a (decision) | `kms/store.go` `SweepDeletions` (the janitor), `kms/actions_keys.go` | read | model: "KMS rotates the key material ... one year (approximately 365 days) from the enable date and every year thereafter"; "If it's been less than a year since the key material in the re-enabled KMS key was rotated, the KMS key resumes its prior rotation schedule" | Decision 3. If F: a fake-clock test across the period. If C: `NextRotationDate` reported and the ledger says it never fires. |
| KMS-47 | P3 | Every error is HTTP 400 except the 500s. The model gives statuses: `NotFoundException` 404, `AlreadyExistsException`, `DisabledException`, `KMSInvalidStateException`, `ConflictException` 409, `DryRunOperationException` 412, `KeyUnavailableException`, `KMSInternalException` 500, `DependencyTimeoutException` 503. SDKs branch on `__type`, not the status. | wrong (cosmetic) | `kms/*.go` `awshttp.Errf(400, ...)` | run | model (`httpError` and `awsQueryError.httpResponseCode`); what the JSON wire actually sends is memory | Recording first (`error_status`). |
| KMS-48 | P3 | The default policy's `Sid` is "Enable IAM policies"; the model's GetKeyPolicy example is "Enable IAM User Permissions" (`Id` "key-default-1" matches). | wrong (cosmetic) | `kms/actions_policy.go` `defaultKeyPolicy` | run | model (the `examples` trait) | Handler test on the default document, after KMS-1. |
| KMS-49 | P2 | **Not established:** a key policy over the limit. The trait is 1 to 131 072 characters and the code answers `ValidationException` "Member must have length less than or equal to 131072" (`run`); the model's prose says "If the key policy exceeds the length constraint, KMS returns a `LimitExceededException`" on both CreateKey and PutKeyPolicy. | wrong code (probable) | `kms/validate.go` | run | model (the two statements conflict) | Written and skipped; recording `policy_length`. |
| KMS-50 | P2 | **Not established:** Decrypt of ciphertext whose key has been deleted answers `InvalidCiphertextException` "the key this ciphertext was encrypted under no longer exists", and the console board Kms-06 draws it as a refusal AWS gives. Decrypt's list has both `NotFoundException` ("the specified entity or resource could not be found") and `InvalidCiphertextException` ("corrupted, missing, or otherwise invalid"). | wrong code (possible) | `kms/actions_crypto.go` `decrypt` | read | model (both listed); behaviour memory | Written and skipped; recording `decrypt_deleted_key`. |
| KMS-51 | P3 | The create form's alias `pattern` is `[a-zA-Z0-9/_\-]{1,250}`; the model's is `^[a-zA-Z0-9:/_-]+$` (colon allowed). The key-spec select offers 7 of the 11 that work (no P521, HMAC_224/384/512), which the board states as deliberate. | stricter (console) | `templates/create.html` `kms_create` | read | model | Browser test types `alias/a:b`. |
| KMS-52 | P2 | "The principals in the key policy must exist and be visible to KMS." A key policy naming an IAM user or role that does not exist is stored here; AWS refuses it (`MalformedPolicyDocumentException`, "invalid principals": memory). The IAM service is in the same process. | more forgiving | `kms/actions_policy.go` | not run | model (the sentence); the code memory | Owed to IAM, with an opt-in `[kms] strict` (L8, decision 9). |
| KMS-53 | P2 | `alias/aws/…` on CreateAlias, UpdateAlias, DeleteAlias answers `NotAuthorizedException`, which is **not a shape of the model**; the bare `alias/` answers `ValidationException`. The model lists `InvalidAliasNameException` ("the specified alias name is not valid", 400) on CreateAlias and says "The alias name cannot begin with `alias/aws/`". UpdateAlias and DeleteAlias do not list it. | wrong code (probable) | `kms/actions_aliases.go` `aliasName`, `ownAliasName` | run | model (the code is listed; AWS's actual is memory: the foundation's A2 note and the old conformance test say `NotAuthorizedException`, unverified) | Handler tests; recording `reserved_alias_prefix`. |
| KMS-54 | P2 | The seventeen stubs answer HTTP 400 `UnsupportedOperationException` with no `X-Doze-Not-Built` header. Decision D5 is 501 `NotImplemented` with the header; the console's "Not built" state is read from the header. The model lists `UnsupportedOperationException` on five of the seventeen only (ImportKeyMaterial, DeleteImportedKeyMaterial, GetParametersForImport, ReplicateKey, UpdatePrimaryRegion). | wrong (contract) | `kms/actions.go` `stub` | run | model; protocol step 1; foundation B6 | The foundation's generic sweep (B6); a KMS case per stub. |
| KMS-55 | P2 | A refusal from Add alias or Repoint lands **inside** the form's one-line flex row and squeezes the input and the button to a sliver (the callout is a flex child). | n/a (console) | `templates/kms.html` the two `tag-row-form`s; `fail_inline` | run (screenshot, 2026-10-11) | n/a | Browser test: after a refused add, the input is at least 200 px wide and the callout is below the row. |

Owned by the foundation, seen here: the reserved-alias refusal
(`NotAuthorizedException`) is shown as Denied with "a policy decided this" (it is
shown as Refused today and carries foundation A2's line, `run`), and
the "because AWS does" line sits under hand-derived messages (A2); the colours
(A3); tags are written one call per tag though KMS takes a batch (A5; the
refused tag save also drops the draft, `run`); a refusal
from Rotate now in the header has no inline slot (A6, read); the "Select a key"
line is unreachable because `/kms` always opens the first key (A7); hard-coded
identity (A8, extended by KMS-1); the generator's drops (B8, KMS-42); the stub
contract (B6, KMS-54).

## 5. What to check

Beyond the common criteria. One protocol (`awsJson1_1`, `model`), so each line
is checked once, through SDK v2 unless it says otherwise.

**The wire (L5).** The model declares no `awsQuery` trait and no flattened
member, so there is no second decoder and no `xmlName` to honour. Checked
2026-10-11 (`run`): JSON 1.1 with the `TrentService.` target; a request with
`Content-Type: application/x-amz-json-1.0` is also served (more forgiving, no
real client sends it); a form-encoded Query request answers `MissingAction`
400; the error body carries `__type` (the shape name, `NotFoundException`), and
both `message` and `Message`. Request validation sees the whole request in the
shape the SDK sent it. Still to do: **one accepted-as-the-reference-spells-it
test per operation** (54), the way SQS's `TestEveryOperationIsAccepted...` does,
with the model's `examples` inputs where they exist.

**Create and describe**
- [ ] Every `KeySpec` and `KeyUsage` pair: works, or is refused with one code, per the tables in section 2.
- [ ] `KeyUsage` omitted: refused for every spec but SYMMETRIC_DEFAULT, or a listed forgiveness (KMS-6).
- [ ] `Description` 0 to 8192 (`model`); `Tags` at create; `Policy` at create is validated like PutKeyPolicy (KMS-3, KMS-35).
- [ ] `Origin`, `MultiRegion`, `CustomKeyStoreId`, `XksKeyId`: each is honoured or refused, never ignored.
- [ ] `KeyMetadata`: every member the model lists is present or deliberately absent (KMS-41); `AWSAccountId` and `Arn` follow the instance's identity; `DeletionDate` only while pending; `EncryptionAlgorithms` only for ENCRYPT_DECRYPT, `SigningAlgorithms` only for SIGN_VERIFY, `MacAlgorithms` only for GENERATE_VERIFY_MAC, `KeyAgreementAlgorithms` for KEY_AGREEMENT (`model`).
- [ ] A key is found by id, key ARN, alias name and alias ARN on the 13 members the model says accept them (below), and the other 28 (20 on implemented operations) are decided (KMS-40).
- [ ] An ARN naming another region, account or service is refused, not resolved by its suffix; a malformed one is `InvalidArnException` (KMS-39). *Corrected: "not established today" is now run.*
- [ ] `alias/aws/<service>` materialises on DescribeKey; whether on every operation stays is decided; its `KeyManager` is `AWS` (`model`, KMS-43).

*Key by alias name or alias ARN as well (`model`, member documentation, 13):*
Decrypt, DeriveSharedSecret, DescribeKey, Encrypt, GenerateDataKey,
GenerateDataKeyPair, GenerateDataKeyPairWithoutPlaintext,
GenerateDataKeyWithoutPlaintext, GetPublicKey, ReEncrypt (`SourceKeyId` and
`DestinationKeyId`), Sign, Verify. The three `AliasName` members (CreateAlias,
UpdateAlias, DeleteAlias) name an alias by definition. `GetKeyLastUsage`: "Alias
names are not supported". *Key ID or ARN only (`model`, 28, of which 20 are on
implemented operations):* CancelKeyDeletion, CreateAlias and UpdateAlias
(`TargetKeyId`), DisableKey, DisableKeyRotation, EnableKey, EnableKeyRotation,
GetKeyPolicy, GetKeyRotationStatus, ListAliases, ListKeyPolicies,
ListKeyRotations, ListResourceTags, PutKeyPolicy, RotateKeyOnDemand,
ScheduleKeyDeletion, TagResource, UntagResource, UpdateKeyDescription, VerifyMac
(these 20), and CreateGrant, DeleteImportedKeyMaterial, GetParametersForImport,
ImportKeyMaterial, ListGrants, ReplicateKey, RevokeGrant, UpdatePrimaryRegion
(the stubs). GenerateMac does not say.

**States** (the table the plan asked for, from each operation's `errors` list,
`model`; which *state* refuses which operation is the "Key states of KMS keys"
page, not read here)
- [ ] Operations that can answer `KMSInvalidStateException` (40): CancelKeyDeletion, CreateAlias, CreateGrant, Decrypt, DeleteAlias, DeleteImportedKeyMaterial, DeriveSharedSecret, DisableKey, DisableKeyRotation, EnableKey, EnableKeyRotation, Encrypt, GenerateDataKey, GenerateDataKeyPair, GenerateDataKeyPairWithoutPlaintext, GenerateDataKeyWithoutPlaintext, GenerateMac, GetKeyPolicy, GetKeyRotationStatus, GetParametersForImport, GetPublicKey, ImportKeyMaterial, ListGrants, ListKeyPolicies, ListKeyRotations, PutKeyPolicy, ReEncrypt, ReplicateKey, RetireGrant, RevokeGrant, RotateKeyOnDemand, ScheduleKeyDeletion, Sign, TagResource, UntagResource, UpdateAlias, UpdateKeyDescription, UpdatePrimaryRegion, Verify, VerifyMac. **Cannot** (so they work in every state): CreateKey, DescribeKey, GenerateRandom, GetKeyLastUsage, ListAliases, ListKeys, ListResourceTags.
- [ ] Operations that can answer `DisabledException` (19): CreateGrant, Decrypt, DeriveSharedSecret, DisableKeyRotation, EnableKeyRotation, Encrypt, GenerateDataKey, GenerateDataKeyPair, GenerateDataKeyPairWithoutPlaintext, GenerateDataKeyWithoutPlaintext, GenerateMac, GetPublicKey, ReEncrypt, ReplicateKey, RotateKeyOnDemand, Sign, UpdatePrimaryRegion, Verify, VerifyMac. So a Disabled key keeps accepting tags, aliases, policy, description and schedule (`model`).
- [ ] The state table: every dispatched operation against Enabled, Disabled and PendingDeletion, with the exact code.
- [ ] Disable then Enable keeps material: ciphertext from before decrypts after (model: DisableKey "temporarily prevents use").
- [ ] Schedule: default 30, 7 to 30 (the trait says 1 to 365), the response's `DeletionDate` and `PendingWindowInDays` and `KeyState`; scheduling twice.
- [ ] Cancel: lands Disabled (`model`); cancelling a key that is not pending.
- [ ] The janitor: at the deletion time the key and every alias pointing at it are gone (`model`: "including all aliases that refer to it"); a Decrypt of its ciphertext answers the code the recording gives (KMS-50); a restart during the window keeps the date (reproduced by hand, `run`). Uses `Options.Clock`.

**Symmetric encryption**
- [ ] Plaintext of exactly 1, 4096 and 4097 bytes; 0 is refused (`model`: 1 to 4 096).
- [ ] Encryption context: same pairs in another order decrypts; a missing, extra or changed pair is refused (`InvalidCiphertextException`, `model`); an empty context equals none.
- [ ] Decrypt without `KeyId`; with the right `KeyId`; with the wrong one (`IncorrectKeyException`, KMS-10); with an alias.
- [ ] A truncated, extended or bit-flipped blob is refused (`FuzzOpenBlob` exists; keep it and add fixed cases).
- [ ] ReEncrypt: source and destination contexts; destination disabled, of another usage, missing; `ReEncryptFrom` and `ReEncryptTo` are the actions checked (`model`).
- [ ] Ciphertext made before one, two and many rotations still decrypts; ciphertext made after uses the new material.

**RSA encryption**
- [ ] RSAES_OAEP_SHA_1 and SHA_256 round trip on each RSA size; the largest plaintext each accepts and one byte more: **RSA_2048 214 and 190, RSA_3072 342 and 318, RSA_4096 470 and 446** (`model`); Decrypt requires `KeyId` and the algorithm; ciphertext made outside with the exported public key decrypts here ("also decrypts ciphertext that was encrypted outside of KMS by the public key", `model`).

**Data keys**
- [ ] AES_128, AES_256, `NumberOfBytes` 1 and 1024; both; neither (KMS-11).
- [ ] The ciphertext of a data key decrypts to the plaintext returned; `WithoutPlaintext` returns none; `KeyMaterialId` present (KMS-41).
- [ ] Key pairs: each `KeyPairSpec` works or is refused with the stub code (the model lists nine); the public key is SPKI DER and matches the private key (PKCS8, `model`); the wrapped private key decrypts.
- [ ] A data key or key pair requested from a non-symmetric key answers `InvalidKeyUsageException` (the only fitting listed code, KMS-37) *Corrected: was "not established".*

**Sign, verify, MAC**
- [ ] Every signing algorithm on every key spec it belongs to, RAW and DIGEST; a DIGEST of the wrong length; an algorithm that does not belong to the spec (KMS-37). A message and its digest are the same message (`model`: "A message and its hash digest are considered to be the same message"), so a RAW signature verifies as DIGEST (reproduced, `run`).
- [ ] A signature made here verifies with the exported public key outside KMS (Go `crypto` in the test), and one made outside verifies here.
- [ ] A wrong signature is `KMSInvalidSignatureException`, not `SignatureValid: false` (`model`).
- [ ] Each HMAC spec with its one algorithm; a wrong algorithm; a wrong MAC is `KMSInvalidMacException` (`model`).
- [ ] `GetPublicKey` on a symmetric and an HMAC key: a 4xx with a listed code (KMS-36); on a disabled key `DisabledException` (`model`).
- [ ] Message of exactly 4096 and 4097 bytes on Sign and GenerateMac; the model's prose for Sign says "0-4096" and the trait says 1 (the trait is what is enforced; recording queue).

**Aliases**
- [ ] Name: `alias/` prefix required, the pattern `^[a-zA-Z0-9:/_-]+$` (colon included, `model`), 256 in all; `alias/aws/` cannot be created (`model`), repointed or deleted (KMS-53 for the code).
- [ ] Create on a name in use (`AlreadyExistsException`, 409); update and delete on a name that is not (`NotFoundException`).
- [ ] UpdateAlias across key types (KMS-9); an alias pointing at a pending key.
- [ ] ListAliases with and without `KeyId`, paged; entries carry `CreationDate` and `LastUpdatedDate` (KMS-41).

**Policy**
- [ ] Every malformed document in KMS-3 is refused at CreateKey and PutKeyPolicy; a statement with no `Action` or `Resource` is accepted and inert (KMS-35).
- [ ] The lockout check and its bypass flag (KMS-4).
- [ ] Under soft: a would-deny is logged and nothing is refused. Under enforce: an explicit Deny refuses the root for exactly the actions it names (reproduced, `run`); a policy without the root closes the gate (reproduced, `run`); with IAM off the policy is stored and returned.
- [ ] An identity policy grants only while the key policy admits the account root.
- [ ] The action checked for each operation, including Decrypt by ciphertext, ReEncrypt's two, and the alias operations; CreateKey with `Tags` needs `kms:TagResource` too (`model`; owed to IAM).
- [ ] `PolicyName` other than `default` (KMS-18).

**Rotation**
- [ ] Enable, disable, status, the period's range (90 to 2 560) and default (365), on symmetric keys; `UnsupportedOperationException` on asymmetric and HMAC keys for each of the **five** operations (the model lists it on Enable, Disable, GetStatus, ListKeyRotations and RotateKeyOnDemand), `DisabledException` on Enable, Disable and RotateKeyOnDemand for a Disabled key (KMS-38).
- [ ] On-demand rotation: the list entry's full shape, **the limit of 25** and `LimitExceededException`, a disabled key, `ConflictException` while one runs (KMS-45).
- [ ] The ledger and the console both say what the schedule does (KMS-46).

**Tags and lists**
- [ ] Key 1 to 128, value 0 to 256 (`model`), the reserved prefix and the per-key count (`memory`), a duplicate key in one call.
- [ ] Every list operation pages with `Limit` and `Marker`, with the model's defaults (KMS-7).

**Stubs**
- [ ] All 17 stubbed model operations answer the contract of decision D5 (KMS-54) and appear in `not-built.md`. The model lists `UnsupportedOperationException` on five of them only.
- [ ] `GetKeyLastUsage` and `VerifyMacForImport` are decided.

**Lifecycle**
- [ ] Everything above survives a restart: material, old material, rotations, state, deletion date, policy, tags, aliases (and, new, alias timestamps).
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

Today's page has no tabs: one scroll of stacked sections in two columns (left:
Key settings, Details, Key policy, Public key or Re-encrypt, Random bytes,
Description, Aliases, Tags; right: the usage's playground). The design groups
the same sections under five tabs (Operations, Configuration, Aliases, Key
policy, Tags). That grouping is **new** and needs either tab routes or in-page
tabs.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Kms-01-None | No keys yet | `kms_home`, `empty_detail`, `lp_empty` | exists; copy exact |
| Kms-Home | `/kms` with keys: the first key, Operations | `kmsKeys` to `kmsKey`, `kms_key_detail` | exists; alias title, row state and tabs are **new** |
| Kms-02-Create | Create, symmetric | `kms_create`, `kmsCreateKey` | form exists; teaching lines, the "what this key will be" strip, the usage table and the cli are **new** |
| Kms-03-Create-Signing | Create, RSA_2048 SIGN_VERIFY | `kms_create` | same |
| Kms-04-Create-Refused | Create refused | `fail_inline`, appended to the form | exists |
| Kms-05-Encrypt | Encrypt result, Decrypt this, Re-encrypt result | `kmsEncrypt`, `kmsDecrypt`, `kmsReEncrypt`; `kms_crypto_result`, `kms_out` | exists |
| Kms-06-Refused | Decrypt and Re-encrypt refused | `fail_inline` | exists; the encryption-context field is **new** |
| Kms-07-Sign-Verify | Sign, one-click verify, public key | `kmsSign`, `kmsVerify`, `kmsPublicKey`; `kms_sign_result`, `kms_verdict`, `kms_out` | exists |
| Kms-08-Mac-Random | MAC, verify MAC, random bytes | `kmsMac`, `kmsVerifyMac`, `kmsRandom` | exists |
| Kms-09-Config | Enabled, rotation, details, description | `kms_key_detail` (Key settings, Details, Description); `kmsToggleEnabled`, `kmsToggleRotation`, `kmsRotateNow`, `kmsDescription` | exists; the period field and rotation list are **new** |
| Kms-10-Aliases | Aliases: list, add, repoint, refusals | `kms_key_detail` (Aliases); `kmsAddAlias`, `kmsUpdateAlias`, `kmsDeleteAlias` | exists; a refusal squeezes the form (KMS-55) |
| Kms-11-Key-Policy | The policy builder | `policy_builder`, `kmsSavePolicy` | exists; the numbered explanation is **new** |
| Kms-12-Tags | Tags as a draft | `tag_editor`, `tagsSave` | exists (foundation A5): the draft model (Unsaved changes, Cancel, Save changes) is built; the refusal drops the draft |
| Kms-13-Confirms | Schedule deletion, remove alias, disable | `hx-confirm` on two; the Enabled switch | two exist as one-line questions; the Disable confirm, the waiting-period field, the dates and the bodies are **new** |
| Kms-14-Disabled | A disabled key | `kms_key_detail`, `fail_inline` | exists |
| Kms-15-Pending-Deletion | A key pending deletion | `kms_key_detail` (the banner), `kmsCancelDeletion` | exists; the date is **new** |
| Kms-16-Rsa-Encrypt | An RSA key with ENCRYPT_DECRYPT | `kms_key_detail` | the refusals exist (KMS-22); the algorithm select is **new** |
| Kms-17-Denied | Encrypt denied under enforce | `fail_inline` | exists (reproduced 2026-10-11, text matches) |
| Kms-18-Lockout | Locked out by a saved policy | `kmsSavePolicy`, `fail_inline` in the builder's slot | exists as a bad day (KMS-24); the inline denial is not what happens today |
| Kms-19-Fidelity | The fidelity dialog | `info_panel`, `svcInfo` | dialog and table exist; "What is real" is **new** (foundation D) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Tabs | The existing sections; no new data |
| Alias as the page title | `ListAliases`, already called by the console's `DescribeKey` (fixes KMS-20) |
| State in a list row | `KeyState`, already fetched per key by `ListKeys` (KMS-28) |
| Teaching lines on the create form and settings | The settings table (foundation B4): enums and ranges from `kms/validate.go`, tiers from the ledger. The spec-to-usage pairs come from `kms/crypto.go` `specs`, which B4's accessor must expose. |
| "What this key will be" and the usage table | The form's two selects and the same `specs` table. A pure function of the form. |
| CLI preview on create | The form's fields in the existing `cli` microformat: `kms create-key --key-spec $spec [--key-usage $usage] [--description $description]`, with the alias as a second command. **It must send `--key-usage` for every spec but SYMMETRIC_DEFAULT** (KMS-6), or say that it relies on doze-aws's default. |
| Byte count under a plaintext or message | The field's own value; for an RSA key the limit is the algorithm's (190, 214, 318, 342, 446, 470; `model`) |
| "Of the 7 keys, 2 can receive it" | Spec, usage and state of each key in the list |
| Encryption context on Encrypt and Decrypt | The `EncryptionContext` member both operations already take (KMS-26) |
| Algorithm select on an RSA encryption key | `EncryptionAlgorithms` in `KeyMetadata`; Decrypt then also sends `KeyId` (KMS-22) |
| Rotation period | `RotationPeriodInDays` on EnableKeyRotation and GetKeyRotationStatus |
| Rotation list | `ListKeyRotations`, already read into `Key.Rotations`; with the model's `KeyMaterialState` once KMS-41 lands |
| Deletion date and time left | `DeletionDate` in `KeyMetadata` |
| Waiting period on the confirm | `PendingWindowInDays` on ScheduleKeyDeletion |
| Disable confirm | None; a question in front of the existing switch |
| "What this policy does", five lines | The stored document (statement count), the ledger row, the IAM mode the console already shows |
| "What is real" in the fidelity dialog | "Differences from AWS" in `SUPPORT.md` (foundation D) |
| Bypass-lockout choice on Save key policy | `BypassPolicyLockoutSafetyCheck` on PutKeyPolicy (KMS-4); needed the moment the safety check exists, or the lockout board cannot be reached |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Spec and usage are fixed at creation | `model` (CreateKey: "You can't change the `KeyUsage` value after the KMS key is created", "You can't change the `KeySpec` after the KMS key is created"). *Corrected 2026-10-11: was doc.* |
| Which usage each spec allows; the default for RSA | `kms/crypto.go` `specs`, `defaultUsageFor`; held by KMS-6's test. AWS has no default for RSA (`model`): the board says so. |
| "7 offered; also over the API: P521, HMAC_224/384/512" | `kms_create`'s options against `specs` |
| Wrong usage is `InvalidKeyUsageException` | `requireUsage`, run; `model` |
| Alias: characters, 250 after the prefix, `alias/aws/` reserved | model (`AliasName` pattern, colon included, and length), `ownAliasName` |
| Description 0 to 8192; tags 1 to 128 and 0 to 256 | model |
| "The ciphertext carries the key id, so Decrypt needs none" | `seal`, `openBlob`; ledger; `model` ("KMS can get this information from metadata that it adds to the symmetric ciphertext blob") |
| ~~"1 to 4 096 on AWS, refused above 3 072 here"~~ | *Corrected 2026-10-11:* **deleted.** KMS-2 is fixed (B7). The boards that carried it were corrected in the working copy. |
| "Opens only ciphertext this doze-aws sealed" | `openBlob`'s magic check, run |
| Re-encrypt: "the plaintext never leaves the service" | the handler's own note; true of the wire, since `reEncrypt` decrypts and encrypts in process; `model` ("reencrypts it entirely within KMS") |
| Signed as RAW; six algorithms on RSA, one per curve on ECC | `client_sec.go` `KMSSign`; `signingAlgorithms`; `model` for RAW hashing ("begins with a hash function") and the RSA six; one per curve is memory |
| "A MAC can only be verified by calling this key"; constant-time | the template's own reasoning; ledger |
| Random: 1 to 1024, else 32 | `kmsRandom`, run (KMS-31 changes it); `model` for the range |
| Rotation: 90 to 2560, default 365, symmetric only, stored and not run | model, `defaultRotationDays`, `setRotation`, ledger |
| Deletion window 7 to 30 | `scheduleKeyDeletion`, run; `model` prose (trait 1 to 365) |
| Cancelled keys land Disabled | `cancelKeyDeletion`, run, ledger; `model`. *Corrected: was doc.* |
| Key policy: one named `default`, gates identity policies, soft and enforce, 131 072 characters | `listKeyPolicies`, `iamguard`, ledger, model |
| RSA encryption needs `KeyId` on Decrypt | ledger, `decrypt`; `model` ("you must specify the KMS key and the encryption algorithm") |

**Statements on the boards not yet backed, or now known wrong.** Fix or remove
each before the board ships.

- Kms-13: "every operation on it is refused as KMSInvalidStateException".
  False today: description, aliases, tags, policy and the rotation flag still
  succeed on a pending key (KMS-8). True of the cryptographic operations.
  *Corrected in the working copy 2026-10-11:* "every cryptographic operation".
- Kms-16: Rotate now's refusal is drawn inline. Read from `shell.js`, a header
  button has no slot, so today it would be a toast (foundation A6). Run
  2026-10-11: the refusal appears as the page's refusal card at the top of the
  detail pane, not in a slot beside the button.
- Kms-15: the Enabled switch's refusal is drawn at the top of its card. Read
  from the same ladder, not checked in a browser.
- Kms-18: "restart with `--iam-mode soft` and Save key policy works again". The
  key's page under soft was run; PutKeyPolicy under soft was read, not run. And
  the board's second step (an inline denial in the builder's slot at the save)
  does not happen (KMS-24); *corrected in the working copy:* the denial is the
  next read. And AWS refuses such a policy without `BypassPolicyLockoutSafetyCheck`
  (KMS-4); *added in the working copy.*
- Kms-06: "the key this ciphertext was encrypted under no longer exists" as an
  `InvalidCiphertextException` AWS gives is not established (KMS-50); *a line
  saying so was added to the board.* "SYMMETRIC_DEFAULT is not valid for key spec
  RSA_3072" is `InvalidKeyUsageException` in the model (KMS-37); *the board's
  code was corrected* (the service still answers `ValidationException`).
- Kms-06 and Kms-10: "the aliases are removed with the key" is `model`;
  "the key ... no longer exists" is read from `SweepDeletions` and `decrypt`;
  the janitor test in section 5 holds them.
- Kms-13: the "got 3" refusal was run over the API; the field it sits beside is new.
- Every board carries the "doze-aws refuses this because AWS does" line under
  hand-derived messages. That line is foundation A2's to replace (the page's
  own refusal card already carries its replacement: "doze-aws refused the
  request: it breaks a rule of the service", `run`).
- Kms-02, Kms-03: "it cannot change after creation" is now `model`, not doc.
  "Default for spec" for RSA is doze-aws's convenience, not AWS's (KMS-6):
  *both boards corrected.*
- Kms-07: "the service hashes the message with the algorithm's SHA" is true of
  doze-aws (`digest`) and is `model` for AWS (RAW "begins with a hash function";
  "The hashing algorithm that `Sign` uses is based on the `SigningAlgorithm`").
- Kms-10: "A call can name the key by id, ARN, alias name or alias ARN" is true
  of some operations only (KMS-40); *corrected in the working copy.*
- Kms-14: "Rotate now ... KMSInvalidStateException · key is not enabled" is the
  service's answer today and not the model's (`DisabledException`, KMS-38);
  *the board shows the model's code.*
- Kms-19: "Stored and returned only under the default off" was a copy of a ledger
  sentence that is itself wrong (KMS-34); *corrected on the board.*

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each of the 20 boards (the working copy of
`console-design/project/Kms-*.dc.html`, 1440 by 900, light; dark for the
design's Home, 02, 03, 09, 14, 15, 16 and for the page's Home, create and
signing-key views; 900 wide for the page's Home and create) and the real page in the equivalent
state (the e2e instance, `E2E_PORT=14802`, a second instance under
`--iam-mode enforce` for 17 and 18), and **looking at each pair**. Screenshots
are in `scratchpad/dcheck/kms/` (`design-*.png`, `real-*.png`; the page is one
scroll, so a `-full` render is beside each). The page's shell (the rail with
labels, "KMS (n)" with a filter in the list header, the top bar's region
selector) is the foundation's and differs from the boards' marks-only rail on
every board; it is not repeated below, as in the SQS table.

| Board | Verdict | What differs |
|---|---|---|
| Kms-01 None | matches | Copy is `copy.go`'s and exact; one CTA. |
| Kms-Home | differs | (1) No tabs: five sections stacked in one scroll in two columns (settings left, Encrypt/Decrypt in one panel on the right) where the board has Encrypt and Decrypt left, Re-encrypt and Random bytes right under an Operations tab. (2) The title is the key id, not the alias (KMS-20). (3) The state and the two chips (`SYMMETRIC_DEFAULT`, `ENCRYPT_DECRYPT`) beside the title are a status pill and a Details table. (4) Rows carry no state (`disabled`, `deleting`) (KMS-28). (5) None of the teaching lines under Encrypt/Decrypt/Re-encrypt: the byte count, "sealed under this key, the ciphertext carries the key id", "opens only ciphertext this doze-aws sealed", "of the 7 keys, 2 can receive it" (KMS-25), "1 – 1 024 · anything else becomes 32". (6) The `cli` is a collapsed chip with no command shown. (7) The Re-encrypt destination offers every key, by id first. |
| Kms-02 Create | differs | The real form is one 660 px column of two panels with no teaching lines: none of the five lines on Key spec, Usage, Alias or Description; no right-hand "What this key will be" panel (Material, Answers, Rotates, Refuses) and no usage table; no cli; the alias input has no `alias/` prefix; the subtitle reads "Symmetric keys encrypt/decrypt; RSA/ECC sign/verify; HMAC generate MACs." (KMS-30). Both selects are native, with no live behaviour. The board's 7-of-11 line is true (the form offers 7). |
| Kms-03 Create signing | differs | As Kms-02, and the strip must redraw as the selects change (nothing on the page does that today), including the RSA "either job" sentence. |
| Kms-04 Create refused | differs | The shape is right (the refusal at the end of the form, what you typed kept; verified `ValidationException`, same message). Missing: the neighbour line under Usage ("SYMMETRIC_DEFAULT allows ENCRYPT_DECRYPT only"). The board's last card describes KMS-5 (a refused alias leaves a key behind); verified (`run`), so it is true and is a bug: when KMS-5 is fixed that card changes. The footer line is foundation A2's. |
| Kms-05 Encrypt | differs | Result slots: the board puts the ciphertext under Encrypt with "Decrypt this →" and the plaintext in the same slot afterwards; the page puts one `#kms-crypto-out` under both forms in the right panel, and the Re-encrypt result in a block under Random bytes at the foot of the left column (`#kms-op-out`), not under Re-encrypt. No byte count; no expanded cli command. |
| Kms-06 Refused | differs | The encryption-context field on Decrypt and Re-encrypt is **new** (KMS-26). The refusal text matches (`ciphertext is malformed or was not produced by this KMS`, `CiphertextBlob is not valid base64` verified). Re-encrypt's refusal lands under its own button, inside its panel (verified), and its success result in the foot slot under Random bytes. |
| Kms-07 Sign/Verify | differs | The content is close: message, algorithm select, Sign →, signature with copy, "Verify this signature →", verdict card (verified). Differs in: no byte count, no "signed as RAW" line, no algorithm teaching sentence; the Public key panel's result lands under Random bytes (the shared `#kms-op-out`) instead of under the Public key panel; no tabs. The default algorithm is `RSASSA_PSS_SHA_256`, the board shows `RSASSA_PKCS1_V1_5_SHA_256`. |
| Kms-08 MAC/Random | differs | HMAC panel content matches (message, algorithm, Generate MAC →, Verify this MAC →, verdict). Missing: byte count, "one per key" sentence, the "constant-time" paragraph, the Random bytes range line; Random's result lands in the foot slot. |
| Kms-09 Config | differs | The board is a two-column Configuration tab: an Enabled card with a sentence on what disabling does, an Automatic rotation card with a **new** period field, a "Stored here, not run" callout and a **new** rotation table; Details and Description on the right. The page has the two switches with their one-line descriptions in one panel, one "Rotated on demand 2 times, last …" note, the Details table below, and the Description under Random bytes. |
| Kms-10 Aliases | differs | Rows: the page's alias rows are bold mono names with a red bar and a trash icon; the board's are plain rows with a Remove button. The add form has no teaching line. **A refusal lands inside the one-line form and squeezes the input to a sliver** (KMS-55, `run`, both Add and Repoint). The board's "When the key goes" and "Where an alias is shown" cards are annotations. |
| Kms-11 Key policy | differs | The builder is the shared component and matches. The page keeps it closed behind "Edit" in a "Resource policy" card inside the one scroll; the board has it open as a tab, with a five-line "What this policy does" panel (**new**) and a "document the service holds" panel (**new**). The two refusal states are the shared ones. |
| Kms-12 Tags | differs | The draft model matches (rows, Add tag, Unsaved changes, Cancel, Save changes). Missing: the "What a tag is here" block and the limits line. The refusal's text matches (`tags.1.tagKey`) and it appears above the editor, which is then **empty ("No tags yet.") with a message "Nothing was changed. Refused: writing "xxx…""**: the draft does not stay as typed, which the board says it does (foundation A5). |
| Kms-13 Confirms | differs | Schedule deletion is a one-sentence question ("Schedule this key for deletion? Ciphertexts become undecryptable after the window.") with Cancel and Schedule; the board has the key's name, a body, a **new** waiting-period field with its refusal, the deletion date, and Keep it / Schedule deletion. Remove alias is a one-line question with no body. Disable has **no confirm** (the switch posts at once). After scheduling, the page lands on the list's first key (KMS-27), as the board says. |
| Kms-14 Disabled | differs | The refusal on Encrypt is the same card (`DisabledException`, "key … is disabled"). Missing: the "nothing is lost" explanation and the list of what is refused and what still works (annotation). The page also offers Rotate now on a Disabled key. |
| Kms-15 Pending deletion | differs | The banner and Cancel deletion exist. Missing: the date and time left (**new**, `DeletionDate`), tabs, the "lands Disabled" sentence beside Cancel; the toast after cancelling still says "the key is usable again" (KMS-21). |
| Kms-16 RSA encrypt | differs | The algorithm select is **new**; the page sends none and shows the refusal on every Encrypt (`SYMMETRIC_DEFAULT is not valid for key spec RSA_3072`, `ValidationException`; the model's code is `InvalidKeyUsageException`, KMS-37). Rotate now and the rotation switch are offered and refused (KMS-22, verified). |
| Kms-17 Denied | matches | Text verified word for word under enforce: "User: arn:aws:iam::000000000000:root is not authorized to perform: kms:Encrypt on resource: arn:aws:kms:us-east-1:000000000000:key/… because the resource policy denies it (DenyConsoleEncrypt)", Denied vocabulary, "Access log →". The board's right-hand "key policy behind it" panels are annotation; the tabs differ as everywhere. |
| Kms-18 Lockout | cannot match yet | Waits on KMS-24 (the save must say what it did and the list must keep the key, marked unreadable) and KMS-4 (the safety check exists, so the console needs the bypass choice before this state is reachable). Today: the toast says saved, the page re-renders normally, the key vanishes from the list on the next load, and opening it shows the generic "That could not be opened" page with the Denied callout. |
| Kms-19 Fidelity | differs | A dialog in both. The page's is narrower, with the table only; the board's has the **new** right-hand "What is real" column (foundation D). Counts match (18 functional, 2 stub). |

**Where the design is wrong** (corrected in the working copy on 2026-10-11 in
the files named; the published canvas still carries the old text until it is
republished; each is an edit of a string, no layout moved)
- "1 – 4 096 on AWS · refused above 3 072 here": KMS-2 was fixed after the
  board was drawn; the page accepts 4 096. *Kms-Home, Kms-05, Kms-07, Kms-08.*
- "Default for spec picks ENCRYPT_DECRYPT" stated as if AWS did: the model says
  KeyUsage "is required" for everything but a symmetric key. *Kms-02, Kms-03.*
- "letters, digits, / _ -": the model's pattern also allows ":" (KMS-51).
  *Kms-02, Kms-03, Kms-10.*
- "A call can name the key by id, ARN, alias name or alias ARN": true of 13
  members, not of the 28 that document id or ARN only (KMS-40). *Kms-10.*
- "every operation on it is refused as KMSInvalidStateException" for a key
  pending deletion. *Kms-13.*
- A refusal drawn as `ValidationException` for a key and algorithm mismatch,
  where the model's code is `InvalidKeyUsageException` (KMS-37). *Kms-06,
  Kms-16.*
- "Rotate now ... KMSInvalidStateException" for a Disabled key: the model's
  code is `DisabledException` (KMS-38). *Kms-14.*
- The lockout reached by a plain Save: AWS needs `BypassPolicyLockoutSafetyCheck`
  (KMS-4); and the denial is the next read, not the save's response (KMS-24).
  *Kms-18.*
- "Stored and returned only under the default off": the default is soft.
  *Kms-19.*
- "Padding, then hash": the name lists padding first; the algorithm hashes
  first. *Kms-07.*
- `InvalidCiphertextException` for a deleted key's ciphertext drawn as
  established. *Kms-06 (a sentence added).*
- Not edited, listed for the integrator: the usage table in Kms-02 and Kms-03
  truncates "RSA_2048 · 3072 · 4096" to "RSA_2048 · 3072 · 40…" in its first
  column (a `white-space: normal` on that span); a layout fault, not a fact.

**Build list, in order** (all open; items 1 and 2 are behaviour, not styling)
1. Alias in the title and row state in the list (KMS-20, KMS-28); the
   `DescribeKey` client already calls `ListAliases`.
2. The refusal and lockout fixes the boards assume: KMS-5 (no orphan key),
   KMS-23 (a verdict that names a refusal), KMS-24 (unreadable key stays in the
   list; the save says what it did), KMS-55 (a refusal below the row).
3. Teaching line 4 on every setting: enforced or stored, from the settings
   record (kit `setting_field`); line 5 per section 12, L11.
4. The create page: the five lines, the live "What this key will be" strip and
   usage table (a pure function of the two selects and `specs`), the cli, the
   alias prefix, the neighbour line under Usage.
5. Five tabs (Operations, Configuration, Aliases, Key policy, Tags) with the
   board's two-column Operations and Configuration, and the result slots under
   the control that produced them (Kms-05, Kms-07, Kms-08).
6. The Configuration tab's period field, "Stored here, not run" callout and
   rotation table (needs KMS-41's entry members to be honest, and the decision
   on KMS-46).
7. Confirms in the board's shape: the key's name, the body, the waiting-period
   field and its refusal, the date, "Keep it"; the alias confirm's body; a
   Disable confirm. The cancel toast names the state the key is in (KMS-21).
8. The pending page's date and "lands Disabled" sentence (`DeletionDate`).
9. The encryption-context field on Encrypt and Decrypt (KMS-26) and the
   algorithm select on an RSA encryption key (KMS-22, KMS-37 first).
10. The key policy tab's "What this policy does" and "document the service
    holds" panels; the bypass choice once KMS-4 lands.
11. The fidelity dialog's "What is real" column (foundation D).
12. Dark and 900 px re-checks of the create page, Operations and Configuration
    (both render today: dark is fine, at 900 the rail collapses and a UUID title
    wraps to three lines, which the alias title fixes).

## 7. Tests to add

| Layer | Tests |
|---|---|
| Shared validation | ~~Blob length at the byte limit, in `internal/modelcheck`~~ (landed with B7); list lengths and `\u` patterns in the generator (B8, KMS-42) |
| Store, handler | KMS has none today. Add `kms/store_test.go` and `kms/actions_test.go`: policy validation (KMS-3, KMS-35, KMS-4), the state table (KMS-8, KMS-38), `IncorrectKeyException` (KMS-10), data-key arguments (KMS-11), tags (KMS-12), rotation limit of 25 and entry shape (KMS-13, KMS-41), every enum value (KMS-15), create switches (KMS-16), DryRun (KMS-17), `PolicyName` (KMS-18), ARN forms (KMS-39), key and algorithm codes (KMS-36, KMS-37), alias codes (KMS-53), the janitor under a fake clock |
| SDK v2 | Paging on five lists (KMS-7); UpdateAlias across types (KMS-9); sign here and verify outside, and the reverse; OAEP encrypted outside and decrypted here; every algorithm on every spec; the identity run (KMS-1); the on-demand rotation delay (KMS-45) |
| Wire | One accepted-as-the-reference-spells-it test per operation (54) from the model's `examples` where they exist |
| Guard | Soft and enforce, default policy, explicit Deny, no root statement, a statement with no Resource, an identity policy with and without the gate, the action per operation |
| Rejection parity | Regenerate `cases_kms.json` after B8 (472 cases against 263); `GetKeyLastUsage` (KMS-14); the 20 missing constraints (KMS-42) |
| Restart | Write, restart, read: material, old material, rotations, state, deletion date, policy, tags, aliases, alias timestamps (foundation B5) |
| Provision | RSA default usage (KMS-6); a key with a policy, disabled, with a rotation period; the CloudFormation properties in section 9 |
| Conformance (boto3) | One scenario per row of section 8, added to the 5 that exist |
| Console (Go) | Orphan key (KMS-5); heading (KMS-20); verdicts (KMS-23); the list under a locked key (KMS-24); random (KMS-31) |
| Browser | RSA encryption key end to end (KMS-22); encryption context (KMS-26); schedule with a window and the landing page (KMS-27); cancel then enable (KMS-21); a refused alias add keeps the input (KMS-55); each `new` item; each confirm's sentence; the five tabs by keyboard; the screenshots of 6a re-taken |
| Mutation | `kms/` baseline, then survivors in `crypto.go`, `store.go`, `guard.go` |

## 8. Recording queue

Every one of these is `doc`, `model` where the rule is the model's and the
*code* is not, or `memory`. Re-tagged 2026-10-11; "Source now" is the rule's
source, then the code's.

| Claim | Source now | Scenario |
|---|---|---|
| Key spec and key usage cannot be changed after creation | model (was doc) | `test_kms::spec_and_usage_are_fixed` |
| `KeyUsage` omitted for an RSA, ECC or HMAC spec; the code | model for the rule, memory for the code (was memory) | `usage_required` |
| Default `KeyUsage` per spec through CloudFormation (`AWS::KMS::Key`) | memory | `default_usage_per_spec` |
| Which operations each key state refuses, and the code | model for which operations can (their `errors`); doc for the states table, not read; memory for the rows | `state_table` |
| ScheduleKeyDeletion on a pending key | memory | `schedule_twice` |
| A cancelled key is Disabled | model (was doc) | `cancel_lands_disabled` (in `test_key_states` today, unverified) |
| UpdateAlias needs the same type and usage; its code | model for the rule, code not named (was memory) | `update_alias_across_types` |
| Creating `alias/aws/…`: `InvalidAliasNameException` or `NotAuthorizedException` | the model lists the first only; memory | `reserved_alias_prefix` |
| Operations that refuse an alias as `KeyId`, and with what | model for which ones document aliases (13 yes, 28 no); memory for the behaviour (was memory) | `alias_not_accepted` |
| Decrypt and ReEncrypt with a `KeyId` that is not the ciphertext's key: `IncorrectKeyException` | model (was doc) | `decrypt_incorrect_key` |
| Wrong encryption context: `InvalidCiphertextException` | model (was doc) | `wrong_context` |
| GenerateDataKey with both or neither of `KeySpec` and `NumberOfBytes`; the code | model for the rule, memory for the code (was memory) | `data_key_arguments` |
| A malformed key policy: which documents, which code; a statement with no Action or Resource is accepted | model (was doc) | `malformed_policy` |
| The lockout safety check and `BypassPolicyLockoutSafetyCheck`; the code | model for the rule (was doc), memory for the code | `policy_lockout_check` |
| `GetKeyPolicy` and `PutKeyPolicy` with a name other than `default`; the code | model for the rule (was memory), memory for the code | `policy_name` |
| Tag key beginning `aws:`; more than 50 tags; a duplicate key in one call | memory | `tag_rules` |
| On-demand rotation limit of **25** and `LimitExceededException`; the list entry's shape | model (was "10, memory") | `on_demand_rotation_limit` |
| On-demand rotation is asynchronous: the in-progress field, the delay, `ConflictException` | model (new) | `on_demand_rotation_async` |
| Rotation operations on an asymmetric or HMAC key; GetKeyRotationStatus on a pending key is false; Disabled gives `DisabledException` | model (was memory) | `rotation_on_asymmetric`, `rotation_states` |
| `GetPublicKey` on a symmetric key: `UnsupportedOperationException` or `InvalidKeyUsageException` | model lists both; memory | `public_key_of_symmetric` |
| Data key from a non-symmetric key: `InvalidKeyUsageException` | model (the only listed code that fits); message memory (was memory) | `data_key_from_rsa` |
| ECDSA algorithm on a curve other than its own | memory | `ecdsa_curve_mismatch` |
| RAW messages are hashed by KMS; DIGEST length rule; a message and its digest are one message | model (was doc) | `message_types` |
| `Origin: EXTERNAL` gives `PendingImport` | model (ImportKeyMaterial: "its `KeyState` must be `PendingImport`") (was doc) | `origin_external` (record only; costs nothing extra) |
| `DryRun` answers `DryRunOperationException` (412) | model (was doc) | `dry_run` |
| An AWS-managed key's `KeyManager` (`AWS`), and what may be done to it | model for both (was memory) | `managed_key` |
| The code an SDK sees for an operation a region does not serve (for the stub code) | memory | `unsupported_operation_code` |
| Default page size (100, 50) and the marker; `InvalidMarkerException` | model (was doc) | `list_paging` |
| `Limit` above 100 on ListAliases; above 50 on ListResourceTags (trait 1 000) | model, two statements that conflict | `list_limit_prose` |
| `PendingWindowInDays` 1 to 6 and 31 to 365 (trait 1 to 365, prose 7 to 30) | model, two statements that conflict | `pending_window_range` |
| A key policy over the limit: `ValidationException` or `LimitExceededException` | model, two statements that conflict (KMS-49) | `policy_length` |
| Decrypt of ciphertext of a deleted key: `NotFoundException` or `InvalidCiphertextException` | model lists both (KMS-50) | `decrypt_deleted_key` |
| The HTTP status of each error (404, 409, 412, 503) | model (`httpError`); the wire memory | `error_status` |
| A principal in a key policy that does not exist | model (the sentence); the code memory | `policy_invalid_principal` |
| An invalid base64 `CiphertextBlob` or `Plaintext` | memory (here `ValidationException`) | `invalid_base64` |
| `Recipient` with an invalid attestation document | model (the field); the code memory | `recipient_invalid` |

Keys cost a prorated dollar a month and cannot be deleted in under seven days;
`test_kms.py`'s header already accounts for it. Recordings leave keys pending.

## 9. Owed to integration

| What | With |
|---|---|
| A key and alias declared in a template. *Established from `internal/cfn/mapper.go` `key` (read 2026-10-11):* the mapper reads `Description`, `EnableKeyRotation`, `KeySpec`, `KeyUsage` and `Tags`. It does **not** read `KeyPolicy`, `Enabled`, `RotationPeriodInDays`, `PendingWindowInDays`, `MultiRegion`, `Origin`, `EnableKeyRotation`'s period or `PendingWindowInDays`; `AWS::KMS::Alias` renames the key it targets. Every unread property is silently dropped. | CloudFormation |
| *Corrected 2026-10-11:* "A key with no alias cannot be declared". Read: the mapper stores a key under its logical id and `applyKeys` creates `alias/<LogicalId>`, so it is declared, under an alias the template did not ask for (and `exportKeys` skips `alias/aws/`). | CloudFormation |
| Export and re-apply round trip of a key with a policy (the export reads spec, usage, description, rotation and tags only) | CloudFormation |
| A stream whose key is disabled or pending (4 tests exist) | Kinesis |
| Secrets, parameters, queues, topics, buckets and log groups record a `KmsKeyId` and never call KMS. Decide per service whether an unknown or disabled key is refused, as Kinesis does. | Secrets Manager, Parameter Store, SQS, SNS, S3, Logs |
| `kms:ViaService` and the other KMS condition keys in a key policy | IAM |
| An identity policy granting `kms:Decrypt` to a user, with the key policy's root statement present and absent | IAM |
| `CreateKey` with `Tags` needs `kms:TagResource` as well (the model's "Required permissions") | IAM |
| The principals of a key policy must exist (KMS-52), with an opt-in strict switch | IAM |
| The access log's two rows (identity, resource) for a KMS call, and the decision headers | IAM |
| Envelope encryption end to end: a function generates a data key, stores ciphertext, another decrypts | Lambda |
| The wire showing a Decrypt with the key resolved from the ciphertext | console, wire |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] KMS-1 to KMS-55 each closed, with the test that failed first named beside it (KMS-2 is closed already, by B7; its remaining parity cases are listed in the row).
- [ ] The shared policy validator is wired with Action and Resource left out of its refusals (KMS-35), and the guard ignores a statement without them.
- [ ] Decided and written into `SUPPORT.md` and `not-built.md`: `GetKeyLastUsage`; `VerifyMacForImport`; `ECC_NIST_EDWARDS25519`, ML-DSA and `DeriveSharedSecret` (build or decline, with the lightness measurement); `MultiRegion`; `DryRun`; `DryRunModifiers` and `Recipient`; whether the lockout stays reproducible only with the bypass flag; the stub contract (D5).
- [ ] The tables in section 2 have no "not established" and no "to decide" left: `KeyPairSpec` values run, the ECDSA curve rule, `MultiRegion`, `DryRunModifiers`, `Recipient`.
- [ ] Every enum value in the model for spec, usage, algorithm, message type and origin either works or answers the stub code and has a ledger row. One table test holds it.
- [ ] The state table test exists with every row asserting a code; rows resting on memory are skipped with the scenario named.
- [ ] Section 5: an ARN for another region or account; an alias as `KeyId` per operation (the 28); AWS-managed keys; `GetPublicKey` on a symmetric key; a data key from an RSA key; the stub code. Each is established and ticked, or on the recording queue with its test written and skipped.
- [ ] Every box in section 5 ticked, or moved the same way.
- [ ] `kms/` has store and handler tests, not only SDK tests.
- [ ] The package's tests and the KMS browser spec pass under `--account-id 111122223333 --region eu-west-1 --iam-mode enforce`.
- [ ] A write-restart-read test covers every field of the stored key and the alias bucket.
- [ ] The CloudFormation properties table (section 9) is turned into tests or into listed differences, even though the fix is CloudFormation's.
- [ ] **The fidelity table in 6a exists, was made from rendered boards beside rendered pages, and no row says "differs" without a decision beside it** (build, or a deliberate deviation with its reason). Re-rendered after the build, in light and dark and at 900 wide.
- [ ] **The per-service AWS icon stays** in the rail, the list header and the key header; where a board draws "KM" it is the placeholder for it.
- [ ] **Every setting carries the five teaching lines** (name and unit; the value in words; what it does; range, default and enforced or stored; the neighbour check with real numbers), generated from the validation table and the ledger: KeySpec, KeyUsage, Alias, Description, Tags, the rotation switch and period, the deletion window, the policy, the encryption context, the plaintext and message fields.
- [ ] **Both wires:** the service speaks one protocol (`awsJson1_1`); one accepted-as-the-reference-spells-it test per operation (54) exists. N/A for a second decoder, with this sentence as the reason.
- [ ] **Each asynchronous or time-based operation is decided (L7, L8)** and the decision is in `SUPPORT.md`: on-demand rotation (KMS-45), the automatic schedule (KMS-46), the deletion window, key-usage tracking (KMS-14), eventual consistency, and the opt-in strictness (KMS-52).
- [ ] No statement on a KMS screen is in the "not yet backed" list in section 6. The "refused above 3 072 here" line does not exist anywhere (the four boards are corrected in the working copy; the published canvas is republished).
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
| 0 | **First act of the stage:** read section 12's decisions with the owner; settle 1, 2, 3, 6, 7, 8 before any code | this page |
| 1 | Contract: the tables in section 2, the decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for KMS-1 to KMS-19 and KMS-35 to KMS-55 | `kms/store_test.go` and `kms/actions_test.go` (new), `kms/sdk_test.go`, `kms/rejection_parity_test.go`, `internal/modelcheck/*_test.go`, `internal/provision/*_test.go` |
| 3 | Validation: B8 (list lengths, `\u` patterns), regenerate the cases (472) | `cmd/dzaudit/cases.go`, `kms/validate.go`, `kms/testdata/cases_kms.json` (regenerated with `dzaudit cases kms`) |
| 4 | Identity | `kms/actions_policy.go`, `kms/actions_keys.go` |
| 5 | Policy validation (B9 without NoAction and NoResource), the lockout check, the guard's inert statements | `kms/actions_policy.go`, `kms/actions_keys.go`, `kms/guard.go`, `internal/iampolicy` |
| 6 | Error classes and codes: no 5xx for a caller's mistake, `InvalidKeyUsageException`, `InvalidArnException`, `DisabledException`, the stub contract | `kms/crypto.go`, `kms/store.go`, `kms/actions.go` |
| 7 | Create switches, enum values, stubs, `GetKeyLastUsage`, the stdlib crypto (if decided) | `kms/crypto.go`, `kms/store.go`, `kms/actions.go`, `kms/coverage_model_test.go` |
| 8 | States, incorrect key, data-key arguments, aliases, tags, rotation (limit 25, async, status), DryRun, policy name, managed keys | `kms/actions_keys.go`, `kms/actions_crypto.go`, `kms/actions_aliases.go`, `kms/store.go`, `kms/kms.go` |
| 9 | Paging, markers, response members (`KeyMaterialId`, alias timestamps, rotation entries) | `kms/actions_keys.go`, `kms/actions_aliases.go`, `kms/actions_policy.go`, `kms/store.go` |
| 10 | Provision default usage | `internal/provision/svc_kms.go`, `internal/cfn/emit_kms.go` |
| 11 | Section 5 sweep, restart test, guard tests | `kms/*_test.go` |
| 12 | Conformance scenarios and claims table | `conformance/tests/test_kms.py`, the claims file from foundation B1 |
| 13 | Console, in the order of the build list in 6a | `internal/console/templates/kms.html`, `create.html` (`kms_create`), `handlers_sec.go` (`kms*`), `client_sec.go`, `routes_config.go`, `testdata/routes.txt` |
| 14 | Browser tests | `e2e/tests/kms.spec.ts` |
| 15 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 3 and 4 touch shared ground (the generator, and the identity rule in
foundation A8). If the foundation has not already taken them, they are done
first here and noted in the foundation's document.

**Prior art.** AWS's "Key states of KMS keys" page is a table of operation
against state, which is the shape of the state-table test in section 5; each
row's test names that page (not read for this review: the model's error lists
are what section 5 uses). The KMS API reference for PutKeyPolicy describes
the lockout safety check and its bypass flag (KMS-4). Nothing is claimed here
about how other emulators handle these.

## 12. Pre-stage review (2026-10-11)

Read for this review: `.audit-models/kms.json` in full as text (all 54
operations' documentation, every member of their input and output shapes, the
`KeyMetadata`, list-entry, `Tag`, `RecipientInfo`, `MultiRegion*` and
`KeyLastUsageData` structures, all 48 error shapes with their `httpError` and
`awsQueryError`, the 30 enums, the plain shapes' traits, the `examples` of
every operation); `kms/*.go` (all non-test files), `internal/console`'s KMS
handlers, client, templates and routes, `internal/provision/svc_kms.go`,
`internal/cfn` (the `key` and `keyAlias` mapper, `emit_kms.go`),
`internal/iampolicy/validate.go`, `docs/SUPPORT.md`'s KMS section and
`docs/not-built.md`; ran a binary built from this tree (about 130 requests, two
instances: default and `--iam-mode enforce --account-id 111122223333`) and the
real console under Playwright; rendered the 20 boards.

| Q | Answer |
|---|---|
| **L1** model text | Every number, range, default, limit, name and source tag in this page was checked; about 50 claims corrected or re-tagged: 12 factual corrections (KMS-2 closed, KMS-13 25 not 10, KMS-16 MultiRegion not echoed, KMS-3's accepted statements, the GetPublicKey code, the `KeyUsage` requirement, the stub counts, the rotation row's cross-reference, the CloudFormation "cannot be declared" claim, the test and constraint counts, KMS-24's inline denial, "cloud-infrastructure-only" for DeriveSharedSecret) and 19 source tags moved to `model` in section 8 plus 6 in section 6. New: KMS-35 to KMS-55. |
| **L2** optional parameters | Read: `KeyUsage` (required but for symmetric: KMS-6), `Origin` EXTERNAL "valid only for symmetric" (KMS-16), `CustomKeyStoreId` "only for symmetric encryption KMS keys in a single Region", `MultiRegion` default false, `Description` default empty, `PendingWindowInDays` default 30 and 7 to 30 against the trait, `RotationPeriodInDays` default 365, `Decrypt`/`ReEncrypt` `KeyId` and algorithm "required only ... asymmetric" and `CiphertextBlob` "required in all cases except" `IGNORE_CIPHERTEXT` (KMS-17), `GenerateDataKey` "either ... (but not both)" (KMS-11), `GenerateRandom.NumberOfBytes` "required" (KMS-31), `ListKeyRotations.IncludeKeyMaterial` (KMS-41), `ListAliases.KeyId` optional, `PolicyName` default `default` (KMS-18), every `Limit` default (KMS-7), `GrantTokens` (KMS-42). |
| **L3** error codes | Each operation's `errors` list was read against what the code answers: KMS-36 (500), KMS-37 (`InvalidKeyUsageException`), KMS-38 (`DisabledException`, `UnsupportedOperationException`), KMS-39 (`InvalidArnException`), KMS-53 (`InvalidAliasNameException`), KMS-54 (the stubs), KMS-47 (statuses), KMS-49, KMS-50 (not established). `ValidationException` is not in the model's lists (it is the framework's) and is kept where no listed code fits. |
| **L4** direction | Named on every new row (more forgiving: KMS-3, 8, 9, 10, 11, 12, 13, 38, 39, 40, 42, 43, 44, 45, 52; stricter: KMS-15 data key pairs, KMS-35 if wired, KMS-51; wrong code: KMS-36, 37, 49, 50, 53; missing: KMS-14, 41; wrong shape: KMS-43, 44). The one place the plan would have been **stricter** than AWS is KMS-35. |
| **L5** every wire | One protocol, `awsJson1_1`, no flattened member, no `xmlName`: checked, none beyond the per-operation test (section 5, "The wire"). The model's `examples` are the inputs. |
| **L6** tokens | `Marker`: opaque, "Do not attempt to construct", `InvalidMarkerException`; a stale or foreign one is accepted here (KMS-7). `GrantTokens`: `InvalidGrantTokenException` is on 15 operations, every token is ignored (KMS-42; grants are S so no token can be valid, memory). The ciphertext carries the key id and, per the model's `KeyMaterialId`, the key material (KMS-41); a ciphertext from an older material still decrypts (`TestSDKRotateKeyOnDemand`). Import tokens are S. `KeyMaterialId` is 64 hex, derivable. A foreign ciphertext (another doze-aws) answers `InvalidCiphertextException`. |
| **L7** async | Decided below. |
| **L8** latency rules | Below. |
| **L9** memory or disk | Below. |
| **L10** the board is the spec | 6a: 20 boards: 2 match (Kms-01, Kms-17), 1 cannot match yet (Kms-18), 17 differ; 13 boards edited in the working copy (19 strings: stale limits, codes and sentences). |
| **L11** five lines | The table at the end of this section. |
| **L12** icons | Checked: the rail, the list header and the key header use `awsIcon "kms"` (`static/aws/kms.svg`); the boards draw "KM". Kept. |

**L7. Asynchronous on AWS, observable on AWS, decided or put to a person.**

| Operation | On AWS (`model`) | Here | Proposal |
|---|---|---|---|
| RotateKeyOnDemand | A background process; `GetKeyRotationStatus` shows `OnDemandRotationStartDate` while it runs; `ListKeyRotations` after; `ConflictException` for a second one or one within 20 minutes of an automatic rotation | Immediate, inside the call | **Model it** (decision 2): the call returns, status shows the start date, the new material becomes current and the entry appears after a short fixed delay (`Options.Clock`, a second by default), memory-only in-progress state; `ConflictException` while it runs. A test asserting the list straight after is then red here too. |
| Automatic rotation | At enable time plus the period (default 365 days), then every period | A flag; never fires | **Run it on the janitor** (decision 3): `NextRotationDate` reported, rotation at the date, `RotationType: AUTOMATIC`; F instead of C. Otherwise report `NextRotationDate` and keep C. |
| ScheduleKeyDeletion | `PendingDeletion` for 7 to 30 days, then gone with its aliases | Real windows; the janitor sweeps once a minute | Keep real windows. **Opt-in shortening** (decision 4): `[kms] min_pending_window = "1m"` for local loops; off by default, listed. |
| CreateKey | `Enabled` at once for AWS_KMS | Same | Immediate, listed: `Creating` and `Updating` are custom-key-store and multi-Region states. |
| Key state `Unavailable`, `PendingImport`, `PendingReplicaDeletion` | Import, replicas, custom stores | Not modelled | S. |
| GetKeyLastUsage | "KMS began recording" at `TrackingStartDate` | Not built | In memory only (decision 5): `TrackingStartDate` = the later of key creation and process start; honest by the model's own definition and no write per Encrypt. |
| Custom key store connect | `CONNECTING` | S | S. |

**L8. Rules that exist because of AWS's latency or consistency.** Each is more
forgiving here by default and one setting away (`[kms] strict = true`, one
switch like `--sqs-strict`), off by default, state in memory:

- "The KMS API follows an eventual consistency model" (every operation's
  documentation): create-then-describe, alias-then-use and policy changes are
  immediate here. A listed difference with no switch; the repo's fault
  injection is the tool for a person who wants the failure.
- "The principals in the key policy must exist and be visible to KMS" (KMS-52):
  opt-in, checked against the IAM service in the same process.
- `ConflictException` within 20 minutes of a scheduled automatic rotation
  (KMS-45, KMS-46): only meaningful if decision 3 runs the schedule.
- The seven-day minimum deletion window (decision 4): not a latency rule, but the
  reason a local loop cannot see a deletion.
- Rate and quota limits (key count, aliases per key, tags per key, request
  rates): not in the model as numbers; `memory`; none are enforced; listed.

**L9. State: memory or disk.**

| State | Where |
|---|---|
| Keys, material, old material, rotations (with their type and, new, the material id), state, deletion date, policy, tags | bbolt, persisted: the application's data (restart reproduced by hand, `run`) |
| Aliases and, new, their creation and update dates (KMS-41) | bbolt: a value the application reads back |
| AWS-managed keys | bbolt, created lazily |
| An on-demand rotation in progress (KMS-45) | memory: the process's; a restart mid-rotation completes it at boot or drops the pending marker, whichever the test pins |
| `GetKeyLastUsage` tracking | memory (decision 5) |
| A pagination marker | stateless: an encoded position, no table |
| The janitor | persisted dates; the loop is the process's |

New fields on the stored key are `omitempty` JSON, so the upgrade fixture
`datadir-1.0.0` keeps reading (`upgrade_test.go` holds it; `schemaver` is the
guard if a rename is ever needed).

**L11. The five lines, per setting** (line 1 name and unit; 2 the value in
words; 3 what it does; 4 range, default, enforced or stored; 5 the neighbour
check with numbers the page has).

| Setting | Line 4 | Line 5 |
|---|---|---|
| KeySpec | 7 of 11 offered; 11 work; enforced (`specs`) | Which usages it allows, live, from the same table |
| KeyUsage | "Anything else is refused as InvalidKeyUsageException"; required but for symmetric (`model`); enforced | The usage table row for the chosen spec; what the key will answer |
| Alias | `^[a-zA-Z0-9:/_-]+$`, 1 to 256 with the prefix (250 after it), `alias/aws/` reserved; enforced | A name already in use among the keys the list holds |
| Description | 0 to 8 192; enforced | none (quiet) |
| Tags | key 1 to 128, value 0 to 256; enforced; the count and the `aws:` prefix are not established, so not stated | The key's current tags, for a duplicate |
| Rotation switch, period | 90 to 2 560, default 365, symmetric only; enforced (range), **stored** (schedule) unless decision 3 | The next date; the 25-rotation limit and how many have been used |
| Waiting period | 7 to 30 (trait 1 to 365), default 30 (the console offers 7); enforced | The deletion date, from the clock |
| Key policy | 1 to 131 072 characters; "evaluated under soft and enforce"; enforced (validation) | Whether the saved document admits the account root; the lockout |
| Encryption context | any pairs; **enforced** (bound as authenticated data) | "The same pairs must come back to open it" |
| Plaintext, message | 1 to 4 096 bytes; for RSA the algorithm's limit; enforced | The byte count against the key's limit, live |

**Classes of mistake this service is most at risk of** (judgement):

1. **Stricter than AWS from a shared validator** (KMS-35): the same shape as
   SQS's 256 KiB and group-id errors, made by wiring a plan item as written.
2. **The wrong class or code** where the model names one: 5xx for a caller's
   mistake, `ValidationException` where `InvalidKeyUsageException` is defined,
   a code that is not in the model at all (KMS-36, 37, 53). SDKs retry the first
   and branch on the second.
3. **Two statements in the model that conflict** (the trait and the prose for
   the deletion window, `Limit` and the message size): written from either and
   tested as if settled. They go to the recording queue, not into a fix.
4. **Silently ignored switches** (create switches, `DryRun`, `Recipient`,
   `GrantTokens`, `CustomKeyStoreId`): the family of "success that does
   nothing".
5. **A state-by-operation table of about 120 cells** assembled partly from
   memory; the model gives which operations can refuse at all, not which state.
6. **Time that cannot be seen** (7-day deletion, yearly rotation): untestable
   unless a fake clock or an opt-in shortening exists.
7. **The console's shape**: 17 of 20 boards differ and the page has no tabs; the
   SQS lesson (green tests, differing boards) applies to the whole group.
8. **Weight**: ML-DSA and the other additions move the binary; RSA key
   generation is fast enough (RSA_4096 about 0.4 s, `run`).

**Decisions this stage needs from a person**

1. `KeyUsage` omitted on RSA/ECC/HMAC: refuse as AWS does, or keep the default
   as a listed forgiveness (KMS-6). Recommendation: keep, list, and make the
   console's cli show the flag.
2. Model on-demand rotation as a short background step (KMS-45)? Recommendation:
   yes, a one-second step.
3. Run the automatic schedule on the janitor (KMS-46)? Recommendation: yes.
4. An opt-in minimum for the pending window, for local loops (L8).
5. `GetKeyLastUsage` in memory with the tracking start as above.
6. Add Ed25519, ML-DSA and ECDH (KMS-15), subject to `task lightness`.
7. Stubs: move the seventeen to D5's 501 `NotImplemented` (KMS-54), including
   the five whose model lists `UnsupportedOperationException`?
8. Wire B9 without NoAction and NoResource, and make the guard ignore those
   statements (KMS-35).
9. A `[kms] strict` switch for "principals must exist" (KMS-52).
10. Alias acceptance per operation: refuse where the model says "key ID or key
    ARN" once the recording says AWS does (KMS-40).
11. `Recipient`, `DryRun`, `Origin: EXTERNAL`, `MultiRegion`: refuse with the
    stub code, or implement `DryRun`.
