# Secrets Manager — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

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
| PutResourcePolicy / GetResourcePolicy / DeleteResourcePolicy | F | the secret's resource policy, evaluated on every request naming the secret under IAM `soft` and `enforce` by AWS's same-account rule (see the IAM ledger, "Resource policies"); stored and returned only under the default `off` |
| ValidateResourcePolicy | C | always passes |
| RotateSecret / CancelRotateSecret | F | RotateSecret invokes the configured rotation Lambda synchronously for the four steps (createSecret, setSecret, testSecret, finishSecret); the function moves the version stages, as on AWS. CancelRotateSecret clears the pending rotation |
| ReplicateSecretToRegions / RemoveRegionsFromReplication / StopReplicationToReplica | S | exactly one region locally |

## Differences from AWS

- **One region, so no replicas.** `ReplicateSecretToRegions` and its siblings
  are refused by name rather than pretended: a replica needs a second region
  to exist.
- **`ValidateResourcePolicy` always passes.** It is a linting service on AWS;
  locally there is nothing behind it to lint against, and failing a policy
  that AWS would accept is the worse error.
- **Rotation runs your Lambda, and that is all it runs.** `RotateSecret`
  invokes the function through the four rotation steps; there is no managed
  rotation template and no scheduled trigger, so rotation happens when it is
  asked for.
- **Values are encrypted with a local key** in the data directory rather than
  by KMS, with the same honest limit as SSM's: encrypted at rest, not
  protected from anything that can read the directory.

## Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): the secret lifecycle, the deletion
  recovery window, partial-ARN lookup the way the console does it, version
  stages and ids, binary values, tags and generated passwords, and the rule
  that only one version may be `AWSPENDING`.
- **aws-sdk-go v1** (`sdkv1_test.go`): the secret round trip through the older
  client.
- **Rotation end to end** (`rotate_test.go`): a real Lambda function driven
  through `createSecret`, `setSecret`, `testSecret` and `finishSecret`, with
  the stage moves each step is supposed to make.
- **Model-derived rejection parity** (`rejection_parity_test.go`) and the
  dispatch table against `testdata/ops_secrets-manager.json`.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what Secrets Manager refuses**.

**132/132 model-derived constraints enforced across 19 of the 20 dispatched
operations, with `knownGaps` empty.** Before this table, 54 were enforced by
hand-written checks and 78 were not.

Generated rather than hand-derived: `dzaudit cases secrets-manager` emits a
violating value per constrained input, `testdata/cases_secretsmanager.json`
commits them, and `rejection_parity_test.go` replays every one from a baseline
it first proves the service accepts.

`RotateSecret`'s 20 cases are skipped with a reason recorded in the test:
rotation invokes a Lambda, and the audit boots this service alone. Three
operations — `ReplicateSecretToRegions`, `RemoveRegionsFromReplication`,
`StopReplicationToReplica` — have no handler and so cannot be audited at all;
an operation that refuses every request tells you nothing about its validation.
