# Secrets Manager

Stage 4. Size M (see the note on size in section 1). Follows
[../01-protocol.md](../01-protocol.md); the common acceptance criteria there
apply in full, and this page adds Secrets Manager's own.

Line numbers are as found on 2026-10-10 and will drift; the symbol beside each
one will not. "Run" below means reproduced on that date against a binary built
from the working tree into a scratch directory, default flags (`--iam-mode soft`),
over the JSON 1.1 wire and the console's own routes.

## 1. Summary

Secrets with versions and staging labels over one bbolt store
(`secretsmanager.bolt`), values sealed with a per-data-dir AES-256-GCM key, on
one wire protocol (AWS JSON 1.1, target prefix `secretsmanager`). A janitor
purges secrets whose recovery window has passed, once a minute.

Peers: it calls **Lambda** (rotation, four synchronous invokes as the
`secretsmanager.amazonaws.com` principal) and **KMS** (`DescribeKey`, to check a
named key exists and is enabled; the key is never used to encrypt). It is
called by `provision` and CloudFormation (`AWS::SecretsManager::Secret`), and
its resource policy is evaluated through the shared IAM guard.

The API is small and its happy paths are sound. What grounding and the run
turned up is mostly at the edges: request members that are validated and then
ignored (paging, filters, rotation rules, half of `GetRandomPassword`),
idempotency tokens that hide a conflicting write, and a console detail page
whose partial re-renders each forget a different piece of the page.

**Estimate:** M, three to four focused days. **Actual:** _fill in when done._
The findings table has 38 rows, 2 of them P0 and 17 P1. If the rotation
schedule (SM-2) is built rather than declared cosmetic, this is an L.

## 2. Contract

The model lists **23 operations** (`secretsmanager/testdata/ops_secrets-manager.json`).
Twenty are dispatched (`secretsmanager/actions.go`, `handlers`); three are
registered as refusals in `init`.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateSecret | F | F | Token replay hides a conflict (SM-1). Replica members ignored (SM-18). |
| GetSecretValue | F | F | Id and stage that disagree (SM-13). |
| BatchGetSecretValue | F | F | `Filters` ignored (SM-9). |
| PutSecretValue | F | F | SM-1. |
| UpdateSecret | F | F | Cannot clear a description (SM-10). |
| DeleteSecret | F | F | Force on an absent secret (SM-15). |
| RestoreSecret | F | F | |
| ListSecrets | F | F | No paging, filters or sort (SM-7). |
| DescribeSecret | F | F | Shape gaps (SM-17). |
| ListSecretVersionIds | F | F | No paging (SM-8). |
| UpdateSecretVersionStage | F | F | SM-11, SM-12. |
| TagResource, UntagResource | F | F | SM-12, SM-16. |
| GetRandomPassword | F | F | Five members ignored (SM-14). |
| PutResourcePolicy, GetResourcePolicy, DeleteResourcePolicy | F | F | Stores a document it cannot parse (SM-5). |
| ValidateResourcePolicy | C | C | Always passes. Target is to keep it C and have it at least refuse what `PutResourcePolicy` will refuse after SM-5; decide in section 10. |
| RotateSecret, CancelRotateSecret | F | F | Rules dropped (SM-2), always immediate (SM-3), failure is a 500 (SM-4). |
| ReplicateSecretToRegions, RemoveRegionsFromReplication, StopReplicationToReplica | S | S | Declined: one region. The code they answer with is not established (SM-19). |

Request members and settings need their own tiers, because "the operation is F"
hides which of them do anything. "Ignored" means validated by the model table
and then never read.

| Operation | Member | Now | Target | Note |
|---|---|---|---|---|
| CreateSecret, UpdateSecret | `KmsKeyId` | C | C | Checked against KMS when wired (must exist, be Enabled); recorded; never used. The console must say so. |
| CreateSecret | `Tags`, `Description`, `SecretString`, `SecretBinary` | F | F | |
| CreateSecret, PutSecretValue, UpdateSecret | `ClientRequestToken` | F, wrong on conflict | F | SM-1 |
| CreateSecret | `AddReplicaRegions`, `ForceOverwriteReplicaSecret` | ignored | S (refuse) | SM-18 |
| CreateSecret, UpdateSecret | `Type` | ignored | not established | What AWS does with it is not established. |
| PutSecretValue | `VersionStages` | F | F | |
| PutSecretValue | `RotationToken` | ignored | not established | |
| DeleteSecret | `RecoveryWindowInDays`, `ForceDeleteWithoutRecovery` | F | F | |
| ListSecrets | `IncludePlannedDeletion` | F | F | |
| ListSecrets | `MaxResults`, `NextToken`, `Filters`, `SortOrder`, `SortBy` | ignored | F | SM-7 |
| ListSecretVersionIds | `IncludeDeprecated` | F | F | |
| ListSecretVersionIds | `MaxResults`, `NextToken` | ignored | F | SM-8 |
| BatchGetSecretValue | `SecretIdList` | F | F | |
| BatchGetSecretValue | `Filters`, `MaxResults`, `NextToken` | ignored | F | SM-9 |
| RotateSecret | `RotationLambdaARN` | F | F | |
| RotateSecret | `RotationRules.AutomaticallyAfterDays`, `.ScheduleExpression`, `.Duration` | ignored | F or C, decide | SM-2 |
| RotateSecret | `RotateImmediately` | ignored (always rotates) | F | SM-3 |
| RotateSecret | `ClientRequestToken` | ignored (a new id is minted) | F | SM-3 |
| RotateSecret | `ExternalSecretRotationMetadata`, `ExternalSecretRotationRoleArn` | ignored | not established | |
| GetRandomPassword | `PasswordLength`, `ExcludeCharacters`, `ExcludePunctuation` | F | F | |
| GetRandomPassword | `ExcludeNumbers`, `ExcludeUppercase`, `ExcludeLowercase`, `IncludeSpace`, `RequireEachIncludedType` | ignored | F | SM-14 |
| PutResourcePolicy | `ResourcePolicy` | F under soft and enforce | F | SM-5 |
| PutResourcePolicy | `BlockPublicPolicy` | ignored | not established | |
| DescribeSecret (output) | `RotationRules`, `NextRotationDate`, `LastAccessedDate`, `OwningService`, `PrimaryRegion`, `ReplicationStatus` | absent | per field, decide | SM-17 |

Declined, and staying declined: replication (three operations and the two
`CreateSecret` members). Managed rotation templates are not an API operation
and are a listed difference.

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `secretsmanager/`: 12 tests. `sdk_test.go` 7 (lifecycle, recovery window, partial ARN, finish-step stages, single pending, stages and ids, binary/tags/password), `sdkv1_test.go` 1, `rotate_test.go` 1 (a real function through four steps), `conformance_fixes_test.go` 2 (names; string or binary, not both) |
| Parity | `rejection_parity_test.go` 1 (replays `testdata/cases_secretsmanager.json`), `coverage_model_test.go` 1. Ledger: 132 of 132 model constraints across 19 of 20 dispatched operations, `knownGaps` empty; `RotateSecret`'s 20 cases are skipped (it needs Lambda) |
| Root tests that touch it | `persistence_test.go` (one value across a restart), `persistence_policies_test.go` (a resource policy across a restart), `identity_test.go` (ARN under another account and region), `upgrade_test.go`, `fuzz_test.go` |
| Conformance | `conformance/tests/test_secretsmanager.py`: 4 scenarios (lifecycle and stages, binary, deletion window and restore, refusals), all **unverified**: `conformance/snapshots/` is empty |
| Browser | `e2e/tests/secretsmanager.spec.ts`: 9 tests. The rotation test's own comment records that the days value does not survive and pins `every \d+ days` rather than the number (SM-2, SM-20) |
| Mutation | No per-package baseline (foundation B3) |

Not covered anywhere today: paging on any list, `BatchGetSecretValue` filters,
token replay with different content, any operation on a pending secret except
get and put, a policy that is not a policy, `GetRandomPassword`'s exclusion
flags, rotation failure, and rotation or version state across a restart.

## 4. Findings

Evidence is `run` or `read` as the protocol defines them. AWS source is
`model`, `doc` (the Secrets Manager API reference page for the operation, or
the user guide page named) or `memory`; a `memory` row is on the recording
queue and its fix waits for it.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SM-1 | P0 | A write replayed with the same `ClientRequestToken` and a **different** value answers 200 and keeps the first value. Seen on `PutSecretValue` (stored "one", sent "TWO", read back "one") and `CreateSecret`; `UpdateSecret` shares the path. A caller is told its write landed. | `secretsmanager/store.go` `AddVersion` ("idempotent replay"), `Create` | run | doc: PutSecretValue and CreateSecret describe `ResourceExistsException` when the token exists with different contents | SDK test: same token, different `SecretString`, expect the error and the first value unchanged; same token, same value, expect success |
| SM-2 | P0 | `RotationRules` is validated and dropped. Nothing stores it, `DescribeSecret` never returns it, and nothing rotates on a schedule. A tool that writes rotation rules and reads them back (Terraform's rotation resource does) sees them vanish. | `secretsmanager/rotate.go` `rotateSecret` (the `Mutate` at about 52); `actions.go` `describe`; `store.go` `secret` has no field | read; the existing browser test's comment records the same thing | model (the members and `DescribeSecret.RotationRules` exist) | SDK test: rotate with `AutomaticallyAfterDays: 45`, describe, expect 45. Then the decision in section 10 on whether a schedule fires |
| SM-3 | P1 | `RotateSecret` always runs the function at once and in the caller's request. `RotateImmediately: false` is ignored, and the caller's `ClientRequestToken` is replaced by a new id. | `rotate.go` `rotateSecret` | read | doc: RotateSecret, `RotateImmediately` | SDK test with `RotateImmediately: false` and a function that records calls: expect none, and rotation enabled |
| SM-4 | P1 | A rotation whose function is missing or throws answers **500 `InternalServiceError`**. Run: a missing function gives `rotation step createSecret failed: lambda invoke: Not Found: {"Message":"Function not found: nope",...}`. An SDK retries a 500. The console half (it is shown as "a bug in the emulator") is foundation A2's "Function error" state. | `rotate.go` about 48 | run | memory: what AWS answers for an ARN it cannot invoke, and for a function that fails, is not recorded | Handler test: missing function and a function that throws each answer a 4xx with a code an SDK does not retry, and the secret's rotation state is unchanged |
| SM-5 | P1 | `PutResourcePolicy` stores any string (`"not json"` answered 200 and reads back). The guard then drops a document it cannot parse without a word, so a policy the user believes is in force is never consulted. | `actions.go` `putResourcePolicy`; `guard.go` `guardRequest` (the ignored `iampolicy.Parse` error, about 32) | run | model: `MalformedPolicyDocumentException` is in the operation's errors | Handler test: `"not json"`, `{}` and a statement with no `Effect` are refused with that code; nothing is stored |
| SM-6 | P1 | A secret pending deletion accepts changes. Run: `TagResource`, `PutResourcePolicy`, `UpdateSecretVersionStage` and `CancelRotateSecret` all answered 200 on one. Only `GetSecretValue`, `PutSecretValue`, `UpdateSecret` and a second `DeleteSecret` check. `UntagResource`, `DeleteResourcePolicy` and `RotateSecret` have the same shape. | `actions.go`: `updateSecretVersionStage`, `tagResource`, `untagResource`, `putResourcePolicy`, `deleteResourcePolicy`; `rotate.go` both handlers | run (four), read (three) | memory: AWS's "marked for deletion" `InvalidRequestException` on these is recalled, not recorded | Table test: every mutating operation against a pending secret, one expectation per operation, skipped per row until recorded |
| SM-7 | P1 | `ListSecrets` ignores `MaxResults`, `NextToken`, `Filters`, `SortOrder` and `SortBy`. Run: three secrets, `MaxResults: 1` and a name filter matching none returned all three and no token. | `actions.go` `listSecrets` | run | model (the members); doc: ListSecrets for filter semantics (prefix match, `!` negation) | SDK test: page three secrets one at a time; filter by name prefix, tag key, description; sort both ways |
| SM-8 | P1 | `ListSecretVersionIds` ignores `MaxResults` and never returns `NextToken`. | `actions.go` `listSecretVersionIds` | run | model | SDK test paging two versions one at a time |
| SM-9 | P1 | `BatchGetSecretValue` reads only `SecretIdList`. With `Filters` alone it returns nothing; it also answers `"SecretValues": null, "Errors": null` rather than lists. `MaxResults` and `NextToken` are ignored. | `actions.go` `batchGetSecretValue` | run | doc: BatchGetSecretValue (filters or a list, not both) | SDK test: filter by name returns the matching values; both given is refused; empty result is `[]` |
| SM-10 | P1 | `UpdateSecret` with `Description: ""` answers 200 and leaves the description. A description cannot be cleared once set. | `actions.go` `updateSecret` (`if d != ""`, about 227) | run | memory: whether AWS clears on an empty string | SDK test, skipped pending the recording |
| SM-11 | P1 | `UpdateSecretVersionStage` moves a label that is attached to another version without `RemoveFromVersionId`. Run: `AWSCURRENT` moved with only `MoveToVersionId`. More forgiving than AWS. | `actions.go` `updateSecretVersionStage` | run | doc: UpdateSecretVersionStage ("you must also specify RemoveFromVersionId") | Handler test: move without it is refused; with the wrong id is refused; with the right id moves |
| SM-12 | P1 | `GetSecretValue` given a `VersionId` and a `VersionStage` that name different versions answers with the id's version. | `store.go` `Resolve` | run | doc: GetSecretValue ("they must both specify the same version") | Handler test for the mismatch |
| SM-13 | P1 | `GetRandomPassword` ignores `ExcludeNumbers`, `ExcludeUppercase`, `ExcludeLowercase`, `IncludeSpace` and `RequireEachIncludedType`. Run: all-exclude flags set, the password still had digits and capitals. It also reads `ExcludeCharacters` as a boolean in one branch (`awsjson.Bool(p, "ExcludeCharacters")`), and picks characters with a modulo, which is biased. | `actions.go` `getRandomPassword` | run | model (the members); doc for the default of `RequireEachIncludedType` | Table test: each flag alone, all together, and an alphabet excluded to nothing |
| SM-14 | P1 | `DeleteSecret` with `ForceDeleteWithoutRecovery` on a secret that does not exist answers `ResourceNotFoundException`. | `store.go` `Delete` | run | doc: DeleteSecret (force on a missing secret is not an error) | SDK test: force-delete twice |
| SM-15 | P1 | Tags are not held to AWS's rules. Run: a key starting `aws:` and two entries with the same key in one call were both accepted. Read: there is no per-secret limit. | `actions.go` `tagResource`, `ptaglist` | run, read (limit) | doc: TagResource (50 tags, `aws:` reserved) | Rejection cases for each rule; the 51st tag |
| SM-16 | P1 | `DescribeSecret` answers shapes AWS does not. Run: `"KmsKeyId": ""` and `"RotationEnabled": false` on a secret that has neither; `DeletedDate` is the time `DeleteSecret` was called, while `DeleteSecret` itself returns the purge time as `DeletionDate`. `LastAccessedDate` and `NextRotationDate` never appear. | `actions.go` `describe` | run | memory for which empty members AWS omits and for what `DeletedDate` means; model for the member names | Snapshot-style SDK test of a fresh secret and a pending one, skipped per field until recorded |
| SM-17 | P1 | `CreateSecret` with no value returns a `VersionId`, and no such version exists (`GetSecretValue` then refuses, `ListSecretVersionIds` is empty). | `actions.go` `createSecret`; `store.go` `Create` | run | memory: whether AWS returns a `VersionId` here | SDK test: no `VersionId` in the answer, or a version that exists |
| SM-18 | P1 | `CreateSecret` accepts `AddReplicaRegions` and ignores it, while the three replication operations refuse by name. A template that asks for a replica is told it has one. | `actions.go` `createSecret` | read | n/a (the decline is ours) | Handler test: the member is refused the way the operations are |
| SM-19 | P1 | The declined operations answer 400 `InvalidRequestException`, and an unknown target answers 400 `InvalidAction`. The protocol wants the code an SDK expects for an unsupported operation; what that is here is not established. | `actions.go` `init`; `secretsmanager.go` `ServeHTTP` | run | memory | Decide in section 10, then a test per declined operation |
| SM-20 | P2 | With rotation on, the console shows "every 0d" in the facts and "every 0 days" in the receipt, whatever was typed. Follows from SM-2; the browser test pins the wrong shape on purpose. | `internal/console/client_sec.go` `GetSecret` (`RotationDays`); `templates/sm.html` about 70 and 77 | read | n/a | Browser test asserts the number typed, once SM-2 is fixed; until then asserts no number is shown |
| SM-21 | P3 | The rotation form says doze-aws "invokes it on the schedule (and on demand)". There is no schedule. | `templates/sm.html` about 85 | read | n/a | Browser test asserts the hint |
| SM-22 | P2 | "Enable rotation" runs the user's function through all four steps before answering, and the toast says only "Rotation configured". Nothing on the form says a function is about to run. | `client_sec.go` `ConfigureRotation`; `handlers_sec.go` `smConfigureRotation` | read | n/a | Browser test: the form states it, and the toast names what ran |
| SM-23 | P2 | After "Make current" the resource policy panel reads "Not set" and its Remove button is gone, though the policy is still there. The partial is rendered without `Policy`. | `handlers_sec.go` `smPromote` (the `partial` call, about 496) | run | n/a | Handler test: promote on a secret with a policy, the fragment says Configured |
| SM-24 | P2 | After "Save as new version", and after any rotation action, the Reveal and Copy buttons are gone and no mode is marked, until a reload. Those partials pass no `Mode`. | `handlers_sec.go` `smPut` (about 408), `smRotationRefresh` (about 354); `templates/workspace.html` `value_workspace` | run | n/a | Handler test: the fragment after a put contains the Reveal control |
| SM-25 | P2 | The "Versions" fact is wrong after a save. Run: it read 2 in the fragment and 3 after a reload. The fragment counts only labelled versions; a page load counts all. | `handlers_sec.go` `smPut`, `smRotationRefresh` (no `SecretVersionIDs` overlay) | run | n/a | Handler test: three puts, the fragment says 4 |
| SM-26 | P2 | The Versions tab lists versions by version id, which is random, and shows no time. The service returns them oldest first with `CreatedDate`; the console keeps only id to labels in a map. | `client_sec.go` `SecretVersionIDs`; `workspace.html` (the `range $id, $st := .Stages`) | read | n/a | Handler test: three versions written a second apart render newest first with their times |
| SM-27 | P2 | The diff swallows a failed read and mislabels. Run: a version id that does not exist rendered a diff headed "previous → current" as if the old value were empty. The heading is the same whichever row was pressed. | `handlers_sec.go` `smDiff` | run | n/a | Handler test: an absent version is a refusal; the heading names the version |
| SM-28 | P2 | A secret whose name contains `+` cannot use its tabs. Run: the list link is escaped, but View, Edit, Versions, Tags and Diff are built with `printf "?name=%s"`, the browser sends `a+b`, and the page answers "can't find the specified secret: a b". `+` is a legal name character. | `templates/sm.html` (the `value_workspace` dict, about 59 to 66) | run | n/a | Browser test: create `a+b`, open each tab |
| SM-29 | P2 | Saving details with the description emptied toasts "Secret details updated" and changes nothing (SM-10). The KMS key field is always empty, because the console never reads `KmsKeyId`. | `handlers_sec.go` `smUpdateMeta`; `client_sec.go` `Secret`, `GetSecret` | run (toast), read (field) | n/a | Browser test: clear the description, reload; set a key, reload, the field shows it |
| SM-30 | P2 | Delete always asks for a 7-day window and the console cannot choose another or delete at once, though the handler already takes `force=true` and AWS's default is 30. | `client_sec.go` `DeleteSecret`; `handlers_sec.go` `smDelete`; `sm.html` (the `hx-confirm`) | read | doc: DeleteSecret (7 to 30, default 30) | Browser test for the window field and for the immediate delete |
| SM-31 | P2 | A secret stored with `SecretBinary`, and a secret with no version, both render as an empty value box with nothing said. | `client_sec.go` `GetSecret` (reads `SecretString` only) | read | n/a | Browser test for each: the page says which it is |
| SM-32 | P2 | The create form cannot do what the API can: no binary value, no KMS key, no tags. | `templates/create.html` `sm_create`; `client_sec.go` `CreateSecret` | read | n/a | Decide which to add (section 10); a browser test for each added |
| SM-33 | P2 | A pending secret's page still offers Edit, "Make current" and the tag editor. Edit is refused; the other two succeed (SM-6). Not reproduced in the browser. | `templates/sm.html`, `workspace.html` (no `Deleted` guard in the workspace) | read | n/a | Browser test: a pending secret offers Restore and reading, nothing else |
| SM-34 | P3 | Masking is cosmetic in three places: Copy copies the real value while hidden, the page source carries it, and the diff shows both versions in the clear. Decide whether the diff should honour Reveal. | `workspace.html` (`data-copy`, both `masked` spans, `value_diff`) | read | n/a | Browser test for whichever is decided |
| SM-35 | P3 | The header always shows an `AWSCURRENT` chip, derived from nothing. | `templates/sm.html` about 50 | read | n/a | Removed, or replaced by the current version id; a handler test |
| SM-36 | P3 | The Disable rotation confirm asks "Turn off automatic rotation?". Nothing here is automatic. Foundation A13's family. | `templates/sm.html` (the `hx-confirm`) | read | n/a | Read every `hx-confirm` in `sm.html` against the code; four exist |
| SM-37 | P3 | The ledger says the resource policy is "stored and returned only under the default `off`". The default is `soft`. The foundation's criteria already cover the IAM default; this is the Secrets Manager row. | `docs/SUPPORT.md`, Secrets Manager table | read | n/a | The ledger test, after the row is corrected |
| SM-38 | P3 | The ledger's "Rotation runs your Lambda, and that is all it runs" and the code comment "matching AWS" on `cancelRotateSecret` state AWS behaviour with no source. | `docs/SUPPORT.md`; `secretsmanager/rotate.go` | read | doc: CancelRotateSecret | Each gets its page named, or goes on the recording queue |

Owned by the foundation, listed so they are not lost:

- **A2.** The "AWS does" line appears under every coded refusal here, including
  the hand-written ones ("scheduled for deletion; restore it or wait for the
  purge"), and a rotation function's failure is shown as "a bug in the emulator".
- **A4.** The key/value editor stringifies numbers. It is the default editor on
  this service's Edit tab.
- **A5.** Saving tags is one `TagResource` per changed key (`handlers_tags.go`).
- **A6.** Delete and Restore live in the header, where a refusal has no slot.
- **A13.** The four confirms on this page (SM-36).

**Corrections to the board-grounding report.** The missing-function message was
given there as unknown; it is `lambda invoke: Not Found: {...}` (run). The
report said policies are consulted "only when IAM is not off" as if `off` were
the default; the default is `soft`, so a secret's policy is evaluated out of the
box. The claim that a pending secret's Versions tab offers "Make current" was
not reproduced and stays `read` (SM-33). Everything else in that report was
reproduced or stands as read; nothing was found false.

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once.

**Secrets**
- [ ] Name: 1 to 512, the hand-derived character set (letters, digits,
      `/_+=.@!-`); the rule has its `doc` page named beside `secretName`.
- [ ] A name ending in a hyphen and six characters resolves to itself, not to
      a shorter name, by name and by full and partial ARN (`candidateNames`).
- [ ] Create twice: same token and value succeeds; same token, other value
      refuses (SM-1); no token refuses with `ResourceExistsException`.
- [ ] Create with the name of a pending secret refuses, and the message is
      compared with the recording.
- [ ] Create with no value, then the first `PutSecretValue` (SM-17).
- [ ] `KmsKeyId`: absent key and disabled key refused when KMS is wired;
      accepted when it is not; an alias and an ARN both resolve.
- [ ] `UpdateSecret`: description alone writes no version; a value writes one;
      clearing a description (SM-10).

**Values and versions**
- [ ] String and binary: one or the other, 1 to 65 536, and a binary round
      trip byte for byte.
- [ ] `PutSecretValue` with no stages moves `AWSCURRENT` and `AWSPREVIOUS`;
      with `VersionStages: ["AWSPENDING"]` leaves `AWSCURRENT` alone; with a
      custom label; with a label already held elsewhere.
- [ ] A label names at most one version, after every path that can move one.
- [ ] `GetSecretValue`: by stage, by id, by both agreeing and disagreeing
      (SM-12), an absent stage, an absent id.
- [ ] `UpdateSecretVersionStage`: move with and without `RemoveFromVersionId`
      (SM-11), remove a custom label, refuse removing `AWSCURRENT`, a missing
      target version.
- [ ] Deprecated versions: AWS removes them past a count; establish the rule
      and decide whether to model it or list it as a difference.
- [ ] `ListSecretVersionIds`: with and without `IncludeDeprecated`, paging
      (SM-8), `CreatedDate` order.
- [ ] `BatchGetSecretValue`: a list, filters, both, a missing id among found
      ones, a pending one, paging (SM-9).

**Listing**
- [ ] `ListSecrets` paging, every filter key (`name`, `description`, `tag-key`,
      `tag-value`, `all`, and the two that cannot match locally,
      `primary-region` and `owning-service`), negation with `!`, both sort
      orders and each `SortBy` (SM-7).
- [ ] `IncludePlannedDeletion` on and off.

**Deletion**
- [ ] Window 7 to 30, default 30, window and force together refused.
- [ ] Force on a present, a pending and an absent secret (SM-14).
- [ ] Every operation against a pending secret, one row each (SM-6).
- [ ] `RestoreSecret` on a pending, a live and an absent secret.
- [ ] The janitor purges at the window's end and not before, under a test
      clock; a purged name can be created again.
- [ ] `DeletedDate` and `DeletionDate` mean what AWS means (SM-16).

**Rotation**
- [ ] The four steps, in order, with the same token, as the service principal
      with `aws:SourceArn` set to the secret.
- [ ] `RotationRules` stored and returned (SM-2); the range 1 to 1000; a
      `ScheduleExpression`; both together.
- [ ] `RotateImmediately` true, false and absent (SM-3).
- [ ] A function that is missing, that throws on each of the four steps, and
      that times out: the code answered (SM-4), and the labels left behind.
- [ ] A second rotation while a version is `AWSPENDING`.
- [ ] `CancelRotateSecret` on a rotating, a never-rotated and a pending secret.
- [ ] The 20 skipped model cases for `RotateSecret` run, with a stub function.

**Random password**
- [ ] Length 1 to 4096 and the default; every exclusion flag; `IncludeSpace`;
      `RequireEachIncludedType` on and off; an alphabet excluded to nothing (SM-13).

**Access**
- [ ] A policy that cannot be parsed is refused (SM-5); an empty document;
      the 20 480 limit; `BlockPublicPolicy`, once its tier is decided.
- [ ] Under enforce: a `Deny` refuses the root; an `Allow` naming a role lets
      it in with no identity policy; naming the account delegates.
- [ ] `ValidateResourcePolicy` agrees with what `PutResourcePolicy` accepts.

**Tags**
- [ ] Add, overwrite, remove, remove an absent key; the limit, `aws:` and
      duplicate keys (SM-15); tags given at create.

**Lifecycle**
- [ ] Across a restart: versions and labels, a pending deletion and its purge
      time, the policy, tags, rotation state including the rules.
- [ ] The sealing key: a store opened with a different key file refuses values
      rather than returning garbage (`renderValue`'s "corrupt" path).

## 6. Console

The design's Secrets Manager page: 20 boards. Templates: `templates/sm.html`
(`sm_lp`, `sm_home`, `sm_secret`, `sm_secret_detail`), the `sm_create` block in
`create.html`, `value_workspace` and `value_diff` in `workspace.html`,
`tag_editor` and `fail_inline` in `panes.html`, `policy_builder` in `iam.html`.
Handlers: the `sm*` functions in `handlers_sec.go` and `handlers_sm_policy.go`,
`tagsView` and `tagsSave` in `handlers_tags.go`; routes in `routes_config.go`
`smRoutes`.

The design draws Value, Versions, Rotation, Resource policy and Tags as tabs.
Today View, Edit, Versions and Tags are one segmented control and Rotation and
Resource policy are sections under it on the same page. The tabs are a layout
change over the same calls.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Sm-Home | Secrets, none open | `sm_home`, `sm_lp`, `empty_detail` | exists; the last-changed column is **new** |
| Sm-01-None | No secrets yet | `sm_home`, `lp_empty`, `empty_detail` | exists |
| Sm-02-Create | Store a new secret | `sm_create`; `smCreate` | form, Generate password and CLI preview exist; teaching lines and the labels card are **new** |
| Sm-03-Create-Refused | Refused inline | `fail_inline` | exists |
| Sm-04-Value | Value, hidden and revealed | `sm_secret_detail`, `value_workspace` view; `smSecret` | exists; the current version line is **new** |
| Sm-05-Details | Description and KMS key | the inline form in `sm_secret_detail`; `smUpdateMeta` | form exists; teaching lines are **new**; see SM-29 |
| Sm-06-Edit | Edit, save as a new version | `value_workspace` edit; `smPut` | exists; "After you save" is **new**; see A4 |
| Sm-07-Edit-Refused | Edit refused | `fail_inline` | exists |
| Sm-08-Versions | Versions and labels | `value_workspace` versions; `smSecret` | table exists; Created and the order are **new** (SM-26) |
| Sm-09-Diff | Diff against current | `value_diff`; `smDiff` | exists; the heading naming the version is **new** (SM-27) |
| Sm-10-Rollback | Make current, and after | `hx-confirm` in `workspace.html`; `smPromote` | exists; the confirm naming three versions is **new** |
| Sm-11-Rotation-Off | Enable rotation | the rotation form in `sm_secret_detail`; `smConfigureRotation` | form exists; teaching lines and the four steps are **new** |
| Sm-12-Rotation-On | Rotation on | the receipt in `sm_secret_detail`; `smRotateNow` | exists; the function link is **new** |
| Sm-13-Rotation-Failed | A rotation that failed | `fail_inline` | exists, with the wrong sentence (A2, SM-4); "what it left behind" is **new** |
| Sm-14-Policy | Resource policy | the policy panel, `policy_builder`; `smSavePolicy` | exists; the three explanatory cards are **new** |
| Sm-15-Tags | Tags as a draft | `tag_editor`; `tagsSave` | exists (A5) |
| Sm-16-Confirms | Delete, disable rotation, remove policy | `hx-confirm` in `sm.html` | three exist; the window field and immediate delete are **new** (SM-30) |
| Sm-17-Pending | In the recovery window | the `Deleted` branches of `sm_secret_detail`; `smRestore` | exists; the locked-value message is **new** |
| Sm-18-Denied | A save denied under enforce | `fail_inline` | exists |
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
| Teaching lines | Foundation B4: ranges from `secretsmanager/validate.go` `constraintTables`, tiers from section 2 once it is in the ledger |

**Where each teaching line's facts come from**

| Line | Source |
|---|---|
| Name, 1 to 512 | `validate.go`, `CreateSecret` `Name` |
| Name's character set | `actions.go` `secretName`. Hand-derived, so `doc` until recorded |
| Description, 0 to 2048 | `validate.go`, `Description` |
| Value, 1 to 65 536 | `validate.go`, `SecretString` and `SecretBinary` |
| "Sealed with a key in the data directory, not KMS" | `SUPPORT.md`, Differences; `store.go` `newStore` |
| KMS key: recorded, checked, not used | `actions.go` `requireUsableKey`; the C tier in section 2 |
| Label meanings | `store.go` `Resolve` (default stage), `AddVersion`; `SUPPORT.md` rows for `PutSecretValue` and `UpdateSecretVersionStage` |
| "A label names one version", "only one AWSPENDING" | `SUPPORT.md`; `sdk_test.go` `TestPutSecretValueSinglePending` |
| Deprecated is still readable by id | `store.go` `Resolve`; a listed difference if AWS prunes |
| Days, 1 to 1000 | `validate.go`, `RotationRules.AutomaticallyAfterDays` |
| "Checked, then dropped; no schedule" | SM-2; `SUPPORT.md`, Differences. The sentence changes when SM-2 is decided |
| The four steps and what each does | `rotate.go` for the names, order and payload; the user guide's rotation page for what each step is for (`doc`) |
| The function needs a permission under enforce | `SUPPORT.md`, IAM ledger, "Service principals" |
| Policy: off, soft, enforce | `SUPPORT.md`, the resource-policy row and the IAM ledger |
| The same-account rule | `SUPPORT.md`, IAM ledger, "Resource policies" |
| Policy, 1 to 20 480 | `validate.go`, `ResourcePolicy` |
| Tag key 1 to 128, value 0 to 256 | `validate.go`, `TagResource` |
| Recovery window 7 to 30 | `store.go` `Delete`; default 30 is `doc` |
| "A sweep wakes once a minute" | `secretsmanager.go` `janitor` |

**Statements on the boards not yet backed.** Remove or prove each before the
board ships.

- Sm-13: the shape of a function's error payload inside the message
  (`{"errorType":...,"errorMessage":...}`). The prefix is from the code; the
  body is whatever the runtime returns.
- Sm-13: "running rotation again takes the label off this one" holds only if
  the function writes `AWSPENDING` on `createSecret`. True of the test
  function; not a property of the service.
- Sm-11, Sm-12: what each of the four steps does. `doc`.
- Sm-10: "it needs both version ids". `doc`, and not enforced here until SM-11.
- Sm-14: "a document that is not valid policy JSON is stored, and then never
  consulted". True today; it must be rewritten when SM-5 is fixed.
- Sm-11, Sm-12: "checked, then dropped". True today; rewritten with SM-2.
- Sm-16, Sm-17: "AWS default 30". `doc`.
- Sm-17: "the date it will be purged is not returned". True of the code; what
  AWS returns as `DeletedDate` is `memory` (SM-16).
- Sm-15: "policy conditions here do not read tags". From `policy.js`'s list of
  context keys, a console file. Needs the IAM ledger to say it.
- Sm-06: "rows save every value as a string". True today; removed with A4.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | Token replay with other content (SM-1); stage move rules (SM-11); id and stage mismatch (SM-12); policy that will not parse (SM-5); every operation on a pending secret (SM-6); replica members (SM-18); random-password flags (SM-13); tag rules (SM-15); janitor under a test clock |
| SDK v2 | Paging and filters on `ListSecrets`, `ListSecretVersionIds`, `BatchGetSecretValue` (SM-7 to SM-9); force-delete an absent secret (SM-14); describe shapes (SM-16, SM-17); rotation rules round trip, `RotateImmediately`, a failing and a missing function (SM-2 to SM-4) |
| Rejection parity | The 20 `RotateSecret` cases, with a stub Lambda peer; the hand-derived name rule |
| Restart | Versions and labels, a pending deletion, tags, rotation state (foundation B5) |
| Second identity | The package's SDK tests under a non-default account and region (foundation B2) |
| Console handlers | The fragment after put, promote and each rotation action has the toolbar, the right version count and the policy badge (SM-23 to SM-25); diff on an absent version (SM-27); versions in time order (SM-26) |
| Conformance (boto3) | Every row in section 8 |
| Browser | A name with `+` through every tab (SM-28); clear a description, set a KMS key (SM-29); binary and version-less secrets (SM-31); a pending secret offers only Restore and reading (SM-33); the window field and immediate delete (SM-30); each `new` item; each confirm's sentence; the rotation test asserts the typed number |
| Mutation | `secretsmanager/` baseline, then survivors in `store.go` (label moves), `actions.go` (`updateSecretVersionStage`, `getRandomPassword`) |

## 8. Recording queue

Every one of these is `doc` or `memory` today. Scenario names are in
`conformance/tests/test_secretsmanager.py`.

| Claim | Source now | Scenario |
|---|---|---|
| Same token, different content, on create, put and update | doc | `token_replay_conflict` |
| The name character set, and the text of the refusal | doc | `name_rules` |
| Create with the name of a pending secret: code and message | memory | `create_over_pending` |
| Create with no value: is a `VersionId` returned | memory | `create_without_value` |
| `UpdateSecret` with an empty description | memory | `clear_description` |
| Each mutating operation against a pending secret | memory | `pending_refusals` |
| `DeletedDate` in describe, `DeletionDate` in delete | memory | `deletion_dates` |
| Force-delete of an absent secret | doc | `force_delete_absent` |
| Which empty members `DescribeSecret` omits | memory | `describe_shape` |
| Moving a held label without `RemoveFromVersionId` | doc | `stage_move_requires_remove` |
| `VersionId` and `VersionStage` that disagree | doc | `id_stage_mismatch` |
| When deprecated versions are removed | doc | `deprecated_version_pruning` |
| `ListSecrets` filters, negation, sort, page size | doc | `list_filters_and_paging` |
| `BatchGetSecretValue` with filters, and with both | doc | `batch_get_filters` |
| A policy that is not a policy: code and message | model (code), memory (message) | `malformed_policy` |
| `BlockPublicPolicy` | doc | `block_public_policy` |
| `ValidateResourcePolicy` on a malformed document | memory | `validate_policy` |
| `RotateSecret` with a function that does not exist | memory | `rotate_missing_function` |
| `RotateSecret` with a function that throws: the answer, and the labels after | memory | `rotate_failing_function` |
| `RotateImmediately: false` | doc | `rotate_not_immediately` |
| `RotationRules` returned by describe, and `NextRotationDate` | doc | `rotation_rules_round_trip` |
| `CancelRotateSecret` on a secret that never rotated | memory | `cancel_never_rotated` |
| `GetRandomPassword` flags and `RequireEachIncludedType`'s default | doc | `random_password_flags` |
| Tag limit, `aws:` keys, duplicate keys in one call | doc | `tag_rules` |
| The code for a replication call where replication is unavailable | memory | `replication_refusal` |
| What `Type` and `RotationToken` do | memory | `type_and_rotation_token` |

The four scenarios that exist are also unverified.

## 9. Owed to integration

| What | With |
|---|---|
| Rotation end to end with a real function, all four steps, and under enforce with and without the function's permission for `secretsmanager.amazonaws.com` | Lambda, IAM |
| A rotation function that fails is the function's failure on the wire and in the console, with a link to its log lines | Lambda, Logs (already row 133 of `90-integration.md`) |
| A rotation function that calls back into Secrets Manager while `RotateSecret` holds the request: no deadlock on the store | Lambda |
| A scheduled rotation firing, if SM-2 is built | Lambda, the clock |
| `KmsKeyId` naming a key that is then disabled or scheduled for deletion: what a later read and write do | KMS |
| A function reading a secret under enforce through its role and through the secret's policy | Lambda, IAM |
| `AWS::SecretsManager::Secret` with `GenerateSecretString`, `SecretString`, tags and a replica; `RotationSchedule` and `ResourcePolicy` resources; `{{resolve:secretsmanager:...}}` references | CloudFormation |
| `provision` applying a secret twice: one version, not two | provision |
| The wire and the access log showing a rotation's four invokes nested under `RotateSecret` | console, trace |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SM-1 to SM-38 each closed, with the test that failed first named beside
      it, or moved to section 8 or 9 with a reason.
- [ ] Decided and written into `SUPPORT.md`:
  - [ ] the rotation schedule (SM-2): rules are stored and returned in either
        case; then either a tick in the janitor fires rotations and the tier is
        F, or nothing fires, the tier is C, and the ledger, the form and the
        Rotation tab all say so;
  - [ ] whether `RotateSecret` stays synchronous, and the code it answers when
        the function fails (SM-3, SM-4);
  - [ ] `ValidateResourcePolicy`: stays C, and whether it refuses what
        `PutResourcePolicy` refuses;
  - [ ] the code the three declined operations answer with (SM-19);
  - [ ] deprecated-version pruning: modelled, or a listed difference;
  - [ ] which of binary, KMS key and tags the create form gains (SM-32);
  - [ ] whether the diff honours Reveal (SM-34).
- [ ] The member table in section 2 has no "ignored" and no "not established"
      left: `Type`, `RotationToken`, `BlockPublicPolicy`, the two
      `ExternalSecretRotation*` members and each absent `DescribeSecret` field
      is F, C and says so, or refused.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] No request member in `constraintTables` is validated and then unread. A
      test walks the table against the handlers, or the list of exceptions is
      written here: ____.
- [ ] `KmsKeyId` reads as cosmetic on the create form (if added) and on Details.
- [ ] The detail page is rendered from one view model: a test renders the
      fragment after every `sm*` mutation and compares it with the same region
      of a full page load.
- [ ] No URL in `sm.html` or its `value_workspace` call is built by `printf`
      without escaping; the browser suite runs once with names containing `+`,
      `=`, `@` and `/`.
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
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: member tiers, the decisions in section 10 | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SM-1 to SM-19 | `secretsmanager/sdk_test.go`, `conformance_fixes_test.go`, `rotate_test.go`, a new handler-level test file in `secretsmanager/` |
| 3 | Idempotency and stage rules (SM-1, SM-11, SM-12, SM-17) | `secretsmanager/store.go`, `actions.go` |
| 4 | Pending-deletion guard in one place; force delete; dates (SM-6, SM-14, SM-16) | `secretsmanager/store.go` (`Mutate`), `actions.go` (`describe`) |
| 5 | Paging, filters, sort, batch (SM-7 to SM-9) | `secretsmanager/actions.go`, a new `list.go` beside it |
| 6 | Policy validation, tags, random password, replica members (SM-5, SM-13, SM-15, SM-18, SM-19) | `secretsmanager/actions.go`, `guard.go` |
| 7 | Rotation: rules stored and returned, `RotateImmediately`, failure codes, and the schedule if decided (SM-2 to SM-4) | `secretsmanager/rotate.go`, `store.go`, `secretsmanager.go` (`janitor`), `rejection_parity_test.go` |
| 8 | Section 5 sweep, restart tests | `secretsmanager/*_test.go`, `persistence_test.go` |
| 9 | Conformance scenarios and claims table | `conformance/tests/test_secretsmanager.py`, the claims file from foundation B1 |
| 10 | Console data: one view model, version list with dates, KMS key, binary, window and force (SM-23 to SM-31) | `internal/console/client_sec.go`, `handlers_sec.go`, `handlers_sm_policy.go` |
| 11 | Console pages (SM-20 to SM-22, SM-28, SM-32 to SM-36) | `internal/console/templates/sm.html`, `create.html` (`sm_create`), `workspace.html`, `routes_config.go` |
| 12 | Browser tests | `e2e/tests/secretsmanager.spec.ts` |
| 13 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Steps 10 to 12 lean on foundation A2, A4, A5, A6 and B4. `workspace.html` is
shared with Parameter Store (stage 2), so the Mode and toolbar fix (SM-24) may
already be in place; check before repeating it.

**Prior art.** AWS's user guide page on rotation Lambda functions is the source
for the four steps, the payload, and which step moves which label; its sample
rotation function is the reference for what `rotate_test.go`'s function should
do. The API reference page for each operation is the source for every `doc` row
in sections 4 and 8. No other emulator's behaviour is cited here because none
was checked.
