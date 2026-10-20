# Parameter Store (SSM)

Stage 2. Size **L** (was S; re-estimated 2026-10-11, see section 1). Follows
[../01-protocol.md](../01-protocol.md); the common acceptance criteria there
apply in full, and this page adds Parameter Store's own.

**Pre-stage review done 2026-10-11.** Every claim on this page was checked
against `.audit-models/ssm.json`'s documentation text, every board was rendered
beside the real page, and the findings below were found by reading and by
running a built binary. Corrections are marked in place as *Corrected
2026-10-11: was …; model: "…"*. A claim that could not be settled from the model
stays `memory` and is not acted on.

## Pre-stage review (2026-10-11)

How each of the twelve questions in
[../02-lessons-from-sqs.md](../02-lessons-from-sqs.md) was answered for this
service, with the finding ids it produced. Finding ids SSM-1 to SSM-24 are the
document's own (section 4); SSM-25 to SSM-49 are new (section 4, second table).

| Q | Answer |
|---|---|
| **L1** model documentation | **Read**: every operation and every member of the input and output shapes of the 13 served operations (`PutParameter`, `GetParameter`, `GetParameters`, `GetParametersByPath`, `GetParameterHistory`, `DescribeParameters`, `DeleteParameter`, `DeleteParameters`, `LabelParameterVersion`, `UnlabelParameterVersion`, `AddTagsToResource`, `RemoveTagsFromResource`, `ListTagsForResource`), plus `GetServiceSetting`, `UpdateServiceSetting`, `ResetServiceSetting`, `PutResourcePolicy`, `GetResourcePolicies`, `DeleteResourcePolicy`, and the 32 error shapes those operations list. **Found**: the model's prose carries what this document called "in the user guide, not the model": the 4 KB and 8 KB limits, the name rules, the label rules, the tier rules, the policy types, the 100 retained versions, the 50 tags. 37 corrections are marked in place; most moved a source from `doc` or `memory` to `model`, and six claims were wrong (SSM-12's 101st version, SSM-8's source, SSM-4's page default, section 2's "Tier is cosmetic", section 5's `Label` filter and the tag-limit "not established", section 9's `svc_ssm.go` description). New: SSM-25, 29, 30, 31, 32, 37, 41. |
| **L2** optional parameters | **Read**: `Type` ("isn't required when updating"), `Tier` (default tier configuration; Intelligent-Tiering; cannot go back to Standard), `Policies` ("preserved until you send new policies or an empty policy"), `KeyId` (default key), `Overwrite`, `LabelParameterVersion.ParameterVersion` ("latest"), `UnlabelParameterVersion.ParameterVersion` ("If it isn't present, the call will fail"), `GetParametersByPath.Recursive` / `ParameterFilters` (only Type, KeyId, Label), `MaxResults` ("between zero and the value of MaxResults"), `DescribeParameters.Shared` / `Filters`, `WithDecryption` ("ignored for String and StringList"). **Found**: SSM-6, 27, 28, 34, 39 and the corrected SSM-4. |
| **L3** error codes | **Read**: each operation's `errors` and each shape's text. **Found**: `HierarchyLevelLimitExceededException` for depth (SSM-12), `HierarchyTypeMismatchException` for a type change (SSM-26), `ParameterVersionLabelLimitExceeded` (SSM-11), `ParameterMaxVersionLimitExceeded` only when the oldest version holds a label (SSM-12), `TooManyTagsError` (SSM-37), `InvalidResourceId` on the tag operations (SSM-38), `InvalidFilterKey` / `InvalidFilterOption` / `InvalidFilterValue` (SSM-34), `InvalidNextToken` (SSM-4), `IncompatiblePolicyException`, `PoliciesLimitExceededException`, `InvalidPolicyTypeException` used for unparsable JSON (SSM-9), `ParameterLimitExceeded` (SSM-41). **Not established**: the code for a value that breaks `AllowedPattern` (the model lists both `ParameterPatternMismatchException`, "The parameter name isn't valid", and `InvalidAllowedPatternException`, "The request doesn't meet the regular expression requirement"), and the code for a value over its tier's size (none of the listed errors names it). Both are in section 8. |
| **L4** direction | Named on every finding (section 4). Of the 25 new: 9 more forgiving, 2 both ways (SSM-31, SSM-34), 2 wrong code, 5 wrong shape, 3 wrong data or behaviour, 1 missing, 3 console or coupling. The stricter case that breaks a correct caller: SSM-31 (a name with a leading space is refused; AWS trims it). The first pass's SSM-12 was a *stricter* error of the SQS-16 kind and is corrected. |
| **L5** every wire | **Checked, none.** The model declares `aws.protocols#awsJson1_1` only (no Query, no `awsQueryCompatible`, no `jsonName`, no flattened lists); the service speaks one wire and the 13 operations are driven by the rejection-parity baselines, the SDK v1 and v2 tests and boto3. Ran: `X-Amz-Target` with the wrong prefix, no prefix and no header answer 400; a malformed body is `SerializationException`; an empty body is a valid empty request; content type `application/x-amz-json-1.0` is accepted (AWS wants 1.1; harmless). |
| **L6** tokens | `NextToken` (SSM-4), a label (`name:label`) and a version selector (`name:version`), an ARN. A `NextToken` of `junk` is accepted today and the whole list comes back (run). After SSM-4: tokens are `internal/awspage` keys (opaque, minted here, a token it did not mint is `InvalidNextToken`); a token minted for another listing is not detected by that package, and `DescribeParameters` and `GetParametersByPath` share a key space, so the test asserts what a foreign token does and the document says it. A stale label or a trimmed version is `ParameterVersionNotFound` (held); a label moved to another version moves for every reader (model: "You can move a label"). **Found**: SSM-25 (an ARN names an account and a region and is resolved by name alone). |
| **L7** asynchronous | **Decided here** (table below). Nothing in Parameter Store is asynchronous in a way a board draws; two things are asynchronous on AWS and are listed differences. |
| **L8** latency rules | **Decided here** (table below). No setting; none of these has an error AWS names except `TooManyUpdates` and throttling. |
| **L9** state | Parameters, versions, labels, tags, policies, sealed values: bbolt, persisted, and a restart test names each (SSM-24's list). `ssm.key` is data-dir state: lose it and every SecureString answers 500 on a decrypting read (held; stays). The janitor's schedule is the process's and is not state. New state this stage adds: the default-tier service setting (SSM-42; the application sees it, so it persists); page tokens are stateless; nothing else. |
| **L10** the board is the spec | **Rendered**: all 16 boards beside the real page in light, three in dark, the main detail and the create form at 900 wide. **0 match**; section 6a. Findings: SSM-18, 19, 45, 46, 47 and the build list. |
| **L11** five teaching lines | The create form has none, the parameter page has none, and `ssm/settings.go` does not exist. Section 6 lists the settings that need a record and which of the five lines each has; the two a board cannot draw without a new fact are the Tier neighbour check (value size against tier) and the label neighbour check (which readers the label moves). |
| **L12** icons | **Kept**: the rail, the empty state and the parameter header already draw the AWS icon (`awsIcon "ssm"`); the boards' `PS` marks are placeholders. The list pane header has no icon on the board or the page. |

**Asynchronous on AWS, decided (L7).**

| Operation or behaviour | On AWS | Here | Decision |
|---|---|---|---|
| `PutParameter` with `DataType` `aws:ec2:image` | "validated asynchronously, and the PutParameter call returns before the validation is complete"; an invalid AMI id means the parameter "will not be created or updated" (model) | Created at once; no EC2 to check against | **Immediate, difference listed** (SSM-16). A board that drew "validating" would need an EC2 service. |
| Expiration policy | "When the expiration time is reached, Parameter Store deletes the parameter" (model); how promptly is not stated | Reads fail from the timestamp, a sweep deletes within a minute | **Immediate for readers**; SSM-36 closes the one-minute window where the parameter is half gone. |
| `ExpirationNotification`, `NoChangeNotification` | "initiates an event in Amazon CloudWatch Events" (model) | Stored, nothing emitted | **Not built**; tier C until EventBridge integration (section 9). |
| Policy `PolicyStatus` | `Pending`, `InProgress`, `Finished`, `Failed` (model) | Not returned (SSM-10) | **Immediate**: `Pending` until its time, `Finished` after, no `InProgress`. |
| Tier change, label move, delete | Not asynchronous | Immediate | none |

**Time and consistency rules (L8).**

| Rule | Source | Here | Switch |
|---|---|---|---|
| "After deleting a parameter, wait for at least 30 seconds to create a parameter with the same name." | model: `DeleteParameter`, `DeleteParameters` | Re-create is immediate; version restarts at 1 | **None.** The model names no error for it, so a strict mode would have nothing to refuse with. A listed difference, and one sentence on Ssm-09. |
| `TooManyUpdates` (429): "concurrent updates for a resource that supports one update at a time" | model: Put, Label, Unlabel, tags | Updates serialise; never answered | None; listed difference. |
| Throughput: "ThrottlingException: Rate exceeded" above the account's transactions per second | model: `PutParameter`, `GetParameter`, `GetParameters` documentation | Not modelled | None. A throttle in a local test loop is a worse tool. Listed. |

**Classes of mistake this service is most at risk of.**

1. **Limits that live in prose and were called `doc`.** Every rule that makes Parameter Store itself (sizes, names, labels, tiers, versions, tags) is in the model's documentation text, and the first draft of this page filed it under the user guide. The stage's first act is to rebuild `ssm/validate.go` from a fresh `dzaudit cases ssm` (SSM-35) and write each prose rule beside it with its quote.
2. **A refusal that AWS does not make.** SSM-12 as first written ("the 101st version is refused") is the SQS-16 shape: the model says the 101st version succeeds and the oldest is dropped. The same risk sits in SSM-8 (the refusal is `memory`) and SSM-27.
3. **A list answered with everything.** Three lists ignore paging and filters; the moment they page, three callers inside this repository (the console list, the path query, `apply`'s export) silently lose items (SSM-43). This is LAM-6 again and is fixed in the same commit.
4. **A board built from its sentences.** Fifteen of sixteen boards have a right-hand or inline panel the page has not got, and four boards describe behaviour that has since changed (A4, A5) or will change when a finding closes.
5. **An identifier that names an account.** SSM-25 is SQS-23: the ARN is parsed for its tail and the account and region are ignored.

## Foundation hand-offs (stage 0, 2026-10-20)

What the foundation (stage 0, package P6) left for this stage, so it is not lost, and what it did already. The shared items are in [../00-foundation.md](../00-foundation.md); each line here says what is done, what is owed, and the test that proves it.

| Item | Parameter Store: done, and what this stage does |
|---|---|
| B6 the model sweep | 90 legacystub and 49 unknown of 152 operations (the false `InvalidAction`). Move the stubs to `awshttp.NotBuilt` and build or refuse the 49; `Unreached` is in `ssm/coverage_model_test.go` and empties here. |
| B8 generated cases | 100 committed, 1752 regenerated (7 only in the committed file, 1659 only in the new one): the largest regeneration (229 constraints the old generator never read). Regenerate in this stage. |
| B9 stored-policy validator | Not on `resource_policy_wiring_test.go`'s list: this service stores no resource policy document: where the model has the operation (`PutResourcePolicy`, `PutPermission`) the sweep answers it `legacystub` or `unknown`, and where it does not there is nothing to store. If this stage builds one, it calls `iampolicy.ValidateResourcePolicy` with the service's own code and wording and is added to that test. |
| B4 settings records | 0 hand-written bounded number inputs; the model carries most of the ranges, so the records can be generated from `validate.go`. |
| A15 fragments | On one view model already (`paramDetailView`, walked by `fragments_test.go`). |
| A2 the why line | Set `Local: true` (`awshttp.APIError`) on each refusal that is doze-aws's own limit and not AWS's, which this document's findings mark; nothing sets the stamp today except the console's own 503. Test: one such limit per service answers with `X-Doze-Local-Limit` and the page says "Refused here" (`internal/console/why_line_test.go` is the worked example). |
| B3 mutation | Baseline recorded in `testdata/mutation.json` (`ssm`); this stage runs `task mutate:check -- ssm` after its changes, and any survivor it adds has a test or a reason. |


## 1. Summary

The Parameter Store slice of Systems Manager: String, StringList and
SecureString parameters with versions, labels, history, tags and an Expiration
policy, over one bbolt store, on one wire protocol (AWS JSON 1.1, target prefix
`AmazonSSM`). SecureString values are sealed with an AES-256-GCM key the service
keeps in its data directory; KMS is not called. It is a leaf: no service calls
it and it calls none (`internal/provision` and the console are clients).
`internal/provision/svc_ssm.go` creates parameters for CloudFormation and
`apply`.

It has no resource policy here (`PutResourcePolicy` is in the AWS model and is
not served), so no console action on it can be Denied.

**Estimate: L, eight to ten focused days.** *Corrected 2026-10-11: was "S, one to
two focused days… nearer the top of S"; the review found 25 findings beyond the
24 the page listed, 15 of 16 boards differing in shape, three client changes
that must ride with API changes, and a decision on the declined operations'
answer.* The split, honestly: the API half is about three days (paging on three
lists, the filter engine with its three options and eight keys, label rules and
limits, version retention, the tier / size / policy rules, the name rules from
prose, tags, six codes, the 139-operation refusal, regenerated tables and parity
cases); the console is about four to five (the create form with its Advanced
block and settings record, the two-column parameter page, versions with its
panels, the side panels on Home, Edit, Diff, Tags and Path, confirms with
bodies, rendering each board and fixing what the render shows); proof is about
one to two (restart, boto3 scenarios, mutation, the identity run). SQS's
estimate was S, two days, and was several times low; this one is already
two to three times the size its first draft claimed, and the review is what
found it. **Actual:** _fill in when done._

## 2. Contract

The model (`ssm/testdata/ops_ssm.json`) lists **152** operations ✓ (counted
2026-10-11: 13 + 90 + 49). They fall in three groups today.

| Group | Count | Now | Target | Note |
|---|---|---|---|---|
| Parameter Store, dispatched (`handlers` in `ssm/actions.go`) | 13 | F | F | Table below. |
| Fleet management refused by name (`fleetOps` in `ssm/ssm.go`) | 90 | S | S | Answers HTTP 400 `UnsupportedOperationException` naming what it would need. *Corrected 2026-10-11: the target is decision D5, HTTP 501 `NotImplemented` with `X-Doze-Not-Built`, through `awshttp.NotBuilt`; `UnsupportedOperationException` is a real SSM code that two operations list and is not what a not-built operation answers (see SSM-5).* |
| Everything else | 49 | **silent** | S | Answers `InvalidAction` "unknown SSM action", which is false: the action is known and not served. Listed in `ssm/coverage_model_test.go` `Unreached` and in `testdata/modelsweep.json` (139 SSM entries: 90 `legacystub`, 49 `unknown`). See SSM-5. |

`docs/SUPPORT.md` and `docs/not-built.md` both say every declined operation
answers `UnsupportedOperationException`. That is true of 90 and false of 49, and
after the foundation's B6 it is the wrong code for all 139.

The 49 include `PutResourcePolicy`, `GetResourcePolicies` and
`DeleteResourcePolicy`. *Corrected 2026-10-11: was "Parameter Store operations
(sharing a parameter across accounts), not fleet management". The model's text
covers three resource types: "OpsItemGroup", "Parameter … used to share a
parameter with other accounts using Resource Access Manager (RAM). To share a
parameter, it must be in the advanced parameter tier", and "Document". The
`GetResourcePolicies` text says "Currently, OpsItemGroup is the only resource
that supports Systems Manager resource policies", which contradicts it; the
reason for declining is that sharing needs RAM, which is not here.* Target S,
declined, with that reason written down.

`GetServiceSetting`, `UpdateServiceSetting` and `ResetServiceSetting` sit in
`fleetOps` ("Resource data sync / service settings"). *Corrected 2026-10-11: two
of the ten setting ids the model lists are Parameter Store's own,
`/ssm/parameter-store/default-parameter-tier` and
`/ssm/parameter-store/high-throughput-enabled`, and the first changes what a
`PutParameter` without `Tier` does ("if you don't specify a tier in a request,
Parameter Store assigns the tier based on the current Parameter Store default
tier configuration"). They are not fleet management.* A decision (SSM-42).

The 13 dispatched operations:

| Operations | Now | Target | Note |
|---|---|---|---|
| PutParameter | F | F | Per-setting tier below. See SSM-1, 2, 8, 9, 14, 15, 16, 26 to 32. |
| GetParameter | F | F | `name`, `name:version`, `name:label`, ARN. See SSM-7, SSM-25. |
| GetParameters | F | F | Found and `InvalidParameters`. Names 1 to 10; results in alphabetical order. See SSM-13, SSM-33. |
| GetParametersByPath | F | F | `Recursive` works. `MaxResults`, `NextToken` and `ParameterFilters` are ignored. See SSM-4, SSM-34. |
| GetParameterHistory | F | F | All versions with labels. No paging; members missing; description, type and data type are the parameter's current ones for every entry. See SSM-4, 10, 40. |
| DescribeParameters | F | F | `ParameterFilters` on Name (Equals, BeginsWith) and Type only; every other key, and the older `Filters`, accepted and ignored; `Contains` matches nothing. No paging. See SSM-4, 10, 34. |
| DeleteParameter, DeleteParameters | F | F | Names 1 to 10. See SSM-13. |
| LabelParameterVersion, UnlabelParameterVersion | F | F | A label moves on re-label. No label rules, no limit, `InvalidLabels` always empty. See SSM-3, 6, 11, 39. |
| AddTagsToResource, RemoveTagsFromResource, ListTagsForResource | F | F | `ResourceType` Parameter only; any other answers `InvalidResourceType`. *Corrected 2026-10-11: was "Per-resource limit not established"; model: "Most resources can have a maximum of 50 tags", error `TooManyTagsError`.* See SSM-37, SSM-38. |

`PutParameter` needs its own tiers, because "PutParameter is F" hides which
settings do anything:

| Setting | Now | Target | To confirm |
|---|---|---|---|
| Name | F | F | 1 to 2048 is the model's length trait. *Corrected 2026-10-11: the rules were called hand-derived; model (PutParameter.Name): "can't be prefixed with "aws" or "ssm" (case-insensitive)", "a-zA-Z0-9_.-" and "/" to delineate, "Parameter hierarchies are limited to a maximum depth of fifteen levels", "you must include a leading forward slash character (/)" for a hierarchy, "The service removes any spaces specified for the beginning or end", spaces between characters are a `ValidationException`, and "The maximum length for a parameter name that you specify is 1011 characters" counting the ARN prefix (45 for us-east-2).* `validParameterName` enforces the characters, the prefix and 15 levels; it does not enforce the leading slash (SSM-29), the 1,011 (SSM-30) or the trim (SSM-31), and 16 levels answers the wrong code (SSM-12). A first segment that merely begins `aws` (`/awsome/x`) is accepted and a plain name that begins `aws` is refused: the model's "prefixed with" does not say which is meant, so which segment is held stays `memory` and is queued. |
| Type | F | F | Required on create, immutable after ("Specifying a parameter type isn't required when updating a parameter", model). *Corrected 2026-10-11: the code AWS gives a type change was "not established"; model: the PutParameter error list names `HierarchyTypeMismatchException`, "you can't change a parameter from a String type to a SecureString type".* See SSM-26. |
| Value | F | F | *Corrected 2026-10-11: was "No size limit and no minimum here"; model: "Standard parameters have a value limit of 4 KB. Advanced parameters have a value limit of 8 KB", and "You can't include values wrapped in double brackets {{}} or {{ssm:parameter-name}}". The model has no `@length` on `PSParameterValue`, so a minimum stays `memory` (SSM-14).* See SSM-2, 14, 15, 32, 49. |
| Overwrite | F | F | |
| KeyId | C | C | Pattern and length enforced; recorded and returned, never used. Defaults to `alias/aws/ssm` (model: "the system uses the default key associated with your Amazon Web Services account"). The model says the alias is returned "Applies to SecureString parameters only"; a String given a key stores and returns it (SSM-44). |
| Description | F | F | 0 to 1024. Returned by DescribeParameters and history (but see SSM-40). |
| Tier | ~~C~~ **F** | F | *Corrected 2026-10-11: was "C … nothing differs by tier"; model: the tier sets the size limit (4 KB, 8 KB), whether policies are allowed ("Standard parameters … can't be configured to use parameter policies"), the count ("a maximum of 10,000 standard parameters for each Region", 100,000 advanced), and goes one way ("you can't revert an advanced parameter to a standard parameter"). Intelligent-Tiering resolves per request to Standard or Advanced. Pricing and throughput stay not modelled.* See SSM-2, 9, 27, 28, 41. |
| Policies: Expiration | F | F | Reads fail once it passes; the janitor deletes within a minute (SSM-36). |
| Policies: ExpirationNotification, NoChangeNotification | silent | C | Stored unread. Nothing says so. See SSM-9. Model: they "initiate an event in Amazon CloudWatch Events". |
| Tags | F | F | Accepted with `Overwrite`; whether AWS refuses is `memory` (SSM-8). |
| DataType | silent | C | Any string up to 128 stored. *Corrected 2026-10-11: model lists three values, "text", "aws:ec2:image", "aws:ssm:integration"; `aws:ec2:image` is validated asynchronously and cannot be validated here (no EC2).* See SSM-16. |
| AllowedPattern | **silent** | F | Length checked, then dropped: not stored, not enforced, not returned. See SSM-1. |

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests | `ssm/`: 13 tests in 5 files. SDK v2 (6: versions and labels, SecureString, overwrite keeps type, by-path and describe, delete and tags, fleet refusals), SDK v1 (1), conformance fixes (3: name rules, type on create, path needs a slash) |
| Parity | `rejection_parity_test.go` (2), `coverage_model_test.go` (1). Ledger: 100 of 100 model constraints enforced across the 13 operations, `knownGaps` empty. *Corrected 2026-10-11: the 100 are the committed `cases_ssm.json`; a fresh `dzaudit cases ssm` (foundation B8) lists the list lengths the committed table never carried, and `modelcheck` has no list-length kind (SSM-13, SSM-35).* |
| Conformance | `conformance/tests/test_ssm.py`: 4 scenarios, all **unverified** (`conformance/snapshots` is empty). `test_paths_and_bulk_reads` asks for `MaxResults=2` and records whether a `NextToken` came back; today none does (SSM-4). No claims file (`conformance/claims/ssm.md`) yet. |
| Browser | `e2e/tests/ssm.spec.ts`: 10 tests |
| Restart | One parameter in the root `persistence_test.go`. Labels, tags, SecureString, Expiration: not covered |
| Mutation | *Corrected 2026-10-11: was "No per-package baseline (foundation B3)"; `testdata/mutation.json` holds one, recorded 2026-10-10: 94 killed, 28 survived, 0 timed out, 19 not covered.* It is recorded against code the stage will rewrite and is re-recorded at the end. |

*Corrected 2026-10-11:* the page said "The model gives SSM lengths, patterns and
enums, so shape validation is `model`. Everything that makes Parameter Store
itself (name rules, label rules, size by tier, limits, policies and tiers) is in
the user guide, not the model, so it is `doc` at best." The model's
documentation text states each of those (see the quotes in section 2 and
section 4), so they are `model`; the *traits* give lengths, patterns and enums,
and the *prose* gives the rest. What is `memory` here is the code of a refusal
the prose does not name (section 8).

## 4. Findings

First pass (SSM-1 to SSM-24), re-sourced against the model.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| SSM-1 | P0 | `AllowedPattern` is accepted and thrown away. A value `abc` with pattern `^[0-9]+$` is stored; the pattern is never returned. *Review 2026-10-11: confirmed by run (a mismatching value stored; a pattern `([` accepted). Two open points: the code of a refusal is not established (the model lists `ParameterPatternMismatchException`, "The parameter name isn't valid", and `InvalidAllowedPatternException`, "The request doesn't meet the regular expression requirement"), and the dialect: the model says only "A regular expression used to validate the parameter value", Go's `regexp` has no lookahead and a pattern that compiles on AWS may not here. Decide: a pattern that does not compile here is accepted and the console says it is not enforced, rather than refused.* | `ssm/actions.go` `putParameter` (never reads it); `ssm/store.go` `Put` | run | model: `AllowedPattern` "A regular expression used to validate the parameter value"; code `memory` | Handler test: mismatching value refused; pattern round-trips through DescribeParameters and history; the refusal's code skipped pending the recording |
| SSM-2 | P0 | No value size limit. A 9000-byte String on the Standard tier is stored (run again 2026-10-11: 9,000 and 5,000 bytes on Standard accepted). | `ssm/store.go` `Put` | run | *Corrected 2026-10-11: was `doc`; model (PutParameter.Value): "Standard parameters have a value limit of 4 KB. Advanced parameters have a value limit of 8 KB"; (Tier): "content size limit of 4 KB". Whether "KB" is 4,096 bytes or characters is not stated: `memory`, queued.* The code of the refusal is not in the model's list (`ValidationException` by elimination, message `memory`). | Handler test at 4096, 4097, 8192, 8193 per tier; Intelligent-Tiering over 4 KB lands on Advanced (SSM-28) |
| SSM-3 | P0 | No label rules. `aws-prod`, `ssm`, `has space`, `1-starts-with-digit` are attached and `InvalidLabels` is always empty. A reader of `name:label` works here and fails deployed. Run again: the console toasts "Label “aws-prod” applied". | `ssm/actions.go` `labelParameterVersion`; `ssm/store.go` `Label` | run | *Corrected 2026-10-11: was `doc`; model (LabelParameterVersion): "Labels can contain letters (case sensitive), numbers, periods (.), hyphens (-), or underscores (_). Labels can't begin with a number, "aws" or "ssm" (not case sensitive). If a label fails to meet these requirements, then the label isn't associated with a parameter and the system displays it in the list of InvalidLabels."* The valid labels in the same request are attached (per-label wording; `memory` that AWS does not fail the call). | Handler test per rule: the label is not attached and comes back in `InvalidLabels` |
| SSM-4 | P0 | No paging on any list. `GetParametersByPath`, `DescribeParameters` and `GetParameterHistory` ignore `MaxResults`, never return `NextToken`, and return everything; a `NextToken` of `junk` is accepted. A caller that does not page gets every parameter here and the first page on AWS. | `ssm/actions.go` `getParametersByPath`, `describeParameters`, `getParameterHistory` | run | model: `MaxResults` 1 to 10 (`GetParametersByPath`), 1 to 50 (`DescribeParameters`, `GetParameterHistory`); `InvalidNextToken` on all three; "Request results are returned on a best-effort basis… between zero and the value of MaxResults". *Corrected 2026-10-11: the default page sizes were `doc`; the model states none, so they are `memory` (queued).* | SDK test: 12 parameters under a path, one call returns at most 10 and a token; a paginator returns 12; a junk token is `InvalidNextToken` |
| SSM-5 | P1 | 49 model operations answer `InvalidAction` "unknown SSM action", and 90 answer 400 `UnsupportedOperationException`. The ledger and `not-built.md` say the latter. *Corrected 2026-10-11: the target was `UnsupportedOperationException`; decision D5 makes it HTTP 501 `NotImplemented` with `X-Doze-Not-Built: declined` or `deferred` for all 139, through `awshttp.NotBuilt`, and the 139 entries of `testdata/modelsweep.json` move to `notbuilt`.* | `ssm/ssm.go` `ServeHTTP`, `fleetOps`; `ssm/coverage_model_test.go` `Unreached` | run | model; D5 | Coverage test with `Unreached` empty; SDK test on `PutResourcePolicy` asserting 501 and the header; the sweep baseline shrinks by 139 |
| SSM-6 | P1 | `LabelParameterVersion` without `ParameterVersion` labels the latest version and answers `ParameterVersion: 0`. | `ssm/actions.go` `labelParameterVersion` | run | *Corrected 2026-10-11: was `doc`; model: "If no version is specified, the system attaches the label to the latest version"; output "The version of the parameter that has been labeled".* | SDK test asserting the returned version |
| SSM-7 | P1 | `GetParameter` by ARN returns `Selector: ":parameter/p/a"`: the selector is cut at the ARN's last colon. | `ssm/actions.go` `getParameter` | run | model: "Either the version number or the label used to retrieve the parameter value. Specify selectors by using one of the following formats: parameter_name:version, parameter_name:label": a bare ARN has none. Whether the member carries the leading colon (`:1`) is `memory`: the model's format has none. | Handler test for name, `name:1`, `name:label`, ARN, ARN with `:1` |
| SSM-8 | P1 | `PutParameter` with `Overwrite` and `Tags` together succeeds and merges the tags. | `ssm/store.go` `Put` | run | *Corrected 2026-10-11: was `doc` ("can't be used with Overwrite"); the model says no such thing. `Overwrite`: "Overwrite an existing parameter. The default value is false." `Tags`: "To add tags to an existing Systems Manager parameter, use the AddTagsToResource operation." The refusal and its code are `memory`; the test is written and skipped.* | Handler test asserting the refusal and that nothing was written, skipped pending the recording |
| SSM-9 | P1 | Policies are accepted on a Standard parameter, the two notification policy types are stored unread with nothing saying so, an unparsable `Policies` answers `InvalidPolicyTypeException`, two Expiration policies are accepted, and nothing counts policies. Only `Type` and `Timestamp` are parsed; `Version` and unknown types are not checked. Run: `[{"Type":"Bogus"}]` on a Standard parameter, 200. | `ssm/actions.go` `parseExpiration` | run | *Corrected 2026-10-11: was `doc`; model: Standard "can't be configured to use parameter policies"; `InvalidPolicyTypeException` "The policy type isn't supported. Parameter Store supports the following policy types: Expiration, ExpirationNotification, and NoChangeNotification" (so unparsable JSON is not that code: wrong code); `IncompatiblePolicyException` "You can't, for example, specify two Expiration policies"; `PoliciesLimitExceededException` "The maximum is 10"; `InvalidPolicyAttributeException`; "All existing policies are preserved until you send new policies or an empty policy".* | Handler test: policy on Standard; unknown type; missing Version; two Expiration; eleven policies; `[]` clears |
| SSM-10 | P1 | Reads omit members the model returns. No read returns `Policies`, `AllowedPattern` or `LastModifiedUser`; history also omits `Tier` and `KeyId`. An Expiration set here cannot be read back. | `ssm/actions.go` `describeParameters` (`meta`), `getParameterHistory` (`histEntry`) | run | model: `ParameterMetadata` and `ParameterHistory` shapes; `ParameterInlinePolicy` {`PolicyText`, `PolicyType`, `PolicyStatus` Pending / InProgress / Finished / Failed}. `LastModifiedUser` is "Amazon Resource Name (ARN) of the Amazon Web Services user who last changed the parameter"; the console records no caller (BRIEF), so the value is a decision: the account root ARN. | SDK test: put with every setting, assert each comes back from both reads |
| SSM-11 | P1 | Eleven labels attach to one version, and `UnlabelParameterVersion` on a version that does not exist answers success with nothing removed. | `ssm/store.go` `Label`, `Unlabel` | run | *Corrected 2026-10-11: was `doc` and `memory`; model: "A version of a parameter can have a maximum of 10 labels", error `ParameterVersionLabelLimitExceeded` ("A parameter version can have a maximum of ten labels"); `UnlabelParameterVersion` lists `ParameterVersionNotFound`.* | Handler tests for the eleventh label and for version 9 of a one-version parameter |
| SSM-12 | P1 | A name deeper than 15 levels answers `ValidationException` with the name-rule text, and **versions are never trimmed** (history grows without bound). *Corrected 2026-10-11: was "There is no limit on versions… 100 versions, `ParameterMaxVersionLimitExceeded`; a 101st version" refused. That was wrong, the SQS-16 shape.* | `ssm/store.go` `validParameterName`, `Put` (`version = len(Versions)+1` also assumes no version was ever dropped) | run (depth), read (versions) | model: `HierarchyLevelLimitExceededException` "A hierarchy can have a maximum of 15 levels"; `ParameterMaxVersionLimitExceeded`: "Parameter Store retains the 100 most recently created versions of a parameter. After this number of versions has been created, Parameter Store deletes the oldest version when a new one is created. However, if the oldest version has a label attached to it, Parameter Store won't delete the version and instead presents this error message". **The 101st version succeeds and the oldest goes; the refusal is only when the oldest holds a label.** | Handler test on a 16-level name asserting the code; 101 puts leave 100 versions and v1 gone; the 101st with a label on v1 is refused and nothing is written; version numbers keep rising |
| SSM-13 | P1 | `GetParameters` and `DeleteParameters` take 11 names (and an empty list); `LabelParameterVersion` and `UnlabelParameterVersion` take 11 labels. Run: 11 names answer 200 with all 11 in `InvalidParameters`. | `ssm/actions.go` `getParameters`, `deleteParameters`; no list-length row in `ssm/validate.go` | run | *Corrected 2026-10-11: was "doc… whether the model states it… not established"; model: `ParameterNameList` length 1 to 10 (Get, Delete), `ParameterLabelList` 1 to 10 (Label, Unlabel), `ParameterStringFilterValueList` and `ParametersFilterValueList` 1 to 50 (three operations). `dzaudit list ssm` prints them; `modelcheck` has no list-length kind and `validate.go` predates B8.* **Rides with SSM-43.** | Rejection-parity cases for 0 and 11 names, 11 labels, 51 filter values |
| SSM-14 | P1 | An empty `Value` is stored. | `ssm/actions.go` `putParameter` (checks presence only) | run | memory: AWS requires a non-empty value; the model's `PSParameterValue` has no `@length`, so there is nothing to derive it from | Skipped test pending the recording |
| SSM-15 | P1 | A StringList is not looked at: `no commas at all` and `a,,b` are stored. | `ssm/store.go` `Put` | run | memory for the refusal; model for the shape (SSM-49). Model: "Items in a StringList must be separated by a comma (,). You can't use other punctuation or special character to escape items in the list." | Skipped test pending the recording |
| SSM-16 | P1 | `DataType` takes any string. `aws:ec2:image` with a value that is not an AMI id is stored. | `ssm/store.go` `Put` | run | *Corrected 2026-10-11: was `doc`; model: "The following data type values are supported: text, aws:ec2:image, aws:ssm:integration"; "The data type for a String parameter"; for `aws:ec2:image` "a successful HTTP 200 response does not guarantee that your parameter was successfully created", validated asynchronously (L7: immediate here, difference listed). The code for an unsupported value is not named.* | Decide (section 10): the three values accepted, others refused, code skipped; then a test either way |
| SSM-17 | P2 | **Closed by foundation A15 (f797c16), not by this stage.** Saving a SecureString from Edit swapped in a detail pane showing the value in plaintext with no Reveal and no badge. *Review 2026-10-11: rendered after a save; masked, badge present, View active; `paramDetailView` builds the page and every fragment; `internal/console/fragments_test.go` holds `/app/secret`.* Label and unlabel go through the same builder. | `internal/console/handlers_sec.go` `paramDetailView` | run (render) | n/a | Exists: `fragments_test.go`. Add the Label and Unlabel cases there |
| SSM-18 | P2 | "Diff vs current" on a SecureString prints both values in cleartext; the View tab's Reveal gate does not apply. *Confirmed by render 2026-10-11 (both passwords visible under Versions).* | `handlers_sec.go` `ssmDiff`; `client_sec.go` `ParameterHistory` (`WithDecryption: true`) | run | n/a | Browser test: diff a SecureString, assert masked until revealed |
| SSM-19 | P2 | With no parameters the list pane is blank. The empty-state block hangs off the inner `range .Rows`, which is never empty. *Confirmed by render (Ssm-01).* | `internal/console/templates/ssm.html` `ssm_lp` | run | n/a | Render test: `ssm_lp` with an empty list contains the `lp_empty` copy |
| SSM-20 | P2 | The console cannot do what the API can: no Description, Tier, KeyId, Expiration or tags at create; Versions shows no value or time though both are fetched; `InvalidLabels` and `RemovedLabels` are discarded, so once SSM-3 is fixed a dropped label would still toast "applied". | `templates/create.html` `ssm_create`; `templates/workspace.html`; `client_sec.go` `PutParameter`, `LabelParameter`, `UnlabelParameter` | read | n/a | Handler tests for each new field; a browser test attaching `aws-x` after SSM-3 |
| SSM-21 | P2 | The list is one `DescribeParameters` call with `MaxResults: 50` and no token. Correct only because of SSM-4; fixing SSM-4 caps the list at 50. | `client_sec.go` `ListParameters` | read | n/a | Console test with 60 parameters. **Rides with SSM-43.** |
| SSM-22 | P3 | `ParameterAlreadyExists` reads "pass Overwrite to update it". The create form has no such control; the way to change a parameter is Edit. | `ssm/store.go` `Put`; `ssm_create` | run | memory: AWS's wording differs (the model's text is "The parameter already exists. You can't create duplicate parameters.") | Browser test: the refusal offers the existing parameter |
| SSM-23 | P3 | A diff for a version that does not exist renders the whole current value as added. The diff matches lines as a set, so a moved line shows unchanged. | `handlers_sec.go` `ssmDiff`; `valueworkspace.go` `lineDiff` | run | n/a | Handler test for `v=9`; a unit test on a reordered value |
| SSM-24 | P3 | "Differences from AWS" in the ledger omits: paging, value size, label rules, AllowedPattern, DataType, the limits in SSM-11 to 13, public parameters under `/aws/service` (answer `ParameterNotFound`), the resource-policy operations, and (new) the 30 seconds, `TooManyUpdates`, throttling, `aws:ec2:image` validation, `kms:Decrypt`, the notification policies. | `docs/SUPPORT.md`, SSM section | read | n/a | The ledger test, after the contract is rewritten |

**Found by the pre-stage review (2026-10-11).** The first pass was written from
memory; these rows come from the model's documentation text and from running a
built binary (`doze-aws` built outside the repo, JSON 1.1 over HTTP). Every row
names its direction and a short quote.

| Id | Sev | Direction | What | Where | Evidence | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| SSM-25 | P1 | more forgiving | An ARN names an account and a region and is resolved by its tail alone. `arn:aws:ssm:eu-west-1:999999999999:parameter/lb` returns the local `/lb`, on `GetParameter`, `GetParameters` and `GetParameterHistory`. A hard-coded account works here and fails on deploy (SQS-23). | `ssm/store.go` `Get` (cuts at `:parameter`) | run | model: "For parameters shared with you from another account, you must use the full ARN" | SDK test under a non-default `--account-id` and `--region`: own ARN found, another account's or region's is `ParameterNotFound` |
| SSM-26 | P1 | wrong code | A type change on overwrite answers `ValidationException` "cannot change type of parameter … from String to SecureString". | `ssm/store.go` `Put` | run | model: `PutParameter` errors list `HierarchyTypeMismatchException`: "Parameter Store doesn't support changing a parameter type in a hierarchy. For example, you can't change a parameter from a String type to a SecureString type." The shape's "in a hierarchy" is not read as limiting it to paths; confirmation queued. | Handler test asserting the code and that nothing was written |
| SSM-27 | P1 | more forgiving | An Advanced parameter is overwritten with `Tier: Standard` and the downgrade is stored; `PutParameter` answers the *requested* tier (or `Standard`) rather than the parameter's, so an Advanced parameter overwritten without `Tier` says `Standard`. | `ssm/store.go` `Put`; `ssm/actions.go` `putParameter` (`orDefault(Tier, "Standard")`) | run | model: "you can't revert an advanced parameter to a standard parameter"; response "The tier assigned to the parameter". The refusal's code and message are `memory`. | Handler test: Advanced then Standard refused and unchanged; overwrite without Tier answers `Advanced` |
| SSM-28 | P1 | wrong shape | `Tier: Intelligent-Tiering` is stored and echoed as typed by `PutParameter` and `DescribeParameters`. It is a request-time choice. A 5,000-byte value put as Intelligent-Tiering says `Intelligent-Tiering`. | `ssm/actions.go` `putParameter`; `ssm/store.go` | run | model: "If the request doesn't include any options that require an advanced parameter, the parameter is created in the standard-parameter tier. If one or more options requiring an advanced parameter are included… Advanced": over 4 KB, a policy, or more than 10,000 parameters. The default when no `Tier` is sent is the service setting (SSM-42). | Handler tests: small value Standard, 5,000 bytes Advanced, with a policy Advanced |
| SSM-29 | P1 | more forgiving | A name with a slash and no leading slash (`a/b`) is accepted; the page said "establish… accepted here". | `ssm/store.go` `validParameterName` (`TrimPrefix(name, "/")`) | run | model: "For parameters in a hierarchy, you must include a leading forward slash character (/) when you create or reference a parameter." The code and message are `memory` (`ValidationException` is the model's default for a bad name). | Handler test: `a/b` refused, `a` accepted, `/a/b` accepted |
| SSM-30 | P2 | more forgiving | A 1,100-character name is accepted; the limit a caller can use is 1,011 less the ARN prefix. | `ssm/validate.go` (2048); `ssm/store.go` | run | model: "The reported maximum length of 2048 characters… includes 1037 characters that are reserved for internal use… The maximum length for a parameter name that you specify is 1011 characters. This count… includes the characters in the ARN that precede the name… 45 characters count toward the 1011 maximum for a parameter created in the US East (Ohio) Region". Exact count with a leading slash: `memory`, queued. | Handler test at the boundary under two regions (the prefix length depends on the partition, region and account) |
| SSM-31 | P2 | stricter and more forgiving | A name with a leading or trailing space is refused (AWS trims it); a name with a space inside is refused on write but a read of it (`GetParameter`, `GetParametersByPath` with `/a b`) answers `ParameterNotFound` or 200. | `ssm/store.go` `validParameterName`; reads | run | model (every operation that takes a name): "Parameter names can't contain spaces. The service removes any spaces specified for the beginning or end of a parameter name. If the specified name for a parameter contains spaces between characters, the request fails with a ValidationException error." | Handler test per operation: ` /a ` finds `/a`; `/a b` is `ValidationException` on all |
| SSM-32 | P1 | more forgiving | A value wrapped in `{{ }}` or `{{ssm:/a/b}}` is stored. A template that works here is refused on deploy. | `ssm/store.go` `Put` | run | model (PutParameter.Value): "Parameters can't be referenced or nested in the values of other parameters. You can't include values wrapped in double brackets {{}} or {{ssm:parameter-name}} in a parameter value." Code `memory`. | Handler test: `{{ssm:/a/b}}` and `{{x}}` refused, `{x}` and `a {{ b` accepted (the model's "wrapped" read literally; how much of a value must be wrapped is `memory`) |
| SSM-33 | P2 | wrong shape | `GetParameters` answers in request order and never sets `Selector`; a `name:1` or `name:label` entry has none. | `ssm/actions.go` `getParameters` (`s.view(…, "")`) | run | model: "The results for GetParameters requests are listed in alphabetical order in query responses."; `Selector` as SSM-7 | SDK test: `["/zz","/aa:1"]` returns `/aa` then `/zz`, the first with a selector |
| SSM-34 | P1 | more forgiving, missing | Filters. `GetParametersByPath` ignores `ParameterFilters` altogether. `DescribeParameters` filters `Name` (Equals, BeginsWith) and `Type` only: `Contains` matches nothing, `Label` (not valid there) and `Path`, `KeyId`, `Tier`, `DataType`, `tag:` and the older `Filters` are ignored and everything comes back; `Shared: true` returns the account's own parameters. `InvalidFilterKey`, `InvalidFilterOption` and `InvalidFilterValue` are never answered. | `ssm/actions.go` `applyFilters`, `getParametersByPath` | run | model: "The following Key values are supported for GetParametersByPath: Type, KeyId, and Label. The following Key values aren't supported…: tag, DataType, Name, Path, and Tier"; "For DescribeParameters, all of the listed patterns are valid except Label"; "valid options include Equals and BeginsWith. The Name filter additionally supports the Contains option. (Exception: … Path, valid options include Recursive and OneLevel.)"; "(Exception: … Label … the only valid option is Equals.)"; `Shared`: "Lists parameters that are shared with you"; `InvalidFilterOption`: "Valid options are Equals and BeginsWith. For Path filter, valid options are Recursive and OneLevel." Which code an unsupported key on `GetParametersByPath` gets is `memory` (`InvalidFilterKey` is listed). | Handler tests per key and option on both operations; `Shared` is empty here; an unsupported key and option are refused |
| SSM-35 | P2 | more forgiving | `ssm/validate.go` and `testdata/cases_ssm.json` predate the model snapshot. The filter-key pattern is `^tag:.+\|Name\|Type\|KeyId\|Path\|Label\|Tier\|DataType$` (the group lost: `Namexyz` passes), the list lengths of SSM-13 are absent, and a regenerated `dzaudit cases ssm` differs from the committed file on 21 entries for the 13 operations (14 list-length cases; the key pattern and three enums spelled differently), the file as a whole being 1,752 cases for 152 operations against 100 committed for the dispatched 13. | `ssm/validate.go` (generated), `ssm/testdata/cases_ssm.json` | run | model: `ParameterStringFilterKey` pattern `^(tag:.+\|Name\|Type\|KeyId\|Path\|Label\|Tier\|DataType)$`; `dzaudit list ssm` | Regenerate; `rejection_parity_test.go` replays the new cases; a case for `Namexyz` |
| SSM-36 | P2 | wrong | A parameter whose Expiration has passed and that the janitor has not yet swept: a read says `ParameterNotFound`, `PutParameter` without `Overwrite` says `ParameterAlreadyExists`, and Label, Unlabel, tags and Delete act on it. Up to a minute. | `ssm/store.go` `Put`, `Label`, `Unlabel`, `UpdateTags`, `Tags`, `Delete` (none checks `expired`) | run | model: "When the expiration time is reached, Parameter Store deletes the parameter." | Handler test with the clock past the timestamp and no sweep: create succeeds at v1, tag and label are `ParameterNotFound` |
| SSM-37 | P1 | more forgiving | A 51st tag is accepted (200, run with 51). | `ssm/store.go` `UpdateTags` | run | model (AddTagsToResource): "Most resources can have a maximum of 50 tags", error `TooManyTagsError` "The Targets parameter includes too many tags". On `PutParameter` the list's own cap is 1,000 (model) and `TooManyTagsError` is not in its error list: what a 51st tag on a create does is `memory`. | Rejection-parity: 51 tags via `AddTagsToResource` are `TooManyTagsError` and nothing is written; 50 are accepted; a tag overwrite of an existing key does not count |
| SSM-38 | P1 | wrong code | Tag operations on a missing parameter answer `ParameterNotFound`; on a valid resource type that is not served (`Document`) answer `InvalidResourceType` "doze-aws supports tagging for ResourceType Parameter only"; an ARN as `ResourceId` answers `ParameterNotFound`. | `ssm/actions.go` `resourceName`; `ssm/store.go` | run | model: `AddTagsToResource`, `RemoveTagsFromResource`, `ListTagsForResource` list `InvalidResourceId` ("The resource ID isn't valid") and `InvalidResourceType` ("if you are attempting to tag an EC2 instance, the instance must be a registered managed node") and not `ParameterNotFound`; ResourceId: "For the Document and Parameter values, use the name of the resource". A `Document` that does not exist is `InvalidResourceId`. | SDK test per case |
| SSM-39 | P1 | wrong shape | `UnlabelParameterVersion` never returns `InvalidLabels`; a label that was not attached comes back in neither list. `LabelParameterVersion` takes the same label twice. | `ssm/actions.go` `unlabelParameterVersion`; `ssm/store.go` `Unlabel` | run | model: `InvalidLabels` "The labels that aren't attached to the given parameter version"; `RemovedLabels` "A list of all labels deleted from the parameter" | Handler test: unlabel an absent label; `InvalidLabels` carries it and the console says so (A12) |
| SSM-40 | P1 | wrong data | Every history entry carries the parameter's *current* `Description`, `Type` and `DataType`: put v1 with no description and v2 with "second", and v1's entry says "second". After SSM-10 adds `KeyId`, `Tier`, `Policies` and `AllowedPattern` they must be per version. | `ssm/actions.go` `getParameterHistory`; `paramVersion` has `Value`, `Labels`, `Created` only | run | model: `ParameterHistory` carries `Description`, `AllowedPattern`, `KeyId`, `Tier`, `Policies`, `DataType`, `LastModifiedUser` on each entry; "Retrieves the history of all changes to a parameter". That AWS keeps each as it was at that version is read from the shape; `memory` as a statement. | SDK test: change description, then history shows each version's own |
| SSM-41 | P2 | more forgiving | The per-account, per-region counts are not enforced; `ParameterLimitExceeded` is never answered. | `ssm/store.go` `Put` | read | model: "a maximum of 10,000 standard parameters for each Region in an Amazon Web Services account… 100,000 advanced"; `ParameterLimitExceeded` (429) "You have exceeded the number of parameters for this Amazon Web Services account". Reproduce with a store of 10,001 before fixing. | Store test with a small injected limit; the real numbers asserted as constants |
| SSM-42 | P2 | missing (decision) | `GetServiceSetting`, `UpdateServiceSetting`, `ResetServiceSetting` answer a legacy refusal. `/ssm/parameter-store/default-parameter-tier` decides the tier of a `PutParameter` without one, and a stack that sets it (Terraform `aws_ssm_service_setting`) fails here. | `ssm/ssm.go` `fleetOps` | run | model: "enter Standard, Advanced, or Intelligent-Tiering"; `/ssm/parameter-store/high-throughput-enabled` "true or false"; `ServiceSettingNotFound` for ids not provisioned. Decide: serve the two Parameter Store ids as F (the other eight as `NotImplemented`), or all ten as S. | If served: set and reset the tier, then a put without `Tier` lands there |
| SSM-43 | P1 | silent loss on fix | Three clients inside this repository page by accident and break when SSM-4 and SSM-13 land, the LAM-6 shape. `console DeleteParameters` sends 50 names a call (the model allows 10); `ParametersByPath` (path query and "Delete everything under") takes the first page with no `NextToken` (10 a page), so a delete-path would delete ten and say so; `ListParameters` takes 50 (SSM-21); `provision` `exportParameters` takes `MaxResults: 50` once and exports the first 50. | `internal/console/client_sec.go` `DeleteParameters`, `ParametersByPath`, `ListParameters`; `internal/provision/svc_ssm.go` `exportParameters` | read | model: `ParameterNameList` 1 to 10; `GetParametersByPath` 1 to 10, `DescribeParameters` 1 to 50 | The three client changes go in the same commit as SSM-4 and SSM-13; tests: 25 parameters under a path are listed and deleted whole; an export of 60 has 60 |
| SSM-44 | P3 | more forgiving | A String with a `KeyId` stores and returns it from `DescribeParameters`. | `ssm/store.go` `Put` | run | model: `KeyId` output "Applies to SecureString parameters only" | Handler test: a String's `KeyId` is not returned |
| SSM-45 | P2 | wrong shape | The masked View of a JSON SecureString lists its keys in alphabetical order (`password, port, username` for a value stored `username, password, port`); a Reveal shows the original. The board shows the original order. Shared with Secrets Manager. | `internal/console/valueworkspace.go` `maskedValue` (round-trips through `map[string]any`) | run (render) | n/a | Unit test: masked output keeps the key order of the input |
| SSM-46 | P2 | console | A refused label attach renders the refusal inside the version's table row, to the left of the buttons, and the label input collapses to a sliver. The board has it below the table. | `internal/console/templates/workspace.html` (versions mode), the `errSlot` rung | run (render) | n/a | Browser test: the refusal is below the table and the input keeps its width |
| SSM-47 | P3 | console | The path result panel sits flush under the query panel with no gap. | `ssm_path_result`, `ssm_home` | run (render) | n/a | Browser test with a screenshot comparison, or a layout assertion |
| SSM-48 | P1 | wrong (enforce only) | IAM resource resolution reads `Name` only. `GetParametersByPath` (`Path`), `GetParameters` and `DeleteParameters` (`Names`), the tag operations (`ResourceId`) and an ARN in `Name` (wrapped into `parameter/arn:aws:…`) are not resolved to the parameter's ARN, so an identity policy scoped to `parameter/harbour/*` does not decide them. | `internal/iamguard/resolve.go` (the `ssm` rule) | read | model: input members above. Reproduce under enforce before fixing; the fix is in the IAM stage's file. | An enforce test: Allow on `parameter/harbour/*` and Deny elsewhere over each operation |
| SSM-49 | P2 | wrong shape | A StringList is returned as typed: `a, b ,c` comes back `a, b ,c`. | `ssm/store.go` `Put`, `render` | run | model (Parameter.Value): "If type is StringList, the system returns a comma-separated string with no spaces between commas in the Value field." Whether the spaces are removed on write or on read is `memory`; either way a read has none. | SDK test: put `a, b ,c`, get `a,b,c` |

Counts: **49 findings** (24 from the first pass, 25 found by the review), of
which SSM-17 is already closed by foundation A15. By severity: P0 4, P1 25,
P2 15 (14 open), P3 5. By direction, the 25 new: more forgiving 9, both ways 2,
wrong code 2, wrong shape 5, wrong data or behaviour 3, missing 1, console or
coupling 3.

Owned by the foundation, seen here: **A2** (the name-rule refusal is told "the
rule comes from AWS's own service model"; *Review 2026-10-11: the foundation's
why-line now reads "doze-aws refused the request: it breaks a rule of the
service", and the board's own sentence was wrong in the other direction; the
model's prose does state the name rules, so neither "from the model" nor "the
model does not carry them" is right: it states them in text, not as a
constraint*), **A4** (the key/value editor keeps types: **landed**; the board
Ssm-08 was stale and is corrected), **A5** (tag Save: **landed**; Ssm-13 stale
and corrected), **A12** ("Label removed" for a label that was not there; SSM-39
is the API half).

**A7 needs correcting for this service.** It lists Parameter Store among the
services that open the first resource. It does not: with parameters present,
`ssm_home` renders the "Query a path" pane. The `Unsel` line in `copy.go` is
unreachable all the same, for that different reason.

## 5. What to check

Beyond the common criteria. One protocol, so each line is checked once.

**Names**
- [ ] 1 to 2048 (the trait) and 1,011 less the ARN prefix (SSM-30); each level
      letters, digits, `.`, `-`, `_`; an empty level (`//`, a trailing slash)
      refused; a hierarchy begins with `/` (SSM-29); leading and trailing
      spaces removed, interior spaces `ValidationException` on every operation
      that takes a name (SSM-31).
- [ ] `aws` and `ssm`, any case: refused as the start of a plain name and as the
      first level of a path. Establish `/awsome/x` and `/x/aws-inside`
      (the conformance scenario already asks; record it).
- [ ] 15 levels accepted, 16 refused with `HierarchyLevelLimitExceededException`
      (SSM-12).
- [ ] ~~Establish whether a name with a slash but no leading slash (`a/b`) is
      legal. It is accepted here.~~ *Corrected 2026-10-11: model: "you must
      include a leading forward slash character (/)"; refused (SSM-29).*
- [ ] Reads are not held to the reserved prefixes; `/aws/service/...` answers
      `ParameterNotFound` and the ledger says public parameters are not served.
      Reads are held to the space rule (SSM-31).

**Put**
- [ ] Create without `Type` refused; overwrite without `Type` keeps it; overwrite
      with another `Type`: `HierarchyTypeMismatchException` (SSM-26).
- [ ] Existing name without `Overwrite`: `ParameterAlreadyExists`.
- [ ] Each overwrite adds exactly one version, including an unchanged value;
      the 101st version succeeds and the oldest is dropped, refused only when
      the oldest has a label (SSM-12). *Corrected 2026-10-11: was "the 101st
      version (SSM-12)" as a refusal.*
- [ ] Value: empty (SSM-14); size per tier (SSM-2); a StringList (SSM-15,
      SSM-49); `{{ }}` (SSM-32).
- [ ] `AllowedPattern` enforced on create and on every overwrite (SSM-1).
- [ ] `Tags` with `Overwrite` refused (SSM-8, `memory`, skipped). Tags on create
      are stored.
- [ ] Overwrite with no `Description`, `KeyId` or `Tier` keeps the stored ones;
      `Policies` are kept until new ones or an empty policy (model). How each of
      the first three is cleared on AWS is `memory`, queued.
- [ ] `Tier`: no downgrade (SSM-27); Intelligent-Tiering resolves to Standard or
      Advanced (SSM-28); the response says the parameter's tier.

**SecureString**
- [ ] Without `WithDecryption`, every read returns ciphertext, never plaintext:
      GetParameter, GetParameters, GetParametersByPath, GetParameterHistory.
      *Review: the model says only that the flag returns "decrypted values";
      "ciphertext otherwise" is `memory`.*
- [ ] `KeyId` default, a given `KeyId`, and that a missing `ssm.key` file or a
      corrupt value answers a 500 and not an empty string.
- [ ] Overwrite without `Type` stays sealed (there is a test; keep it).

**Reads**
- [ ] Selectors: `name`, `name:N`, `name:label`, ARN, ARN with a selector; a
      missing version and a missing label are `ParameterVersionNotFound`; the
      `Selector` member is right for each (SSM-7); an ARN of another account or
      region is not found (SSM-25).
- [ ] `GetParameters`: 1 to 10 names (SSM-13), selectors inside the list,
      duplicates, alphabetical order, `InvalidParameters` (SSM-33).
- [ ] `GetParametersByPath`: the path itself is not under itself; `Recursive`
      off returns one level; `/` as the path; paging with `MaxResults` 1 to 10
      and a stale token (SSM-4); `ParameterFilters` Type, KeyId, Label only,
      Label Equals only (SSM-34).
- [ ] `DescribeParameters`: paging; each `ParameterFilters` key the model
      allows (`Name`, `Type`, `KeyId`, `Path`, `Tier`, `DataType`, `tag:`)
      either works or is refused, not ignored; `Label` is not valid here and is
      refused; `Contains` on Name; `Recursive` and `OneLevel` on Path; the older
      `Filters` (Name, Type, KeyId); `Shared`. *Corrected 2026-10-11: was
      "`Path`, `Label`, `Tier`, `DataType`" all allowed.*
- [ ] `GetParameterHistory`: order, paging, every member in SSM-10, each entry's
      own description (SSM-40).

**Labels**
- [ ] The rules in SSM-3, the limit in SSM-11 (10 a version), 1 to 100
      characters, at most 10 labels in one call.
- [ ] A label moves when attached elsewhere; `ParameterVersion` omitted means
      latest and the response says which (SSM-6).
- [ ] Unlabel: absent label (`InvalidLabels`), absent version
      (`ParameterVersionNotFound`), and `ParameterVersion` required (SSM-39).

**Delete**
- [ ] `DeleteParameter` on a missing name is `ParameterNotFound`.
- [ ] `DeleteParameters`: partial result, 1 to 10 names (SSM-13).
- [ ] Deleting and re-creating starts again at version 1 with no labels or tags;
      AWS asks for 30 seconds in between, doze-aws does not (listed).

**Tags**
- [ ] Key 1 to 128, value 0 to 256, the character pattern (model, enforced).
- [ ] At most 50; the 51st is `TooManyTagsError` (SSM-37). The reserved `aws:`
      key prefix is not in the model: `memory`, queued, not enforced.
      `aws:cloudformation:stack` is accepted today.
- [ ] A missing parameter is `InvalidResourceId` (SSM-38). *Corrected
      2026-10-11: was "AWS's code here is not established; it may be
      InvalidResourceId".*

**Policies and lifecycle**
- [ ] Expiration: unreadable from the timestamp on every read path, gone from
      every list, deleted by the janitor, not creatable-over (SSM-36); a
      timestamp in the past at put time is `memory`.
- [ ] Policies need Advanced (SSM-9); three types; at most 10; two Expiration is
      `IncompatiblePolicyException`.
- [ ] Everything above survives a restart: versions, labels, tags, sealed
      values, Expiration, and the janitor resuming.

**Declined**
- [ ] All 139 undispatched operations answer `NotImplemented` 501 with
      `X-Doze-Not-Built`, and `not-built.md` lists each family with its reason.
      *Corrected 2026-10-11: was "one code, the one a real SDK maps to a typed
      error"; D5.*

## 6. Console

The design's "Parameter Store" page: 16 boards. Templates: `templates/ssm.html`,
the `ssm_create` block in `create.html`, the shared `value_workspace` and
`value_diff` in `workspace.html`, `tag_editor` and `fail_inline` in `panes.html`.
Handlers: the `ssm*` functions in `handlers_sec.go`, `tagsView` and `tagsSave`
in `handlers_tags.go`, routes in `routes_config.go`, grouping in `ssmtree.go`.
*Review 2026-10-11: a settings record (`ssm/settings.go`, registered like
`sqs/settings.go`) does not exist and the kit's `setting_field` is not used on
any Parameter Store form.*

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Ssm-Home | Parameters by parent path, path query, nothing open | `ssm_home`, `ssm_lp`; `ssmParams` | exists as the path box only; title row, facts strip and path chips are **new** |
| Ssm-01-None | No parameters yet | `ssm_home`, `empty_detail`, `lp_empty` | detail exists; the list-pane half is broken (SSM-19) |
| Ssm-02-Create | Create a String | `ssm_create`; `ssmCreate` | form exists; teaching lines, three type cards and "Where it will land" are **new** |
| Ssm-03-Create-Secure | SecureString, Advanced open | `ssm_create`; `ssmCreate` | type exists; the whole Advanced block is **new** (SSM-20) |
| Ssm-04-Create-Refused | Name rule refused | `fail_inline` | exists (why-line: foundation A2); position differs |
| Ssm-05-Create-Exists | Name already exists | `fail_inline` | refusal exists; the panel naming the existing parameter is **new** (SSM-22) |
| Ssm-06-Param | View a String | `ssm_param_detail`, `value_workspace` view; `ssmParam` | exists; "Also on record" and "Read it from code" are **new** |
| Ssm-07-Param-Secure | View a SecureString, masked | same, with `masked` | exists; KMS key, Tier and the read strip are **new** |
| Ssm-08-Edit | Edit, save as the next version | `value_workspace` edit; `ssmPut` | exists; "What saving does" and "What changes" are **new**. A4 has landed: the editor keeps types |
| Ssm-09-Edit-Gone | Save when the parameter has gone | `fail_inline` | exists |
| Ssm-10-Versions | Versions and labels | `value_workspace` versions; `ssmLabel`, `ssmUnlabel` | exists; Value and Modified columns and "Who reads which version" are **new** |
| Ssm-11-Diff | Diff against current | `value_diff`; `ssmDiff` | exists |
| Ssm-12-Label-Refused | A label refused | `fail_inline` | exists; the placement is a defect (SSM-46) |
| Ssm-13-Tags | Tags as a draft, one refused | `tag_editor`; `tagsSave` | exists (foundation A5, landed) |
| Ssm-14-Path | Path query result and its other answers | `ssm_path_result`; `ssmPath`, `ssmDeletePath` | exists |
| Ssm-15-Confirms | Delete one, delete under a path | `hx-confirm` in `ssm_param_detail` and `ssm_path_result`; `ssmDelete`, `ssmDeletePath` | exists; no bodies, wrong verbs (6a) |

There is no Denied board, on purpose: see section 1.

**New items, and what each is built from**

| Item | Built from |
|---|---|
| Facts strip on Home (count, SecureString count, paths, last change) | The `DescribeParameters` result `ListParameters` already holds, and `ssmGroups` |
| Path chips on Home | `ssmGroups`' `Dir` values; each submits the existing path form |
| Teaching lines under Name, Type, Value | The settings record in `ssm/settings.go` (foundation B4): the model's lengths and enums, the hand-derived name rules from the prose (each quoted in the record), the tier table in section 2 |
| "Where it will land" | The typed name, `splitParamPath`, the parameter list, and the instance's account and region for the ARN |
| Advanced: Description, Tier, KMS key, Expires at | `PutParameter` already accepts all four; `client_sec.go` `PutParameter` and `ssmCreate` must send them. "Expires at" builds one Expiration policy; a Tier of Standard with an Expires-at is the neighbour check (policies need Advanced) |
| The parameter that has this name | The typed name and `GetParameter` |
| "Also on record": Tier, Description, KMS key | `DescribeParameters` returns them; `Parameter` in `client_sec.go` does not decode them |
| "Also on record": Labels, and "Who reads which version" | `ParameterHistory`'s `Labels`, already fetched |
| Read it from code | The name and type; the selector forms in `store.Get` |
| Value and Modified on Versions | `ParameterHistory`, already fetched, masked for a SecureString (and SSM-18) |
| What changes | The form against the current value, one row per touched key, typed (A4) |

**Where the teaching facts come from**

| Line | Source |
|---|---|
| Name: 1 to 2048 | `ssm/validate.go` (model trait); the 1,011 is model prose (SSM-30) |
| Name: levels, characters, reserved words, leading slash | `ssm/store.go` `validParameterName`. *Corrected 2026-10-11: was "hand-derived, `doc`"; model prose, quoted in section 2* |
| Type is fixed once created | `ssm/store.go` `Put`; model `HierarchyTypeMismatchException` |
| StringList commas are not checked | SSM-15, SSM-49; changes if those findings are fixed |
| "stores any length" against AWS's 4 KB and 8 KB | SSM-2; the sentence is rewritten when it is fixed. *Corrected: the 4 KB and 8 KB are `model`* |
| SecureString: sealed with a local key, protects the file only | `docs/SUPPORT.md`, Differences from AWS |
| A read without decryption returns ciphertext | `ssm/store.go` `render` (`memory` for AWS) |
| Tier is cosmetic; KMS key recorded and not used | *Corrected 2026-10-11: Tier sets size and policies (SSM-2, 9, 27); the KMS key is recorded and not used (ledger)* |
| Description 0 to 1024; KMS key 1 to 256 and its characters | `ssm/validate.go` (model) |
| Expiration enforced, swept every minute | `ssm/store.go` `expired`, `SweepExpired`; `ssm/ssm.go` `janitor` |
| A label names one version and moves; up to 10 a version; not starting with a digit, aws, ssm | The ledger; model (LabelParameterVersion) |
| Label 1 to 100 | `ssm/validate.go` (model) |
| Tag key 1 to 128, value 0 to 256, the character set, at most 50 | `ssm/validate.go` (model); model prose for the 50 |
| Diff is against current, by whole lines, as a set | `handlers_sec.go` `ssmDiff`, `lineDiff` |
| A path query is recursive and excludes the path itself | `ssm/store.go` `ByPath` |

**The five teaching lines, per setting** (L11). Each row needs a record
`ssm.<setting>` with the first four lines from the record and the fifth live.

| Setting | 1 name and unit | 2 value in words | 3 what it does | 4 range, default, enforced or stored | 5 neighbour check |
|---|---|---|---|---|---|
| Name | Parameter name · path | `= leaf, under /dir` | A path groups related parameters | 1 to 1,011 incl. ARN, 15 levels, charset, not aws/ssm, leading slash · enforced | "`/dir` already holds a, b" (the list) |
| Type | Type | the chosen card | what each stores | fixed once created · enforced | StringList: "commas are the separator, no spaces"; SecureString: "sealed with a local key" |
| Value | Value · bytes | "N bytes of 4,096" | Stored as the text; `{{ }}` refused | 4 KB / 8 KB by tier · enforced (after SSM-2) | size against the tier chosen below; Intelligent-Tiering would choose Advanced |
| Description | Description · characters | n of 1,024 | shown beside the parameter | 0 to 1,024 · enforced | none |
| Tier | Tier | Standard / Advanced / Intelligent | size limit and policies | default Standard (service setting) · enforced | the value's size and a policy against the tier |
| KMS key | KMS key ID | the alias | recorded, not used | 1 to 256, `[a-zA-Z0-9:/_-]` · enforced; **not used** | SecureString only |
| Expires at | Expiration policy | local time and UTC | the parameter stops being readable | needs Advanced · enforced | the tier |
| Label | Label | the label | names one version, moves | 1 to 100, 10 a version, rules · enforced | which readers it moves (`Who reads which version`) |
| Tag key / value | Tag | n of 50 | metadata on the parameter | 1 to 128 / 0 to 256, charset, 50 · enforced | none |

**Statements on the boards not yet backed.** Prove or remove each before the
board ships. *Updated 2026-10-11.*

- Ssm-03: "AWS documents parameter policies for Advanced parameters". *Now
  `model` and stronger: Standard parameters "can't be configured to use
  parameter policies".* "the policy is not returned by reads" describes SSM-10
  and must go when that is fixed.
- Ssm-02, Ssm-03: the 4 KB and 8 KB figures, and "Intelligent-Tiering chooses
  between the two": **now `model`** (PutParameter.Value, Tier).
- Ssm-10, Ssm-12: the sentences about which labels AWS drops into
  `InvalidLabels`: **now `model`**; they describe SSM-3 and are rewritten when
  it is fixed. Ssm-12's "Removing a label with × never refuses" describes
  SSM-39 and A12.
- Ssm-05: "From the CLI the same change is `put-parameter --overwrite`": the
  member is `Overwrite` (model); the flag's spelling is the CLI's (`memory`).
- Ssm-08: *Corrected: the key/value callout described A4 and A4 has landed; the
  board is corrected.*
- Ssm-13: *Corrected: "Tags written before the refused one stay written"
  described A5, which has landed; the board is corrected.*
- Ssm-09: "its Expiration policy passed" is reachable from the console only
  once the Advanced block exists. "DeleteParameter and DeleteParameters take
  effect at once" is true; AWS asks for 30 seconds before the name is reused
  (added to the board).
- Ssm-15: "its 2 versions" needs the history count at confirm time; the confirm
  today has only the name.
- Ssm-04: "These are AWS's own name rules; the service model does not carry
  them… with AWS's wording": **wrong**; corrected on the board. "With AWS's
  wording" is `memory`: the rule text in `store.go` is not the model's.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each board from the dream design
(`console-design/project/Ssm-*.dc.html`, the canvas at claude.ai/artifact/1WSqsF1GUQJdrhscUzzcTw)
and the real page side by side, 1440 by 900, after creating the harbour data
set (10 parameters, a SecureString, labels `prod` and `staging`, two tags)
through the API. Screenshots are in `scratchpad/dcheck/ssm/` (`board-*.png`
beside `real-*.png`; three boards in dark; the parameter page and the create
form at 900 wide).

**Shell, once.** The boards draw a marks-only rail, a "harbour" title and no
header controls; the real shell has the labelled rail, the region and theme
controls and a filter box in the list-pane header. That is the foundation's C
(the shell kit), the same on every service and decided there; it is not
counted below. The boards' underlined tabs against the page's segmented tabs
are the same decision. At 900 wide the page shows the console's own "built for
1024 px" notice and the layout holds (checked on the parameter page and the
create form); dark renders legibly on the three pages compared.

| Board | Verdict | What differs |
|---|---|---|
| Ssm-01 None | differs | The list pane is blank where the board shows "No parameters yet" and "New parameter →" (SSM-19, confirmed). The detail has the copy and the button but not the board's heading line above it (`empty_detail` draws the copy and the button only; the board repeats the sentence as a title). |
| Ssm-Home | differs | The board has a title row (icon, "Parameter Store", a **New parameter** button), a facts strip (Parameters, SecureString, Paths, Last change), the path box, a row of path chips, and a closing sentence about the tree. The page has the path box as a bare "Query a path" label and nothing else. |
| Ssm-02 Create | differs | Two columns in the design (form left, "Where it will land" right with the ARN and the siblings); one column here. Type is three cards with a sentence each in the design, a select here. No teaching lines under Name, Type or Value (the name line with its range, the sibling line, "stores any length"), no "When it will not go through" links. The `cli` preview is behind a button on the page and drawn open on the board (shared behaviour). |
| Ssm-03 Create Secure | differs | As Ssm-02 plus the SecureString explanation lines and the **Advanced** block (Description, Tier, KMS key, Expires at, the generated policy line, the policy-needs-Advanced check). None exists. |
| Ssm-04 Create Refused | differs; design wrong in one sentence | The refusal is above the form on the board and below the action row on the page (off the first screen at 900 high). The board's name-rule sentence is the foundation's generic why-line on the page. The "Other refusals this form can come back with" column is absent. **The design is wrong:** "the service model does not carry them" and "1 – 2 048 characters" (corrected in the working copy). |
| Ssm-05 Create Exists | differs | No "The parameter that has this name" panel (Open it / Edit it), no "Changing a parameter that exists" note; the refusal is below the form. Both missing items are `new` and built from `GetParameter`. |
| Ssm-06 Param | differs | Two columns in the design (value, then the fact table, left; "Read it from code" with the selector table right); one column here with three fact cells. Missing: a sentence beside each fact (Type, Version), "Also on record" (Tier, Description, Labels), "Read it from code". The design's "Tier … Nothing differs between tiers here" is wrong after SSM-2 (corrected). |
| Ssm-07 Param Secure | differs | As Ssm-06 plus the KMS key row and the read strip ("Without --with-decryption the call returns ciphertext"). The masked JSON lists keys alphabetically on the page and in stored order on the board (SSM-45). |
| Ssm-08 Edit | differs | The right column ("What saving does", "Labels do not follow a save", "What changes") is absent. The page's rows carry a type menu (A4) that the board never drew; the board's callout was stale and is corrected. |
| Ssm-09 Edit Gone | differs | The refusal is below the editor (board: above); no "What you typed is kept"; no "How a parameter disappears under you" column; no note that the list is as it was. |
| Ssm-10 Versions | differs | The table lacks the Value and Modified columns; the label input sits stacked under the chips (board: inline after them); no "Labels" panel with the rules and the 10-a-version limit; no "Who reads which version". |
| Ssm-11 Diff | differs | The diff is under the table, full width, with no "Reading the diff" column and no active row; the SecureString diff prints cleartext (SSM-18, confirmed). |
| Ssm-12 Label Refused | differs | The refusal renders inside the row and the input collapses (SSM-46); no right column of other refusals; no "What a label does and does not check" panel. |
| Ssm-13 Tags | differs; design was stale | No "What a tag may hold" column, no "A parameter with no tags" sample. The page's refusal text and redraw are A5's ("Nothing was changed. Refused: writing "bad!key".") and the board said the typed list stays; corrected in the working copy. |
| Ssm-14 Path | differs | No title row or **New parameter**; no "What the box can answer" column with the two refusals it can come back with; the result panel touches the query panel (SSM-47). The page's two refusal sentences are the board's. |
| Ssm-15 Confirms | differs | The parameter confirm is a title with no body and buttons **Cancel / Delete** (board: a body naming the 2 versions, labels and tags, buttons **Keep it / Delete parameter**). The path confirm's body is "Their history goes with them." (board: the names, "queried again as you confirm", **Keep them / Delete 2 parameters**). The ParameterNotFound refusal beside the Delete button is not drawn. |

**Counts:** 16 boards; matches 0, differs 16 (of which "design wrong in a
sentence" 2 and "design was stale" 1), cannot match yet 0. Most differences are
`new` items whose data the console already fetches; build-list items 1 to 3
need an API change first.

**Where the design is wrong** (corrected in the working copy on 2026-10-11:
Ssm-02, 03, 04, 06, 07, 08, 09, 10, 13; the published canvas still carries the
old text until it is republished)
- Ssm-04: "the service model does not carry them" and "with AWS's wording": the
  model states the name rules in its documentation text; the wording of the
  refusal is `memory`. Ssm-02 and Ssm-04 say "1 – 2 048 characters": the model
  says 1,011 including the ARN prefix (the 2,048 is the trait).
- Ssm-06 and Ssm-07: "Nothing differs between tiers here"; Ssm-03: "stored,
  nothing differs by tier · cosmetic". Tier sets the size limit and policies.
- Ssm-08: "Key/value rows save every value as a JSON string" and the quoted
  values in "What changes": A4 landed; rows keep types.
- Ssm-13: "Tags written before the refused one stay written; the list below is
  what you typed": A5 landed; the editor redraws from the parameter. The 50-tag
  limit was missing.
- Ssm-09: no mention of the 30 seconds AWS asks for before a name is reused.
- Ssm-10 and Ssm-12: no "ten labels a version".
- *Not edited, rewritten when the finding closes:* Ssm-02's StringList card
  ("The commas are not checked": SSM-15, SSM-49), Ssm-03's "doze-aws does not
  check it" and "reads do not return it" (SSM-9, SSM-10), Ssm-10 and Ssm-12's
  "doze-aws attaches them all" and "never refuses" (SSM-3, SSM-39), Ssm-02/03's
  "stores any length" (SSM-2).

**Build list, in order**

*Needs an API change first (the board shows a behaviour that is not built):*
1. Ssm-10 "Who reads which version", Ssm-12's refusals: after SSM-3, SSM-11 and
   SSM-39, so the console reports `InvalidLabels` and `RemovedLabels` (SSM-20).
2. Ssm-03 Advanced and Ssm-06 "Also on record": after SSM-10 (members) and
   SSM-40 (per-version history); `client_sec.go` decodes Tier, Description,
   KeyId, Policies.
3. Ssm-14, Home and the list pane: after SSM-4, with SSM-43 in the same commit.

*Console only:*
4. `ssm/settings.go` and the five lines on Name, Type, Value, Description,
   Tier, KMS key, Expires at, Label, Tag (kit `setting_field`).
5. Ssm-02/03: two columns, the three type cards, "Where it will land", the
   Advanced block with its generated policy line and the tier neighbour check.
6. Ssm-06/07: two columns, "Also on record", "Read it from code", the
   sentence per fact; SSM-45 (key order).
7. Ssm-08: the right column; the type menu on the board's rows.
8. Ssm-10/11/12: the Value and Modified columns, inline label form, the Labels
   panel, "Reading the diff", the refusal below the table (SSM-46); SSM-18
   (mask the diff until revealed).
9. Ssm-13: the "What a tag may hold" column with the 50.
10. Ssm-09 and Ssm-05: the right-hand panels; the refusal position on every
    `fail_inline` that sits below the form (shared with other services; a
    foundation decision first).
11. Ssm-Home and Ssm-14: the title row, the facts strip, the chips, the
    answer panel; SSM-47; SSM-19 (the list-pane empty state).
12. Ssm-15: confirm bodies and verbs for the two confirms (the history count at
    confirm time).
13. Re-render all 16 boards beside the pages, light and dark and 900 wide, and
    write the result here before the box in section 10 is ticked.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler | AllowedPattern (SSM-1); size by tier and Intelligent-Tiering (SSM-2, 28); label rules, limit, `InvalidLabels` (SSM-3, 11, 39); label response version (SSM-6); selectors and foreign ARNs (SSM-7, 25); tags with overwrite, skipped (SSM-8); policy tier, types, count, `[]` (SSM-9); depth, retention and numbering (SSM-12); DataType (SSM-16); type change and tier downgrade (SSM-26, 27); names: slash, 1,011, spaces, braces (SSM-29, 30, 31, 32); alphabetical order (SSM-33); filters (SSM-34); expiry window (SSM-36); tags 50 and codes (SSM-37, 38); per-version history (SSM-40); parameter count (SSM-41) |
| SDK v2 | Paging on all three lists with the SDK paginators (SSM-4); every output member (SSM-10); a declined operation's 501 and header (SSM-5); StringList normalised (SSM-49) |
| Rejection parity | List lengths (SSM-13), the regenerated cases (SSM-35); on every operation |
| Coverage | `Unreached` empty in `coverage_model_test.go` (SSM-5) |
| Restart | Versions (after trimming), labels, tags, a sealed value, an Expiration before and after its time, the default-tier setting if served |
| Identity | ARNs and the ARN-form selector under a non-default account and region (SSM-25) |
| Conformance (boto3) | Every row in section 8 |
| Console handler | `ssmPut`, `ssmLabel`, `ssmUnlabel` keep `Secure` and the mode (SSM-17: exists, add label and unlabel); `ssmDiff` on a SecureString and on a missing version (SSM-18, 23); `ssm_lp` empty (SSM-19); the list past 50 and a path over 10 (SSM-21, 43); the new create fields (SSM-20); masked key order (SSM-45) |
| Browser | Edit a SecureString and stay masked; each `new` item; the Exists panel; a dropped label reported as dropped; both confirms' sentences and bodies; the label refusal below the table (SSM-46); a screenshot per board against its design |
| Provision | An export of 60 parameters has 60 (SSM-43) |
| Enforce | Path-scoped identity policy over `GetParametersByPath`, `GetParameters`, `DeleteParameters`, the tag operations (SSM-48) |
| Mutation | `ssm/` re-recorded, then survivors in `store.go` (`validParameterName`, `Put`, `Label`) and `actions.go` (`parseExpiration`, `applyFilters`) |

## 8. Recording queue

Every one of these is `doc` or `memory` today unless it says `model`.

| Claim | Source now | Scenario |
|---|---|---|
| Name rules and the refusal text; `/awsome/x`; `a/b` with no leading slash (model says refuse; the code and message are `memory`) | model, memory | `test_ssm::name_rules` (extends `test_refusals`) |
| 16 levels is `HierarchyLevelLimitExceededException` | **model** | `hierarchy_depth` |
| The longest name: 1,011 including the ARN prefix, with and without a leading slash, under two regions | model, memory (the exact count) | `name_length` |
| A name with leading or trailing spaces is trimmed | **model** | `name_trim` |
| Value over 4 KB on Standard, over 8 KB on Advanced; whether KB is bytes; the code | model, memory | `value_size_by_tier` |
| A value wrapped in `{{ }}` | model, memory (the code) | `value_braces` |
| An empty value is refused | memory | `empty_value` |
| A malformed StringList; normalisation of spaces | memory, model (read form) | `string_list_shape` |
| `AllowedPattern` mismatch and its code (`ParameterPatternMismatchException` or `InvalidAllowedPatternException`); a pattern with lookahead | memory | `allowed_pattern` |
| `Tags` with `Overwrite` and its code | memory | `tags_with_overwrite` |
| A type change on overwrite: `HierarchyTypeMismatchException` | model (the list), memory (the confirmation) | `change_type_on_overwrite` (exists as an outcome) |
| A tier downgrade and its code | model, memory | `tier_downgrade` |
| The 101st version drops the oldest; refused only when the oldest has a label | **model** | `version_retention` |
| A policy on a Standard parameter; an unknown policy type; two Expiration; eleven policies | **model**, code memory | `policy_needs_advanced` |
| What `Intelligent-Tiering` returns as `Tier` (Standard or Advanced by size and policy) | **model** | `intelligent_tiering_resolves` |
| How Description and KeyId are cleared; `Policies` kept until new or empty | memory; **model** | `clear_settings` |
| Label rules, `InvalidLabels` and whether valid labels in the same call are attached; the eleventh label | **model**, memory (partial attach) | `label_rules`, `label_limit` |
| `LabelParameterVersion` returns the version it labelled | **model** | `label_latest_version` |
| Unlabel on a missing version; on an absent label | **model** | `unlabel_absent_version`, `unlabel_absent_label` |
| `Selector` for each read form, ARN included, and in `GetParameters` | memory | `selectors` |
| `GetParameters` returns alphabetical order | **model** | `get_parameters_order` |
| Default and maximum page sizes; a stale or foreign `NextToken` | model (maxima), memory (defaults) | `paging` (extends `test_paths_and_bulk_reads`) |
| Filter keys, options and the three codes on both operations; an unsupported key on `GetParametersByPath`; `Shared` | model, memory (the code of an unsupported key) | `filters` |
| 11 names in GetParameters and DeleteParameters; 11 labels | **model** | `names_limit`, `labels_limit` |
| `DataType` `aws:ec2:image` with a non-AMI value; an unsupported data type's code | model, memory | `data_type_image` |
| Tag limit 50 and `TooManyTagsError`; an `aws:` key; tags on a missing parameter (`InvalidResourceId`); 51 tags on a create | **model**; memory | `tag_limits` |
| The code for a value over its tier's size | memory | `value_too_large_code` |
| The code and status of `ParameterNotFound` (the model's `awsQueryError` says 404; the JSON status is 400 here) | memory | `parameter_not_found_status` |
| 10,001 standard parameters and `ParameterLimitExceeded` | **model**, expensive | `parameter_limit` (skipped by default) |
| The code for an operation an account cannot use (superseded by D5 for not-built; kept for `UnsupportedOperationException` on the two operations that list it) | memory | `declined_operation_code` |
| The wording of `ParameterAlreadyExists` | memory | `put_without_overwrite` (exists) |
| `GetServiceSetting` for the default tier, before and after an update | **model** | `service_setting_default_tier` |

## 9. Owed to integration

| What | With |
|---|---|
| `AWS::SSM::Parameter` through CloudFormation: Tier, Policies, AllowedPattern, DataType and Tags reach the service. *Corrected 2026-10-11: was "`svc_ssm.go` sends Description and Tags only, always with `Overwrite`, which SSM-8 will refuse on update". Read: a create sends Description and Tags and no `Overwrite`; a forced update sends `Overwrite` and Description and no Tags. So SSM-8 breaks nothing in `apply`, and a template whose tags change never updates the parameter's tags.* | CloudFormation |
| `AWS::SSM::Parameter::Value<...>` template parameters and `{{resolve:ssm:...}}` / `ssm-secure` references: whether they resolve is not established (`internal/cfn/resources.go` lists the first as "a parameter reference, not a resource") | CloudFormation |
| A Lambda function reading a parameter at start, under soft and enforce, with the access log naming `ssm:GetParameter` on the parameter's ARN | Lambda, IAM |
| An identity policy scoped to a path (`parameter/harbour/*`) allowing and denying `GetParametersByPath`: the IAM rule reads `Name` only (SSM-48) | IAM |
| `kms:Decrypt` is not required to read a SecureString here; on AWS it is. A listed difference, or a check once KMS's stage is done | KMS, IAM |
| Secrets Manager references through `/aws/reference/secretsmanager/...` | Secrets Manager |
| Parameter change events (`Parameter Store Change`) reaching a rule; `ExpirationNotification` and `NoChangeNotification` events ("initiates an event in Amazon CloudWatch Events") | EventBridge |
| `aws:ec2:image` validation against an AMI | EC2 (not built; listed difference) |
| Sharing a parameter (`PutResourcePolicy`, `DescribeParameters` `Shared`): needs RAM | RAM (not built) |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] SSM-1 to SSM-49 each closed (SSM-17 is closed by A15 and keeps its test),
      with the test that failed first named beside it. *Corrected 2026-10-11:
      was SSM-1 to SSM-24.*
- [ ] All 152 operations have a row or a named family in `SUPPORT.md`; the 139
      declined ones answer 501 `NotImplemented` with `X-Doze-Not-Built`;
      `Unreached` is empty; `modelsweep.json` has no `ssm` entries;
      `not-built.md` gives the resource-policy operations their own reason.
- [ ] Decided and written into `SUPPORT.md`: `DataType` (SSM-16), the
      notification policy types (SSM-9), the regex dialect (SSM-1), public
      parameters under `/aws/service`, whether `kms:Decrypt` is modelled, the
      service settings (SSM-42), `LastModifiedUser` (SSM-10), and the listed
      differences of section 1 (30 seconds, `TooManyUpdates`, throttling,
      `aws:ec2:image`).
- [ ] The per-setting table in section 2 has no "silent" left, and every C
      setting reads as cosmetic in the create form and on the parameter page.
- [ ] Each "not established" on this page is resolved from the model or a
      named doc page, or its recording is queued with the test written and
      skipped: `/awsome/x`; the code of an `AllowedPattern` mismatch; the code
      of an oversized value; how settings are cleared; the empty value; the
      tier-downgrade message; `aws:` tag keys; the selector's colon.
- [ ] Every box in section 5 ticked, or moved to the recording queue.
- [ ] A SecureString's plaintext reaches the page only behind Reveal, on every
      route: view, after save, after label, in the diff, in the version table,
      in the path result.
- [ ] **The fidelity table (6a) exists, was made from rendered boards beside
      rendered pages, and no row says "differs" without a decision beside it:**
      every "differs" is built or recorded as a deliberate deviation with its
      reason; re-rendered after the build, light and dark and at 900 wide.
- [ ] All 16 boards implemented from the foundation's shell, each `new` item
      reading only the data named for it.
- [ ] **Icons kept:** the rail, the list pane, the empty state and the parameter
      header draw the AWS icon; no board's `PS` mark is built.
- [ ] **The five teaching lines on every setting** in the table of section 6
      (Name, Type, Value, Description, Tier, KMS key, Expires at, Label, Tag),
      line 4 from `ssm/settings.go` and saying enforced or stored, line 5 live.
- [ ] **One wire, checked:** the 13 operations are driven on AWS JSON 1.1 by the
      parity baselines and the SDKs; no second protocol exists (model:
      `awsJson1_1` only).
- [ ] **Each asynchronous behaviour decided (L7):** `aws:ec2:image`, Expiration,
      the notification policies and `PolicyStatus` are in the table at the top
      of this page and in `SUPPORT.md`; none is drawn as in progress on a board.
- [ ] **Time rules decided (L8):** the 30 seconds, `TooManyUpdates` and
      throttling are listed differences; no switch.
- [ ] The console's three paging callers (`ListParameters`, `ParametersByPath`,
      `DeleteParameters`) and `provision`'s export follow `NextToken` and the
      10-name limit in the same commit as SSM-4 and SSM-13 (SSM-43).
- [ ] No statement on a Parameter Store screen is in the "not yet backed" list
      in section 6, and every board sentence that describes a finding has been
      rewritten to the fixed behaviour.
- [ ] Both confirms say only what the code does, and have bodies.
- [ ] `e2e/tests/ssm.spec.ts` covers every route under `/ssm` with the route
      gate on; the accessibility sweep is clean on Home, create and all four tabs.
- [ ] Restart test covers versions, labels, tags, a sealed value and Expiration.
- [ ] Conformance: a scenario for every row in section 8 and a claims file
      `conformance/claims/ssm.md`. Zero "answered differently". The unverified
      count is written here: ____.
- [ ] Mutation baseline recorded here: ____ killed, ____ survived, with reasons.
- [ ] Foundation A7 corrected for this service.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 0 | **Regenerate the tables first** (SSM-35); read the model's prose beside each rule | `ssm/validate.go`, `ssm/testdata/cases_ssm.json` (generated); `internal/modelcheck` if the list-length kind is not there |
| 1 | Contract: the three operation groups (13 / 90 / 49 → S with D5), per-setting tiers, decisions | `docs/SUPPORT.md`, `docs/not-built.md`, `testdata/modelsweep.json` |
| 2 | Red tests for SSM-1 to SSM-16 and SSM-25 to SSM-42, SSM-49 | `ssm/*_test.go` (new handler files beside `conformance_fixes_test.go`), `ssm/sdk_test.go`, `ssm/coverage_model_test.go` |
| 3 | Declined operations answer 501 with the header | `ssm/ssm.go` (`fleetOps`, `ServeHTTP`) via `awshttp.NotBuilt` |
| 4 | Paging (`internal/awspage`), the filter engine and the missing output members, per-version fields | `ssm/actions.go`, `ssm/store.go` (`paramVersion` gains its own fields), a new `ssm/filters.go` |
| 5 | Put: names from the prose, AllowedPattern, size by tier, tier rules, tags with overwrite, policies, limits, versions retained | `ssm/store.go`, `ssm/actions.go`, a new `ssm/names.go` and `ssm/policies.go`; tables built lazily (`sync.OnceValue`, as `store.go` does) so idle heap does not move |
| 6 | Labels: rules, limit, response version, `InvalidLabels`; selectors; ARNs; tags | `ssm/store.go`, `ssm/actions.go` |
| 7 | Service settings (SSM-42), if decided | `ssm/settings_api.go` (new), `ssm/ssm.go` |
| 8 | **Console API clients move with the API** (SSM-43) | `internal/console/client_sec.go`, `internal/provision/svc_ssm.go` |
| 9 | Section 5 sweep, parity cases, restart | `ssm/rejection_parity_test.go`, `persistence_test.go` |
| 10 | Conformance scenarios and claims table | `conformance/tests/test_ssm.py`, `conformance/claims/ssm.md` |
| 11 | Console: masking on every route, the empty list, paging the list, key order | `internal/console/handlers_sec.go`, `valueworkspace.go`, `templates/ssm.html`, `templates/workspace.html` |
| 12 | Console: the settings record, the boards and `new` items, by 6a's build list | `ssm/settings.go` (new), `templates/ssm.html`, `templates/create.html` (`ssm_create`), `templates/workspace.html`, `handlers_sec.go`, `client_sec.go`, `ssmtree.go`, `routes_config.go` |
| 13 | Browser tests and a board-beside-page render per board | `e2e/tests/ssm.spec.ts` |
| 14 | IAM resolution for the other members (SSM-48), in the IAM stage's file | `internal/iamguard/resolve.go` |
| 15 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

`workspace.html`, `valueworkspace.go` and `layout.html` are shared with Secrets
Manager. SSM-17, SSM-18, SSM-23 and SSM-45 are fixed here in the SSM handlers;
the Secrets Manager diff and its masked view have the same shape and are that
stage's to check.

**Prior art.** The rules in section 5 come from the model's documentation text
(each quoted above) and from three AWS pages the stage must read for what the
model does not say: the `PutParameter` API reference, "Working with parameter
labels" and "Assigning parameter policies" in the Systems Manager user guide.
No other emulator's behaviour has been read for this page. LocalStack and moto
both serve Parameter Store; reading their SSM providers for label validation,
version retention and filter handling is the first hour of the stage, as a
cross-check, not as a source (they are `memory` for AWS's behaviour).
