# Parameter Store (SSM)

Stage 2. Size S. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds Parameter Store's own.

## 1. Summary

The Parameter Store slice of Systems Manager: String, StringList and
SecureString parameters with versions, labels, history, tags and an Expiration
policy, over one bbolt store, on one wire protocol (AWS JSON 1.1, target prefix
`AmazonSSM`). SecureString values are sealed with an AES-256-GCM key the service
keeps in its data directory; KMS is not called. It is a leaf: no service calls
it and it calls none. `internal/provision/svc_ssm.go` creates parameters for
CloudFormation and `apply`.

It has no resource policy here (`PutResourcePolicy` is in the AWS model and is
not served), so no console action on it can be Denied.

**Estimate:** S, one to two focused days. See section 11: the API half is larger
than the console grounding suggested, and this is nearer the top of S than the
bottom. **Actual:** _fill in when done._

## 2. Contract

The model (`ssm/testdata/ops_ssm.json`) lists **152** operations. They fall in
three groups today.

| Group | Count | Now | Target | Note |
|---|---|---|---|---|
| Parameter Store, dispatched (`handlers` in `ssm/actions.go`) | 13 | F | F | Table below. |
| Fleet management refused by name (`fleetOps` in `ssm/ssm.go`) | 90 | S | S | `UnsupportedOperationException`, naming what it would need. |
| Everything else | 49 | **silent** | S | Answers `InvalidAction` "unknown SSM action", which is false: the action is known and not served. Listed in `ssm/coverage_model_test.go` `Unreached`. See SSM-5. |

`docs/SUPPORT.md` and `docs/not-built.md` both say every declined operation
answers `UnsupportedOperationException`. That is true of 90 and false of 49.

The 49 include `PutResourcePolicy`, `GetResourcePolicies` and
`DeleteResourcePolicy`, which are Parameter Store operations (sharing a
parameter across accounts), not fleet management. Target S, declined, with that
reason written down.

The 13 dispatched operations:

| Operations | Now | Target | Note |
|---|---|---|---|
| PutParameter | F | F | Per-setting tier below. See SSM-1, 2, 8, 9, 14, 15, 16. |
| GetParameter | F | F | `name`, `name:version`, `name:label`, ARN. See SSM-7. |
| GetParameters | F | F | Found and `InvalidParameters`. See SSM-13. |
| GetParametersByPath | F | F | `Recursive` works. `MaxResults`, `NextToken` and `ParameterFilters` are ignored. See SSM-4. |
| GetParameterHistory | F | F | All versions with labels. No paging; members missing. See SSM-4, SSM-10. |
| DescribeParameters | F | F | `ParameterFilters` on Name (Equals, BeginsWith) and Type only; every other key, and the older `Filters`, accepted and ignored. No paging. See SSM-4, SSM-10. |
| DeleteParameter, DeleteParameters | F | F | See SSM-13. |
| LabelParameterVersion, UnlabelParameterVersion | F | F | A label moves on re-label. No label rules. See SSM-3, 6, 11. |
| AddTagsToResource, RemoveTagsFromResource, ListTagsForResource | F | F | `ResourceType` Parameter only; any other answers `InvalidResourceType`. Per-resource limit not established. |

`PutParameter` needs its own tiers, because "PutParameter is F" hides which
settings do anything:

| Setting | Now | Target | To confirm |
|---|---|---|---|
| Name | F | F | 1 to 2048 (model). Segment characters, reserved `aws`/`ssm`, 15 levels are hand-derived (`validParameterName`). A first segment that merely begins `aws` (`/awsome/x`) is accepted; what AWS does is not established. |
| Type | F | F | Required on create, immutable after. The code AWS gives a type change is not established. |
| Value | F | F | No size limit and no minimum here. See SSM-2, SSM-14. |
| Overwrite | F | F | |
| KeyId | C | C | Pattern and length enforced; recorded and returned, never used. Defaults to `alias/aws/ssm`. |
| Description | F | F | 0 to 1024. Returned by DescribeParameters and history. |
| Tier | C | C | Enum enforced; stored; nothing differs by tier. |
| Policies: Expiration | F | F | Reads fail once it passes; the janitor deletes within a minute. |
| Policies: ExpirationNotification, NoChangeNotification | silent | C | Stored unread. Nothing says so. See SSM-9. |
| Tags | F | F | Accepted with `Overwrite`, which AWS refuses. See SSM-8. |
| DataType | silent | C | Any string up to 128 stored. `aws:ec2:image` is not checked. See SSM-16. |
| AllowedPattern | **silent** | F | Length checked, then dropped: not stored, not enforced, not returned. See SSM-1. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `ssm/`: 13 tests in 5 files. SDK v2 (6: versions and labels, SecureString, overwrite keeps type, by-path and describe, delete and tags, fleet refusals), SDK v1 (1), conformance fixes (3: name rules, type on create, path needs a slash) |
| Parity | `rejection_parity_test.go` (2), `coverage_model_test.go` (1). Ledger: 100 of 100 model constraints enforced across the 13 operations, `knownGaps` empty |
| Conformance | `conformance/tests/test_ssm.py`: 4 scenarios, all **unverified** (`conformance/snapshots` is empty). `test_paths_and_bulk_reads` asks for `MaxResults=2` and records whether a `NextToken` came back; today none does (SSM-4) |
| Browser | `e2e/tests/ssm.spec.ts`: 10 tests |
| Restart | One parameter in the root `persistence_test.go`. Labels, tags, SecureString, Expiration: not covered |
| Mutation | No per-package baseline (foundation B3) |

The model gives SSM lengths, patterns and enums, so shape validation is `model`.
Everything that makes Parameter Store itself (name rules, label rules, size by
tier, limits, policies and tiers) is in the user guide, not the model, so it is
`doc` at best.

## 4. Findings

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SSM-1 | P0 | `AllowedPattern` is accepted and thrown away. A value `abc` with pattern `^[0-9]+$` is stored; the pattern is never returned. | `ssm/actions.go` `putParameter` (never reads it); `ssm/store.go` `Put` | run | doc: PutParameter reference, AllowedPattern | Handler test: mismatching value refused; pattern round-trips through DescribeParameters and history |
| SSM-2 | P0 | No value size limit. A 9000-byte String on the Standard tier is stored. | `ssm/store.go` `Put` | run | doc: 4 KB Standard, 8 KB Advanced | Handler test at 4096, 4097, 8192, 8193 bytes per tier |
| SSM-3 | P0 | No label rules. `aws-prod`, `ssm`, `has space`, `1-starts-with-digit` are attached and `InvalidLabels` is always empty. A reader of `name:label` works here and fails deployed. | `ssm/actions.go` `labelParameterVersion`; `ssm/store.go` `Label` | run | doc: "Working with parameter labels" | Handler test per rule: the label is not attached and comes back in `InvalidLabels` |
| SSM-4 | P0 | No paging on any list. `GetParametersByPath`, `DescribeParameters` and `GetParameterHistory` ignore `MaxResults`, never return `NextToken`, and return everything. A caller that does not page gets every parameter here and the first page on AWS. | `ssm/actions.go` `getParametersByPath`, `describeParameters`, `getParameterHistory` | run | model: `MaxResults` ranges (10, 50, 50) and `NextToken` members; doc: default page size | SDK test: 12 parameters under a path, one call returns at most 10 and a token; a paginator returns 12 |
| SSM-5 | P1 | 49 model operations answer `InvalidAction` "unknown SSM action". The ledger and `not-built.md` say each declined operation answers `UnsupportedOperationException`. | `ssm/ssm.go` `ServeHTTP`, `fleetOps`; `ssm/coverage_model_test.go` `Unreached` | run | model | Coverage test with `Unreached` empty; SDK test on `PutResourcePolicy` asserting the code |
| SSM-6 | P1 | `LabelParameterVersion` without `ParameterVersion` labels the latest version and answers `ParameterVersion: 0`. | `ssm/actions.go` `labelParameterVersion` | run | doc: the response carries the version labelled | SDK test asserting the returned version |
| SSM-7 | P1 | `GetParameter` by ARN returns `Selector: ":parameter/p/a"`: the selector is cut at the ARN's last colon. | `ssm/actions.go` `getParameter` | run | memory: no selector for a bare ARN | Handler test for name, `name:1`, `name:label`, ARN, ARN with `:1` |
| SSM-8 | P1 | `PutParameter` with `Overwrite` and `Tags` together succeeds and merges the tags. | `ssm/store.go` `Put` | run | doc: PutParameter reference, Tags ("can't be used with Overwrite") | Handler test asserting the refusal and that nothing was written |
| SSM-9 | P1 | Policies are accepted on a Standard parameter, and the two notification policy types are stored unread with nothing saying so. Only `Type` and `Timestamp` are parsed; `Version` and unknown types are not checked. | `ssm/actions.go` `parseExpiration` | run | doc: "Assigning parameter policies" (Advanced tier only) | Handler test: policy on Standard; unknown policy type; missing Version |
| SSM-10 | P1 | Reads omit members the model returns. No read returns `Policies`, `AllowedPattern` or `LastModifiedUser`; history also omits `Tier` and `KeyId`. An Expiration set here cannot be read back. | `ssm/actions.go` `describeParameters` (`meta`), `getParameterHistory` (`histEntry`) | run | model: output shapes in `ssm/testdata/shapes_ssm.json` | SDK test: put with every setting, assert each comes back from both reads |
| SSM-11 | P1 | Eleven labels attach to one version, and `UnlabelParameterVersion` on a version that does not exist answers success with nothing removed. | `ssm/store.go` `Label`, `Unlabel` | run | doc: 10 labels per version (`ParameterVersionLabelLimitExceeded`); memory: missing version is `ParameterVersionNotFound` | Handler tests for the eleventh label and for version 9 of a one-version parameter |
| SSM-12 | P1 | A name deeper than 15 levels answers `ValidationException` with the name-rule text. There is no limit on versions per parameter. | `ssm/store.go` `validParameterName`, `Put` | run (depth), read (versions) | doc: `HierarchyLevelLimitExceededException`; 100 versions, `ParameterMaxVersionLimitExceeded` | Handler test on a 16-level name asserting the code; a 101st version |
| SSM-13 | P1 | `GetParameters` and `DeleteParameters` take 11 names. | `ssm/actions.go` `getParameters`, `deleteParameters`; no list-length row in `ssm/validate.go` | run | doc: 1 to 10 names. Whether the model states it and `dzaudit` drops list lengths is not established. | Rejection-parity case with 11 names on both |
| SSM-14 | P1 | An empty `Value` is stored. | `ssm/actions.go` `putParameter` (checks presence only) | run | memory: AWS requires a non-empty value | Skipped test pending the recording |
| SSM-15 | P1 | A StringList is not looked at: `no commas at all` and `a,,b` are stored. | `ssm/store.go` `Put` | run | memory | Skipped test pending the recording |
| SSM-16 | P1 | `DataType` takes any string. `aws:ec2:image` with a value that is not an AMI id is stored. | `ssm/store.go` `Put` | run | doc: PutParameter reference, DataType | Decide the tier (section 10), then a test either way |
| SSM-17 | P2 | Saving a SecureString from Edit swaps in a detail pane that shows the value in plaintext, with no Reveal, no Copy, no warn badge and no active tab. The partial is rendered without `Secure` or `Mode`. Label and unlabel also drop `Secure`. | `internal/console/handlers_sec.go` `ssmPut`, `ssmLabel`, `ssmUnlabel` | run | n/a | Browser test: edit a SecureString, assert the value is masked and View is the active tab |
| SSM-18 | P2 | "Diff vs current" on a SecureString prints both values in cleartext; the View tab's Reveal gate does not apply. | `handlers_sec.go` `ssmDiff`; `client_sec.go` `ParameterHistory` (`WithDecryption: true`) | run | n/a | Browser test: diff a SecureString, assert masked until revealed |
| SSM-19 | P2 | With no parameters the list pane is blank. The empty-state block hangs off the inner `range .Rows`, which is never empty. | `internal/console/templates/ssm.html` `ssm_lp` | run | n/a | Render test: `ssm_lp` with an empty list contains the `lp_empty` copy |
| SSM-20 | P2 | The console cannot do what the API can: no Description, Tier, KeyId, Expiration or tags at create; Versions shows no value or time though both are fetched; `InvalidLabels` and `RemovedLabels` are discarded, so once SSM-3 is fixed a dropped label would still toast "applied". | `templates/create.html` `ssm_create`; `templates/workspace.html`; `client_sec.go` `PutParameter`, `LabelParameter`, `UnlabelParameter` | read | n/a | Handler tests for each new field; a browser test attaching `aws-x` after SSM-3 |
| SSM-21 | P2 | The list is one `DescribeParameters` call with `MaxResults: 50` and no token. Correct only because of SSM-4; fixing SSM-4 caps the list at 50. | `client_sec.go` `ListParameters` | read | n/a | Console test with 60 parameters |
| SSM-22 | P3 | `ParameterAlreadyExists` reads "pass Overwrite to update it". The create form has no such control; the way to change a parameter is Edit. | `ssm/store.go` `Put`; `ssm_create` | run | memory: AWS's wording differs | Browser test: the refusal offers the existing parameter |
| SSM-23 | P3 | A diff for a version that does not exist renders the whole current value as added. The diff matches lines as a set, so a moved line shows unchanged. | `handlers_sec.go` `ssmDiff`; `valueworkspace.go` `lineDiff` | run | n/a | Handler test for `v=9`; a unit test on a reordered value |
| SSM-24 | P3 | "Differences from AWS" in the ledger omits: paging, value size, label rules, AllowedPattern, DataType, the limits in SSM-11 to 13, public parameters under `/aws/service` (answer `ParameterNotFound`), and the resource-policy operations. | `docs/SUPPORT.md`, SSM section | read | n/a | The ledger test, after the contract is rewritten |

Owned by the foundation, seen here: **A2** (the name-rule refusal is told "the
rule comes from AWS's own service model"; `ssm/store.go` says none of it is in
the model), **A4** (the key/value editor is the default for any flat JSON
parameter), **A5** (tag Save wrote 1, 2 or 0 of 3 rows across 12 runs),
**A12** ("Label removed" for a label that was not there).

**A7 needs correcting for this service.** It lists Parameter Store among the
services that open the first resource. It does not: with parameters present,
`ssm_home` renders the "Query a path" pane. The `Unsel` line in `copy.go` is
unreachable all the same, for that different reason.

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once.

**Names**
- [ ] 1 to 2048; each level letters, digits, `.`, `-`, `_`; an empty level
      (`//`, a trailing slash) refused.
- [ ] `aws` and `ssm`, any case: refused as the start of a plain name and as the
      first level of a path. Establish `/awsome/x` and `/x/aws-inside`
      (the conformance scenario already asks; record it).
- [ ] 15 levels accepted, 16 refused with AWS's code (SSM-12).
- [ ] Establish whether a name with a slash but no leading slash (`a/b`) is
      legal. It is accepted here.
- [ ] Reads are not held to the reserved prefixes; `/aws/service/...` answers
      `ParameterNotFound` and the ledger says public parameters are not served.

**Put**
- [ ] Create without `Type` refused; overwrite without `Type` keeps it; overwrite
      with another `Type`: establish AWS's code and match it.
- [ ] Existing name without `Overwrite`: `ParameterAlreadyExists`.
- [ ] Each overwrite adds exactly one version, including an unchanged value;
      the 101st version (SSM-12).
- [ ] Value: empty (SSM-14); size per tier (SSM-2); a StringList (SSM-15).
- [ ] `AllowedPattern` enforced on create and on every overwrite (SSM-1).
- [ ] `Tags` with `Overwrite` refused (SSM-8). Tags on create are stored.
- [ ] Overwrite with no `Description`, `KeyId`, `Tier` or `Policies` keeps the
      stored ones. Establish how each is cleared on AWS.
- [ ] `Tier`: `Intelligent-Tiering` is stored as typed and echoed; establish
      what AWS returns for it in `PutParameter` and in `DescribeParameters`.

**SecureString**
- [ ] Without `WithDecryption`, every read returns ciphertext, never plaintext:
      GetParameter, GetParameters, GetParametersByPath, GetParameterHistory.
- [ ] `KeyId` default, a given `KeyId`, and that a missing `ssm.key` file or a
      corrupt value answers a 500 and not an empty string.
- [ ] Overwrite without `Type` stays sealed (there is a test; keep it).

**Reads**
- [ ] Selectors: `name`, `name:N`, `name:label`, ARN, ARN with a selector; a
      missing version and a missing label are `ParameterVersionNotFound`; the
      `Selector` member is right for each (SSM-7).
- [ ] `GetParameters`: 1 to 10 names (SSM-13), selectors inside the list,
      duplicates, order of `InvalidParameters`.
- [ ] `GetParametersByPath`: the path itself is not under itself; `Recursive`
      off returns one level; `/` as the path; paging with `MaxResults` 1 to 10
      and a stale token (SSM-4); `ParameterFilters` (Type, KeyId, Label), which
      are ignored today.
- [ ] `DescribeParameters`: paging; each `ParameterFilters` key the model
      allows (`Name`, `Type`, `KeyId`, `Path`, `Label`, `Tier`, `DataType`,
      `tag:`) either works or is refused, not ignored; the older `Filters`.
- [ ] `GetParameterHistory`: order, paging, every member in SSM-10.

**Labels**
- [ ] The rules in SSM-3, the limit in SSM-11, 1 to 100 characters.
- [ ] A label moves when attached elsewhere; `ParameterVersion` omitted means
      latest and the response says which (SSM-6).
- [ ] Unlabel: absent label, absent version, and `ParameterVersion` required.

**Delete**
- [ ] `DeleteParameter` on a missing name is `ParameterNotFound`.
- [ ] `DeleteParameters`: partial result, 1 to 10 names (SSM-13).
- [ ] Deleting and re-creating starts again at version 1 with no labels or tags.

**Tags**
- [ ] Key 1 to 128, value 0 to 256, the character pattern (model, enforced).
- [ ] The per-resource limit and the reserved `aws:` key prefix: establish and
      enforce. `aws:cloudformation:stack` is accepted today.
- [ ] A missing parameter is `ParameterNotFound` (AWS's code here is not
      established; it may be `InvalidResourceId`).

**Policies and lifecycle**
- [ ] Expiration: unreadable from the timestamp on every read path, gone from
      every list, deleted by the janitor; a timestamp in the past at put time.
- [ ] Tier required for a policy (SSM-9); the two notification types' tier.
- [ ] Everything above survives a restart: versions, labels, tags, sealed
      values, Expiration, and the janitor resuming.

**Declined**
- [ ] All 139 undispatched operations answer one code, the one a real SDK maps
      to a typed error, and `not-built.md` lists each family with its reason.

## 6. Console

The design's "Parameter Store" page: 16 boards. Templates: `templates/ssm.html`,
the `ssm_create` block in `create.html`, the shared `value_workspace` and
`value_diff` in `workspace.html`, `tag_editor` and `fail_inline` in `panes.html`.
Handlers: the `ssm*` functions in `handlers_sec.go`, `tagsView` and `tagsSave`
in `handlers_tags.go`, routes in `routes_config.go`, grouping in `ssmtree.go`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Ssm-Home | Parameters by parent path, path query, nothing open | `ssm_home`, `ssm_lp`; `ssmParams` | exists; facts strip and path chips are **new** |
| Ssm-01-None | No parameters yet | `ssm_home`, `empty_detail`, `lp_empty` | detail exists; the list-pane half is broken (SSM-19) |
| Ssm-02-Create | Create a String | `ssm_create`; `ssmCreate` | form exists; teaching lines and "Where it will land" are **new** |
| Ssm-03-Create-Secure | SecureString, Advanced open | `ssm_create`; `ssmCreate` | type exists; the whole Advanced block is **new** (SSM-20) |
| Ssm-04-Create-Refused | Name rule refused | `fail_inline` | exists (why-line: foundation A2) |
| Ssm-05-Create-Exists | Name already exists | `fail_inline` | refusal exists; the panel naming the existing parameter is **new** (SSM-22) |
| Ssm-06-Param | View a String | `ssm_param_detail`, `value_workspace` view; `ssmParam` | exists; "Also on record" and "Read it from code" are **new** |
| Ssm-07-Param-Secure | View a SecureString, masked | same, with `masked` | exists; KMS key, Tier and the read strip are **new** |
| Ssm-08-Edit | Edit, save as the next version | `value_workspace` edit; `ssmPut` | exists; "What saving does" and "What changes" are **new**. Shows A4 as it behaves today |
| Ssm-09-Edit-Gone | Save when the parameter has gone | `fail_inline` | exists |
| Ssm-10-Versions | Versions and labels | `value_workspace` versions; `ssmLabel`, `ssmUnlabel` | exists; Value and Modified columns and "Who reads which version" are **new** |
| Ssm-11-Diff | Diff against current | `value_diff`; `ssmDiff` | exists |
| Ssm-12-Label-Refused | A label refused | `fail_inline` | exists |
| Ssm-13-Tags | Tags as a draft, one refused | `tag_editor`; `tagsSave` | exists (foundation A5) |
| Ssm-14-Path | Path query result and its other answers | `ssm_path_result`; `ssmPath`, `ssmDeletePath` | exists |
| Ssm-15-Confirms | Delete one, delete under a path | `hx-confirm` in `ssm_param_detail` and `ssm_path_result`; `ssmDelete`, `ssmDeletePath` | exists |

There is no Denied board, on purpose: see section 1.

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Facts strip on Home (count, SecureString count, paths, last change) | The `DescribeParameters` result `ListParameters` already holds, and `ssmGroups` |
| Path chips on Home | `ssmGroups`' `Dir` values; each submits the existing path form |
| Teaching lines under Name, Type, Value | Settings table (foundation B4) for the model's lengths and enums; `validParameterName` for the hand-derived name rules; the tier table in section 2 |
| "Where it will land" | The typed name, `splitParamPath`, the parameter list, and the instance's account and region for the ARN |
| Advanced: Description, Tier, KMS key, Expires at | `PutParameter` already accepts all four; `client_sec.go` `PutParameter` and `ssmCreate` must send them. "Expires at" builds one Expiration policy |
| The parameter that has this name | The typed name and `GetParameter` |
| "Also on record": Tier, Description, KMS key | `DescribeParameters` returns them; `Parameter` in `client_sec.go` does not decode them |
| "Also on record": Labels, and "Who reads which version" | `ParameterHistory`'s `Labels`, already fetched |
| Read it from code | The name and type; the selector forms in `store.Get` |
| Value and Modified on Versions | `ParameterHistory`, already fetched, masked for a SecureString |
| What changes | The form against the current value |

**Where the teaching facts come from**

| Line | Source |
|---|---|
| Name: 1 to 2048 | `ssm/validate.go` (model) |
| Name: levels, characters, reserved words | `ssm/store.go` `validParameterName` (hand-derived, `doc`) |
| Type is fixed once created | `ssm/store.go` `Put` |
| StringList commas are not checked | SSM-15; changes if that finding is fixed |
| "stores any length" against AWS's 4 KB and 8 KB | SSM-2; the sentence is rewritten when it is fixed |
| SecureString: sealed with a local key, protects the file only | `docs/SUPPORT.md`, Differences from AWS |
| A read without decryption returns ciphertext | `ssm/store.go` `render` |
| Tier is cosmetic; KMS key recorded and not used | The ledger |
| Description 0 to 1024; KMS key 1 to 256 and its characters | `ssm/validate.go` (model) |
| Expiration enforced, swept every minute | `ssm/store.go` `expired`, `SweepExpired`; `ssm/ssm.go` `janitor` |
| A label names one version and moves | The ledger |
| Label 1 to 100 | `ssm/validate.go` (model) |
| Tag key 1 to 128, value 0 to 256, the character set | `ssm/validate.go` (model) |
| Diff is against current, by whole lines, as a set | `handlers_sec.go` `ssmDiff`, `lineDiff` |
| A path query is recursive and excludes the path itself | `ssm/store.go` `ByPath` |

**Statements on the boards not yet backed.** Prove or remove each before the
board ships.

- Ssm-03: "AWS documents parameter policies for Advanced parameters" and "the
  policy is not returned by reads". The first is `doc` unrecorded; the second
  describes SSM-10 and must go when that is fixed.
- Ssm-02, Ssm-03: the 4 KB and 8 KB figures, and "Intelligent-Tiering chooses
  between the two".
- Ssm-10, Ssm-12: the sentences about which labels AWS drops into
  `InvalidLabels`. They describe SSM-3 and are rewritten when it is fixed.
- Ssm-05: "From the CLI the same change is `put-parameter --overwrite`".
- Ssm-08: the callout that key/value rows save strings describes A4 and is
  removed, with the quoted values in "What changes", when A4 is fixed.
- Ssm-12: "Removing a label with × never refuses" describes A12 and SSM-11.
- Ssm-13: "Tags written before the refused one stay written" describes A5.
- Ssm-09: "its Expiration policy passed" is reachable from the console only
  once the Advanced block exists.
- Ssm-15: "its 2 versions" needs the history count at confirm time; the confirm
  today has only the name.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | AllowedPattern (SSM-1); size by tier (SSM-2); label rules and limit (SSM-3, 11); label response version (SSM-6); selectors (SSM-7); tags with overwrite (SSM-8); policy tier and types (SSM-9); depth and version limits (SSM-12); DataType (SSM-16) |
| SDK v2 | Paging on all three lists with the SDK paginators (SSM-4); every output member (SSM-10); a declined operation's error type (SSM-5) |
| Rejection parity | List lengths on GetParameters and DeleteParameters (SSM-13) |
| Coverage | `Unreached` empty in `coverage_model_test.go` (SSM-5) |
| Restart | Versions, labels, tags, a sealed value, an Expiration before and after its time |
| Identity | ARNs and the ARN-form selector under a non-default account and region |
| Conformance (boto3) | Every row in section 8 |
| Console handler | `ssmPut`, `ssmLabel`, `ssmUnlabel` keep `Secure` and the mode (SSM-17); `ssmDiff` on a SecureString and on a missing version (SSM-18, 23); `ssm_lp` empty (SSM-19); the list past 50 (SSM-21); the new create fields (SSM-20) |
| Browser | Edit a SecureString and stay masked; each `new` item; the Exists panel; a dropped label reported as dropped; both confirms' sentences |
| Mutation | `ssm/` baseline, then survivors in `store.go` (`validParameterName`, `Put`, `Label`) and `actions.go` (`parseExpiration`, `applyFilters`) |

## 8. Recording queue

Every one of these is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| Name rules and the refusal text; `/awsome/x`; `a/b` with no leading slash | doc | `test_ssm::name_rules` (extends `test_refusals`) |
| 16 levels is `HierarchyLevelLimitExceededException` | doc | `hierarchy_depth` |
| Value over 4 KB on Standard, over 8 KB on Advanced, and the code | doc | `value_size_by_tier` |
| An empty value is refused | memory | `empty_value` |
| A malformed StringList | memory | `string_list_shape` |
| `AllowedPattern` mismatch and its code | doc | `allowed_pattern` |
| `Tags` with `Overwrite` and its code | doc | `tags_with_overwrite` |
| A type change on overwrite, and its code | memory | `change_type_on_overwrite` (exists as an outcome) |
| The 101st version | doc | `version_limit` |
| A policy on a Standard parameter; an unknown policy type | doc | `policy_needs_advanced` |
| What `Intelligent-Tiering` returns as `Tier` | doc | `intelligent_tiering_echo` |
| How Description, Policies and KeyId are cleared | memory | `clear_settings` |
| Label rules and `InvalidLabels`; the eleventh label | doc | `label_rules`, `label_limit` |
| `LabelParameterVersion` returns the version it labelled | doc | `label_latest_version` |
| Unlabel on a missing version | memory | `unlabel_absent_version` |
| `Selector` for each read form, ARN included | memory | `selectors` |
| Default and maximum page sizes; a stale `NextToken` | doc | `paging` (extends `test_paths_and_bulk_reads`) |
| 11 names in GetParameters and DeleteParameters | doc | `names_limit` |
| `DataType` `aws:ec2:image` with a non-AMI value | doc | `data_type_image` |
| Tag limit per parameter; an `aws:` key; tags on a missing parameter | doc | `tag_limits` |
| The code for an operation an account cannot use | memory | `declined_operation_code` |
| The wording of `ParameterAlreadyExists` | memory | `put_without_overwrite` (exists) |

## 9. Owed to integration

| What | With |
|---|---|
| `AWS::SSM::Parameter` through CloudFormation: Tier, Policies, AllowedPattern, DataType and Tags reach the service (`svc_ssm.go` sends Description and Tags only, always with `Overwrite`, which SSM-8 will refuse on update) | CloudFormation |
| `AWS::SSM::Parameter::Value<...>` template parameters and `{{resolve:ssm:...}}` / `ssm-secure` references: whether they resolve is not established (`internal/cfn/resources.go` lists the first as "a parameter reference, not a resource") | CloudFormation |
| A Lambda function reading a parameter at start, under soft and enforce, with the access log naming `ssm:GetParameter` on the parameter's ARN | Lambda, IAM |
| An identity policy scoped to a path (`parameter/harbour/*`) allowing and denying `GetParametersByPath` | IAM |
| `kms:Decrypt` is not required to read a SecureString here; on AWS it is. A listed difference, or a check once KMS's stage is done | KMS, IAM |
| Secrets Manager references through `/aws/reference/secretsmanager/...` | Secrets Manager |
| Parameter change events (`Parameter Store Change`) reaching a rule | EventBridge |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SSM-1 to SSM-24 each closed, with the test that failed first named beside it.
- [ ] All 152 operations have a row or a named family in `SUPPORT.md`; the 139
      declined ones answer one code; `Unreached` is empty; `not-built.md` gives
      the resource-policy operations their own reason.
- [ ] Decided and written into `SUPPORT.md`: `DataType` (SSM-16), the
      notification policy types (SSM-9), public parameters under `/aws/service`,
      whether `kms:Decrypt` is modelled, the 100-version limit.
- [ ] The per-setting table in section 2 has no "silent" left, and every C
      setting reads as cosmetic in the create form and on the parameter page.
- [ ] Each "not established" on this page is resolved from the model or a
      named doc page, or its recording is queued with the test written and
      skipped: `/awsome/x`; `a/b`; the type-change code; how settings are
      cleared; the tag limit and its missing-parameter code; whether the model
      states the names-list length; the empty value; `Intelligent-Tiering`'s echo.
- [ ] Every box in section 5 ticked, or moved to the recording queue.
- [ ] A SecureString's plaintext reaches the page only behind Reveal, on every
      route: view, after save, after label, in the diff, in the version table,
      in the path result.
- [ ] All 16 boards implemented from the foundation's shell, each `new` item
      reading only the data named for it.
- [ ] No statement on a Parameter Store screen is in the "not yet backed" list
      in section 6, and every board sentence that describes a finding has been
      rewritten to the fixed behaviour.
- [ ] Both confirms say only what the code does.
- [ ] `e2e/tests/ssm.spec.ts` covers every route under `/ssm` with the route
      gate on; the accessibility sweep is clean on Home, create and all four tabs.
- [ ] Restart test covers versions, labels, tags, a sealed value and Expiration.
- [ ] Conformance: a scenario for every row in section 8. Zero "answered
      differently". The unverified count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Foundation A7 corrected for this service.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract: the three operation groups, per-setting tiers, decisions | `docs/SUPPORT.md`, `docs/not-built.md` |
| 2 | Red tests for SSM-1 to SSM-16 | `ssm/*_test.go` (a new handler test file beside `conformance_fixes_test.go`), `ssm/sdk_test.go`, `ssm/coverage_model_test.go` |
| 3 | Declined operations answer one code | `ssm/ssm.go` (`fleetOps`, `ServeHTTP`) |
| 4 | Paging and the missing output members | `ssm/actions.go` |
| 5 | Put: AllowedPattern, size by tier, tags with overwrite, policies, limits | `ssm/store.go`, `ssm/actions.go` |
| 6 | Labels: rules, limit, response version, unlabel; selectors | `ssm/store.go`, `ssm/actions.go` |
| 7 | Section 5 sweep, parity cases, restart | `ssm/rejection_parity_test.go`, `ssm/validate.go` (regenerated, not edited), `persistence_test.go` |
| 8 | Conformance scenarios and claims table | `conformance/tests/test_ssm.py`, the claims file from foundation B1 |
| 9 | Console: masking on every route, the empty list, paging the list | `internal/console/handlers_sec.go`, `client_sec.go`, `templates/ssm.html`, `templates/workspace.html` |
| 10 | Console: the boards and `new` items | `templates/ssm.html`, `templates/create.html` (`ssm_create`), `templates/workspace.html`, `handlers_sec.go`, `client_sec.go`, `ssmtree.go`, `routes_config.go` |
| 11 | Browser tests | `e2e/tests/ssm.spec.ts` |
| 12 | Provision follows the API changes | `internal/provision/svc_ssm.go` (only what SSM-8 breaks; the rest is section 9) |
| 13 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

`workspace.html`, `valueworkspace.go` and `layout.html` are shared with Secrets
Manager. SSM-17, SSM-18 and SSM-23 are fixed here in the SSM handlers; the
Secrets Manager diff has the same shape and is that stage's to check.

**Prior art.** The rules in section 5 come from three AWS pages, and each test
names the one it rests on: the `PutParameter` API reference, "Working with
parameter labels" and "Assigning parameter policies" in the Systems Manager
user guide. No other emulator's behaviour was read for this page.
