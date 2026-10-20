# Secrets Manager

Stage 4. Size L (see the note on size in section 1; the first draft said M).
Follows [../01-protocol.md](../01-protocol.md); the common acceptance criteria
there apply in full, and this page adds Secrets Manager's own.

Line numbers are as found on 2026-10-10 and will drift; the symbol beside each
one will not. "Run" below means reproduced against a binary built from the
working tree into a scratch directory, default flags (`--iam-mode soft`), over
the JSON 1.1 wire and the console's own routes. **Pre-stage review, 2026-10-11:**
every claim below was re-read against `.audit-models/secrets-manager.json`'s
own documentation text, every finding was re-run, the console was rendered beside
the dream design (section 6a), and what that found is marked
*Corrected 2026-10-11* in place or sits in the findings from SM-39 on. The
review itself is section 12.

## Foundation hand-offs (stage 0, 2026-10-20)

What the foundation (stage 0, package P6) left for this stage, so it is not lost, and what it did already. The shared items are in [../00-foundation.md](../00-foundation.md); each line here says what is done, what is owed, and the test that proves it.

| Item | Secrets Manager: done, and what this stage does |
|---|---|
| B6 the model sweep | 3 legacystub of 23 operations (stamped; they answer `InvalidRequestException`). Move each to `awshttp.NotBuilt`. |
| B8 generated cases | 152 committed, 179 regenerated (2 only in the committed file, 29 only in the new one). Regenerate in this stage. |
| B9 stored-policy validator | Pending, **SM-5**: wire it with `MalformedPolicyDocumentException`; and `ValidateResourcePolicy` agrees with what `PutResourcePolicy` accepts (SM-59). |
| B4 settings records | 1 hand-written bounded number input in `sm.html`. |
| A15 fragments | On one view model already (`secretDetailView`, walked by `fragments_test.go`). |
| A2 the why line | Set `Local: true` (`awshttp.APIError`) on each refusal that is doze-aws's own limit and not AWS's, which this document's findings mark; nothing sets the stamp today except the console's own 503. Test: one such limit per service answers with `X-Doze-Local-Limit` and the page says "Refused here" (`internal/console/why_line_test.go` is the worked example). |
| B3 mutation | Baseline recorded in `testdata/mutation.json` (`secretsmanager`); this stage runs `task mutate:check -- secretsmanager` after its changes, and any survivor it adds has a test or a reason. |


## 1. Summary

Secrets with versions and staging labels over one bbolt store
(`secretsmanager.bolt`), values sealed with a per-data-dir AES-256-GCM key, on
one wire protocol (AWS JSON 1.1, target prefix `secretsmanager`; the model
declares `aws.protocols#awsJson1_1` and nothing else). A janitor purges secrets
whose recovery window has passed, once a minute.

Peers: it calls **Lambda** (rotation, four synchronous invokes as the
`secretsmanager.amazonaws.com` principal) and **KMS** (`DescribeKey`, to check a
named key exists and is enabled; the key is never used to encrypt). It is
called by `provision` and CloudFormation (`AWS::SecretsManager::Secret`), and
its resource policy is evaluated through the shared IAM guard.

The API is small and its happy paths are sound. What grounding and the run
turned up is mostly at the edges: request members that are validated and then
ignored (paging, filters, rotation rules, half of `GetRandomPassword`),
idempotency tokens that hide a conflicting write, and a console detail page
whose partial re-renders each forgot a different piece of the page (fixed by
foundation A15 since, SM-23 to SM-25). **What the model's own documentation
added on 2026-10-11** is bigger than the first pass: rotation is asynchronous on
AWS and here it is not, the step names a rotation function receives are
different, the state a function finds when it is invoked is different (SM-42 to
SM-47), an ARN resolves when it should not (SM-50), a pending secret and a
stranded `AWSPENDING` are treated more kindly than on AWS (SM-6, SM-45), no
quota is enforced (SM-52), and a password request can drop the connection
(SM-41).

**Estimate:** *Corrected 2026-10-11: was "M, three to four focused days"; the
document had 38 findings and this review adds 23, the same shape as SQS (8
planned, 29 closed, 19 of them from reading the model's documentation).* **L,
eight to eleven focused days, honestly:** the API rules (paging, filters,
tokens, the ARN rule, quotas, the stage and pending rules, policy validation)
about three days; rotation (asynchronous run, step names, the state a function
sees, rules stored and returned, the schedule if it is built, tests with a real
function) two to three days; the console, where all 20 boards are built to shape
and 17 differ in shape today, four to five days. **Actual:** _fill in when
done._ The findings table has **61 rows** (38 original, 5 of them closed by
foundation work since the first draft, and 23 new): **3 P0, 28 P1, 21 P2, 9
P3.** If the rotation schedule (SM-2) is built rather than declared cosmetic,
the rotation share grows by a day.

## 2. Contract

The model lists **23 operations** (`secretsmanager/testdata/ops_secrets-manager.json`).
Twenty are dispatched (`secretsmanager/actions.go`, `handlers`); three are
registered as refusals in `init`.

*Corrected 2026-10-11: the finding ids in the notes below were off by one from
SM-11 on (the first draft numbered them before SM-12 and SM-13 were written);
they now point at the rows in section 4.*

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateSecret | F | F | Token replay hides a conflict (SM-1). Replica members ignored (SM-18). A value-less create returns a `VersionId` for a version that does not exist (SM-17). Name charset (SM-56). |
| GetSecretValue | F | F | Id and stage that disagree (SM-12). An ARN that is not the secret's resolves (SM-50). |
| BatchGetSecretValue | F | F | `Filters` ignored; "Filters or SecretIdList, but not both" not checked (SM-9). |
| PutSecretValue | F | F | SM-1; first version gets `AWSCURRENT` (SM-49); version and label quotas (SM-52). |
| UpdateSecret | F | F | Cannot clear a description (SM-10) or a KMS key (SM-40); an existing token is an error whatever the value (SM-39). |
| DeleteSecret | F | F | Force on an absent secret (SM-14). |
| RestoreSecret | F | F | |
| ListSecrets | F | F | No paging, filters or sort; default order is by name where AWS's is by `CreatedDate` (SM-7). |
| DescribeSecret | F | F | Shape gaps (SM-16). |
| ListSecretVersionIds | F | F | No paging; the order is random within a second (SM-8). |
| UpdateSecretVersionStage | F | F | SM-11. |
| TagResource, UntagResource | F | F | SM-15. |
| GetRandomPassword | F | F | Five members ignored (SM-13); an alphabet excluded to nothing panics (SM-41). |
| PutResourcePolicy, GetResourcePolicy, DeleteResourcePolicy | F | F | Stores a document it cannot parse (SM-5); `BlockPublicPolicy` ignored (SM-60). |
| ValidateResourcePolicy | C | C, **decide** | Always passes. *Corrected 2026-10-11: the model describes three checks ("Sends a call to Zelkova ... Checks for correct syntax in a policy. Verifies the policy does not lock out a caller"), lists `MalformedPolicyDocumentException` and `ResourceNotFoundException`, and a `PolicyValidationPassed: false` with `ValidationErrors`. Staying C is a decision, and the minimum is the syntax check (SM-59).* |
| RotateSecret, CancelRotateSecret | F | F | Rules dropped (SM-2), always immediate and inline where AWS's is asynchronous (SM-3, SM-44), failure is a 500 (SM-4), step names and the state the function sees (SM-42, SM-43), a stranded `AWSPENDING` is not refused (SM-45). |
| ReplicateSecretToRegions, RemoveRegionsFromReplication, StopReplicationToReplica | S | S | Declined: one region. *Corrected 2026-10-11: was "the code they answer with is not established (SM-19)". Decision D5 settles it: 501 `NotImplemented` with `X-Doze-Not-Built`, written by `awshttp.NotBuilt` (landed, foundation B6). Today they answer 400 `InvalidRequestException` and no header (run).* |

Request members and settings need their own tiers, because "the operation is F"
hides which of them do anything. "Ignored" means validated by the model table
and then never read.

| Operation | Member | Now | Target | Note |
|---|---|---|---|---|
| CreateSecret, UpdateSecret | `KmsKeyId` | C | C | Checked against KMS when wired (must exist, be Enabled); recorded; never used. The console must say so. *Added: model, UpdateSecret: "If you set this to an empty string, Secrets Manager uses the Amazon Web Services managed key aws/secretsmanager" (SM-40). Refusal codes for an unusable key (SM-55).* |
| CreateSecret | `Tags`, `Description`, `SecretString`, `SecretBinary` | F | F | Tag rules SM-15. A string is measured in characters on AWS and in bytes here (SM-61). |
| CreateSecret, PutSecretValue, UpdateSecret | `ClientRequestToken` | F, wrong on conflict | F | SM-1, SM-39 |
| CreateSecret | `AddReplicaRegions`, `ForceOverwriteReplicaSecret` | ignored | S (refuse) | SM-18 |
| CreateSecret, UpdateSecret | `Type` | ignored | C: stored and returned | *Corrected 2026-10-11: was "what AWS does with it is not established". Model: "The exact string that identifies the partner that holds the external secret"; `DescribeSecret` and the `ListSecrets` entry both return `Type`. It has no local effect, so C, and it must round-trip.* |
| PutSecretValue | `VersionStages` | F | F | First version rule SM-49; 1 to 20 labels per call is not checked (SM-51). |
| PutSecretValue | `RotationToken` | ignored | C: validated, accepted, nothing checks the identity | *Corrected 2026-10-11: was "not established". Model: "A unique identifier that indicates the source of the request. Required for secret rotations using an IAM assumed role or cross-account rotation". There is no role assumption to validate against locally.* |
| DeleteSecret | `RecoveryWindowInDays`, `ForceDeleteWithoutRecovery` | F | F | |
| ListSecrets | `IncludePlannedDeletion` | F | F | |
| ListSecrets | `MaxResults`, `NextToken`, `Filters`, `SortOrder`, `SortBy` | ignored | F | SM-7, SM-48 |
| ListSecretVersionIds | `IncludeDeprecated` | F | F | |
| ListSecretVersionIds | `MaxResults`, `NextToken` | ignored | F | SM-8, SM-48 |
| BatchGetSecretValue | `SecretIdList` | F | F | At most 20 is not checked (SM-51). |
| BatchGetSecretValue | `Filters`, `MaxResults`, `NextToken` | ignored | F | SM-9, SM-48 |
| RotateSecret | `RotationLambdaARN` | F | F | Absent and none configured: wrong code (SM-46). |
| RotateSecret | `RotationRules.AutomaticallyAfterDays`, `.ScheduleExpression`, `.Duration` | ignored | F or C, decide | SM-2, SM-47 |
| RotateSecret | `RotateImmediately` | ignored (always rotates) | F | SM-3. *Corrected 2026-10-11: false is not "no rotation"; see SM-3.* |
| RotateSecret | `ClientRequestToken` | ignored (a new id is minted) | F | SM-3 |
| RotateSecret | `ExternalSecretRotationMetadata`, `ExternalSecretRotationRoleArn` | ignored | S for a rotation that needs them, C for a stored value; **decide** | *Corrected 2026-10-11: was "not established". Model: "The metadata needed to successfully rotate a managed external secret", "the role that allows Secrets Manager to rotate a secret held by a third-party partner". Both come back on `DescribeSecret`. A rotation of an external secret calls the partner, which does not exist here.* |
| GetRandomPassword | `PasswordLength`, `ExcludeCharacters`, `ExcludePunctuation` | F | F | Default 32 and range 1 to 4096 are the model's. |
| GetRandomPassword | `ExcludeNumbers`, `ExcludeUppercase`, `ExcludeLowercase`, `IncludeSpace`, `RequireEachIncludedType` | ignored | F | SM-13, SM-41 |
| PutResourcePolicy | `ResourcePolicy` | F under soft and enforce | F | SM-5 |
| PutResourcePolicy | `BlockPublicPolicy` | ignored | F, with a stated approximation | *Corrected 2026-10-11: was "not established". Model: "Specifies whether to block resource-based policies that allow broad access to the secret, for example those that use a wildcard for the principal", and `PublicPolicyException` is in the operation's errors. What counts as broad is AWS's analysis (Zelkova) and is `memory`; a wildcard principal with no condition is the stated approximation (SM-60).* |
| DescribeSecret (output) | `RotationRules`, `NextRotationDate`, `LastAccessedDate`, `OwningService`, `PrimaryRegion`, `ReplicationStatus`, `Type`, `ExternalSecretRotationMetadata`, `ExternalSecretRotationRoleArn` | absent | per field, decide | SM-16 |
| ListSecretVersionIds (output) | `LastAccessedDate`, `KmsKeyIds` | absent | per field, decide | SM-16 |

Declined, and staying declined: replication (three operations and the two
`CreateSecret` members); also `PutResourcePolicy`'s "broad access" analysis is
approximated, not replicated. *Corrected 2026-10-11: the first draft said
"managed rotation templates are not an API operation and are a listed
difference"; the model has two other things under that name: "managed rotation"
(service-linked secrets, "omit [RotationLambdaARN]") and "managed external
secrets" (`Type`, the two `ExternalSecretRotation*` members). Neither can run
locally; both are listed differences, and the second is the `Type` row above.*

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `secretsmanager/`: **13** tests (`go test -list`). `sdk_test.go` 7 (lifecycle, recovery window, partial ARN, finish-step stages, single pending, stages and ids, binary/tags/password), `sdkv1_test.go` 1, `rotate_test.go` 1 (a real function through four steps), `conformance_fixes_test.go` 2 (names; string or binary, not both), `rejection_parity_test.go` 1, `coverage_model_test.go` 1. *Corrected 2026-10-11: was 12.* |
| Parity | `rejection_parity_test.go` replays `secretsmanager/testdata/cases_secretsmanager.json` (152 cases: 132 enforced, `RotateSecret`'s 20 skipped because it needs Lambda), `coverage_model_test.go`. Ledger: 132 of 132 model constraints across 19 of 20 dispatched operations, `knownGaps` empty. **The 132 omit 9 list-length constraints** (`SecretIdList` 1 to 20, `Filters` 0 to 10, `Filters.Values` 1 to 10 on two operations, `VersionStages` 1 to 20, `AddReplicaRegions` and `RemoveReplicaRegions` at least 1); foundation B8 made the generator read them and this service has not regenerated (SM-51). |
| Root tests that touch it | `persistence_test.go` (one value across a restart), `persistence_policies_test.go` (a resource policy across a restart), `identity_test.go` (ARN under another account and region), `upgrade_test.go`, `fuzz_test.go`; `internal/console/fragments_test.go` `TestSecretsManagerFragmentsMatchAFullReload` (foundation A15) |
| Conformance | `conformance/tests/test_secretsmanager.py`: 4 scenarios (lifecycle and stages, binary, deletion window and restore, refusals), all **unverified**: `conformance/snapshots/` is empty |
| Browser | `e2e/tests/secretsmanager.spec.ts`: **12** tests (create and mask, generate password, new version and diff, three key/value editor, rotation, delete and restore, details, rollback, policy save, policy remove). The rotation test's own comment records that the days value does not survive and pins `every \d+ days` rather than the number (SM-2, SM-20). *Corrected 2026-10-11: was 9.* |
| Mutation | `testdata/mutation.json` holds a baseline for `secretsmanager`, recorded 2026-10-10 (foundation B3): 118 killed, 46 survived, 21 not covered. *Corrected 2026-10-11: was "no per-package baseline".* |

Not covered anywhere today: paging on any list, `BatchGetSecretValue` filters,
token replay with different content, any operation on a pending secret except
get and put, a policy that is not a policy, `GetRandomPassword`'s exclusion
flags, rotation failure, and rotation or version state across a restart.

## 4. Findings

Evidence is `run` or `read` as the protocol defines them. AWS source is
`model` (the model's documentation text or traits, quoted), `doc` (a named
documentation page, read on 2026-10-11) or `memory`; a `memory` row is on the
recording queue and its fix waits for it. Direction (L4) is named on every row.

**State on 2026-10-11:** SM-23, SM-24, SM-25, SM-28 and SM-37 are **closed in
the working tree** by foundation items (A15 f797c16/788f865, A14 0d87bb0, and
a ledger row corrected); each was re-run and is marked below. The other 33
original rows were re-run or re-read and all stand.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SM-1 | P0 | A write replayed with the same `ClientRequestToken` and a **different** value answers 200 and keeps the first value. Seen on `PutSecretValue` (stored "one", sent "TWO", read back "one") and `CreateSecret`; `UpdateSecret` has its own, different rule (SM-39). A caller is told its write landed. *Direction: more forgiving.* | `secretsmanager/store.go` `AddVersion` ("idempotent replay"), `Create` | run (again 2026-10-11) | *Corrected 2026-10-11: was `doc`.* model, PutSecretValue: "if the secret data is different, then the operation fails because you can't modify an existing version; you can only create new ones"; CreateSecret.ClientRequestToken: "that version's SecretString and SecretBinary values are different from those in the request, then the request fails because you cannot modify an existing version". The model names no code. `ResourceExistsException` ("A resource with the ID you requested already exists") is in the errors of all three operations and is the likely one; which is returned is `memory` | SDK test: same token, different `SecretString`, expect an error from the model's list and the first value unchanged; same token, same value, expect success |
| SM-2 | P0 | `RotationRules` is validated and dropped. Nothing stores it, `DescribeSecret` never returns it, and nothing rotates on a schedule. A tool that writes rotation rules and reads them back (Terraform's rotation resource does) sees them vanish. *Direction: missing.* | `secretsmanager/rotate.go` `rotateSecret` (the `Mutate` at about 52); `actions.go` `describe`; `store.go` `secret` has no field | read; run again (2026-10-11: `RotationRules` absent after configuring); the existing browser test's comment records the same | model: DescribeSecret.RotationRules "If the secret previously had rotation turned on, but it is now turned off, this field shows the previous rotation schedule and rotation function. If the secret never had rotation turned on, this field is omitted."; NextRotationDate "The next rotation is scheduled to occur on or before this date."; AutomaticallyAfterDays "Manually updating the secret value by calling PutSecretValue or UpdateSecret is considered a valid rotation." | SDK test: rotate with `AutomaticallyAfterDays: 45`, describe, expect 45, `NextRotationDate` present, and the rules still shown after `CancelRotateSecret`. Then the decision in section 10 on whether a schedule fires |
| SM-3 | P1 | `RotateSecret` always runs the function at once and in the caller's request. `RotateImmediately: false` is ignored, and the caller's `ClientRequestToken` is replaced by a new id. *Direction: wrong.* | `rotate.go` `rotateSecret` (`token := newUUID()`) | read | *Corrected 2026-10-11: was `doc` and the test said "expect none".* model, RotateImmediately: "The default for RotateImmediately is true. If you don't specify this value, Secrets Manager rotates the secret immediately. If you set RotateImmediately to false, Secrets Manager tests the rotation configuration by running the testSecret step of the Lambda rotation function. This test creates an AWSPENDING version of the secret and then removes it." | SDK test with `RotateImmediately: false` and a function that records steps: expect exactly one step (`test_secret`, SM-42) and no `AWSPENDING` version left, rotation configured; with the token given, the version id returned is that token |
| SM-4 | P1 | A rotation whose function is missing or throws answers **500 `InternalServiceError`**. Run (again 2026-10-11): a missing function gives `rotation step createSecret failed: lambda invoke: Not Found: {"Message":"Function not found: nope",...}`; a function that errors gives `rotation step createSecret failed: function error (Unhandled): {...}`. An SDK retries a 500. The console half (it is shown as "a bug in the emulator", still, in the 2026-10-11 render of Sm-13) is foundation A2's "Function error" state, which A2 did not build: the 5-state table has "Failed" only. *Direction: wrong code, and wrong time (SM-44).* | `rotate.go` about 48 | run | *Corrected 2026-10-11: was `memory`.* model: the operation's errors are `InternalServiceError`, `InvalidParameterException`, `InvalidRequestException`, `ResourceNotFoundException` and none describes a function failing; "Rotation is an asynchronous process" and the example's "The rotation function runs asynchronously in the background" mean a failing function is **not** in `RotateSecret`'s answer on AWS at all. What AWS answers at call time for an ARN it cannot invoke is `memory` (an access-denied for the function's resource policy is what is recalled) | Handler test: a missing function and a function that throws each leave the call successful (200, the token as `VersionId`), the failure observable afterwards (`AWSPENDING` left, `LastRotatedDate` absent); the missing-function call-time refusal is skipped until recorded |
| SM-5 | P1 | `PutResourcePolicy` stores any string (`"not json"` and `"{}"` answered 200 and read back; run again). The guard then drops a document it cannot parse without a word, so a policy the user believes is in force is never consulted. The console's panel reads "Configured" for `not json` (run, Sm-14 render). *Direction: more forgiving.* | `actions.go` `putResourcePolicy`; `guard.go` `guardRequest` (the ignored `iampolicy.Parse` error, about 32) | run | model: `MalformedPolicyDocumentException` ("The resource policy has syntax errors") is in the errors of PutResourcePolicy and ValidateResourcePolicy (CreateSecret and UpdateSecret list it too, with no member that can raise it) | Handler test: `"not json"`, `{}` and a statement with no `Effect` are refused with that code; nothing is stored |
| SM-6 | P1 | A secret pending deletion accepts changes. Run (again): `TagResource`, `UntagResource`, `PutResourcePolicy`, `DeleteResourcePolicy`, `UpdateSecretVersionStage` and `CancelRotateSecret` all answered 200 on one; `GetResourcePolicy` answered 200. Only `GetSecretValue`, `PutSecretValue`, `UpdateSecret` and a second `DeleteSecret` check; `RotateSecret` has no check (it got as far as invoking Lambda). *Direction: more forgiving.* | `actions.go`: `updateSecretVersionStage`, `tagResource`, `untagResource`, `putResourcePolicy`, `deleteResourcePolicy`; `rotate.go` both handlers | run | *Corrected 2026-10-11: was `memory`.* model, InvalidRequestException: "A parameter value is not valid for the current state of the resource. Possible causes: The secret is scheduled for deletion." The code and the cause are the model's. Which operations raise it is bounded by each operation's own `errors`: **`DescribeSecret` and `ListSecretVersionIds` do not list it and must keep working** (they do); that every other operation raises it for a pending secret is the model's cause list read per operation, and the exact set is `memory` | Table test: every operation against a pending secret, one expectation per operation; the two that list no `InvalidRequestException` expect success; the others skipped per row until recorded |
| SM-7 | P1 | `ListSecrets` ignores `MaxResults`, `NextToken`, `Filters`, `SortOrder` and `SortBy`. Run (again): five secrets, `MaxResults: 1` and a name filter matching none returned all five and no token; `SortBy`/`SortOrder` change nothing; the default order is by **name** and `SortBy: "created-date", SortOrder: "desc"` still returns name order. *Direction: wrong shape, and missing.* | `actions.go` `listSecrets` | run | *Corrected 2026-10-11: was "model (the members); doc for filter semantics".* model, all of it: Filter keys "description: Prefix match, not case-sensitive. name: Prefix match, case-sensitive. tag-key: Prefix match, case-sensitive. tag-value: Prefix match, case-sensitive. primary-region ... owning-service ... all: Breaks the filter value string into words and then searches all attributes for matches. Not case-sensitive."; Values "You can prefix your search value with an exclamation mark (!) in order to perform negation filters."; SortBy "If not specified, secrets are listed by CreatedDate."; MaxResults 1 to 100 (the default page size is `memory`); `InvalidNextTokenException` | SDK test: page five secrets one at a time; filter by name prefix (case-sensitive), description (not), tag key, tag value, `!` negation, `all`; sort every `SortBy` both ways; default order is `CreatedDate` |
| SM-8 | P1 | `ListSecretVersionIds` ignores `MaxResults` and never returns `NextToken`. **New evidence:** the versions come back in a random order when several were written in the same second (8 different orders in 8 calls, run), because the handler sorts a map's contents by second-resolution `CreatedDate` with an unstable sort; paging cannot be built on that. *Direction: missing; wrong order.* | `actions.go` `listSecretVersionIds` | run | model (MaxResults 1 to 100, `NextToken`, `InvalidNextTokenException`); order is not stated by the model, so a stable order is ours (decide: oldest first, ties by id) | SDK test paging two versions one at a time; a second test that eight calls return one order |
| SM-9 | P1 | `BatchGetSecretValue` reads only `SecretIdList`. With `Filters` alone it returns nothing; it answers `"SecretValues": null, "Errors": null` rather than lists; `MaxResults` and `NextToken` are ignored; a call with **neither** or with **both** is answered 200. A pending secret in the list is reported per secret as `ResourceNotFoundException`. 21 ids are accepted (SM-51). *Direction: more forgiving, and wrong code per secret.* | `actions.go` `batchGetSecretValue` | run (again) | *Corrected 2026-10-11: was `doc`.* model: "You must include Filters or SecretIdList, but not both."; MaxResults "To use this parameter, you must also use the Filters parameter."; per-secret `ErrorCode` "for example ResourceNotFoundException, InvalidParameterException, InvalidRequestException, DecryptionFailure, or AccessDeniedException" and a pending secret is InvalidRequestException's first cause. The code for neither/both is `memory` (`InvalidParameterException` is the likely one) | SDK test: filter by name returns the matching values; both given is refused; neither is refused; empty result is `[]`; a pending id is an `Errors` entry with `InvalidRequestException` |
| SM-10 | P1 | `UpdateSecret` with `Description: ""` answers 200 and leaves the description. A description cannot be cleared once set. *Direction: wrong (or more forgiving of nothing; not established).* | `actions.go` `updateSecret` (`if d != ""`, about 227) | run | memory: whether AWS clears on an empty string. model gives only `DescriptionType` length 0 to 2048, so "" is a legal value; the console's own form relies on "left empty, the current one is kept" (Sm-05) | SDK test, skipped pending the recording |
| SM-11 | P1 | `UpdateSecretVersionStage` moves a label that is attached to another version without `RemoveFromVersionId`. Run (again): `AWSCURRENT` moved with only `MoveToVersionId`; a custom label too. *Direction: more forgiving.* | `actions.go` `updateSecretVersionStage` | run | *Corrected 2026-10-11: was `doc`.* model, RemoveFromVersionId: "If the staging label you are trying to attach to one version is already attached to a different version, then you must include this parameter and specify the version that the label is to be removed from. If the label is attached and you either do not specify this parameter, or the version ID does not match, then the operation fails." **The model contradicts itself:** the operation text says "To add a staging label to a version when it is already attached to another version, Secrets Manager first removes it from the other version first and then attaches it to this one." The member text is the specific one and agrees with the recalled error; keep it queued (`stage_move_requires_remove`) and do not fix until recorded | Handler test: move without it is refused; with the wrong id is refused; with the right id moves (skipped until recorded) |
| SM-12 | P1 | `GetSecretValue` given a `VersionId` and a `VersionStage` that name different versions answers with the id's version (run again). *Direction: more forgiving.* | `store.go` `Resolve` | run | *Corrected 2026-10-11: was `doc`.* model, VersionId and VersionStage: "If you include both this parameter and VersionStage, the two parameters must refer to the same secret version." The code is `memory` | Handler test for the mismatch |
| SM-13 | P1 | `GetRandomPassword` ignores `ExcludeNumbers`, `ExcludeUppercase`, `ExcludeLowercase`, `IncludeSpace` and `RequireEachIncludedType`. Run (again): all four exclusion flags set, the password was still letters and digits; `IncludeSpace` never produced a space in 4096 characters. It also reads `ExcludeCharacters` as a boolean in one branch (`awsjson.Bool(p, "ExcludeCharacters")`), and picks characters with a modulo, which is biased. *Direction: wrong.* | `actions.go` `getRandomPassword` | run | model: every member's text. `RequireEachIncludedType`'s default is **contradictory in the model**: the trait says `default=false` and the text says "If you don't include this switch, the password contains at least one of every character type." So the default is not established; test both explicitly | Table test: each flag alone, all together; the default-32 length (model: "the default length is 32 characters") |
| SM-14 | P1 | `DeleteSecret` with `ForceDeleteWithoutRecovery` on a secret that does not exist answers `ResourceNotFoundException` (run again, absent and never-existed). *Direction: stricter.* | `store.go` `Delete` | run | *Corrected 2026-10-11: was `doc`.* model: "If you forcibly delete an already deleted or nonexistent secret, the operation does not return ResourceNotFoundException." What it returns for a name with no ARN to give is `memory` | SDK test: force-delete twice |
| SM-15 | P1 | Tags are not held to AWS's rules. Run (again): a key starting `aws:` and two entries with the same key in one call were both accepted, **62 tags** on one secret and **70 at create** were accepted. *Direction: more forgiving.* | `actions.go` `tagResource`, `ptaglist` | run, read (limit) | *Corrected 2026-10-11: was "doc: TagResource (50 tags, `aws:` reserved)", which the model does not say.* model, TagResource: "For tag quotas and naming restrictions, see Service quotas for Tagging in the Amazon Web Services General Reference guide" (no number). **doc**, the Tag Editor user guide, "Best practices and strategies", "Tag naming limits and requirements" (read 2026-10-11): "Each resource can have a maximum of 50 user created tags. System created tags that begin with `aws:` are reserved for AWS use ... You can't edit or delete a tag that begins with the `aws:` prefix. For each resource, each tag key must be unique." Whether this service refuses a 51st tag or an `aws:` key and with which code is not stated (`memory`). model: UntagResource "is idempotent" (the code agrees) | Rejection cases for each rule; the 51st tag; each skipped until recorded |
| SM-16 | P1 | `DescribeSecret` answers shapes AWS does not. Run (again): `"KmsKeyId": ""`, `"Description": ""` and `"RotationEnabled": false` on a secret that has none of them; **both** `VersionIdsToStages` and `SecretVersionsToStages` on Describe **and** on the `ListSecrets` entry (each shape has only one of them); `LastAccessedDate` and `NextRotationDate` never appear; `ListSecretVersionIds` entries carry no `LastAccessedDate` or `KmsKeyIds`. `DeletedDate` is the time `DeleteSecret` was called, while `DeleteSecret` itself returns the purge time as `DeletionDate`. *Direction: wrong shape.* | `actions.go` `describe`, `listSecretVersionIds` | run | *Corrected 2026-10-11: was "memory for which empty members AWS omits".* model, DescribeSecret: "Secrets Manager only returns fields that have a value in the response."; KmsKeyId: "If the secret is encrypted with the Amazon Web Services managed key aws/secretsmanager, this field is omitted."; RotationEnabled: "If the secret has never been configured for rotation, Secrets Manager returns null."; LastAccessedDate "is omitted if the secret has never been retrieved". **`DeletedDate` is contradictory in the model**: DescribeSecret "The date the secret is scheduled for deletion", ListSecrets "The date and time the deletion of the secret occurred". Not established; stays `memory` | Snapshot-style SDK test of a fresh secret and a pending one; the empty members absent (model); `DeletedDate` skipped until recorded |
| SM-17 | P1 | `CreateSecret` with no value returns a `VersionId`, and no such version exists (`GetSecretValue` then refuses, `ListSecretVersionIds` is empty; run again). *Direction: wrong.* | `actions.go` `createSecret`; `store.go` `Create` | run | model: "If you include SecretString or SecretBinary then Secrets Manager creates an initial secret version", and by the same text none is created without. Whether `VersionId` is omitted is `memory`. The model also says, on the members, "Either SecretString or SecretBinary must have a value, but not both" for CreateSecret; the Terraform two-resource pattern (secret, then secret version) depends on a value-less create, so the operation text is followed | SDK test: no `VersionId` in the answer, or a version that exists |
| SM-18 | P1 | `CreateSecret` accepts `AddReplicaRegions` and ignores it, while the three replication operations refuse by name. A template that asks for a replica is told it has one. *Direction: more forgiving.* | `actions.go` `createSecret` | read | n/a (the decline is ours); model: the member exists with `ReplicationStatus` in the answer | Handler test: the member is refused the way the operations are (SM-19) |
| SM-19 | P1 | The declined operations answer 400 `InvalidRequestException` with no `X-Doze-Not-Built` header (run, headers read); an unknown target answers 400 `InvalidAction`. *Direction: wrong code.* | `actions.go` `init`; `secretsmanager.go` `ServeHTTP` | run | *Corrected 2026-10-11: was `memory`/"not established".* Protocol decision D5 and foundation B6: 501 `NotImplemented`, `X-Doze-Not-Built`, written by `awshttp.NotBuilt(kind, op, why)` (landed). The unknown-operation code on AWS is not in the model (`memory`) | One test per declined operation through the Go v2 SDK asserting one attempt and the header; move the three entries to `notbuilt` in `testdata/modelsweep.json` |
| SM-20 | P2 | With rotation on, the console shows "every 0d" in the facts and "every 0 days" in the receipt, whatever was typed (run, Sm-12 render). Follows from SM-2; the browser test pins the wrong shape on purpose. | `internal/console/client_sec.go` `GetSecret` (`RotationDays`); `templates/sm.html` lines 70 and 77 | run | n/a | Browser test asserts the number typed, once SM-2 is fixed; until then asserts no number is shown |
| SM-21 | P3 | The rotation form says doze-aws "invokes it on the schedule (and on demand)". There is no schedule (read again, `sm.html` line 85). | `templates/sm.html` | read | n/a | Browser test asserts the hint |
| SM-22 | P2 | "Enable rotation" runs the user's function through all four steps before answering, and the toast says only "Rotation configured". Nothing on the form says a function is about to run. *Corrected 2026-10-11: AWS does the same, and says so: model, RotateSecret "If you include the configuration parameters, the operation sets the values for the secret and then immediately starts a rotation." The form still has to say it (Sm-11 does), and on AWS it returns before the function finishes (SM-44).* | `client_sec.go` `ConfigureRotation`; `handlers_sec.go` `smConfigureRotation` | read | model | Browser test: the form states it, and the toast names what ran |
| SM-23 | P2 | **Closed in the working tree (foundation A15, f797c16 and 788f865; re-run 2026-10-11: a fragment after `put` says "Configured").** After "Make current" the resource policy panel read "Not set" and its Remove button was gone. The partials now come from `secretDetailView`. | `handlers_sec.go` `smPromote` -> `smDetailPartial` | run | n/a | `fragments_test.go` `TestSecretsManagerFragmentsMatchAFullReload` holds it |
| SM-24 | P2 | **Closed (A15; re-run: the put fragment carries Reveal).** After "Save as new version", and after any rotation action, the Reveal and Copy buttons were gone and no mode was marked. | `handlers_sec.go` `smPut`, `smRotationRefresh` | run | n/a | as SM-23 |
| SM-25 | P2 | **Closed (A15; re-run: three versions after a create and two puts, the fragment says 3).** The "Versions" fact read 2 in the fragment and 3 after a reload. | `handlers_sec.go` | run | n/a | as SM-23 |
| SM-26 | P2 | The Versions tab lists versions by version id, which is random, and shows no time (re-rendered 2026-10-11, Sm-08: the table is VERSION / STAGES / ACTIONS in map order). The service returns them with `CreatedDate`; the console keeps only id to labels in a map. | `client_sec.go` `SecretVersionIDs`; `workspace.html` (the `range $id, $st := .Stages`) | read, run | n/a | Handler test: three versions written a second apart render newest first with their times |
| SM-27 | P2 | The diff swallows a failed read and mislabels. Run (again): a version id that does not exist rendered `previous → current` with `- (empty)` and `+ v2` as if the old value were empty. The heading is the same whichever row was pressed. | `handlers_sec.go` `smDiff` (`old, _ :=`) | run | n/a | Handler test: an absent version is a refusal; the heading names the version |
| SM-28 | P2 | **Closed (foundation A14, 0d87bb0; re-run 2026-10-11: a secret `app/db+prod=1@x` has every link `urlquery`-escaped on the list and on View, Edit, Versions and Tags).** A secret whose name contains `+` could not use its tabs. | `templates/sm.html` | run | n/a | `TestALinkFromANameSurvivesItsCharacters` |
| SM-29 | P2 | Saving details with the description emptied toasts "Secret details updated" and changes nothing (SM-10; run again). The KMS key field is always empty, because the console never reads `KmsKeyId` (read; Sm-05 render). | `handlers_sec.go` `smUpdateMeta`; `client_sec.go` `Secret`, `GetSecret` | run (toast), read (field) | n/a | Browser test: clear the description, reload; set a key, reload, the field shows it |
| SM-30 | P2 | Delete always asks for a 7-day window and the console cannot choose another or delete at once, though the handler already takes `force=true` and AWS's default is 30. | `client_sec.go` `DeleteSecret`; `handlers_sec.go` `smDelete`; `sm.html` (the `hx-confirm`, "with a 7-day recovery window", re-rendered) | read | *Corrected 2026-10-11: was `doc`.* model, RecoveryWindowInDays: "The number of days from 7 to 30 ... by default Secrets Manager uses a 30 day recovery window" | Browser test for the window field and for the immediate delete |
| SM-31 | P2 | A secret stored with `SecretBinary`, and a secret with no version, both render as an empty value box with nothing said (re-rendered 2026-10-11, Sm-19). | `client_sec.go` `GetSecret` (reads `SecretString` only) | read, run | n/a | Browser test for each: the page says which it is |
| SM-32 | P2 | The create form cannot do what the API can: no binary value, no KMS key, no tags (re-rendered). | `templates/create.html` `sm_create`; `client_sec.go` `CreateSecret` | read | n/a | Decide which to add (section 10); a browser test for each added |
| SM-33 | P2 | A pending secret's page still offers Edit, "Make current" and the tag editor. Edit is refused; the other two succeed (SM-6). Re-rendered: View, Edit, Versions and Tags tabs and Reveal/Copy are all offered on a pending secret, with an empty value box; Rotation and Resource policy are hidden. | `templates/sm.html`, `workspace.html` (no `Deleted` guard in the workspace) | read, run | n/a | Browser test: a pending secret offers Restore and reading, nothing else |
| SM-34 | P3 | Masking is cosmetic in three places: Copy copies the real value while hidden, the page source carries it, and the diff shows both versions in the clear. Decide whether the diff should honour Reveal. | `workspace.html` (`data-copy`, both `masked` spans, `value_diff`) | read | n/a | Browser test for whichever is decided |
| SM-35 | P3 | The header always shows an `AWSCURRENT` chip, derived from nothing (re-rendered, every state including a secret with no version). | `templates/sm.html` line 50 | read, run | n/a | Removed, or replaced by the current version id; a handler test |
| SM-36 | P3 | The Disable rotation confirm asks "Turn off automatic rotation?". Nothing here is automatic (re-rendered: the dialog is that question and "Turn off", no body). Foundation A13's family; A13 gave the dialog its verb and left the sentence. | `templates/sm.html` (the `hx-confirm`) | read, run | n/a | Read every `hx-confirm` in `sm.html` against the code; four exist |
| SM-37 | P3 | **Closed (ledger row now reads "evaluated ... under IAM `soft` and `enforce` ...; under `off` it is stored and returned and nothing evaluates it").** The ledger said the resource policy is "stored and returned only under the default `off`". The default is `soft` (`cmd/doze-aws/main.go`, `--iam-mode`). | `docs/SUPPORT.md`, Secrets Manager table | read | n/a | The ledger test |
| SM-38 | P3 | The ledger's "Rotation runs your Lambda, and that is all it runs" and "CancelRotateSecret clears the pending rotation" state AWS behaviour with no source, and the second is **false of the code** (it leaves any `AWSPENDING` label alone, which the code comment calls "matching AWS"). *Corrected 2026-10-11: the comment is right and the ledger is wrong.* model, CancelRotateSecret: "If you cancel a rotation in progress, it can leave the VersionStage labels in an unexpected state. You might need to remove the staging label AWSPENDING from the partially created version." | `docs/SUPPORT.md`; `secretsmanager/rotate.go` | read | model (above) | The ledger sentence corrected and a test that cancel leaves `AWSPENDING` where it was |

### Found by the pre-stage review (2026-10-11)

Each row was run against a binary built from the working tree, or read, and
each source is the model's own text. Every one needs a test that fails first.

| Id | Sev | What | Direction | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|---|
| SM-39 | P1 | `UpdateSecret` with a `ClientRequestToken` that is an existing version's id answers 200 with that version's id, **whatever the value** (same value, and a different one: both 200), and **applies the description and KMS key first**: a call that should fail changed the description (run). | more forgiving; partial application | `actions.go` `updateSecret` (`Mutate` runs before `AddVersion`; `AddVersion`'s "idempotent replay") | run | model, UpdateSecret: "If you call this operation with a ClientRequestToken that matches an existing version's VersionId, the operation results in an error. You can't modify an existing version, you can only create a new version." The code is `memory` (`ResourceExistsException` is in the list) | SDK test: existing token with the same value is refused; with a different value and a new description is refused and the description is unchanged |
| SM-40 | P1 | `UpdateSecret` with `KmsKeyId: ""` does not reset the secret to the AWS-managed key (`if k != ""`). The console cannot do it either. | wrong | `actions.go` `updateSecret` | read (the empty description half is run, SM-10) | model, UpdateSecret.KmsKeyId: "If you set this to an empty string, Secrets Manager uses the Amazon Web Services managed key aws/secretsmanager." | SDK test: set a key, update with `""`, describe shows no `KmsKeyId` |
| SM-41 | P1 | `GetRandomPassword` panics when the alphabet is empty: an `ExcludeCharacters` that names every character of the alphabet (the 94 characters, or the 62 letters and digits with `ExcludePunctuation`) divides by zero. (The four exclusion flags are ignored, SM-13, so they cannot empty it today; once they are honoured they can.) The connection is reset with no HTTP answer (`http: panic serving ... integer divide by zero`); an SDK retries it. | wrong (a dropped connection where AWS answers) | `actions.go` `getRandomPassword` (`int(b)%len(alphabet)`) | run | model: GetRandomPassword's errors are `InvalidParameterException` and `InvalidRequestException`; which one is `memory` | SDK test: excluding every character is refused with a 4xx and the server survives |
| SM-42 | P0 | The rotation function is invoked with `"Step": "createSecret"`, `"setSecret"`, `"testSecret"`, `"finishSecret"`. The user guide's parameter list for the function's event names the values `create_secret`, `set_secret`, `test_secret` and `finish_secret`. A function written from AWS's rotation templates would reject every step (that the templates compare on these strings is `memory`; the template source was not read); only a function written for this emulator runs. The payload also never carries `RotationToken`. The ledger, the tests (`rotate_test.go` line 118), the console's Sm-11 sample and the board all use the camelCase names, which are the user guide's *method* headings. | wrong | `rotate.go` line 39 | read; the page read 2026-10-11 | doc: AWS Secrets Manager User Guide, "Rotation by Lambda function", the request structure: "Step – The rotation step: create_secret, set_secret, test_secret, or finish_secret." (the neighbouring page "Lambda rotation functions" names the methods `createSecret` ... `finishSecret`). Queue `rotation_step_names` | Rotation test with a function that echoes the event: the four `Step` values are the snake_case ones, in order, with one `ClientRequestToken` and `SecretId` the ARN |
| SM-43 | P1 | The state a function finds when it is invoked is not AWS's: `RotationEnabled` is still false and `RotationLambdaARN`/`RotationRules` are not stored (they are written **after** the four steps, `rotate.go` line 52), and the version named by `ClientRequestToken` does not exist yet, so `DescribeSecret.VersionIdsToStages` has no entry for it. A function that checks that rotation is enabled and that its token is staged `AWSPENDING` (AWS's templates do) refuses. A rotation that fails leaves **nothing** configured, where on AWS the configuration is already stored. | wrong order | `rotate.go` `rotateSecret` | read; run (a failed rotation left `RotationEnabled` false and no `RotationLambdaARN`) | model, RotateSecret: "If you include the configuration parameters, the operation sets the values for the secret and then immediately starts a rotation."; DescribeSecret.VersionIdsToStages: "During rotation, Secrets Manager creates an AWSPENDING version ID before creating the new secret version." What the templates check is `memory` (template source not read) | Rotation test: the function calls `DescribeSecret` and records `RotationEnabled` and whether its token is in `VersionIdsToStages` with `AWSPENDING`: both true |
| SM-44 | P1 | `RotateSecret` runs the whole rotation inside the call (L7). AWS returns once the configuration is stored and the rotation is started; the function runs afterwards, so a test that asserts "rotated" straight after passes here and races there, a slow function holds the request open, and a function's failure is a 500 instead of a state to read later. | more forgiving (time) | `rotate.go` `rotateSecret` | read | model: "Configures and starts the asynchronous process of rotating the secret"; "Rotation is an asynchronous process"; the example: "The rotation function runs asynchronously in the background." **Decision in section 10.** Recommended: the SQS move-task shape, a background task with a visible running state, one per secret, cancellable, memory-only (L9), the function's own failure left as a stranded `AWSPENDING` and no `LastRotatedDate` | SDK test: the call returns before a function that waits on a gate completes; `DescribeSecret` shows the `AWSPENDING` token while it runs and `LastRotatedDate` after |
| SM-45 | P1 | A second `RotateSecret` while `AWSPENDING` is on a version that is not `AWSCURRENT` is not refused; it invokes the function again (no check in `rotateSecret`). The design's Sm-13 said the opposite ("writes a fresh pending version and takes the label off this one"), which is corrected on the board. | more forgiving | `rotate.go` `rotateSecret` | read | model, RotateSecret: "If the AWSPENDING staging label is present but not attached to the same version as AWSCURRENT, then any later invocation of RotateSecret assumes that a previous rotation request is still in progress and returns an error."; CancelRotateSecret: "Failing to clean up a cancelled rotation can block you from starting future rotations." The code is `memory` (`InvalidRequestException` is the likely one) | SDK test: strand an `AWSPENDING`, rotate, expect an error from the operation's list and the function not invoked; remove the label, rotate, expect success |
| SM-46 | P1 | `RotateSecret` for a secret with no rotation function and none in the request answers `InvalidParameterException`. | wrong code | `rotate.go` line 34 | run (again, with the message "a RotationLambdaARN is required to rotate the secret") | model, InvalidRequestException: "You tried to enable rotation on a secret that doesn't already have a Lambda function ARN configured and you didn't include such an ARN as a parameter in this call." | Handler test: the code is `InvalidRequestException` |
| SM-47 | P1 | `RotationRules` is never validated beyond the traits: `AutomaticallyAfterDays` **and** `ScheduleExpression` together are accepted; a `ScheduleExpression` of `rate(1 hour)` (more often than every four hours) and a `Duration` with no schedule reach the function call. | more forgiving | `rotate.go` `rotateSecret` | read; run (each got as far as invoking Lambda) | model, AutomaticallyAfterDays: "In RotateSecret, you can set the rotation schedule in RotationRules with AutomaticallyAfterDays or ScheduleExpression, but not both."; ScheduleExpression: "You can rotate a secret as often as every four hours."; Duration: "The window must not extend into the next rotation window or the next UTC day." The codes are `memory` (`InvalidParameterException`) | Rejection cases per rule; each skipped until recorded |
| SM-48 | P1 | A `NextToken` is never validated (L6): `ListSecrets` with `NextToken: "garbage"` answers 200 and the whole list; the other two paged operations are the same by reading. A token from another call, another filter set or another secret is equally accepted. | more forgiving | `actions.go` `listSecrets`, `listSecretVersionIds`, `batchGetSecretValue` | run (ListSecrets), read (the others) | model: `InvalidNextTokenException` ("The NextToken value is invalid.") on ListSecrets, ListSecretVersionIds and BatchGetSecretValue; NextToken "This can occur even when the response includes no values at all" | After SM-7 to SM-9: a made-up token, a token from `ListSecrets` given to `ListSecretVersionIds`, and a token with different filters are each refused |
| SM-49 | P1 | `PutSecretValue` with `VersionStages: ["AWSPENDING"]` as a secret's first version leaves the secret with no `AWSCURRENT`; `GetSecretValue` then fails (run). | wrong | `store.go` `AddVersion` | run | model, PutSecretValue: "If this operation creates the first version for the secret, then Secrets Manager automatically attaches the staging label AWSCURRENT to it." | SDK test: a value-less create, then a put with only `AWSPENDING`: the version carries `AWSPENDING` and `AWSCURRENT` |
| SM-50 | P1 | A secret is found by an ARN that is not its ARN: the **old** ARN of a deleted-and-recreated secret (the new secret has a different suffix) resolves to the new one; an ARN with another region, another account or a wrong six-character suffix resolves too (all run: 200 and the value). A hard-coded ARN works locally and fails on deploy (SQS-23's class). | more forgiving | `store.go` `candidateNames` (the name is taken from the ARN's last segment, the suffix stripped, and region and account ignored) | run | model, CreateSecret.ARN: "The ARN includes the name of the secret followed by six random characters. This ensures that if you create a new secret with the same name as a deleted secret, then users with access to the old secret don't get access to the new secret because the ARNs are different."; GetSecretValue.SecretId: "To retrieve a secret from another account, you must use an ARN." Partial ARNs (no suffix) stay valid: the model's "Finding a secret from a partial ARN" | SDK test: create, force-delete, create again, read by the first ARN: `ResourceNotFoundException`; a foreign region or account in the ARN: `ResourceNotFoundException`; the partial ARN still works |
| SM-51 | P2 | The list-length constraints are not enforced: 21 ids in `SecretIdList`, 11 filters, 11 filter values, 0 filter values, 21 `VersionStages`, 0 stages and an empty `AddReplicaRegions` are all accepted (run). The ledger's "132 of 132" does not count them (foundation B8). | more forgiving | `secretsmanager/validate.go`; the generator read them after B8 and this service has not regenerated | run | model traits: `SecretIdListType` length 1 to 20, `FiltersListType` 0 to 10, `FilterValuesStringList` 1 to 10, `SecretVersionStagesType` 1 to 20, `AddReplicaRegionListType` and `RemoveReplicaRegionListType` min 1 | Regenerate `cases_secretsmanager.json`; the new cases red first |
| SM-52 | P2 | No quota is enforced: 131 versions of one secret and 26 labels on one version were accepted (run). The operations list `LimitExceededException` ("The request failed because it would exceed one of the Secrets Manager quotas."). Unlabelled versions are never removed. | more forgiving | `store.go` `AddVersion`; `actions.go` `updateSecretVersionStage` | run | doc: the user guide, "AWS Secrets Manager quotas" (read 2026-10-11): "Versions per secret: 100", "Staging labels attached across all versions of a secret: 20", "Secrets: 500,000"; "Secrets Manager removes unlabeled versions when there are more than 100, but it does not remove versions created less than 24 hours ago." model, PutSecretValue: "Secrets Manager keeps 100 of the most recent versions, but it keeps all secret versions created in the last 24 hours." and `LimitExceededException` in CreateSecret, PutSecretValue, UpdateSecret and UpdateSecretVersionStage. Exactly which write fails is `memory` | Tests skipped until recorded: the 101st version within a day, the 21st label; a janitor test under a test clock that removes unlabelled versions beyond 100 and older than 24 hours |
| SM-53 | P2 | The Value tab's masked view is not the value: it decodes into a map and re-encodes, so keys are **alphabetical** (stored `username, password, host, port` shows `host, password, port, username`), and numbers go through `float64` (`12345678901234567890` shows as `1.2345678901234567e+19`, `1.0` as `1`). The revealed view and the editor show the stored text; foundation A4 fixed the editor and not this. | wrong (console) | `internal/console/valueworkspace.go` `maskedValue`, `maskJSON` | run (Sm-04 render beside Sm-04 revealed); read for the numbers | n/a | Handler test: the masked view's key order and number tokens are the stored text's |
| SM-54 | P2 | The console reads `ListSecrets` and `ListSecretVersionIds` once and never follows `NextToken`. It works only because the service ignores paging (SM-7, SM-8); the day those land with a default page of 100, a console list truncates silently and a secret with more than 100 versions loses its history. | will break | `client_sec.go` `ListSecrets`, `SecretVersionIDs` | read | model: `NextToken` "can occur even when the response includes no values at all" (a loop on token, not on emptiness) | Console test over 150 secrets and 120 versions |
| SM-55 | P2 | Refusal codes for an unusable KMS key are ours: a missing key is `InvalidParameterException`, a disabled one `InvalidRequestException` (run, Sm-05). The model's `EncryptionFailure` describes the unusable key: "Secrets Manager can't encrypt the protected secret text using the provided KMS key. Check that the KMS key is available, enabled, and not in an invalid state." | wrong code | `actions.go` `requireUsableKey` | run (missing), read (disabled) | model, EncryptionFailure (above); a *missing* key's code is not established (`ResourceNotFoundException` "can't find the resource that you asked for" is a candidate) | Handler tests: disabled key and pending-deletion key give `EncryptionFailure`; the missing key skipped until recorded |
| SM-56 | P3 | The name rule: the code accepts `!` (`a!b` created, run) and its refusal lists `-/_+=.@!`; the model's text is "ASCII letters, numbers, and the following characters: /_+=.@-". The first draft's `/_+=.@!-` was written from the console message (`memory`). The advice "Do not end your secret name with a hyphen followed by six characters" is a risk warning, not a refusal. *Unsettled:* the recalled AWS refusal text lists `!`; the model text does not. | more forgiving (by the model) | `actions.go` `secretName` | run | model (above); the message and the code (`InvalidRequestException` is recalled) are `memory` | Recording `name_rules` first; until then no change |
| SM-57 | P3 | The rotation form's days input is `max="365"`; the model's range is 1 to 1000. The design says so ("the form offers 1 – 365"). | stricter (console) | `templates/sm.html` line 88 | read | model: `AutomaticallyRotateAfterDaysType` range 1 to 1000 | Browser test: 1000 is accepted by the form |
| SM-58 | P3 | `CancelRotateSecret` answers no `VersionId`. | missing | `rotate.go` `cancelRotateSecret` | read | model, CancelRotateSecret response: "The unique identifier of the version of the secret created during the rotation. This version might not be complete, and should be evaluated for possible deletion." | SDK test: strand a rotation, cancel, `VersionId` is the `AWSPENDING` version |
| SM-59 | P2 | `ValidateResourcePolicy` answers `PolicyValidationPassed: true` for `not json` and for a `SecretId` that does not exist (run). | more forgiving | `actions.go` `validateResourcePolicy` | run | model: errors `MalformedPolicyDocumentException`, `ResourceNotFoundException`; "Checks for correct syntax in a policy."; the response's `ValidationErrors` (`CheckName`, `ErrorMessage`); `SecretId` is **not** required on this operation | Handler tests: not JSON is refused with `MalformedPolicyDocumentException`; an absent secret is `ResourceNotFoundException`; no `SecretId` is accepted |
| SM-60 | P2 | `BlockPublicPolicy: true` is ignored: a policy with `"Principal": "*"` and no condition is stored (run). | more forgiving | `actions.go` `putResourcePolicy` | run | model: BlockPublicPolicy "block resource-based policies that allow broad access to the secret, for example those that use a wildcard for the principal"; `PublicPolicyException`: "The BlockPublicPolicy parameter is set to true, and the resource policy did not prevent broad access to the secret." The analysis is AWS's own and the approximation is ours (a wildcard principal with no condition) | Handler test with the wildcard policy and the flag; a policy naming the account does not trip it |
| SM-61 | P2 | A string's length is measured in **bytes**, so 40,000 characters of "é" (80,000 bytes) are refused as longer than 65,536 (run), and a 1,500-character non-ASCII description is refused at 2,048. The same in every service's `modelcheck` table (`len(t)`); the blob case was fixed in B7 and the string case was not. | stricter | `internal/modelcheck/modelcheck.go` `lengthOf` | run | doc: the quotas page, "Secret value size ... 65,536 Bytes ... If the secret value is a string, then this is the number of characters permitted in the secret value." For the other members the `length` trait on a string is a character count (Smithy's definition, `memory`) | Handler test: 65,536 two-byte characters accepted, 65,537 refused; shared, so it lands once in foundation and every service re-runs |

Owned by the foundation, listed so they are not lost (state on 2026-10-11):

- **A2.** *Landed (5 states).* The "AWS does" line is classified from facts
  stamped on the response. Still open for this service: a rotation function's
  failure is the service's own `InternalServiceError`, so it renders as
  **Failed**, "a bug in the emulator" (re-rendered, Sm-13); the table's
  "Function error" row (a peer the user owns answered with an error, naming the
  function) has no state in `stateCopyFor`. It is also moot once rotation is
  asynchronous (SM-44), when the failure is a state on the page.
- **A4.** *Landed.* The key/value editor keeps types; the design's Sm-06 sentence
  was stale (corrected on the board).
- **A5.** *Landed.* Saving tags is one `TagResource` and one `UntagResource`.
- **A6.** *Landed.* A header action's refusal lands under the header.
- **A13.** *Landed for the component;* the four confirms' sentences are this
  service's (SM-36, section 6a).
- **A14, A15.** *Landed;* SM-28, SM-23 to SM-25.
- **B7.** *Landed* for blobs; strings are SM-61.
- **B8.** *Landed* in the generator; this service regenerates (SM-51).

**Corrections to the board-grounding report.** The missing-function message was
given there as unknown; it is `lambda invoke: Not Found: {...}` (run). The
report said policies are consulted "only when IAM is not off" as if `off` were
the default; the default is `soft`, so a secret's policy is evaluated out of the
box. The claim that a pending secret's Versions tab offers "Make current" was
not reproduced and stays `read` (SM-33). Everything else in that report was
reproduced or stands as read; nothing was found false.

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once;
`awsJson1_1` only (the model declares no Query or REST binding and no member
carries `xmlName`, `xmlFlattened`, `jsonName`, `httpHeader`).

**Secrets**
- [ ] Name: 1 to 512, `/_+=.@-` with letters and digits; *Corrected 2026-10-11:
      was "`/_+=.@!-`, hand-derived"; the model's text is `/_+=.@-` and the
      code accepts `!` too (SM-56, recording first).* The response shapes carry
      1 to 256 on every `Name` (a model inconsistency; the request member is the
      limit).
- [ ] A name ending in a hyphen and six characters resolves to itself, not to
      a shorter name, by name and by full and partial ARN (`candidateNames`);
      a stale or foreign ARN does not resolve (SM-50). *The advice not to name
      secrets this way is the model's; it is not a refusal.*
- [ ] Create twice: same token and value succeeds; same token, other value
      refuses (SM-1); no token refuses with `ResourceExistsException`.
- [ ] Create with the name of a pending secret refuses, and the message is
      compared with the recording.
- [ ] Create with no value, then the first `PutSecretValue` (SM-17, SM-49).
- [ ] `KmsKeyId`: absent key and disabled key refused when KMS is wired
      (`EncryptionFailure`, SM-55); accepted when it is not; an alias and an
      ARN both resolve; `""` on update resets (SM-40).
- [ ] `UpdateSecret`: description alone writes no version; a value writes one;
      clearing a description (SM-10); an existing token is an error and
      changes nothing (SM-39).

**Values and versions**
- [ ] String and binary: one or the other, 1 to 65 536 (a string in characters,
      SM-61; binary in bytes), and a binary round trip byte for byte.
- [ ] `PutSecretValue` with no stages moves `AWSCURRENT` and `AWSPREVIOUS`;
      with `VersionStages: ["AWSPENDING"]` leaves `AWSCURRENT` alone *unless it
      is the first version (SM-49)*; with a custom label; with a label already
      held elsewhere.
- [ ] A label names at most one version, after every path that can move one;
      at most 20 labels across a secret's versions (SM-52).
- [ ] `GetSecretValue`: by stage, by id, by both agreeing and disagreeing
      (SM-12), an absent stage, an absent id.
- [ ] `UpdateSecretVersionStage`: move with and without `RemoveFromVersionId`
      (SM-11, skipped until recorded), remove a custom label, **removing
      `AWSCURRENT` is refused today; the model says nothing either way
      (`memory`, queued `remove_awscurrent`)**, a missing target version.
- [ ] Deprecated versions: *Corrected 2026-10-11: was "AWS removes them past a
      count; establish the rule".* Established (model and quotas page): removed
      when there are more than 100 **and** they are older than 24 hours; a write
      that would pass 100 inside 24 hours is `LimitExceededException` (SM-52).
      Decide: modelled in the janitor, tested under a test clock.
- [ ] `ListSecretVersionIds`: with and without `IncludeDeprecated`, paging
      (SM-8), one stable order, `CreatedDate`.
- [ ] `BatchGetSecretValue`: a list, filters, both (refused), neither (refused),
      a missing id among found ones, a pending one (`InvalidRequestException`
      per secret), paging (SM-9).

**Listing**
- [ ] `ListSecrets` paging, every filter key (`name`, `description`, `tag-key`,
      `tag-value`, `all`, and the two that cannot match locally,
      `primary-region` and `owning-service`), the case rules per key, negation
      with `!`, both sort orders and each `SortBy` (`created-date`,
      `last-accessed-date`, `last-changed-date`, `name`), the default order is
      by `CreatedDate`, a bad token (SM-7, SM-48).
- [ ] `IncludePlannedDeletion` on and off.

**Deletion**
- [ ] Window 7 to 30, default 30, window and force together refused.
- [ ] Force on a present, a pending and an absent secret (SM-14).
- [ ] Every operation against a pending secret, one row each (SM-6);
      `DescribeSecret` and `ListSecretVersionIds` keep working.
- [ ] `RestoreSecret` on a pending, a live and an absent secret.
- [ ] The janitor purges at the window's end and not before, under a test
      clock; a purged name can be created again.
- [ ] `DeletedDate` and `DeletionDate` mean what AWS means (SM-16; the model
      contradicts itself, recording first).

**Rotation**
- [ ] The four steps, in order, `create_secret` ... `finish_secret` (SM-42),
      with the same token, as the service principal with `aws:SourceArn` set to
      the secret, with the secret already configured and the token already
      `AWSPENDING` in `VersionIdsToStages` (SM-43).
- [ ] `RotationRules` stored and returned (SM-2); the range 1 to 1000; a
      `ScheduleExpression` (not less than four hours); days and schedule
      together refused; `Duration` (SM-47).
- [ ] `RotateImmediately` true, false (the test step only) and absent (SM-3).
- [ ] Asynchronous: the call returns before the function finishes (SM-44).
- [ ] A function that is missing, that throws on each of the four steps, and
      that times out: the call, the labels left behind and what Describe shows
      (SM-4).
- [ ] A second rotation while a version is `AWSPENDING` and not current is
      refused (SM-45).
- [ ] `CancelRotateSecret` on a rotating, a never-rotated and a pending secret;
      the rules are still returned afterwards; `VersionId` (SM-58).
- [ ] The 20 skipped model cases for `RotateSecret` run, with a stub function.

**Random password**
- [ ] Length 1 to 4096 and the default 32; every exclusion flag; `IncludeSpace`;
      `RequireEachIncludedType` on and off (the default is contradictory in the
      model); an alphabet excluded to nothing is refused, not a panic (SM-13,
      SM-41).

**Access**
- [ ] A policy that cannot be parsed is refused (SM-5); an empty document;
      the 20 480 limit; `BlockPublicPolicy` (SM-60).
- [ ] Under enforce: a `Deny` refuses the root; an `Allow` naming a role lets
      it in with no identity policy; naming the account delegates.
- [ ] `ValidateResourcePolicy` agrees with what `PutResourcePolicy` accepts
      (SM-59).

**Tags**
- [ ] Add, overwrite, remove, remove an absent key (idempotent, model); the
      limit of 50 and `aws:` (doc), and duplicate keys (SM-15); tags given at
      create.

**Wire (one protocol)**
- [ ] Content type `application/x-amz-json-1.1`; `X-Amz-Target:
      secretsmanager.<Operation>`; blobs are base64; timestamps are epoch
      seconds; every error carries `__type` and `x-amzn-errortype` (checked on
      2026-10-11, run). Declined operations: 501 (SM-19). An unknown target is
      `InvalidAction` here; the AWS code is not in the model.

**Lifecycle**
- [ ] Across a restart: versions and labels, a pending deletion and its purge
      time, the policy, tags, rotation state including the rules. A running
      rotation is the process's and is not carried (L9).
- [ ] The sealing key: a store opened with a different key file refuses values
      rather than returning garbage (`renderValue`'s "corrupt" path).

## 6. Console

The design's Secrets Manager page: 20 boards. Templates: `templates/sm.html`
(`sm_lp`, `sm_home`, `sm_secret`, `sm_secret_detail`), the `sm_create` block in
`create.html`, `value_workspace` and `value_diff` in `workspace.html`,
`tag_editor` and `fail_inline` in `panes.html`, `policy_builder` in `iam.html`;
the kit's `res_header` and `setting_field` (`kit.html`) are not used here yet.
Handlers: the `sm*` functions in `handlers_sec.go` (the page and every fragment
build from `secretDetailView`, foundation A15) and `handlers_sm_policy.go`,
`tagsView` and `tagsSave` in `handlers_tags.go`; routes in `routes_config.go`
`smRoutes`. *Corrected 2026-10-11: `tagsView`/`tagsSave` unchanged; the
value view's masking is `valueworkspace.go`.*

The design draws Value, Versions, Rotation, Resource policy and Tags as tabs.
Today View, Edit, Versions and Tags are one segmented control and Rotation and
Resource policy are sections under it on the same page (re-rendered
2026-10-11). The tabs are a layout change over the same calls.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sm-Home | Secrets, none open | `sm_home`, `sm_lp`, `empty_detail` | exists; the last-changed column and the list note are **new**; rows show no time |
| Sm-01-None | No secrets yet | `sm_home`, `lp_empty`, `empty_detail` | exists, matches |
| Sm-02-Create | Store a new secret | `sm_create`; `smCreate` | form, Generate password and CLI preview exist; teaching lines, byte counter, two-column page and the labels card are **new** |
| Sm-03-Create-Refused | Refused inline | `fail_inline` | exists; placement and "other refusals" differ |
| Sm-04-Value | Value, hidden and revealed | `sm_secret_detail`, `value_workspace` view; `smSecret` | exists; the current version line and the side panel are **new**; see SM-53 |
| Sm-05-Details | Description and KMS key | the inline form in `sm_secret_detail`; `smUpdateMeta` | form exists as a header row; the panel with teaching lines is **new**; see SM-29 |
| Sm-06-Edit | Edit, save as a new version | `value_workspace` edit; `smPut` | exists; "After you save" is **new** |
| Sm-07-Edit-Refused | Edit refused | `fail_inline` | exists |
| Sm-08-Versions | Versions and labels | `value_workspace` versions; `smSecret` | table exists; Created and the order are **new** (SM-26) |
| Sm-09-Diff | Diff against current | `value_diff`; `smDiff` | exists; the heading naming the version is **new** (SM-27) |
| Sm-10-Rollback | Make current, and after | `hx-confirm` in `workspace.html`; `smPromote` | exists; the confirm naming three versions is **new** |
| Sm-11-Rotation-Off | Enable rotation | the rotation form in `sm_secret_detail`; `smConfigureRotation` | form exists; teaching lines and the four steps are **new** |
| Sm-12-Rotation-On | Rotation on | the receipt in `sm_secret_detail`; `smRotateNow` | exists; the function link is **new**; "While it runs" waits on SM-44 |
| Sm-13-Rotation-Failed | A rotation that failed | `fail_inline` | exists, with the wrong sentence (A2, SM-4); "what it left behind" is **new**; the premise changes with SM-44 |
| Sm-14-Policy | Resource policy | the policy panel, `policy_builder`; `smSavePolicy` | exists; the explanatory cards are **new** |
| Sm-15-Tags | Tags as a draft | `tag_editor`; `tagsSave` | exists (A5) |
| Sm-16-Confirms | Delete, disable rotation, remove policy | `hx-confirm` in `sm.html` | three exist; the window field and immediate delete are **new** (SM-30) |
| Sm-17-Pending | In the recovery window | the `Deleted` branches of `sm_secret_detail`; `smRestore` | exists; the locked-value message is **new** |
| Sm-18-Denied | A save denied under enforce | `fail_inline` | exists (the callout matches; the two cards are **new**) |
| Sm-19-No-Value | Binary, and no version | `value_workspace` view | renders an empty box; both messages are **new** (SM-31) |

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Last changed, per list row | `LastChangedDate`, already parsed into `Secret.Changed` by `ListSecrets` |
| "Showing the AWSCURRENT version, id" | `VersionId` from `GetSecretValue`, already read into `Secret.VersionID` |
| After you save | `ListSecretVersionIds` and the label move in `store.AddVersion` |
| Created column, newest first | `CreatedDate` from `ListSecretVersionIds`; `SecretVersionIDs` must stop collapsing to a map |
| Diff heading | The version id in the request, and its labels |
| Confirm naming three versions | The same label map the table is drawn from |
| Rotation function link | `RotationLambdaARN` through `resourceFromARN` |
| What a failed rotation left behind | `ListSecretVersionIds`, the `AWSPENDING` row |
| Recovery window field | `DeleteSecret`'s `RecoveryWindowInDays`; `backend.DeleteSecret` takes a number instead of the constant |
| Delete now | `smDelete`'s existing `force` form value |
| The value is locked | `Secret.Deleted`, and the text of `errDeleted` |
| Binary, and no version | `SecretBinary` in the `GetSecretValue` answer; an empty `VersionIdsToStages` |
| Teaching lines | Foundation B4: ranges from `secretsmanager/validate.go` `constraintTables`, tiers from section 2 once it is in the ledger; a record per setting in `internal/settings` (none exists for Secrets Manager) |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Name, 1 to 512 | `validate.go`, `CreateSecret` `Name` |
| Name's character set | `actions.go` `secretName`. *Corrected 2026-10-11: was "hand-derived, so `doc`"; the model's text is `/_+=.@-` and the code differs (SM-56)* |
| Description, 0 to 2048 | `validate.go`, `Description` |
| Value, 1 to 65 536 | `validate.go`, `SecretString` and `SecretBinary`. *The board says "bytes"; the quotas page says characters for a string (SM-61)* |
| "Sealed with a key in the data directory, not KMS" | `SUPPORT.md`, Differences; `store.go` `newStore` |
| KMS key: recorded, checked, not used | `actions.go` `requireUsableKey`; the C tier in section 2 |
| Label meanings | `store.go` `Resolve` (default stage), `AddVersion`; `SUPPORT.md` rows for `PutSecretValue` and `UpdateSecretVersionStage` |
| "A label names one version", "only one AWSPENDING" | `SUPPORT.md`; `sdk_test.go` `TestPutSecretValueSinglePending` |
| Deprecated is still readable by id | `store.go` `Resolve`. *Corrected 2026-10-11: AWS removes unlabelled versions past 100 and a day old (SM-52); the line must say so* |
| Days, 1 to 1000 | `validate.go`, `RotationRules.AutomaticallyAfterDays` |
| "Checked, then dropped; no schedule" | SM-2; `SUPPORT.md`, Differences. The sentence changes when SM-2 is decided |
| The four steps and what each does | `rotate.go` for the names, order and payload (*the names are wrong, SM-42*); the user guide's rotation pages for what each step is for (`doc`) |
| The function needs a permission under enforce | `SUPPORT.md`, IAM ledger, "Service principals" |
| Policy: off, soft, enforce | `SUPPORT.md`, the resource-policy row and the IAM ledger |
| The same-account rule | `SUPPORT.md`, IAM ledger, "Resource policies" |
| Policy, 1 to 20 480 | `validate.go`, `ResourcePolicy` |
| Tag key 1 to 128, value 0 to 256 | `validate.go`, `TagResource`; the limit of 50 is `doc` (SM-15) |
| Recovery window 7 to 30 | `store.go` `Delete`; default 30 is `model` |
| "A sweep wakes once a minute" | `secretsmanager.go` `janitor` |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Sm-13: the shape of a function's error payload inside the message
  (`{"errorType":...,"errorMessage":...}`). The prefix is from the code; the
  body is whatever the runtime returns.
- Sm-13: "running rotation again takes the label off this one". *Corrected
  2026-10-11 on the board: false; AWS refuses (SM-45).*
- Sm-11, Sm-12: what each of the four steps does. `doc`.
- Sm-10: "it needs both version ids". `model` (SM-11, with the model's own
  contradiction), and not enforced here until SM-11.
- Sm-14: "a document that is not valid policy JSON is stored, and then never
  consulted". *Corrected 2026-10-11 on the board to the AWS behaviour (SM-5).*
- Sm-11, Sm-12: "checked, then dropped" and "never starts one by itself". True
  today; rewritten with SM-2 and SM-44.
- Sm-12: "calls the function ... and waits for it". True today; it is SM-44.
- Sm-16, Sm-17: "AWS default 30". `model`.
- Sm-17: "the date it will be purged is not returned". True of the code; what AWS
  returns as `DeletedDate` is contradictory in the model (SM-16).
- Sm-15: "policy conditions here do not read tags". From `policy.js`'s list of
  context keys, a console file. Needs the IAM ledger to say it.
- Sm-06: "rows save every value as a string". *Corrected 2026-10-11 on the
  board: false since A4.*
- Sm-08: "a version with no label is still stored here". True here; AWS removes
  unlabelled versions beyond 100 and a day old (SM-52).
- Sm-02, Sm-03: the character set including `!` (SM-56).
- Sm-04: the masked block drawn in the stored key order; the page sorts the keys
  (SM-53).

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each of the 20 boards from the dream design
(`console-design/project/Sm-*.dc.html`, 1440 by 900, light) and the real page in
the same state beside it (Playwright, a fresh data dir, the fixtures named in
the spec `e2e/tests/zz-sm-fidelity.spec.ts`, since deleted; PNGs under
`scratchpad/dcheck/sm/` as `board-*` and `real-*`), then reading both. The main
create and detail boards were also rendered in the dark theme and the main
detail page at 900 wide: **both themes and the 900 render hold** (the page's
own tokens; nothing Secrets Manager specific breaks; at 900 the notice and the
slim rail appear, which is the shell's). The first pass built none of this and
the console was never put beside its boards.

**Shell, once for every board.** The boards draw a marks-only rail (a two-letter
mark per service), a top bar reading "doze-aws harbour", a list pane headed
"Secrets" with a "New" button, a fact strip, and a key-hint footer
"j k secret / filter c new secret ⌘K jump or act". The page draws the wide rail
with labels, "doze-aws console", "Secrets Manager (4)" with a "+", and a filter
field. L12 keeps the AWS icon where a board draws a mark, and **the icon is
there**: the rail entry, the resource header and the empty state all carry
`awsIcon "sm"` (checked in every render). Everything else in that chrome is the
shell, shared by sixteen services, and is foundation C's remaining work ("the
rest of the anatomy ... not built"); it is recorded here once and not repeated
per row.

| Board | Verdict | What differs |
|---|---|---|
| Sm-Home | differs | The design's list rows carry the time each changed ("2 h", "3 d", "9 d") and a note card ("The time is when the secret last changed. new / Built from LastChangedDate ... A secret in its recovery window stays listed, struck through"); the page's rows are names only. The empty-detail sentence and the deleted row ("deleted", struck through) match. |
| Sm-01-None | matches | The sentence ("No secrets yet. A secret is encrypted at rest here and read through the same SDK call your code already makes, so nothing has to change between here and production.") and the one call to action ("New secret") are the same; the list pane says "No secrets yet / New secret →" in both. |
| Sm-02-Create | differs | The design is two columns: General and Secret value on the left, and on the right **What storing makes** (the three labels in a table and "Each later save adds a version, moves AWSCURRENT to it, and gives the old one AWSPREVIOUS") and **Generate password** ("Asks the service for 24 characters ..."). The page is one column. The design's every field has teaching lines (name and unit "Secret name · Name", the sentence "How your code asks for it. A slash is only a character here", range and "enforced" `1 – 512 characters · ... · enforced`, a live "SecretString · 38 bytes" counter, "sealed with a key in the data directory, not with KMS", "This form stores a string. A binary value is stored through the SDK"); the page has none. The CLI preview is drawn open with the real command; the page has it folded behind "cli". The design's value is static pretty JSON; the page's is the editor with "valid JSON" and Format (a real capability the board did not draw; keep). |
| Sm-03-Create-Refused | differs | The design puts the refusal directly under the Name field with "What you typed is kept. Fix the name and press Store secret again." and a card of four **other refusals this form can come back with** (`ResourceExistsException`, the pending-name `InvalidRequestException`, two `ValidationException`s). The page puts one callout **below the buttons**, after the form, and scrolls the page; no other-refusals card, no "what you typed is kept" line (it is kept). The page's why-line reads "doze-aws refused the request: it breaks a rule of the service. The input is what to change."; the design's reads "doze-aws refuses this because AWS does — the rule comes from AWS's own service model, so the same call would have failed on deploy." The code and message are the same. The `InvalidRequestException` code for an invalid name is `memory` (SM-56). |
| Sm-04-Value | differs | **Shape.** The design is tabbed (Value, Versions 5, Rotation, Resource policy, Tags) with the fact strip **above** the value (Last changed, Versions, "Rotation on · function"), "Edit details" as a text link, the full ARN with no `AWSCURRENT` chip, the View/Edit toggle with Reveal and Copy on one row, "Showing the AWSCURRENT version, 7c1e9a4b-… new", and a right-hand **After reveal** panel with the revealed value beside the masked one. The page has a View / Edit / Versions / Tags control, Rotation and Resource policy as sections below the value, the fact strip **below** the value ("Rotation: manual" / "every 0d", SM-20), a truncated ARN with a copy button and a permanent `AWSCURRENT` chip (SM-35), no version id, and Reveal swaps the masked block for a compact one-line value in place. **Content.** The page sorts the masked keys alphabetically and rewrites numbers (SM-53); the design shows the stored order. The two teaching sentences ("Only strings are masked; numbers and booleans show as they are. Copy puts the real value on the clipboard even while it is hidden.") are not on the page. |
| Sm-05-Details | differs | The design is a panel: **Description · UpdateSecret** and **KMS key ID · KmsKeyId · optional**, each with name, sentence, range and "enforced", the KMS line "checked against KMS when it is running: the key must exist and be Enabled", a refusal under the field, a card of other refusals and **Where the key goes** ("Values here are sealed with a key kept in the data directory, whatever KMS key is named ..."). The page edits in place in the header row (two bare inputs, Save, Cancel), the refusal is a floating callout beside the inputs, the KMS field never shows the key set earlier (SM-29), and there are no teaching lines. |
| Sm-06-Edit | differs | The design adds **After you save** (a VERSION / NOW / AFTER table: the new version takes `AWSCURRENT`, the current becomes `AWSPREVIOUS`, the previous loses it) and **Readers** ("A read that names no version gets the new one at once ..."). The page has neither. The design's rows had no type menu and said "Rows save every value as a string"; that is false since foundation A4 (rows keep types and have a type menu) and the board is corrected. The page shows the rows and the raw editor together under a Key/value / Plaintext control; the design draws the rows only. |
| Sm-07-Edit-Refused | differs | The refusal and its position (below the editor and buttons, "What you typed is kept. Nothing was written") are the same, and the message is the same ("1 validation error detected: Value at 'secretString' failed to satisfy constraint: Member must have length less than or equal to 65536"). The design's card of other refusals (the pending-secret refusal, the denied save), the "key/value is unavailable" notice with "70 214 bytes in all", and **The limit** card are not on the page. |
| Sm-08-Versions | differs | The design's table is VERSION / LABEL / CREATED / ACTIONS, newest first, with the time each was written ("2026-10-10 09:12"), and two cards: **Labels** (the three labels, "A label names one version", "Only one version at a time can be AWSPENDING") and **Deprecated is not gone**. The page's table is VERSION / STAGES / ACTIONS in a random order (SM-26, SM-8), no time, no cards. The "Make current" and "Diff vs current" buttons match. |
| Sm-09-Diff | differs | The design's heading names the version and its label ("2b8d05f3-… → current") and the diff is line by line over the pretty-printed value with one removed and one added line. The page's heading is always "previous → current" (SM-27), and the diff is of the stored text, which is one line for a compact value, so the whole value shows as removed and added. The design's two cards (**How it compares**, **Values are in the clear**) are not on the page. |
| Sm-10-Rollback | differs | The design's dialog is titled "Make e49a7b10-… current?" and its body names the three versions: "AWSCURRENT moves to this version. 7c1e9a4b…, which is current now, becomes AWSPREVIOUS, and 2b8d05f3…, which was AWSPREVIOUS, loses that label. No version is written; only labels move." The page's is "Make this version current? The one that is current now becomes AWSPREVIOUS." with Cancel and "Make current". True, and less. |
| Sm-11-Rotation-Off | differs | The design has teaching fields (**Rotation function · RotationLambdaARN**, "Required · a function in this stack"; **Every · AutomaticallyAfterDays · days**, "1 – 1 000 · the form offers 1 – 365 · checked, then dropped"), a card **Enable rotation also runs it** ("Saving calls the function through all four steps ... before the page answers"), and **The four steps** with the payload. The page has one line ("Point at a Lambda that implements the rotation protocol; doze-aws invokes it on the schedule (and on demand)", false, SM-21), the two bare inputs, and no cards. The design's payload used `createSecret` (corrected on the board to `create_secret`, SM-42). The days row waits on the SM-2 decision. |
| Sm-12-Rotation-On | cannot match yet | Waits on SM-44 (asynchronous rotation) for **While it runs**, and on SM-2 for **The days are not here** and the "every N days" fact. The design shows a function link and "Last rotated", a **What each button does** card and the BEFORE / AFTER FINISHSECRET table. The page shows the receipt "✓ Rotating with fn every 0 days · last rotated 2026-10-11 17:40" and "doze-aws runs the real 4-step rotation protocol against your local function", Rotate now and Disable rotation; "every 0d" in the strip (SM-20); the function name is plain text. |
| Sm-13-Rotation-Failed | design is wrong | See "Where the design is wrong". The premise (the call fails with a 500 and "A 5xx here is a bug in the emulator ... worth reporting", while the same card says it is the user's function) contradicts foundation A2's "Function error" row and, under SM-44, the model (the failure is not in the call). The page draws exactly the red **Failed** callout the board draws. Not drawn on the page: the log-group link, **What it left behind** (the `AWSPENDING` row), **Other messages**. |
| Sm-14-Policy | differs | The builder and JSON toggle, the "Configured" / "Not set" badge, Edit / Close, Save and Remove match (the shared `policy_builder`). The design's four cards (**When it is consulted** off/soft/enforce, **How it decides**, **What is checked on save**, **A secret with none**) are not on the page. The "not valid JSON is stored" sentence was corrected on the board (SM-5, SM-59). |
| Sm-15-Tags | differs | The draft editor (rows, Add tag, Unsaved changes, Cancel / Save changes) is the same shape (A5). The design's **What a tag is here** card ("Key 1 – 128 characters · value 0 – 256 · enforced ... Saving writes only the rows that differ"), the refused callout with `tags.1.key` and the empty state's card are not on the page. The design is silent on the 50-tag limit (`doc`, SM-15), which the line must add as "not enforced yet" until SM-15 is fixed. |
| Sm-16-Confirms | differs | The design's three confirms have bodies and "Keep it" buttons ("It waits out a 7-day recovery window. Meanwhile its value cannot be read and its name cannot be used for another secret ..."; "The function is no longer called for this secret ..."; "The policy is deleted from the secret. Only identity policies will decide access.") and the delete dialog carries a **Recovery window** field and **Delete now, with no window**. The page's dialogs are one sentence each with the verb button: "Schedule “x” for deletion with a 7-day recovery window?" [Cancel] [Schedule]; "Turn off automatic rotation?" [Cancel] [Turn off] (SM-36); "Remove the resource policy? Only identity policies will decide access." (read, same words as the design's body). No window field, no delete-now (SM-30). |
| Sm-17-Pending | differs | The page has the "pending deletion" badge, the warning ("⚠ Scheduled for deletion since ... the value is locked until the recovery window ends." and the restore sentence), Restore, and hides Rotation and Resource policy (matches). Missing: **The value is locked** with the real `GetSecretValue` message in the value box (the page draws an empty box with Reveal and Copy), **The recovery window** card and **While it waits** (SM-33, SM-31). The page still offers Edit, Versions "Make current" and the tag editor. |
| Sm-18-Denied | differs | The callout is the design's text ("Denied AccessDeniedException ... because the resource policy denies it (DenyConsoleWrites)", "A policy decided this, not the input: the request was well formed. The fix is the policy. Access log →") and sits in the same place with the "unsaved changes" bar. Missing: **The statement that did it** (the policy statement quoted, "Resource policy tab →") and **Why only a Deny can do this**. |
| Sm-19-No-Value | differs | Both states draw an empty box with Reveal and Copy; the design says "This version holds binary data" with its explanation and "No version yet" with the real refusal. The "Versions 0" strip fact is right on the page for the version-less secret. |

**Where the design is wrong** (corrected in the working copy on 2026-10-11 where it
is a stale fact: Sm-06, Sm-11, Sm-13 and Sm-14; the published canvas carries the
old text until it is republished)

- **Sm-13: "Running rotation again writes a fresh pending version and takes the
  label off this one".** Model: "If the AWSPENDING staging label is present but
  not attached to the same version as AWSCURRENT, then any later invocation of
  RotateSecret assumes that a previous rotation request is still in progress and
  returns an error." *Corrected on the board: "Running rotation again is
  refused while that label stays on a version that is not current: AWS reads it
  as a rotation still in progress. The label has to come off first."* (SM-45.)
- **Sm-13: the premise.** It draws the user's function failing as the call
  failing, in red, and then explains that this is not an emulator bug. The
  foundation's table gives that case its own state (a peer the user owns
  answered with an error, naming the function), and the model says rotation is
  asynchronous, so the call does not fail. The board is **not redrawn** until
  SM-44 is decided; once it is, the board shows the rotation's state on the page
  (Describe, the `AWSPENDING` row) and not a refused call.
- **Sm-11: `"Step": "createSecret"`.** The user guide's parameter list names
  `create_secret`, `set_secret`, `test_secret` and `finish_secret` (SM-42).
  *Corrected on the board for the sample payload;* the card's headings stay,
  because the guide uses those names for the methods.
- **Sm-06: "Rows save every value as a string".** False since foundation A4.
  *Corrected on the board to "Each row keeps the JSON type it arrived with ...
  The menu beside a row changes its type on purpose, and a number row holding
  words is saved as text and says so."*
- **Sm-14: "A document that is not valid policy JSON is stored, and then never
  consulted."** AWS refuses it (`MalformedPolicyDocumentException`, model).
  *Corrected on the board; the code is SM-5.*
- **Sm-02 and Sm-03 (and the Name line everywhere): the character set with
  `!`.** The model says `/_+=.@-` (SM-56). **Not edited:** the recalled AWS
  refusal text lists `!`, the recording decides.
- **Sm-08: "A version with no label is still stored here, and a read by its
  version id returns it."** True here and not the whole truth: AWS removes
  unlabelled versions past 100 and a day old (SM-52), which the card must say.
- **Sm-02, Sm-04: "1 – 65 536 bytes".** For a string the unit is characters
  (doc, SM-61). Not edited; the line is rebuilt from the settings record.
- **Sm-12: "never starts one by itself".** Becomes false if the schedule is
  built (SM-2); and "waits for it" is false of AWS (SM-44). Decisions first.

**Build list, in order** (all open; none of it changes behaviour except where a
board needs one, named)

1. Shell anatomy (foundation C, shared): marks-only rail, top bar, list-pane
   header, footer hints. Not done here, tracked once.
2. Data the boards read (`client_sec.go`): the version list with `CreatedDate`
   newest first (needs SM-8's stable order), `KmsKeyId`, string versus binary
   versus none, `RotationRules`/`NextRotationDate`, the `AWSCURRENT` version id;
   a `NextToken` loop for `ListSecrets` and `ListSecretVersionIds` (SM-54). The
   masked view keeps the stored order and number tokens (SM-53).
3. Page anatomy: Value / Versions / Rotation / Resource policy / Tags as tabs
   (`tab=` and `secretDetailView`'s mode), the kit's `res_header` with the fact
   strip **above** the value, "Edit details" link, the full ARN, no permanent
   `AWSCURRENT` chip (SM-35), "Showing the AWSCURRENT version", After reveal.
4. Create: two columns, a `setting_field` per field with the five teaching lines
   from a settings record per setting (name, description, value, KMS key,
   recovery window, rotation function, days, schedule, duration, policy, tag key
   and value, label), the byte counter, **What storing makes** and **Generate
   password** cards, the CLI preview open, the refusal beside its field with the
   other-refusals card. Behaviour: binary, KMS key and tags on the form only if
   decided (SM-32).
5. Details as a panel: both fields, teaching lines, the key shown, **Where the
   key goes**. Behaviour: SM-29, SM-40 (the empty key and description).
6. Edit: **After you save** and **Readers**, from `ListSecretVersionIds` and the
   label move.
7. Versions: Created column, order, **Labels** and **Deprecated is not gone**
   (with the 100 and 24-hour rule, SM-52); Diff heading naming the version;
   an absent version is a refusal (SM-27); the three-version Make-current body.
8. Rotation: teaching fields, **Enable rotation also runs it**, the four steps
   with the real names; On state with the function link, last rotated, the rules;
   **While it runs** and the Failed / left-behind states need SM-44; the days row
   needs SM-2; the Function-error state from A2.
9. Policy and tags cards; the malformed-document refusal (needs SM-5); the
   50-tag line stated as enforced or not.
10. Confirms with bodies and "Keep it"; the recovery-window field (7 to 30,
    default 30) and **Delete now** (the handler already takes `force`).
11. Pending page: the locked-value message, the window card, **While it waits**,
    and no Edit, Make current or tag editor (SM-33).
12. Binary and no-value messages (SM-31); Denied's two cards; Home's time column
    and note.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Token replay with other content (SM-1, SM-39); stage move rules (SM-11); id and stage mismatch (SM-12); policy that will not parse (SM-5, SM-59); every operation on a pending secret (SM-6); replica members (SM-18); random-password flags and the empty alphabet (SM-13, SM-41); tag rules (SM-15); first version gets `AWSCURRENT` (SM-49); an ARN that is not the secret's (SM-50); janitor under a test clock, including unlabelled versions (SM-52) |
| SDK v2 | Paging, filters, order and bad tokens on `ListSecrets`, `ListSecretVersionIds`, `BatchGetSecretValue` (SM-7 to SM-9, SM-48); force-delete an absent secret (SM-14); describe shapes (SM-16, SM-17); rotation rules round trip, `RotateImmediately`, asynchronous return, step names, the state the function finds, a stranded `AWSPENDING`, a failing and a missing function (SM-2 to SM-4, SM-42 to SM-47); declined operations once (SM-19); a string of multi-byte characters (SM-61) |
| Rejection parity | The 20 `RotateSecret` cases, with a stub Lambda peer; the 9 list-length cases after regenerating (SM-51); the hand-derived name rule |
| Restart | Versions and labels, a pending deletion, tags, rotation state (foundation B5) |
| Second identity | The package's SDK tests under a non-default account and region (foundation B2); the ARN rule (SM-50) under it |
| Console handlers | The fragment after put, promote and each rotation action matches a reload (already: `fragments_test.go`); diff on an absent version (SM-27); versions in time order (SM-26); masked key order and numbers (SM-53); a list over 100 (SM-54) |
| Conformance (boto3) | Every row in section 8 |
| Browser | Clear a description, set a KMS key (SM-29); binary and version-less secrets (SM-31); a pending secret offers only Restore and reading (SM-33); the window field and immediate delete (SM-30); each `new` item; each confirm's sentence; the rotation test asserts the typed number; **the fidelity pairs** (section 6a) once the kit's board-beside-page task exists (B14) |
| Mutation | `secretsmanager/` baseline (118 killed, 46 survived), then survivors in `store.go` (label moves), `actions.go` (`updateSecretVersionStage`, `getRandomPassword`) |

## 8. Recording queue

Every one of these is `doc` or `memory` today unless it says `model`. Scenario
names are in `conformance/tests/test_secretsmanager.py`.

| Claim | Source now | Scenario |
|---|---|---|
| Same token, different content, on create and put fails; on update an existing token always fails; the code | model (the rule), memory (the code) | `token_replay_conflict` |
| The name character set (`/_+=.@-` or with `!`), and the text and code of the refusal | model (the set), memory (the rest) | `name_rules` |
| Create with the name of a pending secret: code and message | memory | `create_over_pending` |
| Create with no value: is a `VersionId` returned | memory (model: no version is created) | `create_without_value` |
| `UpdateSecret` with an empty description | memory | `clear_description` |
| `UpdateSecret` with an empty `KmsKeyId` | model | `clear_kms_key` |
| Each mutating operation against a pending secret | model (code and cause), memory (the per-operation set) | `pending_refusals` |
| `DeletedDate` in describe and in list, `DeletionDate` in delete | memory (the model contradicts itself) | `deletion_dates` |
| Force-delete of an absent secret: the answer | model (no `ResourceNotFoundException`), memory (the shape) | `force_delete_absent` |
| Which empty members `DescribeSecret` omits | model | `describe_shape` |
| Moving a held label without `RemoveFromVersionId` (the model contradicts itself) | model, memory | `stage_move_requires_remove` |
| Removing `AWSCURRENT` by `RemoveFromVersionId` alone | memory | `remove_awscurrent` |
| `VersionId` and `VersionStage` that disagree: the code | model (the rule), memory (the code) | `id_stage_mismatch` |
| Deprecated versions: removed past 100 and a day old; `LimitExceededException` at 100 versions and 20 labels | model, doc | `deprecated_version_pruning`, `quotas` |
| `ListSecrets` filters, negation, sort, default order, page size | model (filters, sort), memory (default size, default `SortOrder`) | `list_filters_and_paging` |
| A made-up `NextToken` | model | `invalid_next_token` |
| `BatchGetSecretValue` with filters, with both, with neither: the code | model (the rule), memory (the code) | `batch_get_filters` |
| A policy that is not a policy: code and message | model (code), memory (message) | `malformed_policy` |
| `BlockPublicPolicy` and what counts as broad | model (code, intent), memory (what counts) | `block_public_policy` |
| `ValidateResourcePolicy` on a malformed document and on an absent secret | model (codes), memory (the answer shape) | `validate_policy` |
| `RotateSecret` with a function that does not exist | memory | `rotate_missing_function` |
| `RotateSecret` with a function that throws, and a stranded `AWSPENDING` | model (asynchronous; refused while stranded), memory (the code) | `rotate_failing_function`, `rotate_while_pending` |
| `RotateSecret` with no function configured | model | `rotate_no_function` |
| `RotateImmediately: false` runs the test step only | model | `rotate_not_immediately` |
| The rotation function's `Step` values, and the state it finds | doc, model | `rotation_step_names`, `rotation_state_at_invoke` |
| `RotationRules` returned by describe, and `NextRotationDate` | model | `rotation_rules_round_trip` |
| `CancelRotateSecret` on a secret that never rotated; its `VersionId` | memory, model | `cancel_never_rotated` |
| `GetRandomPassword` flags, `RequireEachIncludedType`'s default, an empty alphabet | model, memory | `random_password_flags` |
| Tag limit of 50, `aws:` keys, duplicate keys in one call | doc (the Tag Editor page), memory (the code) | `tag_rules` |
| The unusable-KMS-key codes | model (`EncryptionFailure`), memory (missing key) | `kms_key_refusals` |
| A stale or foreign ARN | model | `arn_identity` |
| A string of 65,536 multi-byte characters | doc | `string_length_in_characters` |
| The code for a replication call where replication is unavailable | memory (here: 501 by D5) | `replication_refusal` |
| What `Type`, `RotationToken` and the external-rotation members do | model (what they are), memory (effects) | `type_and_rotation_token` |
| A table violation's code (`ValidationException` here) | memory (the model lists no such code) | `validation_code` |

The four scenarios that exist are also unverified.

## 9. Owed to integration

| What | With |
|---|---|
| Rotation end to end with a real function, all four steps, and under enforce with and without the function's permission for `secretsmanager.amazonaws.com` | Lambda, IAM |
| A rotation function that fails is the function's failure on the wire and in the console, with a link to its log lines | Lambda, Logs (already row 133 of `90-integration.md`) |
| A rotation function that calls back into Secrets Manager while `RotateSecret` holds the request: no deadlock on the store (disappears if SM-44 makes rotation a background task, and the callbacks then run beside it) | Lambda |
| A scheduled rotation firing, if SM-2 is built | Lambda, the clock |
| `KmsKeyId` naming a key that is then disabled or scheduled for deletion: what a later read and write do | KMS |
| A function reading a secret under enforce through its role and through the secret's policy | Lambda, IAM |
| `AWS::SecretsManager::Secret` with `GenerateSecretString`, `SecretString`, tags and a replica; `RotationSchedule` and `ResourcePolicy` resources; `{{resolve:secretsmanager:...}}` references | CloudFormation |
| `provision` applying a secret twice: one version, not two | provision |
| The wire and the access log showing a rotation's four invokes nested under `RotateSecret` | console, trace |
| String lengths in characters (SM-61), once, for every service's tables | foundation |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SM-1 to SM-61 each closed, with the test that failed first named beside
      it, or moved to section 8 or 9 with a reason. (SM-23, SM-24, SM-25, SM-28
      and SM-37 are closed already; their tests are named in section 4.)
- [ ] Decided and written into `SUPPORT.md`:
  - [ ] the rotation schedule (SM-2): rules are stored and returned in either
        case; then either a tick in the janitor fires rotations and the tier is
        F, or nothing fires, the tier is C, and the ledger, the form and the
        Rotation tab all say so;
  - [ ] **each asynchronous operation (L7)**: `RotateSecret` (SM-44: modelled
        as a background task with a visible running state, or inline with the
        difference listed); scheduled rotation; force deletion ("an asynchronous
        background process", immediate here, a listed difference with no switch
        because AWS's refusal for an immediate re-create is not in the model);
        the purge at the window's end (a background task with no guarantee: the
        once-a-minute janitor stays); `ListSecrets`' five-minute
        eventual consistency (immediate here, a listed difference); version
        pruning (SM-52);
  - [ ] **each time- and consistency-based rule (L8)** and its opt-in switch
        (off by default, in memory): none of those above has a refusal the model
        names except the version quota; decide whether `LimitExceededException`
        at 100 versions is on by default (a quota, so recommended on) or behind
        a switch;
  - [ ] what `ValidateResourcePolicy` and `BlockPublicPolicy` check (SM-59,
        SM-60), and the stated approximation of "broad";
  - [ ] the code the three declined operations answer with is settled by D5
        (SM-19): ticked when the three are in `notbuilt`;
  - [ ] deprecated-version pruning: modelled in the janitor under a test clock,
        or a listed difference (SM-52);
  - [ ] which of binary, KMS key and tags the create form gains (SM-32);
  - [ ] whether the diff honours Reveal (SM-34).
- [ ] The member table in section 2 has no "ignored" and no "decide" left:
      `Type`, `RotationToken`, `BlockPublicPolicy`, the two
      `ExternalSecretRotation*` members and each absent `DescribeSecret` field
      is F, C and says so, or refused.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] No request member in `constraintTables` is validated and then unread. A
      test walks the table against the handlers, or the list of exceptions is
      written here: ____.
- [ ] **Wire (L5):** one protocol, `awsJson1_1`. Every one of the 23 operations
      is accepted in the shape the API reference spells it (checked by the
      sweep and the SDK tests), blobs are base64 and timestamps epoch seconds,
      and the list-length cases are regenerated (SM-51). There is no Query or
      REST wire, no `xmlName` and no flattened member: N/A, with that sentence.
- [ ] **Fidelity (L10):** section 6a exists, was made from rendered boards
      beside rendered pages in both themes and at 900 wide, and **no row says
      "differs" without a decision** (the build list is the decision; a row left
      unbuilt is moved to "differs deliberately" with its reason).
- [ ] **Icons (L12) kept:** the AWS icon from `static/aws/sm.svg` is in the rail,
      the resource header and the empty state, wherever a board draws "SM".
- [ ] **Teaching lines (L11):** every setting carries all five lines (name and
      unit; the value in words; one sentence; range, default and enforced or
      stored from the settings record; the neighbour check with real numbers)
      on the create form, Details, Rotation and Policy; a record per setting
      exists in `internal/settings`.
- [ ] `KmsKeyId` reads as cosmetic on the create form (if added) and on Details.
- [ ] The detail page is rendered from one view model: a test renders the
      fragment after every `sm*` mutation and compares it with the same region
      of a full page load. *Done by foundation A15 (`fragments_test.go`); this
      box stays for the rotation actions and the new tabs.*
- [ ] No URL in `sm.html` or its `value_workspace` call is built by `printf`
      without escaping; the browser suite runs once with names containing `+`,
      `=`, `@` and `/`. *Done by A14 for the existing links; stays for the new
      tabs.*
- [ ] All 20 boards implemented from the foundation's shell, and each `new`
      item reads only the data named for it in section 6.
- [ ] No statement on a Secrets Manager screen is in the "not yet backed" list.
- [ ] The four confirms (delete, disable rotation, remove policy, make
      current) say only what the code does; Restore, Enable rotation and
      Rotate now do not ask, and the form states that a function will run.
- [ ] `e2e/tests/secretsmanager.spec.ts` covers every route under `/sm` with
      the route gate on, and the accessibility sweep is clean on create and
      all five tabs, for a live and a pending secret.
- [ ] Conformance: scenarios for every row in section 8 exist. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons
      (today 118 and 46, recorded 2026-10-10).
- [ ] Actual time recorded in section 1, against the **L, eight to eleven
      days** estimate.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 0 | Reading: the model's documentation, the user guide's rotation pages and the quotas page, as the first act (protocol step 1); regenerate the cases (B8) | `.audit-models/secrets-manager.json`, `secretsmanager/testdata/cases_secretsmanager.json` |
| 1 | Contract: member tiers, the decisions in section 10 | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SM-1 to SM-19 and SM-39 to SM-61 | `secretsmanager/sdk_test.go`, `conformance_fixes_test.go`, `rotate_test.go`, a new handler-level test file in `secretsmanager/` |
| 3 | Idempotency, stage and identity rules (SM-1, SM-11, SM-12, SM-17, SM-39, SM-40, SM-49, SM-50) | `secretsmanager/store.go`, `actions.go` |
| 4 | Pending-deletion guard in one place; force delete; dates (SM-6, SM-14, SM-16) | `secretsmanager/store.go` (`Mutate`), `actions.go` (`describe`) |
| 5 | Paging, tokens, filters, sort, batch (SM-7 to SM-9, SM-48), quotas and pruning (SM-52) | `secretsmanager/actions.go`, a new `list.go` beside it, `internal/awspage` (foundation B11) |
| 6 | Policy validation, tags, random password, replica members, declined operations (SM-5, SM-13, SM-15, SM-18, SM-19, SM-41, SM-59, SM-60) | `secretsmanager/actions.go`, `guard.go`, `awshttp.NotBuilt` |
| 7 | Rotation: step names, state at invoke, rules stored and returned, `RotateImmediately`, asynchronous run and its failures, the stranded label, and the schedule if decided (SM-2 to SM-4, SM-42 to SM-47) | `secretsmanager/rotate.go`, `store.go`, `secretsmanager.go` (`janitor`), `rejection_parity_test.go` |
| 8 | Section 5 sweep, restart tests | `secretsmanager/*_test.go`, `persistence_test.go` |
| 9 | Conformance scenarios and claims table | `conformance/tests/test_secretsmanager.py`, the claims file from foundation B1 |
| 10 | Console data: version list with dates, KMS key, binary, window and force, paging loops, masking (SM-26 to SM-31, SM-53, SM-54) | `internal/console/client_sec.go`, `handlers_sec.go`, `handlers_sm_policy.go`, `valueworkspace.go` |
| 11 | Console pages to shape, per the build list in 6a (SM-20 to SM-22, SM-32 to SM-36, SM-57) | `internal/console/templates/sm.html`, `create.html` (`sm_create`), `workspace.html`, `kit.html` (`res_header`, `setting_field`), `routes_config.go`, `internal/settings` |
| 12 | Browser tests, and the fidelity pairs | `e2e/tests/secretsmanager.spec.ts` |
| 13 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 10 to 12 lean on foundation A2, B4 and C. `workspace.html` is shared with
Parameter Store (stage 2); A15 already put both on one view function.

**Prior art.** AWS's user guide page on rotation Lambda functions is the source
for the four steps, the payload, and which step moves which label; its sample
rotation function is the reference for what `rotate_test.go`'s function should
do and, once SM-42 and SM-43 are fixed, what the function sees. The API
reference page for each operation is the source for every `doc` row in sections
4 and 8. The SQS stage's move task (`sqs/move_task.go`, SQS-29) is the in-repo
precedent for SM-44: a background task with a visible running state, one per
resource, cancellable, memory-only. No other emulator's behaviour is cited here
because none was checked.

## 12. Pre-stage review (2026-10-11)

Answers the twelve questions in [../02-lessons-from-sqs.md](../02-lessons-from-sqs.md).
Method: every operation's and member's documentation in
`.audit-models/secrets-manager.json` read (23 operations, 16 error shapes, 4
enums, the member docs of every input and output shape; the model has no
`awsQueryError`, `xmlName`, `jsonName` or `httpError`); the package, the
console's `sm*` code and templates read; a binary built into a scratch
directory and driven over the JSON wire (about 200 calls) and through the
console (Playwright, a fresh data dir); the user guide's quotas, rotation and
Lambda-function pages and the Tag Editor guide's tagging limits fetched and
read; the 20 boards rendered beside the pages.

| Q | Answer | Findings and what was read |
|---|---|---|
| **L1** Read the model | **Findings.** 30 claims in this document corrected in place (each marked *Corrected 2026-10-11*): the test and browser counts, the mutation baseline, the finding ids in the contract table, the source of SM-1, SM-3, SM-4, SM-6, SM-7, SM-9, SM-11, SM-12, SM-14, SM-15, SM-16, SM-17, SM-19, SM-30 and the name character set, the deprecated-version rule, `Type`, `RotationToken`, `BlockPublicPolicy`, the external-rotation members, `ValidateResourcePolicy`, the estimate. Seven `doc` tags became `model` (SM-3, SM-7, SM-9, SM-11, SM-12, SM-14, SM-30) and four `memory` tags were upgraded in part (SM-4, SM-6, SM-16, SM-17); one `doc` claim (50 tags) was not in the model and was found on a named page; two more are contradictions inside the model (SM-11, SM-16, and the `RequireEachIncludedType` default in SM-13) and are left `memory`. | SM-1 to SM-19 re-sourced; SM-42, SM-52, SM-61 from `doc` pages; the whole of section 2's member table |
| **L2** Optional parameters | **Findings.** "You must include Filters or SecretIdList, but not both", "To use this parameter, you must also use the Filters parameter", "If you set this to an empty string", "If you don't include VersionStages", "If you don't specify this value, Secrets Manager rotates the secret immediately", "If you forcibly delete an already deleted or nonexistent secret", "If this operation creates the first version", "only returns fields that have a value", "default length is 32". | SM-3, SM-9, SM-14, SM-16, SM-40, SM-49 |
| **L3** Error codes | **Findings.** Per operation: `RotateSecret` with no function is `InvalidRequestException` (SM-46); a pending secret is `InvalidRequestException` and two operations must not raise it (SM-6); `EncryptionFailure` for an unusable key (SM-55); `InvalidNextTokenException` (SM-48); `MalformedPolicyDocumentException`, `PublicPolicyException` (SM-5, SM-60, SM-59); `LimitExceededException` (SM-52); a function's failure is not in `RotateSecret`'s errors (SM-4). **Not established by the model** and kept `memory`: the code for a token conflict, a table violation (`ValidationException` is in no operation's list), an invalid name, the declined operations' AWS code, an unknown operation. | SM-4, SM-5, SM-6, SM-46, SM-48, SM-52, SM-55, SM-59, SM-60; the model has no per-protocol status, only `client`/`server`, so every refusal is HTTP 400 and `InternalServiceError` 500 |
| **L4** Direction | **Findings.** Stricter: SM-14, SM-57, SM-61. More forgiving: SM-1, SM-5, SM-6, SM-11, SM-12, SM-39, SM-45, SM-47, SM-48, SM-50, SM-51, SM-52, SM-59, SM-60. Wrong code: SM-4, SM-19, SM-46, SM-55. Wrong shape: SM-7, SM-16. Wrong order or time: SM-8, SM-43, SM-44. Missing: SM-2, SM-58. Every row names its direction. | section 4 |
| **L5** Every wire | **Checked, none beyond the wire facts.** One protocol, `awsJson1_1`. Content type, target prefix, blob base64, epoch-second timestamps and the error `__type` and `x-amzn-errortype` read and run; `application/json` and JSON 1.0 content types are tolerated. No flattened or renamed member exists. What the single wire does get wrong: the list-length constraints (SM-51) and string lengths (SM-61). The SDK that exercises it is Go v2 and boto3. | the service shape's traits; a grep of the model for `xmlName`, `xmlFlattened`, `jsonName`, `awsQueryError`; `secretsmanager/secretsmanager.go` `ServeHTTP` |
| **L6** Tokens | **Findings.** `ClientRequestToken` is the version id: same token with other content (SM-1, SM-39), an existing token on update (SM-39), a rotation's token the function receives (SM-3, SM-43); `NextToken` is never validated (SM-48); a version id given with a stage (SM-12); the ARN's suffix as an identity token (SM-50); `RotationToken` (section 2, C). Not established: a pagination token's scope across filters. | SM-1, SM-12, SM-39, SM-43, SM-48, SM-50 |
| **L7** Asynchronous on AWS | **Findings and a decision list.** `RotateSecret` ("Rotation is an asynchronous process"): SM-44 and section 10. Scheduled rotation (SM-2). Force deletion ("an asynchronous background process, so there might be a short delay"): immediate here, listed difference. The purge ("a background task with low priority. There is no guarantee of a specific time"): the once-a-minute janitor stays. `ListSecrets` ("eventually consistent ... might not reflect changes from the last five minutes"): immediate here, listed difference, not simulated. Version pruning (SM-52). Replication (declined). Boards that draw an in-progress state: Sm-12 ("While it runs"), Sm-13: they need SM-44. | SM-2, SM-44, SM-52; section 10 |
| **L8** Latency rules, opt-in | **Checked; one row.** The model names no refusal for an immediate re-create after a force delete (only "use appropriate back off and retry logic"), so there is nothing to switch on and the difference is listed. The version quota is a count, not a latency rule, and is recommended **on** (section 10). No `--secretsmanager-strict` is needed unless the quota decision goes the other way. | `DeleteSecret`, `ListSecrets`, `PutSecretValue` documentation |
| **L9** State persistence | **Checked.** Versions, labels, tags, policy, the deletion window and its purge time, rotation configuration and `LastRotatedDate` persist (they are the application's). A running rotation (SM-44) and any in-flight scheduled-rotation due time are the process's and are not carried; a restart mid-rotation leaves an `AWSPENDING` the next call must treat as stranded (SM-45). The sealing key persists in the data dir. | `store.go` `secret` struct, `restart_test.go`, `persistence_test.go` |
| **L10** The board is the spec | **Findings.** All 20 boards rendered beside the page in light; Sm-02 and Sm-04 in dark and Sm-04 at 900. 1 matches, 17 differ, 1 cannot match yet, 1 design is wrong (section 6a). Four boards carried stale facts and are corrected in the working copy. | section 6a; SM-53 found only by putting Sm-04 beside its page |
| **L11** Five teaching lines | **Findings, as build items.** The page has none on any setting; there is no `internal/settings` record for Secrets Manager. The settings: name, description, value, KMS key, recovery window, rotation function, days, schedule, duration, rotate immediately, policy, block public policy, tag key and value, label (and the two limits of 100 versions and 20 labels as a neighbour check on the Versions tab). | section 6a build list items 4, 5, 8, 9; section 10 |
| **L12** Icons | **Checked, none.** `awsIcon "sm"` is in the rail, the resource header and the empty state in every render; boards' "SM" mark maps to it. The list-pane header has no icon in the board or the page. The rest of the chrome is the shell's (6a). | every render in section 6a |

**Classes of mistake this service is most at risk of**

1. **A rule written from a console message or a memory of the API** (the name
   character set, the 50 tags, "doc" on a thing the model also says). This
   document did it in 14 rows; the first act of the stage is to read the
   documentation text again, per member, before writing a test.
2. **An operation treated as synchronous that is not** (rotation, force delete,
   the purge, list freshness), and a board built on the synchronous version.
   Rotation is the whole of the risk: SM-42 to SM-47 are one design question,
   asked six ways, and none of them can be tested against a function written for
   this emulator.
3. **Identity by name instead of by token** (the ARN's suffix, the version id,
   the client token, the next token). SQS-23, SQS-27 and SM-48 to SM-50 are the
   same mistake: the emulator resolves what the caller means, and AWS refuses
   what the caller named wrongly.
4. **A refusal that skips a state** (pending deletion, a stranded
   `AWSPENDING`, an absent secret on a forced delete): every operation is
   written to its happy path and the state check is added to the four the author
   thought of. A table over operations, not a check per handler.
5. **A console that renders from a second source of truth** (the masked view
   re-encoding, partial re-renders, the version map). A15 fixed the partials;
   SM-53 shows the view itself is still a second parse of the value.
6. **A decided-by-the-code sentence on a board** ("checked, then dropped",
   "stored and never consulted", "waits for it"): each of these was true of the
   code on the day the board was written and false of AWS, and the board's
   teaching line reads as a statement about AWS.

**Needs a human decision before the stage starts:** (1) `RotateSecret`
asynchronous (recommended) or inline with the difference listed (SM-44); (2) the
rotation schedule fires or is declared cosmetic (SM-2); (3) the version and
label quotas on by default (SM-52); (4) which of binary, KMS key and tags the
create form gains (SM-32); (5) `ValidateResourcePolicy` as the syntax check
only, or with the wildcard-principal approximation (SM-59, SM-60).
