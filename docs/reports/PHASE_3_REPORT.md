# Phase 3 report — KMS (all key families), SSM Parameter Store, Secrets Manager

Date: 2026-07-10

## Scope delivered

Three JSON-1.1 services on the shared awsjson codec, all with real crypto.

- **kms**: originally planned as symmetric-only with asymmetric in Phase 8;
  the user pulled asymmetric + HMAC forward, so all three key families landed
  together, all standard library:
  - SYMMETRIC_DEFAULT: AES-256-GCM, encryption context as AAD, ciphertext blob
    embeds the key id (Decrypt needs no KeyId, like real KMS).
  - RSA_2048/3072/4096 + ECC_NIST_P256/P384/P521: PKCS#8 material,
    RSAES_OAEP_SHA_1/256 encrypt/decrypt, RSASSA_PKCS1_V1_5 + PSS + ECDSA
    sign/verify (RAW and DIGEST), GetPublicKey returns genuine SPKI DER,
    GenerateDataKeyPair wraps the private key under a symmetric key.
  - HMAC_224/256/384/512: GenerateMac/VerifyMac, constant-time compare.
  - Lifecycle: enable/disable, 7–30-day scheduled deletion with janitor
    finalization, aliases (all identifier forms resolve), tags, policy
    round-trips, rotation flags (mechanics in Phase 8).
  - ECC_SECG_P256K1 answers honestly: Go has no stdlib secp256k1 and doze-aws
    takes no crypto dependencies.
- **ssm**: Parameter Store with version history, labels (a label names at most
  one version; re-labeling moves it), name/version/label/ARN selectors,
  GetParametersByPath hierarchy walks, DescribeParameters filters, tags, and
  parameter policies with the Expiration policy actually enforced by janitor.
  SecureString is genuinely encrypted at rest with a per-data-dir AES-GCM key
  (self-managed, so ssm works without kms enabled). The ~100-op fleet surface
  (Run Command, sessions, patching, ...) answers UnsupportedOperationException
  from an explicit list.
- **secretsmanager**: version stages with correct AWSCURRENT→AWSPREVIOUS
  movement, custom stages via UpdateSecretVersionStage (a stage names at most
  one version), ClientRequestToken idempotency, recovery-window deletion +
  restore + janitor purge + ForceDeleteWithoutRecovery, IncludePlannedDeletion
  listing, binary secrets, tags, policy round-trips, GetRandomPassword,
  BatchGetSecretValue. Values sealed at rest. Rotation waits for the lambda
  service (Phase 8) and says so.
- **Stack**: all three wired; `Implemented` is now 6 of 10 services.

## Key decisions / findings

- **DescribeSecret vs ListSecrets field naming**: the stage map is
  `VersionIdsToStages` in DescribeSecret but `SecretVersionsToStages` in the
  ListSecrets entry shape. Caught by contract test; we emit both keys.
- Asymmetric Decrypt requires KeyId + algorithm (RSA blobs carry no envelope),
  exactly like real KMS; symmetric blobs are self-describing.
- SSM/SecretsManager self-encrypt rather than calling the kms service: no
  hard inter-service dependency, and the KeyId fields still round-trip.

## Test evidence

- Contract tests (aws-sdk-go-v2): KMS — symmetric round-trip incl. wrong-
  context rejection, data-key envelopes, ECDSA sign + verify inside AND
  outside KMS (x509-parsed public key), RSA OAEP + PSS, HMAC + tamper
  rejection, aliases, disable/deletion state machine, secp256k1 honesty.
  SSM — versions/labels/history, SecureString encryption visible without
  decryption flag, path walks, filters, tags, delete split, fleet-op honesty.
  Secrets Manager — lifecycle, stage movement, recovery-window semantics,
  restore, force delete, custom stages, binary, random password.
- SDK v1 contract tests for all three services (round-trips + coded error
  envelopes through the legacy deserializers).
- Full suite `-race` clean; `-short` still <2s.
