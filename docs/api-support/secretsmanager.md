# Secrets Manager — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

Secret values (string and binary) are genuinely encrypted at rest with a
per-data-dir AES-256-GCM key; the KMS KeyId is recorded and returned
cosmetically.

| Operation | Tier | Notes |
|---|---|---|
| CreateSecret | F | ClientRequestToken idempotency, tags, ResourceExistsException on conflict |
| GetSecretValue | F | by name or ARN; VersionId / VersionStage (default AWSCURRENT); deleted secrets refuse with InvalidRequestException |
| BatchGetSecretValue | F | per-secret error entries |
| PutSecretValue | F | stage movement: new AWSCURRENT demotes the old one to AWSPREVIOUS |
| UpdateSecret | F | description/kms + optional new version |
| DeleteSecret | F | RecoveryWindowInDays 7–30 (default 30) → janitor purge; ForceDeleteWithoutRecovery immediate |
| RestoreSecret | F | |
| ListSecrets | F | IncludePlannedDeletion flag |
| DescribeSecret / ListSecretVersionIds | F | version→stages maps |
| UpdateSecretVersionStage | F | a stage names at most one version |
| TagResource / UntagResource | F | |
| GetRandomPassword | F | length, ExcludeCharacters, ExcludePunctuation |
| PutResourcePolicy / GetResourcePolicy / DeleteResourcePolicy | C | stored and returned; not evaluated (no IAM locally) |
| ValidateResourcePolicy | C | always passes |
| RotateSecret / CancelRotateSecret | S→F | Phase 8: RotateSecret will invoke the configured rotation lambda (4-step protocol) once the lambda service exists |
| ReplicateSecretToRegions / RemoveRegionsFromReplication / StopReplicationToReplica | S | exactly one region locally |
