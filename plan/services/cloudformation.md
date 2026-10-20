# CloudFormation

Stage 16. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds CloudFormation's own.

## Pre-stage review (2026-10-11)

Done before the stage starts, as [`../02-lessons-from-sqs.md`](../02-lessons-from-sqs.md)
asks. Nothing in the code or tests was changed; the document, and four
sentences on three of this service's design boards, were.

**What was read.** The protocol and the SQS document (sections 4, 6a, 10).
The model, `.audit-models/cloudformation.json`: the documentation of all 23
dispatched operations and of every member of their inputs and outputs, the
`errors` list and `awsQueryError` code and status of each, `required`, `length`,
`range`, `pattern`, enums, and the `paginated` and `waitable` traits (4 operations
carry waiters). The cleaned prose of the 67 refused operations (first
sentences; full text for `DescribeEvents`, `RollbackStack`, `ContinueUpdateRollback`,
`DetectStackDrift`, `SignalResource`). The code: `cloudformation/` whole,
`internal/cfn` mapper and resources, `internal/provision/apply.go`,
`internal/modelcheck` `FromQuery`, the console's `cfn.html`, the `cfn_create`
block, `handlers_cfn.go`, `docs/SUPPORT.md`. Four AWS pages (`doc`): *Template
Parameters syntax*, *Execute a change set*, *Understand CloudFormation quotas*,
*Pseudo parameters*. A binary built from this tree and driven over the wire
(about 60 calls) and the real console driven by Playwright, 33 screenshots
beside the 21 rendered boards (`scratchpad/dcheck/cfn/`).

**Result.** The plan listed 39 findings. This review adds **29**
(CFN-40 to CFN-68: 20 P1, 6 P2, 3 P3, no P0), re-sources or re-aims 19 of the
existing 39 (CFN-4 narrowed, CFN-24 recoded, CFN-22 and CFN-14 updated, the
rest re-tagged), and makes 50 corrections in sections 1 to 10 (each marked
*Corrected 2026-10-11*), plus 18 re-tagged rows in section 8. The total is
**68**: the README's 39 is a floor, as the SQS document said it would be, and
its severity columns become P0 8, P1 38, P2 16, P3 6. Of the 21 boards, **0 match**, 20 differ (one of them,
Cfn-18, also waits on a recording) and 1 (Cfn-20) is a P0 bug drawn as a
design; three boards also carried a fact that was wrong and were corrected.
Section 6a is the table.

**What the model changed in the plan.** Four things that were "not
established" are now: stack ids are tokens (a deleted stack is reached "by
specifying the unique stack ID" and kept "up to 90 days"); an omitted parameter
on an update is the template default, not the previous value (that is what
`UsePreviousValue` is for), so the console's "blank keeps the value" needs it;
`Tags` are propagated to resources, and an update that omits them leaves them
alone; and an IAM resource, a transform, `ResourceTypes` and a second list
element are all things AWS refuses and doze-aws accepts. The request-size limit
(51,200 bytes) is in the prose of `TemplateBody` and in no trait.

### L1 to L12, answered

| | Answer |
|---|---|
| **L1** model docs read | **Findings** CFN-47 (51,200 and 1 MB are prose, not traits; `https://` for `TemplateURL`), CFN-48 (list lengths 50, 5, 5, 200 are traits the table lacks), CFN-44, CFN-43, CFN-52 (default `UPDATE`), CFN-61 ("Default: True" on `DisableRollback`, which contradicts every tool). Every number, range, length, count and name in sections 2 and 4 re-checked; the corrections are marked. |
| **L2** optional parameters | **Findings** CFN-43 (omitted `Tags` "doesn't modify"; omitted `RoleARN` "uses the role previously associated"; omitted `RollbackTriggers` kept), CFN-44 (omitted parameter is the default), CFN-52 (`ChangeSetType` default), CFN-63 (`RetainResources` "for stacks in DELETE_FAILED"), CFN-56 (`TemplateStage` default `Processed`), CFN-57 (`StackName` or `PhysicalResourceId`, "not both"), CFN-46 (the "not both" family). |
| **L3** error codes | Checked the 23 `errors` lists. Held: `AlreadyExistsException` (create), `ChangeSetNotFound` 404, `InvalidChangeSetStatus` 400, `ValidationError` for a missing stack on every other operation. **Never produced:** `InsufficientCapabilitiesException` (CFN-19), `TokenAlreadyExistsException` (CFN-51), `LimitExceededException`, `AlreadyExistsException` on a change set (CFN-53), `ChangeSetNotFound` on `GetTemplate` (CFN-56). CFN-24 is the refusal code for the 67 stubs and is **decided** (D5: 501 `NotImplemented`), so it is a wrong code, not an open question. |
| **L4** direction | Every new row names one. By primary direction, of the 29: 9 more forgiving (list elements, sizes, a duplicate change set, "not both", parameters omitted, `CREATE` over a stack), 8 missing (tokens, tags, notifications, stack policy, `DeleteStack` members, capabilities in the summary, `RetainExceptOnCreate`, protection on create), 6 wrong (an id, an update that drops settings, a template stage, a physical-id lookup, import members, the stub reasons), 4 wrong shape (the resource detail, the deleted record, the change set record, the change set id), 1 wrong message, and **1 stricter** (CFN-59, an unnamed FIFO queue: it breaks a correct template). Seven rows carry a second direction. |
| **L5** wires | One protocol (`awsQuery`), the only one the model declares. **Checked: no `xmlName`, no `xmlFlattened` anywhere in the model**, so every list is `.member.N` and every map is `.entry.N`; the one map is `ResourcesToImport.member.N.ResourceIdentifier`. Findings: CFN-49 (validation sees one element: run, 3 of 4 requests accepted a bogus second `Capabilities`; foundation B13 fixes `FromQuery` once), CFN-41 (`Timestamp` for `LastUpdatedTimestamp`), CFN-55, CFN-58, CFN-42 (response members missing). No test posts all 23 operations as the reference spells them: that test is added (section 7). |
| **L6** tokens | **Findings** CFN-40 (stack id: UUID, account and region ignored; a re-created name answers the old id), CFN-65 (a change set id is `<stack id>/changeSet/<name>`, so a re-created set has the same id), CFN-51 (`ClientRequestToken`, `OperationId`), CFN-25 (`NextToken`). `EventId` is unique per event (held). |
| **L7** asynchronous | **Decided (proposed, needs a yes)**, section 1: immediate, difference listed; none of the 21 boards draws a *live* in-progress state, only trails. The model's waiters and statuses are the evidence for what a tool needs. |
| **L8** latency rules | Checked: the only time-based rules are the 90-day retention of a deleted stack (CFN-42) and the in-progress window (L7). Neither is a refusal, so no `--strict` switch is needed; a deleted record is kept until its name is reused (listed difference). |
| **L9** memory vs disk | Checked: stacks, events (500), change sets and nested bodies are in bbolt and survive a restart. Nothing is process-only today. If L7 is decided "visible window", the in-flight status is the one new in-memory piece; a restart inside it must land terminal (stated in section 1). |
| **L10** boards beside pages | 21 boards rendered (light; dark for 02, 03, 06, 14) and the real page in the same state (light; dark for 02 and 06; 900 wide for 02, 06, 08). **Section 6a.** |
| **L11** five lines | Checked against the boards: the stack name, change-set name and parameter fields carry lines 1 to 4 on the design; line 5 (neighbour check) exists only for the export registry and the retained-resource sentence. The real page has none. The foundation's `setting_field` cannot serve parameters (a template's parameters are the user's), so section 6 keeps the second source. New: the template body has a limit that is **not enforced** today (CFN-47), and the board must say so, not "enforced". |
| **L12** icons | Checked: the design's `CF` mark and the resource-type marks (`SQ`, `DB`, `S3`...) are placeholders; the page keeps `awsIcon "cfn"` in the rail, list header and detail header, and the resource table gets the owning service's icon per row (`Cfn-06`, `Cfn-10`, `Cfn-14` draw one; the real table draws none). Nothing else of the chrome is exempt. |

### Classes of mistake this service is most at risk of

1. **"Accepted, shown as complete, did nothing"** (CFN-1, 2, 3, 4, 23 and the
   new CFN-50 tags, CFN-62 stack policy, CFN-60 `ResourceTypes`). The
   transpiler drops what it does not read; the fix is a property table and a
   test that fails on a read-or-dropped property, not forty point fixes.
2. **A response member the SDK reads and the code never writes**
   (CFN-20, 41, 42, 55, 58). The views are hand-written structs; the model has
   far more members than they carry. A "response carries every required member"
   test per operation is cheap and nothing like it exists.
3. **Defaults and omissions** (CFN-43, 44, 52). Each is "the request left a
   member out"; each was answered with "keep what was there" or "do the
   convenient thing", which is the SQS `GetQueueAttributes` mistake again.
4. **The design drawn from the bug** (Cfn-06, 08, 12, 18, 20). The boards
   copy today's trail and today's `Parameters` panel. Fixing CFN-1, 11, 12, 13,
   17 changes the boards; none of those boards may be built before its finding
   is decided.
5. **Memory presented as AWS.** Of the 14 `doc` tags in the plan's findings
   table, 9 turned out to be in the model, 1 named no page and now names one,
   and 2 were `memory` (CFN-11, CFN-15). Messages (`Stack [x] cannot be deleted while ...`, the
   `ExecuteChangeSet` status text, `No updates are to be performed.`) are
   `memory` and stay in the recording queue; none may be "fixed" to a guess.

### Decisions this stage needs before it starts

| # | Decision | Proposed | Why it cannot be settled here |
|---|---|---|---|
| 1 | Stacks asynchronous? (L7) | **Immediate, difference listed**; revisit with a "visible window" only if a user reports a test that polls for `CREATE_IN_PROGRESS`. Costed in section 1: about 4 more days. | A product trade; the design boards already say "never `CREATE_IN_PROGRESS` here". |
| 2 | Is `AWS::IAM::*` mapped now that IAM is served? | Yes for `Role`, `Policy`, `ManagedPolicy`, `InstanceProfile`, `User`, `Group`, with `Capabilities` enforced (CFN-19), because D6 makes a stack's own calls IAM-evaluated and a role a Lambda assumes has to exist for that to mean anything. | Section 10 already asks; it depends on IAM's stage order (15 before 16: it is). |
| 3 | `Capabilities` enforced by default? | Yes (model: "CloudFormation returns an InsufficientCapabilities error"), **and the console form carries the acknowledgement in the same change** (CFN-36), or the console cannot deploy its own Cfn-03 template. | The tools Srini runs (`sam`, `cdk`, `terraform`) pass them; a raw SDK caller does not. |
| 4 | Page size for the list operations | `DescribeStacks`, `ListStacks`, `ListStackResources`, `DescribeStackEvents`, `ListChangeSets`: AWS pages at "1 MB", which no local test reaches; use `awspage` with a documented local default of 100 and `ListExports` at 100 (model). | The model gives no count except `ListExports`. |
| 5 | `ChangeSetType=IMPORT`, `ResourcesToImport`, `ImportExistingResources` | **Declined, 501 `NotImplemented`** (`doze-aws apply` already adopts what exists). | Silently accepted today (CFN-54). |
| 6 | `NotificationARNs` publish to local SNS | Build it (the topic is local); the message body shape is `memory` and goes to the recording queue. | The model says "publish stack related events"; the format is not in it. |
| 7 | `Tags` propagation | Propagate to every mapped kind whose service has tags; list the kinds that do not. | Which of the 56 kinds take tags is the property table's job. |
| 8 | `ExecuteChangeSet.DisableRollback` "Default: True" | **Record, do not follow**: the model's text contradicts every deploy tool's behaviour (rollback on failure). Keep rollback by default; recording queue. | An AWS documentation oddity. |
| 9 | `RollbackConfiguration` triggers | C (stored, validated: at most 5, 0 to 180 minutes). F is buildable (alarms are local) and is not worth the stage. | |
| 10 | `UpdateTerminationProtection` | F (the ledger says C; the code enforces it). Refused on a nested stack. | |
| 11 | `RollbackStack`, `ContinueUpdateRollback` | F, after CFN-8 and CFN-12 give them a failed stack to act on (about a day each, with tests). Until then 501. | Their stub reasons are false (CFN-67); both are buildable. |

### Corrected in this document

Each correction is marked in place with *Corrected 2026-10-11*. By section: 1 (1),
2 (18), 3 (3), 4 (17), 5 (3), 6 (4), 9 (1), 10 (3): 50 in all. Section 8 is
re-sourced row by row (its header says how). The ones that matter:

- **CFN-24**: "nobody has decided"; D5 decided (501 `NotImplemented`).
- **`AWS::SQS::QueuePolicy`** and "SQS-1 owned elsewhere": built and closed in
  the SQS stage; the property table, CFN-4, CFN-22, section 9 and section 10 said
  otherwise.
- **`DescribeEvents`** "an alias": a different operation (CFN-67); and the stub
  reasons of `RollbackStack` and `ContinueUpdateRollback` are false (they act on
  *failed* stacks, which exist here).
- **StackSets "(17, plus three)"**: 17; the ledger's registry "14": 16, and 3 hooks.
- **"Not established", now established by the model:** `TemplateStage` and
  `ChangeSetName` (change the answer), `EnableTerminationProtection` (a model
  member that is ignored), `RetainResources`, `DeletionMode`,
  `ChangeSetType=IMPORT`, `Tags` (propagated), `AWS::NotificationARNs` (a pseudo
  parameter, refused here), `AWS::Serverless::LayerVersion` (rewritten in
  `sam.go`).
- **Sources:** 14 `doc` tags in the findings table re-read (see "Memory presented
  as AWS"); CFN-37 and CFN-38 had been swapped in the contract table.
- **Counts of tests** in section 3 (54 / 47 / 13, were 50 / 46 / 9).

## 1. Summary

A stack service over one bbolt store (`cloudformation/`), speaking the Query
protocol, in front of two internal packages that do the work and are part of
this stage:

- `internal/cfn` parses a template (JSON or YAML, SAM transform), evaluates
  intrinsics and parameters, classifies every resource type as mapped, ignored
  or refused, and transpiles the mapped ones into a resource graph.
- `internal/provision` applies that graph through the gateway (`Apply`), removes
  it (`Destroy`), reports what already exists (`Existing`), and reads a running
  instance back into a graph (`Export`), which `cfn.Emit` writes as a template.
  `doze-aws apply` and `doze-aws export` use the same code.

Everything is synchronous: a stack is terminal when the call returns. Peers:
every other service, because a stack creates resources in all of them. It goes
last for that reason, and most of what is wrong with it is only visible through
a second service.

**Asynchronous on AWS, decided (L7).** *Added 2026-10-11, from the model.* On
AWS `CreateStack`, `UpdateStack`, `DeleteStack`, `ExecuteChangeSet` and
`RollbackStack` return as soon as the operation *starts*
("After the call completes successfully, the stack creation starts. You can
check the status of the stack through the DescribeStacks operation"), and a
change set goes `CREATE_PENDING`, `CREATE_IN_PROGRESS`, `CREATE_COMPLETE`.
The model's waiters (`StackCreateComplete`, `StackUpdateComplete`,
`StackDeleteComplete`, `ChangeSetCreateComplete`; `minDelay` 30 s) are what the
SDKs and the CLI's `wait` commands are generated from; that a waiter polls once
before it first sleeps is `memory`, and the "returns on its first poll" below
rests on it.

| Operation | On AWS | Here | Difference, listed |
|---|---|---|---|
| CreateStack, UpdateStack, DeleteStack | returns the id; `*_IN_PROGRESS`, events appear over minutes | **Immediate.** The status is terminal when the call returns; the whole trail is already there. | A caller that reads `Outputs` straight after `CreateStack` with no waiter passes here and races on AWS. A test that asserts `CREATE_IN_PROGRESS` fails here. Both listed in `SUPPORT.md`. |
| CreateChangeSet | `CREATE_PENDING`, then `CREATE_COMPLETE` or `FAILED` | **Immediate.** `CREATE_COMPLETE` or `FAILED` on return. | `aws cloudformation wait change-set-create-complete` returns on its first poll. |
| ExecuteChangeSet | stack goes `UPDATE_IN_PROGRESS` (or `CREATE_IN_PROGRESS`); the set `EXECUTE_IN_PROGRESS` | **Immediate**, as above; the set ends `EXECUTE_COMPLETE` or `EXECUTE_FAILED`. | The set is never seen `EXECUTE_IN_PROGRESS`. |
| CancelUpdateStack | acts on an in-flight update | Refused: nothing is in flight. It answers the `ValidationError` AWS gives a stack not in `UPDATE_IN_PROGRESS` (wording `memory`). | Cannot succeed here; says why. |
| RollbackStack, ContinueUpdateRollback | act on a *failed* stack (`CREATE_FAILED`, `UPDATE_FAILED`; `UPDATE_ROLLBACK_FAILED`), not an in-flight one | Candidates for F (decision 11); 501 until then. | |
| DetectStackDrift and its status operations | `DETECTION_IN_PROGRESS` | S, 501. | |

Why immediate. No board draws a live in-progress stack (the trails on
Cfn-08, 17, 18, 19 are the *record* of one; the one non-terminal status drawn,
`REVIEW_IN_PROGRESS`, is a resting state), every tool is satisfied by its first
poll, and the alternative needs a stack operation to run concurrently across
every service plus a rule for what a restart mid-apply leaves. **What the
alternative costs, honestly:** about four more days: run `Apply` in a
background task holding the record in `*_IN_PROGRESS`, spread the trail over a
short window, refuse a second operation while one runs (`UpdateStack` on an
in-progress stack, `InvalidChangeSetStatus` on `ExecuteChangeSet`, both in the
model), give `CancelUpdateStack` something to cancel, and on restart move an
in-flight stack to its failure end (that state is process memory, L9). It is
decision 1 in the pre-stage review; it needs a yes or a no before section 11
starts. The immediate model is what the document assumes below.

The size was L, and **it is larger**: see the estimate. Four things make it so:
56 mapped resource types whose per-property behaviour has never been
tabulated, an apply that converges on create but skips most kinds on update,
21 console boards of which none matches today, and 68 findings (the plan
listed 39).

**Estimate.** *Corrected 2026-10-11: was "L, five or more focused days"; the
README carries 11.* **About 37 focused days, in three parts that can each be
signed off**, and the same calibration as SQS (planned two days, listed 8
findings, closed 29 and rebuilt six boards): the model's own documentation
found 29 more findings here in one reading, and the board comparison is the
largest block.

| Part | Work | Days |
|---|---|---|
| 16a API and provision | Contract and decisions: 1.5. Red tests first: 2.5. Property table and "no silent drop" for 56 kinds (CFN-5, 23): 5. Update convergence per service (CFN-1, 22, 23): 3. Stack lifecycle: refused create, delete, importers, events, ids, tags, tokens, capabilities (CFN-6 to 8, 12, 14, 17, 18, 26, 40 to 46, 49 to 51): 6. Describe shapes, parameters, pagination, sizes and list lengths (CFN-10, 11, 20, 25, 41, 42, 44, 47, 48, 55 to 58): 3. Change sets (CFN-13 to 15, 52 to 55, 65, 68): 2. Parity sweep: 1. Conformance scenarios and claims: 1.5 | 25.5 |
| 16b console | 21 boards, all rebuilt in shape (section 6a build list), the capabilities acknowledgement, parameter teaching lines, confirms with bodies, the `What this deploy will do` panel on an exported classifier: 8. Browser tests, route gate, accessibility sweep: 1 | 9 |
| 16c IAM (D6), close | Control-plane and per-call evaluation (D6): 2. Ledger, changelog, three mutation baselines (background hours): 0.5 | 2.5 |

(The README's 11 includes the two days for D6. The three parts sum to 37; the
table in section 11 is the same figure by step.)
**Actual:** _fill in when done._

## 2. Contract

The model lists 90 operations. 23 are dispatched (`handlers` in
`cloudformation/stacks.go`), 67 are refused by name (`stubActions` in
`cloudformation/service.go`). Counted from the code on 2026-10-10; the ledger
says the same.

| Operations | Now | Target | Note |
|---|---|---|---|
| CreateStack | F | F | CFN-6, 19, 40, 43, 45, 46, 47, 50, 51, 60, 61. `TemplateURL` from the local S3. |
| UpdateStack | F | F | CFN-1, 17, 23, 26, 43, 44, 46, 51. |
| DeleteStack | F | F | CFN-7, 8, 51, 63. *Corrected 2026-10-11: was "`RetainResources` and `DeletionMode`: not established"; model: `RetainResources` "For stacks in the DELETE_FAILED state"; `DeletionMode` `FORCE_DELETE_STACK` "Delete the stack if it's stuck in a DELETE_FAILED state". Both are meaningful only once CFN-8 makes `DELETE_FAILED` reachable; both are silently ignored today.* |
| DescribeStacks | F | F | CFN-10, 11, 20, 25, 40, 42. |
| ListStacks | F | F | CFN-25, 42. |
| DescribeStackEvents | F | F | CFN-12, 17, 18, 25, 40, 51. |
| DescribeStackResource, DescribeStackResources, ListStackResources | F | F | CFN-16, 17, 25, 41, 42, 57. |
| GetTemplate | F | F | *Corrected 2026-10-11: was "`TemplateStage` and `ChangeSetName` are accepted; whether either changes the answer is not established"; model: `ChangeSetName` "the associated template" and `ChangeSetNotFoundException`; `TemplateStage` "By default, CloudFormation specifies Processed". Both change the answer (CFN-56).* |
| GetTemplateSummary | F | F | `ResourceTypes` lists ignored and refused types too (CFN-34). No `Capabilities` (CFN-58). |
| ValidateTemplate | F | F | CFN-9, 58. |
| CreateChangeSet | F | F | CFN-13, 15, 52, 53, 54, 55. *Corrected 2026-10-11: was "`ChangeSetType=IMPORT`, `ResourcesToImport`: not established"; model: `IMPORT` "To create a change set for an import operation"; `ResourcesToImport` "The resources to import into your stack". Decided: declined, 501 (decision 5), and today silently an ordinary change set (CFN-54).* |
| DescribeChangeSet, ListChangeSets | F | F | CFN-20, 25, 55, 65, 68. |
| ExecuteChangeSet | F | F | CFN-14, 51, 61. |
| DeleteChangeSet | F | F | |
| ListExports, ListImports | F | F | CFN-7, 21, 25. |
| SetStackPolicy, GetStackPolicy | C | C | Round-trips what `SetStackPolicy` carries; `StackPolicyBody` on create and update is not stored, and nothing checks an update against it (CFN-62). |
| UpdateTerminationProtection | C | **F** | The ledger says C; `hDeleteStack` enforces it (run). CFN-37. Refused on a nested stack (CFN-45). *Corrected 2026-10-11: was "CFN-38"; the stale-ledger row is CFN-37.* |
| CancelUpdateStack | F (refusal) | F (refusal) | *Corrected 2026-10-11: was S.* Not "not built": the one answer it can give is the one AWS gives a stack that is not `UPDATE_IN_PROGRESS` ("You can cancel only stacks that are in the UPDATE_IN_PROGRESS state"). Wording `memory`. Becomes real only if decision 1 changes. |
| ContinueUpdateRollback, RollbackStack | S | **decide: F** | *Corrected 2026-10-11: the stub reasons are false (CFN-67).* Model: `RollbackStack` "from CREATE_FAILED or UPDATE_FAILED", "This operation will delete a stack if it doesn't contain a last known stable state"; `ContinueUpdateRollback` "from UPDATE_ROLLBACK_FAILED to UPDATE_ROLLBACK_COMPLETE", `ResourcesToSkip` xor `ForceRollback`. Both states exist locally. F if decision 11 says yes; otherwise 501 as the other S rows. |
| DetectStackDrift, DetectStackResourceDrift, DetectStackSetDrift, DescribeStackDriftDetectionStatus, DescribeStackResourceDrifts | S | S | |
| StackSets (17, which includes ListStackInstanceResourceDrifts, ListStackSetAutoDeploymentTargets, DescribeStackInstance) | S | S | *Corrected 2026-10-11: was "17, plus" those three, which made 20; `stubActions` has 17 with the three inside.* |
| Extension registry (16: RegisterType … DescribePublisher) and hooks (3: DescribeChangeSetHooks, GetHookResult, ListHookResults) | S | S | *Corrected 2026-10-11: the ledger says "Extension registry (14 operations)"; `stubActions` has 16 with the one reason "the extension registry is cloud infrastructure", and 3 hooks (CFN-67).* |
| Resource scanning (5), generated templates (6), stack refactoring (5), Organizations access (3) | S | S | |
| EstimateTemplateCost, DescribeAccountLimits, SignalResource, RecordHandlerProgress, DescribeEvents | S | S | `DescribeEvents` is not an alias (CFN-67). |

The counts: 5 + 17 + 3 + 16 + 5 + 6 + 5 + 1 + 1 + 1 + 3 + 1 + 1 (`DescribeEvents`)
+ 1 (`RollbackStack`) + 1 (`ContinueUpdateRollback`) = 67, checked by script
against `stubActions` and the model on 2026-10-11: no operation is in neither
table, none is in both.

Every S row answers `400 ValidationError` with "doze-aws does not implement
<op>: <reason>" (run, `DetectStackDrift`). An action in no table answers
`InvalidAction` (run: `FooBar` → 400 `InvalidAction`). *Corrected 2026-10-11:
was "Whether `ValidationError` is the code the common criterion wants for an
unsupported operation is a decision (CFN-24)". It is decided: D5 (README) is
HTTP 501, code `NotImplemented`, a message naming the operation, and the
`X-Doze-Not-Built` header, built as `awshttp.NotBuilt`. CFN-24 is a wrong code
against a decision, not an open question.*

**Request settings on the F operations**

| Setting | Tier now | Target | To confirm |
|---|---|---|---|
| `TemplateBody` | F | F | "a minimum length of 1 byte and a maximum length of 51,200 bytes" (model, prose only; **not enforced**, CFN-47). With `TemplateURL` too: accepted, and AWS refuses ("but not both"; `ValidateTemplate`: "If both are passed, only TemplateBody is used") (CFN-46). |
| `TemplateURL` | F | F | Three URL shapes (`parseS3URL`). Fetched from the local S3 only. Model: "must start with https://", "(max size: 1 MB)", "URLs from S3 static websites are not supported"; `s3://` is accepted here (CFN-47). |
| `Parameters`, `UsePreviousValue` | F | F | *Corrected 2026-10-11: was "An empty value means 'keep'; `UsePreviousValue` itself is not read. Whether an explicit empty string can be set is not established".* Model: "If you don't specify a key and value for a particular parameter, CloudFormation uses the default value that's specified in your template"; `UsePreviousValue` "During a stack update, use the existing parameter value". An omitted parameter is the **default**, never "keep"; here it keeps (CFN-44). An explicit empty string: `memory`. |
| `UsePreviousTemplate` | F | F | Both a template and `UsePreviousTemplate`: accepted; AWS "only one" (CFN-46). |
| `DisableRollback`, `OnFailure` | F | F | Tested (`rollback_test.go`). Both given: accepted; model "You can specify either DisableRollback or OnFailure, but not both" (CFN-46). `OnFailure` is read on `UpdateStack` too, where it is not a member. `RetainExceptOnCreate` (the request member) is read nowhere (CFN-61). |
| `Capabilities` | C | **F** | *Corrected 2026-10-11: was "F or listed".* Model: IAM resources "require you to specify either the CAPABILITY_IAM or CAPABILITY_NAMED_IAM capability... If you have IAM resources with custom names, you must specify CAPABILITY_NAMED_IAM... CloudFormation returns an InsufficientCapabilities error"; `CAPABILITY_AUTO_EXPAND` for a template with macros "includes the AWS::Include and AWS::Serverless transforms" on `CreateStack` and `UpdateStack` and "doesn't apply to creating change sets"; "Only one of the Capabilities and ResourceType parameters can be specified". Stored and returned today; never required (CFN-19); lost on the next update (CFN-43). |
| `Tags` | C | **F** | *Corrected 2026-10-11: was "not established".* Model: "CloudFormation also propagates these tags to the resources created in the stack"; on update, omitted "doesn't modify the stack's tags", empty "removes all". Stored on the stack, **not** propagated (run: the queue has none, CFN-50) and dropped by an update that omits them (CFN-43). |
| `NotificationARNs` | C | C, decision 6 | Model: "The Amazon SNS topic ARNs to publish stack related events"; at most 5 (not enforced, CFN-48). Nothing is published (CFN-64). Update: "Specify an empty list to remove all notification topics". |
| `RollbackConfiguration` | C | C | Stored and echoed; `MonitoringTimeInMinutes` 0 to 180 enforced; at most 5 triggers is not (CFN-48). "By default, CloudFormation saves the rollback triggers... and applies them to any subsequent update": lost on an update here (CFN-43). |
| `TimeoutInMinutes`, `RoleARN`, `ClientRequestToken`, `ResourceTypes`, `StackPolicyBody`, `StackPolicyURL`, `DeploymentConfig`, `DisableValidation` | C | C, each listed | *Corrected 2026-10-11: was "Stored or ignored".* **Ignored, none stored, none echoed** (run: `RoleARN` and `TimeoutInMinutes` are not in `DescribeStacks` though the model's `Stack` has both; `ClientRequestToken` is not on the events). `RoleARN`: "If you don't specify a value, CloudFormation uses the role that was previously associated" (D6 reads it). CFN-51, 60, 62. |
| `EnableTerminationProtection` on CreateStack | F | F | *Corrected 2026-10-11: was "not established"; model: "Whether to enable termination protection on the specified stack... Termination protection is deactivated on stacks by default".* `readDeployFields` does not read it (run: stack created with `true` describes `false`, CFN-45). |
| `RetainResources`, `DeletionMode` on DeleteStack | F after CFN-8 | F | Model, above. CFN-63. |
| `ChangeSetType`, `ResourcesToImport`, `ImportExistingResources` | S | S (501, decision 5) | *Corrected 2026-10-11: was "not established".* CFN-54. |
| `IncludeNestedStacks`, `OnStackFailure`, `Description`, `DeploymentMode` on CreateChangeSet | C | C, each listed | Ignored today; `Description` is "A description to help you identify this change set" and is not returned (CFN-54, 55). `OnStackFailure` "DELETE... is only valid when the ChangeSetType parameter is set to CREATE". `DeploymentMode=REVERT_DRIFT` is a drift feature: S. |

**Template sections and attributes**

| Part | Tier now | Note |
|---|---|---|
| `Parameters`: `Type`, `Default`, `NoEcho`, `Description` | F | `NoEcho` is not masked (CFN-10; run: `s3cr3t` comes back in clear from `DescribeStacks`). Limits `doc` (quotas page, read): at most 200 parameters, a name of 255 characters, a value of 4,096 bytes; `Description` up to 4000 characters (parameters page). **None enforced** (CFN-47). |
| `AllowedValues`, `MinValue`, `MaxValue`, `MinLength`, `MaxLength`, `AllowedPattern`, `ConstraintDescription` | F | `CheckParameters`; run for two. A pattern RE2 cannot compile is let through. `ConstraintDescription` is documented to replace the message ("Malformed input-Parameter MyParameter must match pattern ..."); whether it does here is not established. A missing value: AWS says `Parameters: [KeyName] must have values` (`doc`), here `parameter "V" has no value and no default` (run, CFN-66). |
| Parameter types: `String`, `Number`, `CommaDelimitedList`, `List<…>` | F | Coerced on both paths. |
| AWS-specific and SSM parameter types (`AWS::EC2::…`, `AWS::SSM::Parameter::Value<…>`) | not established | `ignoredTypes` carries `AWS::SSM::Parameter::Value` as if it were a resource type. |
| `Mappings`, `Conditions` | F | Conditions evaluated to a fixed point. `doc` limits: 200 mappings, 200 attributes each, names of 255 characters; not enforced (CFN-47). |
| `Outputs`, `Export.Name` | F | A conflicting export is refused at the call. `doc`: at most 200 outputs, an output name of 255 characters; not enforced. `Description` 1 to 1,024 (model: `Output.Description`). |
| `Transform: AWS::Serverless-2016-10-31`, `Globals.Function` | F | Any other transform is refused (read). |
| `Rules`, `Metadata` (template level), `Hooks` | not established | Not parsed by `template.go`. |
| `DeletionPolicy: Retain`, `RetainExceptOnCreate` | F | Run for Retain on delete and on rollback. |
| `DeletionPolicy: Snapshot`, `Delete` | not established | Only the two Retain spellings are read. |
| `UpdateReplacePolicy`, `UpdatePolicy`, `CreationPolicy` | not established | Not parsed. |
| `DependsOn` | C | Linted for dangling names; not used for ordering (CFN-22). |
| `Condition` on a resource | F | A false condition is reported as ignored. |

**Intrinsic functions and pseudo parameters**

| Item | Tier now | Note |
|---|---|---|
| `Ref`, `Fn::GetAtt`, `Fn::Sub`, `Fn::Join`, `Fn::Select`, `Fn::Split`, `Fn::FindInMap`, `Fn::If`, `Fn::Equals`, `Fn::And`, `Fn::Or`, `Fn::Not`, `Fn::Base64`, `Fn::ImportValue`, `Fn::ToJsonString`, `Fn::Length`, `Condition` | F | `Scope.call` in `intrinsics.go`. |
| `Fn::GetAZs` | C | Always one zone, `<region>a`. |
| `Fn::Cidr`, `Fn::Transform`, `Fn::ForEach` | S | "unsupported intrinsic"; fails the deploy. `!Transform` is in the short-tag table. |
| `AWS::Region`, `AWS::AccountId`, `AWS::StackName`, `AWS::NoValue` | F | |
| `AWS::Partition`, `AWS::URLSuffix` | C | Constants `aws` and `amazonaws.com`, whatever the instance's suffix. |
| `AWS::StackId` | wrong | Always ends `/local`, not the stack's id (CFN-20). `doc` (pseudo-parameters page): "the ID (ARN) of the stack, such as arn:aws:cloudformation:us-west-2:123456789012:stack/teststack/51af3dc0-da77-11e4-872e-1234567db123". |
| `AWS::NotificationARNs` | **refused** | *Corrected 2026-10-11: was "not established".* Run: `Ref: AWS::NotificationARNs` fails the create, `output Nt: Ref "AWS::NotificationARNs": no such parameter or resource`. `doc`: "the list of ARNs for the Amazon SNS topics that receive stack event notifications"; a list, used with `Fn::Select`. A template that uses it (Auto Scaling) is refused here and accepted by AWS (CFN-64). |
| `Fn::GetAtt` attributes per type | not established | `attributes()` in `resources.go`; no table of which attributes each type answers. |

**Resource types**

`classify` in `internal/cfn/resources.go` puts every type in one of three
outcomes. The property column is from `docs/cloudformation.md` and the mappers;
no test asserts it per property, so each cell is `read` until section 5 is done.
A property not named is dropped without a word (CFN-5).

*Mapped: 56 types (`nameProperty`). The stack owns them; they appear under Resources.*

| Type | What is made | Properties applied (read) |
|---|---|---|
| `AWS::SQS::Queue` | a queue | `QueueName`, `FifoQueue`, `ContentBasedDeduplication`, `VisibilityTimeout`, `DelaySeconds`, `MessageRetentionPeriod`, `ReceiveMessageWaitTimeSeconds`, `MaximumMessageSize`, `RedrivePolicy`, `Tags`. Dropped: `KmsMasterKeyId` (run), and, *added 2026-10-11 from `mapper.go` `queue`*, `DeduplicationScope`, `FifoThroughputLimit` (run: absent from `GetQueueAttributes`), `RedriveAllowPolicy`, `KmsDataKeyReusePeriodSeconds`, `SqsManagedSseEnabled`, `MessageGroupId` n/a. **An unnamed FIFO queue fails the stack** (run: `FIFO queue names must end in .fifo, and only a FIFO queue may use that ending`; AWS names it with the suffix, `memory`, CFN-59). `MaximumMessageSize` passes through to SQS's own range, which is now 1,024 to 1,048,576: nothing in `internal/cfn` or `provision` restates 262,144 (grep, 2026-10-11). |
| `AWS::SQS::QueuePolicy` | a queue's policy | *Corrected 2026-10-11: was "**nothing** (CFN-4)"; closed in the SQS stage (`cloudformation/queue_policy_apply_test.go`, `mapper.go` `queuePolicy`): `PolicyDocument` and `Queues` (by URL or `Ref`); a queue the template does not declare is applied against one that must exist; the export writes it back (`emit_sqs.go`).* The policy is validated by SQS when stored (SQS-8), so a bad document now fails the stack: held by the SQS tests, not by a stack test. |
| `AWS::SNS::Topic` | a topic | `TopicName`, `Subscription`, `Tags`. FIFO and the rest: not established. |
| `AWS::SNS::Subscription` | a subscription | Attached after all resources. Filter policy, raw delivery: not established. |
| `AWS::SNS::TopicPolicy` | **nothing** (CFN-4) | none |
| `AWS::S3::Bucket` | a bucket | versioning, object lock, CORS, lifecycle, website, notifications, `PublicAccessBlockConfiguration`, `OwnershipControls` |
| `AWS::S3::BucketPolicy` | the bucket's policy | `Bucket`, `PolicyDocument` |
| `AWS::DynamoDB::Table`, `::GlobalTable` | a table | key schema, GSIs, LSIs, TTL, deletion protection, `Tags`. Streams, billing mode, PITR: not established. |
| `AWS::Serverless::SimpleTable` | a table | `PrimaryKey`, `TableName`, `Tags` |
| `AWS::Lambda::Function`, `AWS::Serverless::Function` | a function | runtime, handler, code (three spellings), environment, timeout, memory, DLQ, `Layers`, `Tags`; SAM `Events`, `AutoPublishAlias`, `FunctionUrlConfig` |
| `AWS::Lambda::EventSourceMapping` | a trigger | SQS sources. Other sources and the batch settings: not established. |
| `AWS::Lambda::LayerVersion` | a layer version | `Content`, `CompatibleRuntimes`, `Description` |
| `AWS::Lambda::Version`, `::Alias`, `::Url` | a version, an alias, a function URL | as in `docs/cloudformation.md` |
| `AWS::Lambda::Permission` | **nothing** (CFN-4) | none |
| `AWS::Events::Rule` | a rule | pattern, schedule, state, targets, input shaping, API destination targets |
| `AWS::Events::EventBus` | **nothing** (CFN-3) | none |
| `AWS::Events::Connection`, `::ApiDestination` | a connection, a destination | as in `docs/cloudformation.md` |
| `AWS::KMS::Key`, `::Alias` | a key, renamed by its alias | Key policy, rotation, key spec: not established. |
| `AWS::SecretsManager::Secret` | a secret | `Name`, `SecretString`, `GenerateSecretString` as a placeholder. Not updated once it exists (CFN-1). |
| `AWS::SSM::Parameter` | a parameter | `Name`, `Type`, `Value`, `Description`, `Tags`. Not updated once it exists (CFN-1). |
| `AWS::Kinesis::Stream` | **nothing** (CFN-2) | none |
| `AWS::StepFunctions::StateMachine`, `AWS::Serverless::StateMachine` | a state machine | definition, substitutions, type, role, tags, logging. `DefinitionUri` refused. |
| `AWS::StepFunctions::Activity`, `::StateMachineVersion`, `::StateMachineAlias` | each of those | as in `docs/cloudformation.md` |
| `AWS::Logs::LogGroup` | a log group | `LogGroupName`, `RetentionInDays`, `Tags` |
| `AWS::Logs::SubscriptionFilter`, `::MetricFilter` | a filter | as in `docs/cloudformation.md` |
| `AWS::CloudWatch::Alarm`, `::Dashboard` | an alarm, a dashboard | Per property: not established. |
| `AWS::ApiGateway::RestApi`, `AWS::Serverless::Api` | a REST API | routes from functions; SAM `Auth` |
| `AWS::ApiGateway::Resource`, `::Method`, `::Stage`, `::Authorizer`, `::ApiKey`, `::UsagePlan`, `::UsagePlanKey` | the resource tree, stage settings, authorizers, keys and plans | as in `docs/cloudformation.md` |
| `AWS::ApiGateway::Deployment`, `::Account` | nothing of their own | A deployment happens on every apply. Says so in the docs; not in the console. |
| `AWS::ApiGatewayV2::Api`, `AWS::Serverless::HttpApi` | an HTTP API | `ProtocolType` HTTP only, CORS, quick-create `Target` |
| `AWS::ApiGatewayV2::Integration`, `::Route`, `::Stage`, `::Authorizer`, `::Deployment` | each of those | JWT, VPC links, service integrations refused by name |
| `AWS::CloudFormation::Stack` | a child stack record, its resources merged into the parent's graph | `TemplateURL`, `Parameters` |

*Ignored: 13 types (`ignoredTypes`). Accepted, referenceable, not made, and not listed under Resources (CFN-16).*

| Type | Reason given |
|---|---|
| `AWS::IAM::Role` | no IAM evaluation during apply; create the role via the iam service if you need it. *Added 2026-10-11: AWS requires `CAPABILITY_IAM` for it (model, the list of eight types), so a template with a role deploys here and is refused there (CFN-19).* |
| `AWS::IAM::Policy`, `::ManagedPolicy`, `::User`, `::Group`, `::ServiceLinkedRole` | no IAM evaluation during apply. The model's capability list also names `AWS::IAM::AccessKey` and `AWS::IAM::UserToGroupAddition`, which are in neither table: they are *refused* (run: `resource K: doze-aws does not serve IAM (AWS::IAM::AccessKey)`, by the "any other" rule), which is the opposite of the ones listed here and says something false, because IAM is served (CFN-67). Decision 2 settles both. |
| `AWS::IAM::InstanceProfile` | no EC2 locally |
| `AWS::Logs::LogStream` | a stream is created by the first PutLogEvents |
| `AWS::CDK::Metadata` | CDK bookkeeping |
| `AWS::ECR::Repository` | no container registry locally |
| `AWS::CloudFormation::WaitCondition`, `::WaitConditionHandle` | nothing to wait for locally |
| `AWS::SSM::Parameter::Value` | "a parameter reference, not a resource" (it is not a resource type; see above) |

doze-aws serves IAM. Whether `AWS::IAM::Role` and its siblings should now be
mapped onto it is a decision this stage makes (section 10).

*Refused: everything else. The whole template fails; nothing is made.*

| Type | Message |
|---|---|
| `AWS::Serverless::Application` | `resource <id>: nested applications need the Serverless Application Repository (<type>)` |
| any other `AWS::<Service>::…` | `resource <id>: doze-aws does not serve <Service> (<type>)` (run, ECS) |
| anything else | `resource <id>: unsupported resource type (<type>)` |
| `AWS::Serverless::LayerVersion` | *Corrected 2026-10-11: was "handled by the SAM transform according to the docs; where is not established".* Rewritten to `AWS::Lambda::LayerVersion` in `internal/cfn/sam.go` (the SAM pass, line 42), `RetentionPolicy` dropped; held by `transpile_test.go`. So it is mapped, as a SAM type, and is not "refused". |

Only the first refused resource is named.

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests, `cloudformation/` | 54 tests in 27 files *(Corrected 2026-10-11: was 50 in 25; `go test -list` count: the SQS stage added `queue_policy_apply_test.go` and `dlq_order_apply_test.go`)*: service, rollback (7), apply, change-set property diff, export conflict, nested, declared fields, ids, and one apply test per deployment shape (Step Functions, API Gateway authorizers, keys, HTTP APIs, Lambda versioning, CloudWatch, Logs, S3 access, EventBridge HTTP) |
| Package tests, `internal/cfn/` | 47 tests in 11 files *(Corrected 2026-10-11: was 46)*: transpile (15), intrinsics (11), template (7), regression (4), nested, GetAtt hosts, parameters, lint |
| Package tests, `internal/provision/` | 13 tests in 7 files *(Corrected 2026-10-11: was 9 in 5; the SQS stage added `dlq_order_test.go`)*: destroy (5), subset (2), existing, inline, adopt, DLQ order |
| Parity | `rejection_parity_test.go` (2), `coverage_model_test.go`; ledger says 182 of 182 model constraints across 22 of 23 operations, `knownGaps` empty. **Stale** *(2026-10-11)*: `testdata/cases_cloudformation.json` predates foundation B8 and holds no list-length case (no `NotificationARNs`, no 51-tag case); 51 tags and 6 notification ARNs are accepted (run, CFN-48). Regenerate with `dzaudit cases cloudformation`. |
| Console handler | `internal/console/cfn_rollback_test.go`: 1 test |
| Conformance | `conformance/tests/test_cloudformation.py`: 9 scenarios, all **unverified** (`conformance/snapshots/` is empty) |
| Browser | `e2e/tests/cloudformation.spec.ts` (6) and `cfn-starter.spec.ts` (2): 8 tests |
| Deploy smokes | `e2e/deploy/`: `sam`, `cdk`, `serverless`, `terraform`, each a `smoke.sh`, run by `run.sh` |
| Mutation | No per-package baseline (foundation B3) |

What the counts hide: there is no test of an update that changes a property of
each mapped type, none of a parameter-only update reaching a resource, none of
`NoEcho`, none of delete with a deleted importer, and the one browser test of
delete is titled "what it created goes with it". *Added 2026-10-11:* none posts
all 23 operations as the reference spells them (SQS's `TestEveryOperationIsAcceptedOnTheQueryWire`
has no twin); none puts a violation in the second element of a list; none
reads a response member the SDK requires; and `dzaudit`'s regenerated cases
are not committed.

## 4. Findings

Evidence `run` means reproduced on 2026-10-10 (CFN-1 to 39) or 2026-10-11 (CFN-40
to 68) against a binary built from this tree, through the console's endpoints
or the wire. Owned elsewhere and not repeated: the queue-and-DLQ ordering bug
is **SQS-1**, *Corrected 2026-10-11: closed in the SQS stage* (`cloudformation/dlq_order_apply_test.go`;
the general case is CFN-22); the refusal "why" line is **A2** (landed: the
console now prints Refused, and the sentence under it is the foundation's
generic one, so the CFN boards' own sentences are build items in 6a); the
header-button refusal falling to a toast is **A6** (landed: run, the delete
refusal draws under the header); the unreachable and stale `copy.go` lines are
**A7** (landed); the delete confirm's "Everything it created goes with it" is
**A13** (landed as a component; the sentence itself is still this service's,
read against the code in 6a and false for a Retain resource: run).

**Source column, re-read 2026-10-11.** `model` is the model's documentation or
traits (quoted); `doc` is a page read in the pre-stage review (named); `memory`
is not fixed on. Eleven `doc` tags in the first pass named no page; they are
re-tagged below.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| CFN-1 | P0 | An update that changes an SSM parameter's `Value` reports UPDATE_COMPLETE and changes nothing: the value and `Version` stay. `applyParameters` skips an existing parameter unless `Force`, and no mapper sets `Force`. `applySecrets` has the same branch. | `internal/provision/svc_ssm.go` `applyParameters`; `svc_secrets.go` `applySecrets`; `internal/cfn/mapper.go` | run (SSM; and 2026-10-11: after the update to `EarnRate` 15, `doze-aws export` of the instance still says `Value: "10"`), read (Secrets) | n/a: a silent no-op is a bug whatever AWS does. *Corrected 2026-10-11: was "doc: an update to Value updates the parameter"; no page was read; that an `AWS::SSM::Parameter` `Value` change updates in place is `memory`.* | Stack test: create with `Value: a`, update to `b`, read SSM. Same for a secret. |
| CFN-2 | P0 | `AWS::Kinesis::Stream` is mapped, reported CREATE_COMPLETE with a physical id, and no stream is made. | `internal/cfn/mapper.go`, the `AWS::Kinesis::Stream` case | run | n/a | Stack test: deploy a stream, `DescribeStream` |
| CFN-3 | P0 | `AWS::Events::EventBus` is mapped, CREATE_COMPLETE, and no bus is made. | `mapper.go`, the `AWS::Events::EventBus` case | run (the console's bus list) | n/a | Stack test: deploy a bus and a rule on it, `DescribeEventBus` |
| CFN-4 | P0 | **Queue part closed in the SQS stage** (`cloudformation/queue_policy_apply_test.go`; a policy for an undeclared queue is applied against one that must exist; *Corrected 2026-10-11*). `AWS::SNS::TopicPolicy` and `AWS::Lambda::Permission` are still mapped and dropped (`mapper.go`, the shared `return nil`, with `AWS::Events::EventBus`). A topic policy is evaluated under soft and enforce, so a template that grants a principal `Publish` deploys clean and is denied at run time. | `mapper.go`, the shared `return nil` case | run (queue policy on a missing queue), read | n/a | Stack test under enforce: topic, queue, queue policy, publish, receive |
| CFN-5 | P0 | A property a mapper does not read is dropped silently. `NotAProperty: 7` on a queue deploys; `KmsMasterKeyId` is dropped. | every `m.<kind>` in `internal/cfn/mapper*.go` | run | memory: AWS refuses an unknown property (the resource reference, not read in this review; the model has no resource schemas) | Transpile test per type: an unknown property fails; a known-unapplied one is reported |
| CFN-6 | P0 | A create refused in transpile leaves a CREATE_FAILED record. Reproduced for a parameter with no value: the corrected resubmission answers `AlreadyExistsException` until the record is deleted. The same happens for an unserved type, which is ours to define. | `cloudformation/stacks.go` `deploy`, `recordFailure` | run (2026-10-11 again, through the console: a refused create of `harbour-fleet2` left a `CREATE_FAILED` record in the stack list) | doc for the message (`Parameters: [KeyName] must have values`, parameters page); `memory` that no stack record exists after a synchronous validation refusal. *Corrected 2026-10-11: was "doc: ... no stack exists".* | Handler test: refused create, then `DescribeStacks` is not found and a second create succeeds. `TestServiceBadTemplateRecordsFailure` asserts the opposite and changes with the decision. |
| CFN-7 | P0 | A deleted stack still counts as an importer. After deleting the importer, deleting the exporter is refused "in use by <deleted stack>", for good. `ListImports` lists it too. | `stacks.go` `importerOf`, `hListImports` | run | model: `ListImports` "To modify or remove an exported output value, first use this action to see which stacks are using it" (a *live* importer); the refusal's wording is `memory`. *Corrected 2026-10-11: was "doc".* | Handler test: exporter, importer, delete importer, delete exporter |
| CFN-8 | P0 | `DeleteStack` answers DELETE_COMPLETE when `Destroy` failed, and when the stored template no longer transpiles. Both are only logged; the resources stay, owned by nothing. | `stacks.go` `hDeleteStack` | read | model: `StackStatus` has `DELETE_FAILED`; `DeleteStack.RetainResources` "For stacks in the DELETE_FAILED state"; `DeletionMode` `FORCE_DELETE_STACK` "Delete the stack if it's stuck in a DELETE_FAILED state". *Corrected 2026-10-11: was "doc".* | Handler test with a gateway that fails one delete |
| CFN-9 | P1 | `ValidateTemplate` calls a template with an unserved type valid; `CreateStack` then refuses it. It parses and checks references only. | `stacks.go` `hValidateTemplate` | run | n/a (the limit is ours) | Handler test: validate a template with `AWS::ECS::Cluster` |
| CFN-10 | P1 | A `NoEcho` parameter's value is returned in clear by `DescribeStacks` and `DescribeChangeSet`. | `stacks.go` `viewStack`; `changesets.go` `hDescribeChangeSet` | run (2026-10-11: `s3cr3t` back in clear from `DescribeStacks`, and from `DescribeChangeSet` for a changed value) | doc: "CloudFormation returns the parameter value masked as asterisks (*****) for any calls that describe the stack or stack events" (*Template Parameters syntax*, read 2026-10-11); not for `Outputs` or `Metadata`. *Corrected 2026-10-11: page named.* | SDK test |
| CFN-11 | P1 | A stack records only the parameters it was given. One that took its default is absent from `DescribeStacks`. | `stacks.go` `deploy` (`Parameters: params`) | run | **memory**: the model's `Stack.Parameters` is "A list of Parameter structures" and does not say defaults are echoed; "CloudFormation uses the default values" (parameters page) is about the input. *Corrected 2026-10-11: was "doc"; not fixed on memory, recording `parameters_include_defaults`.* | SDK test: create with no parameters, read `Parameters` |
| CFN-12 | P1 | In a failed create, a Retain resource gets DELETE_IN_PROGRESS and DELETE_COMPLETE events and that status under Resources while it still exists. A resource that failed because its name was taken, and was never made, is also reported DELETE_COMPLETE. | `cloudformation/rollback.go` `undoCreate` | run (both; 2026-10-11 again: Cfn-18's trail) | model: `ResourceStatus` includes `DELETE_SKIPPED`; and `RetainExceptOnCreate` "newly created resources are deleted when the operation rolls back. This includes newly created resources marked with a deletion policy of Retain" implies that by default they are not. *Which* status AWS writes for it is `memory`. *Corrected 2026-10-11: was "doc".* | Rollback test asserts the trail for a Retain bucket |
| CFN-13 | P1 | A change set for a parameter-only change is empty (FAILED, "didn't contain changes") unless the parameter changes a physical name. The diff fingerprints template text. | `cloudformation/changesets.go` `diffChanges`; `internal/cfn/resources.go` `fingerprint` | run (2026-10-11: `param-only`, `EarnRate` 16, is `FAILED` "didn't contain changes") | model: `CreateChangeSet` "compares the stack's information with the information that you submit" (parameters are part of it); the status text is `memory`. *Corrected 2026-10-11: was "doc".* | `propdiff_test.go`: a parameter feeding a property shows Modify |
| CFN-14 | P1 | `ExecuteChangeSet` calls `deploy` without `refuseUpdate`, so a change set updates a ROLLBACK_COMPLETE or CREATE_FAILED stack. A change set made before a later update still executes, with its stale template and parameters. | `changesets.go` `hExecuteChangeSet` | read (first), run (second; 2026-10-11: after `ExecuteChangeSet a`, `b` stays `AVAILABLE`, and executing it, a CREATE-type set against the now existing stack, ended `ROLLBACK_COMPLETE`) | model: `ExecuteChangeSet` "CloudFormation deletes all other change sets associated with the stack because they aren't valid for the updated stack"; doc: "After you execute a change set, CloudFormation deletes any additional change sets" (*Execute a change set*, read). *Corrected 2026-10-11: both now named.* The executed set stays, `EXECUTE_COMPLETE`. | Handler tests for both |
| CFN-15 | P1 | `CreateChangeSet` does not run `CheckReferences` or `CheckParameters`. A value outside `AllowedValues` makes an AVAILABLE change set that fails at execute. | `changesets.go` `hCreateChangeSet` | read | **memory** (the model's `CreateChangeSet` lists no validation of parameter values). *Corrected 2026-10-11: was "doc".* | Handler test, skipped until recorded |
| CFN-16 | P1 | Ignored types are not in `DescribeStackResources`, `ListStackResources` or the events. A template's IAM role does not exist as far as the stack says. | `stacks.go` `deploy`, the `e.Kind != cfn.Mapped` skip | run | model: `ListStackResources` "Returns descriptions of all resources of the specified stack"; a resource that a template declares is a stack resource. *Corrected 2026-10-11: was "doc".* | SDK test: a role is listed |
| CFN-17 | P1 | *2026-10-20: the trail's ORDER is fixed (the SQS leftovers package, sqs.md SQS-57: `synthesizeEvents` emits each resource's pair in the order apply made them; `TestServiceAQueueAndItsDeadLetterQueueInOneStack`); the timestamps below are not.* On an update every resource becomes UPDATE_COMPLETE and gets a pair of events, changed or not. A resource the update adds and fails on is UPDATE_FAILED, not CREATE_FAILED. Every resource's timestamp is the stack's. | `stacks.go` `deploy`; `deploy_support.go` `synthesizeEvents`; `rollback.go` `markFailed` | run | memory | Event-trail test for a one-property update |
| CFN-18 | P1 | A failed resource's reason is the internal wrapper and the raw wire body: `stackfile: parameters: parameter "…": aws 400: {"Message":…}`. | `internal/provision/apply.go` `Apply`; `rollback.go` `markFailed` | run | n/a | Rollback test asserts the reason is the peer's code and message |
| CFN-19 | P1 | A template with IAM resources deploys with no `Capabilities`. | `stacks.go` `hCreateStack`, `hUpdateStack`, `hCreateChangeSet`, `hExecuteChangeSet` | run (2026-10-11: a role, and a SAM function with no `CAPABILITY_AUTO_EXPAND`, both deploy) | **model**: "If you don't specify either of these capabilities, CloudFormation returns an InsufficientCapabilities error"; the eight types (`AWS::IAM::AccessKey`, `Group`, `InstanceProfile`, `ManagedPolicy`, `Policy`, `Role`, `User`, `UserToGroupAddition`); `CAPABILITY_NAMED_IAM` for custom names; `CAPABILITY_AUTO_EXPAND` "includes the AWS::Include and AWS::Serverless transforms" for create and update, "doesn't apply to creating change sets"; "Only one of the Capabilities and ResourceType parameters". Error code `InsufficientCapabilitiesException`, 400 (`awsQueryError`). Listed on `CreateStack`, `UpdateStack`, `CreateChangeSet`, `ExecuteChangeSet`. *Corrected 2026-10-11: was "doc"; the rule is wider than IAM, and the console must send the acknowledgement in the same change (CFN-36), or its own Cfn-03 template is refused.* | Rejection test, in the same commit as the form's acknowledgement; no longer "skipped until recorded" because the model states it |
| CFN-20 | P1 | `DescribeStacks` never returns `Description`. `DescribeChangeSet` and `ListChangeSets` build `StackId` with an empty id instead of the stack's. `AWS::StackId` always ends `/local`. | `stacks.go` `viewStack`; `changesets.go`; `internal/cfn/intrinsics.go` `pseudo` | run (2026-10-11: `<StackId>arn:...:stack/cs1/</StackId>` on `DescribeChangeSet`) | model: `Stack.Description` "A user-defined description associated with the stack"; `DescribeChangeSet.StackId` "The Amazon Resource Name (ARN) of the stack that's associated with the change set"; `AWS::StackId` doc (pseudo-parameters page, read). *Corrected 2026-10-11.* | SDK test for each |
| CFN-21 | P1 | Importer detection is a substring search: a stack whose template contains the export's name and the word `ImportValue` anywhere counts. | `stacks.go` `importerOf`, `hListImports` | read | n/a | Handler test with the name in a comment |
| CFN-22 | P1 | Apply runs in fixed phases by kind, by name inside a phase. `DependsOn` and references do not order anything. SQS-1 was one case and is closed (a queue is applied after the queue its redrive policy names, `internal/provision`); any reference from an earlier phase to a later one is another. Which pairs fail is not established. `Apply`'s phase list (read 2026-10-11): queues, tables, keys, buckets, functions, topics, connections, rules, notifications, log groups, secrets, parameters, APIs, API keys, state machines, alarms. | `internal/provision/apply.go` `Apply` | read | n/a | A table test: for each reference a mapper emits, the target is applied first |
| CFN-23 | P1 | For an existing resource, apply reports "skipped: exists" for buckets, tables, topics, keys, log groups, REST and HTTP APIs, and a queue with no attributes. Which property edits an update applies, per type, is not established. | `internal/provision/svc_*.go` | read | n/a | Per type: update one property of each kind, read it back |
| CFN-24 | P1 | The 67 refused operations answer 400 `ValidationError` (run: `DetectStackDrift`). *Corrected 2026-10-11: "nobody has decided"; D5 decided: 501 `NotImplemented`, a message naming the operation and why, and `X-Doze-Not-Built`; `awshttp.NotBuilt` exists and the 111 "unknown" entries of the model sweep move to `notbuilt` in this stage.* Wrong code. `ContinueUpdateRollback` and `RollbackStack` are S; `CancelUpdateStack` is F (section 2). | `cloudformation/service.go` `ServeHTTP` | run | n/a (ours: README D5) | One test over `stubActions`: 501, `NotImplemented`, the header, on the Query envelope |
| CFN-25 | P1 | No list operation paginates. `NextToken` passes validation and is ignored on `DescribeStacks`, `ListStacks`, `DescribeStackEvents`, `ListStackResources`, `ListChangeSets`, `ListExports`, `ListImports`. | `stacks.go`, `changesets.go` | read | model: `NextToken` (length 1 to 1,024) on `DescribeStacks`, `ListStacks`, `DescribeStackEvents`, `ListStackResources`, `ListChangeSets`, `ListExports`, `ListImports`, `DescribeChangeSet` (`Changes`); "If the output exceeds 1 MB in size, a string that identifies the next page"; `ListExports`: "If the output exceeds 100 exported output values". The 1 MB page is not reachable in a test; the one count is 100 for exports (decision 4). `DescribeStackResources` has no token and "Only the first 100 resources will be returned" (CFN-57). Built on `internal/awspage` (B11, landed). *Corrected 2026-10-11: was "doc", and `DescribeChangeSet` was missing from the list.* | SDK paginator test past one page of events (`dozetest.AssertPaginates`) |
| CFN-26 | P1 | `UpdateStack` on a REVIEW_IN_PROGRESS stack is not refused. | `stacks.go` `refuseUpdate` | read; run 2026-10-11: `UpdateStack` on a `REVIEW_IN_PROGRESS` stack goes to `UPDATE_COMPLETE` | memory | Handler test, skipped until recorded |
| CFN-27 | P2 | Validate and Load parameters show a rejection as `aws 400: <?xml …<ErrorResponse>…`. Both use `err.Error()` where every other partial uses `errText`. | `internal/console/handlers_cfn.go` `cfnValidate`, `cfnSummary` | run | n/a | Handler test: the partial holds the message and no `<` |
| CFN-28 | P2 | The Parameters panel says "The template declares no parameters" and the Update form "The deployed template declares no parameters" when the template declares some that took defaults. | `templates/cfn.html`, `cfn_stack` | run | n/a | Browser test. Closes with CFN-11 or by reading the summary. |
| CFN-29 | P2 | A `NoEcho` value is shown in the Parameters panel and prefilled in a plain input on the Update form. The create form prints a `NoEcho` default in the placeholder of its password input. | `cfn.html`, `cfn_stack` and `cfn_params` | run | n/a | Browser test |
| CFN-30 | P2 | The toast says "Change set created — review the diff, then execute it" for an empty FAILED set. | `handlers_cfn.go` `cfnCreate`, `cfnUpdate` | run | n/a | Handler test on the flash |
| CFN-31 | P2 | Deleted stacks are rendered only when the list is not empty, so deleting the last stack hides the record of it. | `cfn.html`, `cfn_home` | read | n/a | Handler test |
| CFN-32 | P2 | The change-set table prints "in place" for Add and Remove rows. | `cfn.html`, the Replacement cell | run | n/a | Handler test |
| CFN-33 | P2 | The empty Resources row says "A stack in REVIEW_IN_PROGRESS has a change set waiting" for CREATE_FAILED stacks too. | `cfn.html`, `cfn_stack` | run | n/a | Handler test |
| CFN-34 | P2 | Load parameters prints "Creates:" followed by every declared type, including ignored and refused ones. | `cfn.html`, `cfn_params`; `stacks.go` `hGetTemplateSummary` | run | n/a | Handler test with an IAM role |
| CFN-35 | P2 | Resources links every physical id whose type has a console page, including rows in DELETE_COMPLETE and resources that were never made (CFN-2). | `internal/console/client_cfn.go` `resourceLink` | run | n/a | Handler test on a rolled-back stack |
| CFN-36 | P2 | The console cannot send what the API takes: `Capabilities`, `Tags`, `DisableRollback`, `OnFailure`, `TemplateURL`, termination protection, a stack policy. Termination protection is shown and cannot be changed. The Update form has no CLI preview. | `handlers_cfn.go` `formParams`, `cfnCreate`, `cfnUpdate` | read | n/a | Decide which belong in the form; a browser test per one added |
| CFN-37 | P3 | Stale comments and ledger: "nothing local acts on" termination protection (`client_cfn.go` `Stack`, `store.go` `stackRecord`); the ledger tiers `UpdateTerminationProtection` C; `store.go`'s header says "no resource-level rollback"; `Fn::ImportValue`'s error says "doze-aws has no cross-stack export registry". | as named | read | n/a | Ledger test after the tier change |
| CFN-38 | P3 | `docs/cloudformation.md` lists `AWS::CloudWatch::*`, log groups and alarms as skipped; they are mapped. | `docs/cloudformation.md`, "The three outcomes" and "Resource types" | read | n/a | A doc test that the three lists equal `nameProperty`, `ignoredTypes` and the rest |
| CFN-39 | P3 | The Validate verdict says "Rejected" where every other refusal says "Refused". The list pane's number is the count of outputs, unlabelled. | `cfn.html`, `cfn_validate_result`, `cfn_lp` | read | n/a | With the foundation's vocabulary test |

**Found by reading the model's own documentation, and by driving a binary
(2026-10-11; CFN-40 to CFN-68).** Every row has a direction, a quoted source and
a test to write first. Evidence `run` is a call made against a binary built from
this tree on 2026-10-11 (script in `scratchpad/cfn/`; each is a short `curl`).

| Id | Sev | What | Direction | Where | Evidence | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| CFN-40 | P1 | **A stack id is not a token.** `GetStack` takes the name out of `…:stack/<name>/<uuid>` and ignores the uuid, the account and the region; the record is keyed by name, so deleting a stack and creating the name again overwrites the record: the old id then answers with the new stack, a garbage uuid or another account's arn resolves, and the deleted stack's events, resources and template are gone. | wrong; more forgiving | `store.go` `GetStack`, `PutStack`; `stacks.go` `hCreateStack` | run (create `s2`, delete, create `s2`: `DescribeStacks` by the first id returns the second stack, `CREATE_COMPLETE`; `…/s2/00000000-…` too) | model: `DescribeStackEvents` "You can list events for stacks that have failed to create or have been deleted by specifying the unique stack identifier (stack ID)"; `DescribeStacks` "Deleted stacks: You must specify the unique stack ID"; `GetTemplate`, `ListStacks` "up to 90 days after the stack has been deleted" | Handler test: create, delete, create the same name; the first id is `DELETE_COMPLETE` with its own events and template; a foreign uuid is "does not exist". Key the store by id. |
| CFN-41 | P1 | `DescribeStackResource` answers `<Timestamp>` in `StackResourceDetail`; the model's member is `LastUpdatedTimestamp` (required). `StackResource` (from `DescribeStackResources`) does carry `Timestamp`. Both views come from one struct, so the detail is wrong and the list is right. Also missing from the detail: `Description`, `Metadata`, `DriftInformation`. | wrong shape | `stacks.go` `hDescribeStackResource`, `resourceView` | run | model: `StackResourceDetail.LastUpdatedTimestamp` "Time the status was updated", REQUIRED | SDK v2 test: `LastUpdatedTimestamp` is non-zero |
| CFN-42 | P1 | **A deleted stack's record.** `LastUpdatedTime` is set to the deletion time ("only returned if the stack has been updated at least once"); `DeletionTime` is never written (`Stack` and `StackSummary`); the resources and outputs are cleared, so `DescribeStackResources` and `ListStackResources` by id answer empty where the model says "up to 90 days"; `StackSummary` lacks `TemplateDescription`, `ParentId` and `RootId`, `Stack` lacks `RoleARN`, `TimeoutInMinutes`, `ChangeSetId`, `DetailedStatus`. | wrong shape; missing | `stacks.go` `hDeleteStack`, `viewStack`, `hListStacks` | run (`DescribeStacks` by id after delete) | model: `Stack.DeletionTime`, `LastUpdatedTime`; `DescribeStackResources`, `ListStackResources`, `DescribeStackResource` "returns resource information for up to 90 days after the stack has been deleted" | SDK test: delete, then `DescribeStacks`/`ListStackResources` by id: `DeletionTime` set, no `LastUpdatedTime`, every resource `DELETE_COMPLETE` (or `DELETE_SKIPPED`) |
| CFN-43 | P1 | **An update that does not mention a setting drops it.** The updated record is built new and carries only the id, termination protection, policy and parent: `Tags`, `Capabilities`, `NotificationARNs`, `RollbackConfiguration` and `DisableRollback` are lost (a stack created with a tag, a capability, an SNS arn and `DisableRollback=true`, updated with only a parameter: tags and capabilities gone, `DisableRollback` false). | wrong (data loss) | `stacks.go` `deploy` (`st := &stackRecord{…}`), `readDeployFields` | run | model: `UpdateStack.Tags` "If you don't specify this parameter, CloudFormation doesn't modify the stack's tags. If you specify an empty value, CloudFormation removes all associated tags"; `NotificationARNs` "Specify an empty list to remove all"; `RoleARN` "If you don't specify a value, CloudFormation uses the role that was previously associated with the stack"; `RollbackTriggers` "By default, CloudFormation saves the rollback triggers... applies them to any subsequent update" | SDK test per member: create with it, update without it, describe; and with an empty list/value (removed) |
| CFN-44 | P1 | **An omitted parameter keeps the stack's previous value; AWS uses the template's default.** `UpdateStack` and `CreateChangeSet` merge the stack's existing parameters under whatever was sent, read an empty value as "keep", and never read `UsePreviousValue`. The console's "blank keeps 15" depends on this and has to send `UsePreviousValue=true` instead. | more forgiving | `stacks.go` `hUpdateStack` (`merged`), `changesets.go` `hCreateChangeSet`; `service.go` `keyValues`; `handlers_cfn.go` `formParams` | run (`UpdateStack` with the parameter omitted: `UPDATE_COMPLETE`, value still `2`) | model: `Parameter.ParameterKey` "If you don't specify a key and value for a particular parameter, CloudFormation uses the default value that's specified in your template"; `UsePreviousValue` "During a stack update, use the existing parameter value that the stack is using for a given parameter key. If you specify true, do not specify a parameter value." Message for no default: doc (`Parameters: [KeyName] must have values`) | Handler test: update omitting `P` resets it to its default, or is refused when it has none; `UsePreviousValue=true` keeps; `true` with a value is refused |
| CFN-45 | P1 | `EnableTerminationProtection` on `CreateStack` is read by nothing: the stack is created unprotected (run) and the console shows the flag it cannot set. `UpdateTerminationProtection` on a nested stack is accepted, and a nested stack does not report its root's setting. | missing; more forgiving | `stacks.go` `hCreateStack`, `hUpdateTerminationProtection`; `readDeployFields` | run (create); read (nested) | model: `CreateStack.EnableTerminationProtection`; `UpdateTerminationProtection` "For nested stacks, termination protection is set on the root stack and can't be changed directly on the nested stack" | Handler tests: create protected then delete refused; update protection on a child refused (code: `memory`, `ValidationError`) |
| CFN-46 | P1 | **"Not both" and "only one of" are not checked.** Accepted: `DisableRollback` with `OnFailure`; `TemplateBody` with `TemplateURL` (the body wins, the URL is never read); `UsePreviousTemplate` with a template; `StackPolicyBody` with `StackPolicyURL`; `Capabilities` with `ResourceTypes`. Neither body nor URL is "template is empty". `OnFailure` is also read on `UpdateStack`, where it is not a member. | more forgiving | `stacks.go` `hCreateStack`, `hUpdateStack`, `templateOf`; `rollback.go` `policyOf` | run (the first, second, fifth) | model: "You can specify either DisableRollback or OnFailure, but not both"; "You must specify either TemplateBody or TemplateURL, but not both"; "Only one of the Capabilities and ResourceType parameters can be specified"; `UpdateStack`: "only one of the following parameters: TemplateBody, TemplateURL, or set the UsePreviousTemplate to true". `ValidateTemplate`: "If both are passed, only TemplateBody is used" (accepted there, correctly). Error wording `memory` | One rejection case each, `ValidationError`, wording not asserted |
| CFN-47 | P1 | **Prose limits are enforced nowhere.** `TemplateBody` accepts 52,000 bytes (the model: "a maximum length of 51,200 bytes"); `TemplateURL` accepts `s3://` ("must start with https://"; read in `parseS3URL`; S3 website hosts were not tried) and any size ("max size: 1 MB"). From the quotas page (doc): 200 parameters, 200 outputs, 500 resources, 200 mappings, a parameter value of 4,096 bytes, a name of 255 characters, a description of 1,024 bytes, a stack name of 128 (held), 2,000 stacks. None are in a trait; none are checked. | more forgiving | `validate.go` (the table has none); `template.go` `Parse`; `templateOf` | run (52,000-byte body: 200) | model (prose) for 51,200, 1 MB, https; doc (*Understand CloudFormation quotas*, read) for the rest | Rejection cases; the template-size case on both `CreateStack` and `ValidateTemplate`. `s3://` is a deliberate leniency or a refusal: decide (decision list) |
| CFN-48 | P1 | **List lengths are not in the constraint table.** `Tags` (50), `NotificationARNs` (5), `RollbackTriggers` (5), `ResourcesToImport` (200) are traits on the model's lists; the table has none (`cases_cloudformation.json` predates B8), so 51 tags and 6 notification ARNs are accepted (run) and the ledger's "182 of 182" is stale. | more forgiving | `validate.go`; `testdata/cases_cloudformation.json` | run | model: `Tags` `length max 50`; `NotificationARNs` `max 5`; `RollbackTriggers` `max 5`; `ResourcesToImport` `max 200` | Regenerate `dzaudit cases cloudformation`; the new cases are red |
| CFN-49 | P1 | **A Query request is validated one element at a time, at random.** `FromQuery` keeps one element of every list, merges the other elements' members into it in map-iteration order, and for a scalar list keeps whichever key was visited first: `Capabilities.member.2=BOGUS` was accepted in 3 of 4 identical requests; `Tags.member.2.Key` of 200 characters is refused with the index `tags.1`. The same defect as SQS-26, in the function four services share. | more forgiving, nondeterministic | `internal/modelcheck` `FromQuery`, `insertQuery`; `service.go` `ServeHTTP` | run | model: every list constraint applies to every element. **Foundation B13** (to build; CloudFormation is named): this service gets the fix for free and must add the cases | Rejection case with the violation in element 2 and 3 only, `Capabilities`, `Tags`, `Parameters`, `StackStatusFilter`; repeated 20 times |
| CFN-50 | P1 | **Stack tags do not reach the resources.** `Tags` are stored on the stack only (run: the queue's tag list is empty). | missing | `internal/cfn/mapper*.go`; `stacks.go` `deploy` | run | model: `CreateStack.Tags` "CloudFormation also propagates these tags to the resources created in the stack"; `UpdateStack.Tags` "to supported resources in the stack". The `aws:cloudformation:*` system tags are `memory` | Stack test per kind that has tags (decision 7); listed kinds that do not |
| CFN-51 | P1 | **`ClientRequestToken` and `OperationId` do not exist.** The token is read by nothing: no `TokenAlreadyExistsException`, events carry no `ClientRequestToken` or `OperationId`, and `CreateStack`, `UpdateStack` and `RollbackStack` return no `OperationId`. The same token reused for another stack succeeded (run). | missing | `stacks.go`, `deploy_support.go` `synthesizeEvents`, `store.go` `stackEvent` | run | model: "All events initiated by a given stack operation are assigned the same client request token... all the StackEvents generated by that operation will have ClientRequestToken set as token1"; `CreateStackOutput.OperationId` "A unique identifier for this stack operation"; errors `TokenAlreadyExistsException` ("A client request token already exists") on `CreateStack`, `UpdateStack`, `DeleteStack`, `ExecuteChangeSet`, `CancelUpdateStack`. What a *repeat* of the same token and name does is `memory` | SDK test: events of one operation carry its token; a second operation's do not |
| CFN-52 | P1 | **`ChangeSetType` default and the two "can't"s.** An omitted type is `CREATE` when the stack is missing (model default is `UPDATE`, which AWS refuses for a new stack); `CREATE` against an existing `CREATE_COMPLETE` stack is accepted and later executes as a create over it (run: the stack ended `ROLLBACK_COMPLETE`). | more forgiving | `changesets.go` `hCreateChangeSet`, `hExecuteChangeSet` | run | model: "By default, CloudFormation specifies UPDATE. You can't use the UPDATE type to create a change set for a new stack or the CREATE type to create a change set for an existing stack." Message `memory` | Handler tests for the three cases |
| CFN-53 | P1 | **A duplicate change set name overwrites.** Creating `a` twice answers 200 with the same id, and the record is replaced. | more forgiving | `changesets.go` `hCreateChangeSet`, `store.go` `PutChangeSet` | run | model: `ChangeSetName` "must be unique among all change sets that are associated with the specified stack"; error `AlreadyExistsException` listed on `CreateChangeSet` | Handler test: the second create is `AlreadyExistsException`, 400 |
| CFN-54 | P1 | **Change-set members that do nothing, silently.** `ChangeSetType=IMPORT` makes an ordinary change set; `ResourcesToImport`, `ImportExistingResources`, `IncludeNestedStacks`, `OnStackFailure`, `DeploymentMode`, `Description`, `RoleARN`, `ResourceTypes`, `Capabilities`, `RollbackConfiguration`, `NotificationARNs` are read by nothing. | wrong; missing | `changesets.go` | run (`IMPORT`: 200) | model: each member's text, quoted in section 2 | Decision 5 for the import family (501); each other member stored and returned (CFN-55) or listed C |
| CFN-55 | P1 | **A change set forgets what it was made with, and `DescribeChangeSet` is thin.** The record keeps template, parameters and tags. `DescribeChangeSet` returns none of `Description`, `Capabilities`, `NotificationARNs`, `RollbackConfiguration`, `Tags`, `IncludeNestedStacks`, `OnStackFailure`, `StackDriftStatus`, `ParentChangeSetId`, `RootChangeSetId`, and a `Change` has no `Scope`, `Details` or `PolicyAction`. `ListChangeSets` lacks `Description`, `CreationTime` is there. Capabilities and the failure policy come from the *execute* call instead of the set. | wrong shape; missing | `changesets.go` `hDescribeChangeSet`, `hExecuteChangeSet` (`policyOf(p)`) | run (`Description=hello`: not returned) | model: `DescribeChangeSet` output members; `Capabilities` "the list of capabilities that were explicitly acknowledged when the change set was created"; `OnStackFailure`, `DisableRollback` "can't be specified when the OnStackFailure parameter ... was specified" | SDK test: round trip of every settable member |
| CFN-56 | P1 | **`GetTemplate` ignores `ChangeSetName` and `TemplateStage`.** It answers the stack's submitted template always, with `StagesAvailable` `Original`, `Processed`. A SAM stack's `Processed` (the default) is the transformed template, not what was sent; a change set's name is "the associated template", and an unknown one is `ChangeSetNotFound` 404. | wrong | `stacks.go` `hGetTemplate` | run (`ChangeSetName=zzz`: 200 with the stack's template; the SAM stack's body is the original) | model: `GetTemplate` errors `ChangeSetNotFoundException` (code `ChangeSetNotFound`, 404); `TemplateStage` "By default, CloudFormation specifies Processed"; `StagesAvailable` "For change sets, the Original template is always available" | SDK test: change set name returns its template; unknown is 404; `Original` of a SAM stack |
| CFN-57 | P1 | **`DescribeStackResources`.** `PhysicalResourceId` is read by nothing (a call with only it answers "Stack with id  does not exist"); `StackName` with `PhysicalResourceId` is accepted (AWS: `ValidationError`); "Only the first 100 resources will be returned" is not applied. | wrong; more forgiving | `stacks.go` `hDescribeStackResources` | run | model: "You must specify either StackName or PhysicalResourceId, but not both... A ValidationError is returned if you specify both"; "If PhysicalResourceId is specified, the associated resources of the stack that the resource belongs to are returned"; "Only the first 100 resources" | SDK test: by physical id; both refused; 101 resources |
| CFN-58 | P1 | `ValidateTemplate` and `GetTemplateSummary` return no `Capabilities` or `CapabilitiesReason` (so a caller cannot learn that its template needs `CAPABILITY_IAM`), and `GetTemplateSummary` no `Metadata`, `ResourceIdentifierSummaries` or `Warnings`; its `StackSetName` is ignored. | missing | `stacks.go` `hValidateTemplate`, `hGetTemplateSummary` | run (an IAM role: neither member) | model: `Capabilities` "The capabilities found within the template. If your template contains IAM resources, you must specify the CAPABILITY_IAM or CAPABILITY_NAMED_IAM"; `CapabilitiesReason` "The list of resources that generated the values" | SDK test with a role and with a SAM transform |
| CFN-59 | P1 | **An unnamed FIFO queue fails the stack** (the SQS stage's name rule meeting the mapper): `FifoQueue: true` with no `QueueName` names the queue by its logical id, SQS refuses it ("FIFO queue names must end in .fifo, and only a FIFO queue may use that ending"), and the stack is `ROLLBACK_COMPLETE`. AWS generates a name that ends `.fifo`. | stricter | `internal/cfn/mapper.go` `queue`, `resources.go` naming | run | SQS-3 (the rule is right); the generated name is `memory` (the resource reference, not read) | Stack test: unnamed FIFO queue deploys with a `.fifo` physical name |
| CFN-60 | P2 | `ResourceTypes` (the request member) is read by nothing: a stack that creates a type outside the list deploys. | more forgiving | `stacks.go` | run | model: "If the list of resource types doesn't include a resource that you're creating, the stack creation fails" | Rejection case |
| CFN-61 | P2 | `RetainExceptOnCreate` (a request member on create, update and execute) and `DeploymentConfig.DisableRollback` are read by nothing; only the template's `DeletionPolicy: RetainExceptOnCreate` is. `ExecuteChangeSet.DisableRollback` is documented "Default: True", which contradicts what every tool does and what the code does (rollback). | missing; contradiction | `rollback.go` `retained`, `policyOf` | read | model, quoted. The default is **recorded, not followed** (decision 8) | Case for `RetainExceptOnCreate=true` deleting a new Retain bucket on rollback; `test_execute_change_set_default_rollback` on the recording queue |
| CFN-62 | P2 | **Stack policy.** `StackPolicyBody`/`StackPolicyURL` on create and update are read by nothing (`GetStackPolicy` answers empty); `SetStackPolicy` accepts text that is not JSON and a call with neither member; `StackPolicyDuringUpdateBody/URL` are ignored; an update is never checked against the policy. | missing | `stacks.go` `hSetStackPolicy`, `hCreateStack` | run | model: `ExecuteChangeSet` "If a stack policy is associated with the stack, CloudFormation enforces the policy during the update. You can't specify a temporary stack policy that overrides the current policy"; `StackPolicyDuringUpdateBody` "temporary overriding stack policy". The tier stays C (decision: not enforced) and is *said* | Cases: body on create is returned; a non-JSON body is refused (wording `memory`) |
| CFN-63 | P2 | `DeleteStack`'s `RetainResources`, `DeletionMode`, `RoleARN`, `ClientRequestToken` are read by nothing (a delete with `RetainResources=Q` on a healthy stack deleted the queue; AWS: only for `DELETE_FAILED`). | missing | `stacks.go` `hDeleteStack` | run | model: `RetainResources` "For stacks in the DELETE_FAILED state"; `DeletionMode` | After CFN-8: delete from `DELETE_FAILED` with `RetainResources`, with `FORCE_DELETE_STACK`; on a healthy stack the member is refused or ignored (`memory`) |
| CFN-64 | P2 | `NotificationARNs` publish nothing, and `Ref: AWS::NotificationARNs` is refused as "no such parameter or resource". | missing; stricter | `stacks.go`; `internal/cfn/intrinsics.go` `pseudo` | run | model: "The Amazon SNS topic ARNs to publish stack related events"; doc (pseudo-parameters page): "Returns the list of ARNs for the Amazon SNS topics that receive stack event notifications" | Intrinsic test; publish test (decision 6) |
| CFN-65 | P2 | **A change set id is its name.** `<stack id>/changeSet/<name>`: deleting and re-creating `a` reuses the id, and `GetChangeSet` resolves an id by string match. AWS's is `arn:aws:cloudformation:<region>:<account>:changeSet/<name>/<uuid>`. | wrong shape (matches the model's pattern); L6 | `changesets.go`, `store.go` `GetChangeSet` | run | model: `ChangeSetId` `pattern ^arn:[-a-zA-Z0-9:/]*$`; doc (*Execute a change set* example) for the shape | Test: re-created set has a new id; the old id is `ChangeSetNotFound` |
| CFN-66 | P3 | A missing parameter's message is `parameter "V" has no value and no default`; AWS's is `Parameters: [KeyName] must have values`. | wrong message | `internal/cfn/transpile.go` | run | doc (*Template Parameters syntax*, read) | Case asserts the message (it is quoted from a page) |
| CFN-67 | P3 | **Wrong statements in the ledger and the stubs.** `DescribeEvents` is "an alias for DescribeStackEvents that no current SDK emits"; the model: "Returns CloudFormation events based on flexible query criteria. Groups events by operation ID... One of ChangeSetName, OperationId or StackName must be specified" (a new operation, and `OperationId` is what CFN-51 adds). The "does not serve IAM" refusal for `AWS::IAM::AccessKey` says what is false. The ledger counts the registry as 14 (it is 16, and 3 hooks; the ledger's "StackSets (17)" is right). `RollbackStack` and `ContinueUpdateRollback` are refused as "no in-flight update to roll back" and "no rollback to continue", and neither is about an in-flight operation: `RollbackStack` "Rolls back the specified stack to the last known stable state from CREATE_FAILED or UPDATE_FAILED stack statuses" and `ContinueUpdateRollback` "from UPDATE_ROLLBACK_FAILED"; both states are reachable here (`DisableRollback`, and `rollback.go` writes `UPDATE_ROLLBACK_FAILED`). They are F candidates, not stubs. | wrong | `service.go` `stubActions`; `docs/SUPPORT.md`; `internal/cfn/resources.go` | read, run | model: `DescribeEvents`, `RollbackStack` | Ledger test after the edit |
| CFN-68 | P3 | A change set named without `StackName` is resolved across every stack by name. | more forgiving | `store.go` `GetChangeSet` | read | model: `ChangeSetName` "If you specify a name, you must also specify the StackName" (`GetTemplate`); "If you specified the name of a change set, specify the stack name" (`DescribeChangeSet`, `ExecuteChangeSet`, `DeleteChangeSet`) | Case per operation |

**IAM scope, decided (D6).** CloudFormation is evaluated by IAM, in two parts that land in this stage.
(1) The control plane: every stack operation is evaluated and recorded as
`cloudformation:<Operation>` on the stack ARN. (2) What a stack does on the
caller's behalf: each call `provision` makes for a stack is evaluated against
the caller (or the stack's `RoleARN` when one is given), so creating a stack
with a queue needs `sqs:CreateQueue`, and a denial fails that resource with
the service's message and rolls the stack back, as on AWS. Those calls are
recorded against the caller, so a policy generated from a deploy covers the
whole deploy. `doze-aws apply` is not a stack and keeps acting as the root.
Acceptance: under enforce, a principal allowed `cloudformation:*` and nothing
else cannot create a stack with a queue; with `sqs:CreateQueue` added it can;
a soft-mode deploy generates a policy that then passes under enforce. How AWS
attributes the downstream call (`aws:CalledVia`) is a `doc` claim on the
recording queue. Adds about two days to this stage.

## 5. What to check

Beyond the common criteria. Query protocol only: *Corrected 2026-10-11, checked:*
the model declares `awsQuery` and nothing else, and has no `xmlName` and no
`xmlFlattened` member, so every list is `.member.N` and the one map
(`ResourceIdentifier`) is `.entry.N`.

**Wire and shape** *(added 2026-10-11, L5, L6)*
- [ ] A table test posts all 23 operations as the reference spells them
      (`Parameters.member.1.ParameterKey`, `Tags.member.1.Key`,
      `Capabilities.member.1`, `RollbackConfiguration.RollbackTriggers.member.1.Arn`,
      `ResourcesToImport.member.1.ResourceIdentifier.entry.1.key`, `StackStatusFilter.member.1`)
      and each is accepted; the S operations answer 501.
- [ ] A violation in the **second and third** element of every list is
      refused (CFN-49), on `Capabilities`, `Tags`, `Parameters`,
      `NotificationARNs`, `ResourceTypes`, `StackStatusFilter`, `RollbackTriggers`.
- [ ] Every member the model marks required is in the response, per operation
      (CFN-20, 41, 42, 55, 58): the SDK v2 test reads each `REQUIRED` member and
      asserts it is not the zero value.
- [ ] A stack id is a token: the old id of a re-created name, a foreign uuid, a
      foreign account (CFN-40). A change set id likewise (CFN-65).
- [ ] `ClientRequestToken` and `OperationId` on every operation that takes
      one, and on the events (CFN-51).
- [ ] Defaults: `ChangeSetType`, an omitted parameter, omitted `Tags`,
      `NotificationARNs`, `RoleARN`, `RollbackTriggers` (CFN-43, 44, 52).
- [ ] Each size and count limit, from the model and the quotas page (CFN-47, 48).
- [ ] The error code of each refusal is the model's, per operation:
      `AlreadyExistsException`, `InsufficientCapabilitiesException`,
      `ChangeSetNotFound` (404), `InvalidChangeSetStatus`,
      `TokenAlreadyExistsException`, `ValidationError`; and 501 for S.

**Templates**
- [ ] JSON and YAML, every short tag in `template.go`'s table, both `!GetAtt` spellings.
- [ ] Each intrinsic in section 2: its argument shapes, and its error for a wrong one.
- [ ] An intrinsic that cannot resolve fails the deploy; none yields an empty string.
- [ ] `Fn::GetAtt`: a table of attributes per mapped type, each tested; an
      unknown attribute fails.
- [ ] `Ref` per mapped type follows AWS's rule for that type (URL, ARN or name).
- [ ] Every pseudo parameter, under a non-default account and region.
- [ ] Conditions: fixed point, an undefined name, a cycle.
- [ ] Parameters: each constraint and its message; each type's coercion;
      a missing value; a supplied name the template does not declare.
- [ ] Establish and tier: `Rules`, `UpdateReplacePolicy`, `DeletionPolicy:
      Snapshot`, AWS-specific parameter types. (*Corrected 2026-10-11:*
      `AWS::NotificationARNs` is established, CFN-64.)
- [ ] A reference to nothing is refused at validate, create, update and
      change-set creation alike, and leaves no stack.

**Resource types**
- [ ] For each of the 56 mapped types: every property is in exactly one of
      applied, cosmetic and said so, or refused. The table is checked in and a
      test fails when a mapper reads a property the table does not list.
- [ ] For each mapped type: create, update one property, drop from the
      template, each read back from the owning service.
- [ ] No mapped type reports COMPLETE with nothing made (CFN-2, CFN-3, CFN-4).
- [ ] Each ignored type appears in the stack's resources with a reason.
- [ ] Each refused type's message names the type; all refused types in a
      template are named, or the message says it names the first.
- [ ] Physical names: logical id when unnamed, sanitised per service; an
      explicit name never rewritten.
- [ ] Decide whether `AWS::IAM::*` is mapped now that IAM is served.

**Create**
- [ ] A refused create leaves no stack (CFN-6).
- [ ] A name already taken outside the stack fails that resource and rolls
      back; the existing resource is unchanged and survives the stack's delete.
- [ ] `DisableRollback`, `OnFailure=DO_NOTHING`, `OnFailure=DELETE`.
- [ ] A duplicate stack name; create over a DELETE_COMPLETE and over a
      REVIEW_IN_PROGRESS record.
- [ ] An export name another stack owns is refused before anything is made.
- [ ] `Capabilities` (CFN-19: IAM resources, named IAM, `AUTO_EXPAND` for a
      transform on create and update and not on a change set, and not with
      `ResourceTypes`), `Tags` and where they go (CFN-50),
      `EnableTerminationProtection` (CFN-45). *Corrected 2026-10-11: was "and where they go"; the model says where.*

**Update**
- [ ] No change is refused with "No updates are to be performed."
- [ ] A parameter-only update reaches every resource the parameter feeds (CFN-1).
- [ ] A dropped resource is deleted; a dropped Retain resource is kept.
- [ ] A changed physical name replaces the resource; what happens to the old
      one, with and without Retain, is established.
- [ ] A failed update restores template, parameters, resources and outputs;
      what it created is deleted; the previous template is re-applied.
- [ ] The three states an update is refused from, and REVIEW_IN_PROGRESS (CFN-26).
- [ ] Untouched resources keep their status and emit no event (CFN-17).

**Delete**
- [ ] Reclaims what the stack made; keeps Retain; keeps what it never made.
- [ ] A ROLLBACK_COMPLETE stack's delete touches nothing.
- [ ] Refused under termination protection, for a nested child, and while a
      live stack imports an export. Not refused by a deleted importer (CFN-7).
- [ ] A delete that cannot remove a resource says so (CFN-8).
- [ ] Idempotent for a missing stack. The record answers by id, not by name.

**Events and resources**
- [ ] The trail for create, update, delete and each rollback, in order, ending
      on a terminal stack event, with unique event ids.
- [ ] A failed resource's reason is readable (CFN-18).
- [ ] The 500-event bound keeps the newest.

**Change sets**
- [ ] CREATE makes a REVIEW_IN_PROGRESS stack; UPDATE on a missing stack is refused.
- [ ] Add, Modify, Modify with replacement, Remove; a property edit; a
      parameter edit (CFN-13).
- [ ] The empty set's exact status reason.
- [ ] Execute: AVAILABLE only; the outcome on the set when the stack rolls
      back; a second execute; a stale set (CFN-14).
- [ ] Validation at creation (CFN-15).

**Exports and nested stacks**
- [ ] `ListExports`, `ListImports` by real import, not substring (CFN-21).
- [ ] Nested: five deep, a cycle refused, child outputs, child names, parent
      delete marks children, child delete refused, a dropped child's record removed.

**Provision, shared with the CLI**
- [ ] Apply twice is a no-op. `Existing` knows every kind `Apply` makes.
- [ ] `Destroy` removes every kind `Apply` makes, and reports what it could not.
- [ ] `Export` then `Emit` then `Parse` then `Transpile` for every kind;
      secret and SecureString values are blank.
- [ ] Order: every reference a mapper can emit has its target applied first (CFN-22).

**Lifecycle**
- [ ] Stacks, change sets, events and nested bodies survive a restart.
- [ ] Each deploy smoke under `e2e/deploy` passes; each is widened in stage 17.

## 6. Console

The design's "CloudFormation" page: 21 boards. Templates:
`internal/console/templates/cfn.html` (`cfn_lp`, `cfn_home`, `cfn_stack`,
`cfn_validate_result`, `cfn_params`, `cfn_resource_detail`) and the `cfn_create`
block in `create.html`. Handlers: `handlers_cfn.go`; client `client_cfn.go` and
`client_cfn_export.go`; routes `cfnRoutes` in `routes_stackwide.go`; the
"Start from what's running" script is `dozeCfnStarter` in `static/editor.js`.

| Board | Screen | Lands in | Today |
|---|---|---|---|
| Cfn-Home | Stacks, exports, deleted stacks | `cfn_home`, `cfnStacks` | exists; the title row with the count and the section sentences are **new** (6a) |
| Cfn-01-None | No stacks yet | `cfn_home`, `empty_detail` | exists, with the button (`New stack`): *Corrected 2026-10-11: was "the button is new"; the page has one since A7.* The "How to point a deploy tool" link is **new** |
| Cfn-02-Create | Create stack | `cfn_create` | form exists; the strip and field lines are **new** |
| Cfn-03-Create-Loaded | Validated, parameters answered, CLI open | `cfn_create`, `cfn_validate_result`, `cfn_params` | exists; the strip, the export check and the limits line are **new** |
| Cfn-04-Create-Refused | Create refused | `fail_inline` in `#cfn-validate-out` | exists |
| Cfn-05-Export | Start from what's running | `cfn_create`, `cfnExportTemplate`, `dozeCfnStarter` | exists |
| Cfn-06-Resources | Resources, parameters, outputs | `cfn_stack` default tab | exists; Retain pill and the role note are **new** |
| Cfn-07-Resource-Open | One resource opened | `cfn_resource_detail`, `cfnResource` | exists |
| Cfn-08-Events | Events | `cfn_stack`, `tab=events` | exists |
| Cfn-09-Template | Template | `cfn_stack`, `tab=template` | exists; the two notes are **new**; two columns is layout only |
| Cfn-10-Review-Pending | A stack awaiting review | `cfn_stack`, `tab=changesets` | exists; the callout is **new** |
| Cfn-11-Update | Update stack | `cfn_stack`, `tab=update`, `cfnUpdate` | form exists; full parameter list and the strip are **new** |
| Cfn-12-Update-Review | Update as a change set | same | exists; "What the diff will show" is **new** |
| Cfn-13-Update-Refused | Update refused | `fail_inline` at the form's end | exists |
| Cfn-14-ChangeSets | Change sets and one diff | `cfn_stack`, `tab=changesets&cs=` | exists |
| Cfn-15-Confirms | Four confirms | `hx-confirm` on Delete, Execute, Delete change set | exist; the Retain and rolled-back sentences and the counts are **new** |
| Cfn-16-Delete-Refused | Delete refused | `fail_inline` under the header | exists. *Corrected 2026-10-11: was "needs A6; today a toast"; A6 landed and the refusal draws under the header (run, screenshot).* The explanatory paragraph is **new**. |
| Cfn-17-Rollback-Create | A create that was undone | `cfn_stack`, `tab=events` | trail exists; the callout is **new** |
| Cfn-18-Rollback-Retain | A rollback that kept a bucket | same | trail exists; the callout and Retain pill are **new** |
| Cfn-19-Rollback-Update | An update that was undone | same | trail exists; the callout is **new** |
| Cfn-20-Left-Behind | A refused create's leftover | same | exists; the callout is **new**. The board goes when CFN-6 is fixed. |

There is no Denied board. The console acts as root and a stack has no resource
policy, so no stack action can be denied in its own right. A peer's denial
during apply is a failed resource, not a denied call; that is section 9.

**New items, and what each is built from**

| Item | Built from |
|---|---|
| "Create a stack" on the empty pane | `copy.go`'s `CTA` and `CTAPath`, unset for `cfn` (A7) |
| What this deploy will do: made, accepted, refused | `GetTemplateSummary`'s `ResourceTypes` against `cfn.IsMappable`, `ignoredTypes` and `classify`. `classify` and `ignoredTypes` are unexported: an exported classifier is needed. |
| Export names and "no other stack claims either" | the template's `Outputs` (`cfn.Parse`) against `ListExports` |
| The limits line under a parameter | `cfn.Parse`'s `Parameter` (AllowedValues, Min, Max, pattern). `GetTemplateSummary` here does not return them. |
| Every declared parameter on Update | `backend.TemplateSummary(ctx, "", stack)`, which exists and is unused for this |
| What an update does; what the diff will show | Static text. Each sentence is a section 5 box. |
| Retain pill, "Kept", the role note, the Template notes | the stored template (`GetTemplate`, `cfn.Parse`): `DeletionPolicy` and the type's class |
| Counts in the Execute confirm | the change list already fetched for the tab |
| The rolled-back stack's delete sentence | the stack's status |
| Rollback and leftover callouts | the stack's status, and the reason on its ROLLBACK_IN_PROGRESS or failed event |

**Teaching lines, and where their facts come from**

| Line | Source |
|---|---|
| Stack name: a letter, then letters, digits, hyphens, up to 128 | `stackName` in `stacks.go`. *Corrected 2026-10-11: was "the model's pattern"; the model has no pattern trait on `CreateStack.StackName`; the rule is its prose: "can contain only alphanumeric characters (case sensitive) and hyphens. It must start with an alphabetical character and can't be longer than 128 characters" (model, prose). The `pattern` traits exist only on `StackNameOrId` (name or ARN).* |
| Change set name: a letter, then letters, digits, hyphens, 1 to 128, unique in the stack | model: `ChangeSetName` `length 1..128`, `pattern ^[a-zA-Z][-a-zA-Z0-9]*$`; unique: enforced after CFN-53 *(added 2026-10-11)* |
| Template body: JSON or YAML, at most 51,200 bytes; a URL, at most 1 MB | model (prose). **Not enforced today** (CFN-47): line 4 says *stored, not checked* until it is *(added 2026-10-11)* |
| Parameters: up to 200; a value up to 4,096 bytes | doc (quotas page), not enforced (CFN-47) *(added 2026-10-11)* |
| No stack-name prefix, no random suffix | `SUPPORT.md`, "Physical names are logical IDs" |
| A SAM template is understood; any other Transform is refused | `Transpile`, read (section 6, not yet backed) |
| A blank input takes the default (create) or keeps the value (update) | `formParams`, `hUpdateStack`'s merge. *Corrected 2026-10-11:* true of the console, and it has to be built on `UsePreviousValue=true` (CFN-44), because the API's omitted parameter is the default, so the sentence is the console's, not AWS's. |
| A number, enforced; one of the allowed values, enforced | `Parameter.check` |
| Nothing deploys until you execute; never CREATE_IN_PROGRESS | `StatusReviewInProgress`; `SUPPORT.md`, "Everything is synchronous" |
| Read whole, made, if one fails, recorded | `deploy`, `refuseTaken`, `settleFailure`, `refuseExportConflict` |
| Retain is kept by a rollback, a dropping update and a delete | `retained` in `rollback.go`; run for rollback and delete |
| A parameter on its own may show nothing | CFN-13. The line is removed when CFN-13 is fixed. |
| The stack records only the values it was given | CFN-11. Removed when fixed. |
| A stack keeps its latest 500 events | `boundEvents` |

The parameter lines are not in the foundation's settings table (B4): a
template's parameters are the user's, not the service model's. This stage adds
a second source to that accessor, or builds these lines beside it.

**Statements on the boards not yet backed.** Remove or prove each.

| Board | Statement | State |
|---|---|---|
| Cfn-02 | Any other Transform is refused | **run 2026-10-11** (`AWS::Include` → "unsupported Transform ... doze-aws understands only the SAM transform"). Note: `CAPABILITY_AUTO_EXPAND` is then required for a SAM template on `CreateStack` (CFN-19) and the board's form has no control for it |
| Cfn-04 | The export-conflict message | read; `export_conflict_test.go` holds the behaviour, the text not checked |
| Cfn-05 | Secret and SecureString values are left blank | docs, not run |
| Cfn-05 | A name in the exported template that is already running fails that resource | follows from taken names; not run end to end |
| Cfn-12 | `console-52219` | an example; the real default is `console-` and the time mod 100000 |
| Cfn-14 | "what the AWS CLI reads as nothing to deploy" | doc claim, section 8 |
| Cfn-16 | The nested-stack refusal's text and ARN | read, not run |
| Cfn-16 | Deleting the parent removes the child's resources | read |
| Cfn-17, Cfn-19 | ROLLBACK_FAILED names the resources; UPDATE_ROLLBACK_FAILED says why | read |
| Cfn-04, Cfn-20 | "AWS would take this template" for an ECS cluster | true by definition of the limit; the callout's colour depends on A2's "Refused here" |
| Cfn-08 | "no change means apply found the resource as the template describes it" | read; false for a parameter or secret that was skipped (CFN-1): **run 2026-10-11** (`EarnRateParam` shows "no change" while its value is the old one). Whether AWS emits events for an unchanged resource is `memory` (CFN-17) |
| Cfn-06 | "Only values that were supplied are listed" (a defaulted parameter is not shown) | true of the code (CFN-11); whether AWS lists defaults is `memory`: the board changes if the recording says it does |
| Cfn-Home | "Export names are unique within the account and region" | `memory`/doc not read: `ListExports` "in the account and Region in which you call this action" is the model's; "unique" is the code's refusal (`refuseExportConflict`, run) |
| Cfn-03 | Template with `AWS::IAM::Role` creates without a capability | **wrong** about AWS (CFN-19); corrected in the working copy, 6a |
| Cfn-14 | "Made from Update" | **wrong**: tools make change sets (`sam deploy`, `cdk deploy`); corrected, 6a |
| Cfn-09 | "An IAM role has no local analogue" | **wrong**: IAM is served; corrected, 6a |
| Cfn-06, Cfn-18 | Resource rows show DELETE_COMPLETE for what still exists | true today; the boards change with CFN-12 |

### 6a. Design fidelity, board by board

Written 2026-10-11, **before** any CloudFormation console work, by rendering
each of the 21 boards (`console-design/project/Cfn-*.dc.html`, 1440 by 900,
light; dark for Cfn-02, 03, 06 and 14) and the real page in the same state
(light, 1440 by 900; dark for Cfn-02 and 06; 900 wide for Cfn-02, 06 and 08), by
driving a binary with a spec that made eleven stacks: an updated stack with a
retained bucket, an exporter and its importer, a review-only stack, a create
and an update that rolled back on a taken name, a rollback that kept a bucket,
an unserved kind, a termination-protected stack, a nested stack and a deleted
one. Images: `scratchpad/dcheck/cfn/design-*.png` and `real-*.png`. **No board
matches today**; the SQS lesson was that a table written at the end finds six
boards, so this one is written first and is the build list.

The chrome (rail, list-pane head with count and filter, `res_header` fact chips,
the stack's status as a bordered chip, the action bar) is the foundation shell
and is **differs, deliberately** on every row, as in SQS 6a: not re-opened here.
The rail keeps the per-service AWS icons (L12); where a board draws `CF`, `SQ`,
`DB`, `S3` the page draws the service's icon, and the resource table gets the
owning service's icon per row.

| Board | Verdict | What differs |
|---|---|---|
| Cfn-Home | differs | A title row with the stack count and a *Create stack* button, and two sentences ("The registry Fn::ImportValue resolves against. Export names are unique within the account and region."; "The record the stack list no longer shows. A deleted stack is kept as DELETE_COMPLETE so a deploy tool polling it by id sees it finish.") sit above the tables; the real home is two section titles with a dim suffix and no sentence, no count, no button. The export value is cut with an ellipsis in a narrow column (the design gives it the width). The link "what that looks like" has no target on the page. |
| Cfn-01-None | differs | The design's mark, a heading, the copy, a primary *Create a stack* with `new`, and "How to point a deploy tool at 127.0.0.1:4566". The real page is the shell's empty state: icon, one sentence in `copy.go`'s voice, *New stack*. The sentence differs ("Stacks appear here when you deploy" against "Create one from a template here, or deploy one"); the foundation owns the voice (A7), so the sentence is its, the link is **build**. |
| Cfn-02-Create | **differs** | **Two columns** on the design: the form left, a "What this deploy will do" card right (made / accepted / would be refused, then four lines: read whole, made, if one fails, recorded). The real page is one column of four panels (General, Template, Parameters, How to deploy). Stack name has five lines on the design (name, "A letter, then letters, digits and hyphens. An underscore is refused.", "up to 128 · enforced", the naming sentence); the real input has a placeholder and a `pattern` and nothing under it. Template: no line about the dialect, no size limit (and none enforced, CFN-47). "Parameters" and "How to deploy" are side by side on the design and stacked on the page. The *cli* control is bottom-left of the action bar on the design and an inline block in the form on the page. |
| Cfn-03-Create-Loaded | differs | The right card is computed (5 made with names and kinds, 1 accepted with its reason and **the capability**, 0 refused, two export names, "if a resource fails" with *Statements is Retain*); the page has none of it. Parameters: two-up, with "EarnRate · Number", the description, "a number · enforced", the allowed values with `new` and "enforced"; the page has one input each with `placeholder="default: …"` and the type in the label. The valid banner is a green well on the design and a line of plain text on the page. The CLI line must carry `--capabilities` (design corrected). |
| Cfn-04-Create-Refused | differs | The refusal is inline under the editor on both (shape matches). The design adds the sentence "AWS would take this template. doze-aws serves the services in the rail, and a kind outside them refuses the whole template: Jobs was not made either. Only the first unserved kind is named." and "What you typed is kept. Validate says this same template is valid"; the page prints the foundation's generic line ("doze-aws refused the request: it breaks a rule of the service"). **And the page leaves a `CREATE_FAILED` record** (the stack list showed `harbour-fleet2`): CFN-6. The design's right-hand column of other refusals is commentary, not a page element. |
| Cfn-05-Export | differs | Content matches (the editor holds the exported template; the armed button says "Replace what's there?", tested). The design adds three sentences under the editor ("What it writes: everything running here, not one stack's resources... Secret and SecureString values are left blank..."; "It is a starting point...") that the page does not print. |
| Cfn-06-Resources | **differs** | Per-row service marks (the page draws none); the *Retain* pill and `new` mark on the bucket; a sentence under the table (physical-name rule, the unlisted role, Retain from `DeletionPolicy`); tab counts ("Resources 6", "Change sets 4"); the status in the heading beside the name (the page puts it in a chip row). Parameters and Outputs: the design lists an export under its key and gives the value the width; the page puts the export name inside the narrow key column, which wraps to six short lines, and the value wraps onto a second line. The note "Only values that were supplied are listed" is the CFN-11 state. |
| Cfn-07-Resource-Open | differs | The panel on the design is beside the "when the resource is no longer in the stack" refusal and replaces Parameters and Outputs; the page opens it full width under the table and keeps the two panels below. The selected row is not marked on the page; `esc` closes on the design only; the panel on the design has a title bar with `esc`. |
| Cfn-08-Events | differs | One sentence above ("Newest first. When a deploy fails, the reason is at the end of the run, so that is where this starts."), the stack's own rows in bold, a folded "earlier events: the create" row, and two notes below ("no change means...", "A stack keeps its latest 500 events. Time is shown to the minute."); the page has the section title and the table. The trail is today's (CFN-17: every resource twice, "no change"): the board **draws the bug**. |
| Cfn-09-Template | differs | Two note cards above the code ("AccrueRole is accepted and not made", "Statements is Retain"), the code in two columns without scrolling and pretty-printed as YAML; the page has one scrolling box of the submitted text (the JSON as sent). |
| Cfn-10-Review-Pending | differs | The "Nothing is deployed yet" callout; the Replacement cell is "—" for Add (the page prints "in place": CFN-32); the type column has the owning mark; the two annotation cards at the foot ("The Resources tab says", "The Events tab has") are commentary. The page opens the Resources tab for this stack, which is empty with a sentence about review (CFN-33 is about the other states). |
| Cfn-11-Update | **differs** | Two columns: a "What an update does" card right (four lines, `new`); the form left with the template **as deployed, editable**, the "Use previous template" switch as a segmented off/on in the card's head, parameters two-up with their type and teaching lines and **every declared parameter**, including `Stage` that took its default (the page lists only the supplied ones: CFN-11, CFN-28), and "Create a change set instead" as a card. The page has no CLI preview (CFN-36). |
| Cfn-12-Update-Review | differs | With the switch on: "This will not deploy. It makes the change set console-52219 and opens it under Change sets" and a "What the diff will show" card (Add, Modify, Remove, Replaced; "A parameter on its own may show nothing" = CFN-13, the bug); the page shows a name field and nothing else. |
| Cfn-13-Update-Refused | differs | The refusal is inline on both; the design is placed under the form's end and carries "The template and the parameters are the ones the stack already has, so there is nothing to apply... Deploy tools rely on this answer"; the page prints the foundation's line and puts it after the sticky bar. The right column is commentary. |
| Cfn-14-ChangeSets | **differs** | Newest first with the created time (the page sorts by name); a right-hand card "empty-one — why it is FAILED" with the sentence and the CLI note (the page prints the reason as a line under the opened set's title); Replacement as an amber *replaced* chip, "—" for Remove (the page: "in place" for Remove, CFN-32); the note "Made from Update..." (**corrected**: tools make change sets too); marks per type. |
| Cfn-15-Confirms | **differs** | Dialogs with bodies, the Retain line (`Kept: Statements`), the rolled-back line (`Left as it is`), the counts in the execute confirm ("1 added, 2 modified (1 replaced), 1 removed"), and the verbs "Keep it / Delete stack", "Not yet / Execute". The page asks "Delete stack “x”?" with "Everything it created goes with it." (**false** for a Retain resource, run: the bucket stays) and `Cancel` / `Delete`; "Execute change set “x”? The reviewed template deploys." The execute sentence on the design says "as an update to harbour-loyalty": for a CREATE-type set (Cfn-10) it must say "creates". |
| Cfn-16-Delete-Refused | differs | Inline under the header on both (A6 landed: shape matches). The design adds the paragraph with two links ("Deleting the stack would remove the export …, and harbour-worker imports it. Delete the importer first, or deploy it without the import") that the page lacks; the nested stack's list row reads "nested under harbour-platform" on the design and is cut to "harbour-platform-Data …" on the page. The two lower cards (termination protection, nested) are commentary for two *other* stacks. |
| Cfn-17-Rollback-Create | differs | The "The create failed and was undone" callout (resource, reason, what was touched, what Update and Delete do), failed rows tinted, the Resources-tab account and "Then what" cards below. The page: a red status chip and the trail. |
| Cfn-18-Rollback-Retain | differs, waits | As Cfn-17, plus the *Retain* pill and "kept; still in S3" on the bucket's rows. **The trail shows `DELETE_COMPLETE` for a bucket that exists (CFN-12); the model has `DELETE_SKIPPED`**: the board is built after CFN-12 and a recording. The failed resource's reason is the wrapper and the raw body on both (CFN-18); the design shows it in full in a card. |
| Cfn-19-Rollback-Update | differs | The "The update failed and was undone" callout, "What was put back" and "Then what" cards; the page: the red status chip and the trail. The status text is correct on both (`UPDATE_ROLLBACK_COMPLETE`). |
| Cfn-20-Left-Behind | **design is wrong, goes with CFN-6** | The board is the P0 bug drawn as a design: a refused create leaves `CREATE_FAILED`, "its name is still taken", and a corrected create is `AlreadyExistsException`. AWS refuses a malformed create at the call and leaves no stack (`memory` for the second half; the message is `doc`). The page shows exactly this today. Dropped when CFN-6 is fixed; until then the board is not built. |

**Where the design is wrong** (corrected in the working copy on 2026-10-11 in
Cfn-03, Cfn-09 and Cfn-14; the published canvas carries the old text until it is
republished)
- **Cfn-03**: a template with an `AWS::IAM::Role` is created with no
  acknowledgement and the CLI line has no `--capabilities`. The model:
  "If you don't specify either of these capabilities, CloudFormation returns an
  InsufficientCapabilities error". *Changed:* the CLI line carries
  `--capabilities CAPABILITY_IAM`; the "accepted, with no local effect" item says
  AWS asks for it and the form carries the acknowledgement (`new`). The control
  itself is a build item (below), and is not drawn.
- **Cfn-09**: "An IAM role has no local analogue". IAM is served (the document
  says so in section 2). *Changed to:* "A stack does not make IAM resources here:
  no IAM evaluation during apply, and the iam service is not asked." True before
  and after decision 2.
- **Cfn-14**: "Made from Update, with 'Create a change set instead' on." A change
  set is also made by `sam deploy`, `cdk deploy` and the CLI (the service's own
  header comment). *Changed to:* "Made from Update with 'Create a change set
  instead' on, or by a deploy tool (sam, cdk, the CLI)."
- **Cfn-20, Cfn-04's "leaves a failed stack behind"**: depict CFN-6 as intended.
  Not edited: the finding, not the board, is what changes.
- **Cfn-06, Cfn-08, Cfn-12, Cfn-18**: draw CFN-11, CFN-17, CFN-13 and CFN-12 as the
  state of the world. Not edited (the corrections are `memory`, nothing is
  fixed on it): they are re-rendered when each finding is closed, and the
  console item is built **after** its finding.
- **Cfn-11**: "Blank keeps 15" is the console's rule and only works by sending
  `UsePreviousValue` (CFN-44). Not wrong; a build constraint.
- **Cfn-02, Cfn-03**: no line for the 51,200-byte template limit or the 200
  parameters. Add them as *stored, not checked* until CFN-47, then *enforced*.

**Build list, in order**
1. **Prerequisites that are not console work:** an exported classifier
   (`cfn.Classify`: made / accepted with a reason / refused with a reason) for
   the right-hand cards; `Capabilities`, `CapabilitiesReason` and parameter
   constraints from `GetTemplateSummary` (CFN-58); `UsePreviousValue` on the
   console's update (CFN-44); `Capabilities` enforcement (CFN-19) in the same
   change as 3 below.
2. **Shared pieces:** the per-row owning-service mark (`awsIcon`), the *Retain*
   pill, tab counts, the explanatory-sentence line under a table, the callout
   component (one for Cfn-10, 17, 18, 19) and the `res_header` kit's status
   beside the name.
3. **Create:** two columns; "What this deploy will do" (Cfn-02, 03); the five
   lines on stack name, change-set name, template body and each parameter; the
   *cli* control as the design places it; the capability acknowledgement; the
   card's export check against `ListExports`.
4. **Create refused, Update refused:** the "why" sentences of Cfn-04 and Cfn-13
   (A2's states: *Refused* for the input, *Refused here* for an unserved kind).
5. **Resources tab:** marks, pill, note, tab counts, Parameters and Outputs layout
   (key and export in two lines, the value wide), the inline resource panel
   with the selected row and `esc`.
6. **Template tab:** the two notes and two-column pretty-printed code (the
   pretty print needs the template as parsed, not the submitted text: the
   notes are read from `cfn.Parse`).
7. **Change sets:** newest first, the FAILED-reason card, *replaced* chip, "—"
   for Add and Remove (CFN-32), "Made from…" sentence; the review-pending
   callout and the default tab for a `REVIEW_IN_PROGRESS` stack.
8. **Update and update review:** two columns, "What an update does", every
   declared parameter with its lines, "This will not deploy" and "What the diff
   will show", the CLI preview (CFN-36). Behaviour: CFN-44.
9. **Confirms:** bodies, verbs, counts, the Retain and "left as it is" lines, the
   create-versus-update sentence on Execute.
10. **Delete refused:** the paragraph with the two links; the nested label
    untruncated.
11. **Events and rollbacks:** the intro and footnotes, bold stack rows, the fold;
    the three callouts. **Only after CFN-12, CFN-17 and CFN-18** (the trail they
    draw changes); Cfn-20 is not built.
12. **Home and empty:** the title row, the sentences, the export value at full
    width, "How to point a deploy tool at …" (to `Connect`).
13. **Narrow:** at 900 wide the resources table clips the Status column and
    the Outputs panel overflows (screenshot); the shell's 1024 notice says it is
    out of range, so this is the shell's call (list, do not build) unless the
    table is given a minimum.

## 7. Tests to add

| Layer | Tests |
|---|---|
| Transpile (`internal/cfn`) | Unknown and unapplied properties per type (CFN-5); the property table against the mappers; every `GetAtt` attribute; every pseudo parameter incl. `AWS::NotificationARNs` and `AWS::StackId` (CFN-20, 64); an unnamed FIFO queue (CFN-59) |
| Provision | Update of each kind (CFN-1, CFN-23); order by reference (CFN-22, SQS-1); a destroy that fails; `Existing` for every kind |
| Stack handler | CFN-2, 3, 4 (something is made); CFN-6, 7, 8, 9, 14, 15, 21, 26; event trails (CFN-12, 17, 18); the *added 2026-10-11* set: ids (40), update drops (43), parameter defaults (44), termination protection (45), exclusivity (46), sizes (47), change-set type, duplicate and members (52 to 55), `GetTemplate` (56), `DescribeStackResources` (57) |
| SDK v2 | CFN-10, 11, 16, 20, 25; `Capabilities` (CFN-19). *Added:* every `REQUIRED` member of every response is non-zero (41, 42, 55, 58); `ClientRequestToken` on the events (51); `AssertPaginates` over the seven list operations |
| Wire | *Added 2026-10-11:* all 23 operations posted as the reference spells them (accepted); the 67 stubs 501 with the header (CFN-24); a violation in the second and third element of each list (CFN-49), 20 repeats |
| Rejection parity | The 182 cases stay; regenerate with `dzaudit cases cloudformation` for list lengths (CFN-48); the stub code is decided (501, D5); exclusivity and size cases (CFN-46, 47) are hand-written beside them |
| Restart | A stack with a change set, a nested stack, and a rolled-back stack; a deleted stack by id with its resources (CFN-40, 42); an executed change set and a token (CFN-51) |
| Console handler | CFN-27 to CFN-35; every `hx-confirm` in `cfn.html` read against the code |
| Conformance (boto3) | Every row in section 8; parameter constraints; `NoEcho`; change-set shapes; delete with Retain; exports and imports |
| Browser | Each of the 21 boards; each `new` item; refusals inline at the three places they land; keyboard on the list and both forms |
| Deploy smokes | Unchanged here; widened in stage 17 |
| Mutation | Baselines for `cloudformation/`, `internal/cfn/`, `internal/provision/` |

## 8. Recording queue

Every one of these is `model`, `doc` or `memory` today; nothing is recorded (there is no AWS account). *Re-tagged 2026-10-11:* a `doc` here now means a page was read in the pre-stage review and is named; the rest that said `doc` are `memory`.

| Claim | Source now | Scenario |
|---|---|---|
| A `NoEcho` value reads back as asterisks | doc (*Template Parameters syntax*, read) | `noecho_masked` |
| `DescribeStacks` returns defaulted parameters | **memory** (was doc) | `parameters_include_defaults` |
| A missing parameter is refused and no stack exists; its message | message doc (`Parameters: [KeyName] must have values`), "no stack exists" memory | `missing_parameter` |
| A supplied parameter the template does not declare; its message | memory | `undeclared_parameter` |
| Each parameter-constraint message | the pattern one doc ("Malformed input-Parameter MyParameter must match pattern"), the rest memory | `parameter_constraints` |
| An unknown resource property is refused, and when | memory | `unknown_property` |
| IAM resources without `Capabilities` is InsufficientCapabilitiesException; `AUTO_EXPAND` on create and update, not on a change set | model | `capabilities_required` |
| A Retain resource in a rollback is DELETE_SKIPPED | model (the status exists), memory (when it is written) | `rollback_retain_trail` |
| A taken name's reason text, per type | **memory** (was doc) | `taken_name` (exists, unverified) |
| The trail of a one-property update: which resources emit events | memory | `update_trail` |
| A resource added by an update that fails is CREATE_FAILED | memory | `update_adds_failing_resource` |
| The ROLLBACK_IN_PROGRESS reason, "Rollback requested by user." | memory | `rollback_reason` |
| A parameter-only change set lists the resources it changes | model ("compares the stack's information"), memory (the detail) | `changeset_parameter_only` |
| The empty change set's status (`FAILED`, `UNAVAILABLE`) and reason text | status model, reason **memory** (was doc) | `changeset_empty` (in `test_change_set`, unverified) |
| Other change sets are removed when a change set is executed | model, doc (*Execute a change set*, read) | `changeset_stale` |
| Updating a ROLLBACK_COMPLETE stack's message | **memory** (was doc) | `update_rolled_back` |
| Updating a REVIEW_IN_PROGRESS stack | memory | `update_in_review` |
| "No updates are to be performed." | **memory** (was doc) | `update_no_change` |
| Deleting an exporter with a live importer; its message | the refusal model (`ListImports` prose), the message memory | `delete_exporter` |
| A delete that cannot remove a resource is DELETE_FAILED | model | `delete_failed` |
| `Description`, and `StackId` on a change set | model | `describe_fields` |
| What `AWS::StackId` resolves to | doc (pseudo-parameters page, read) | `pseudo_parameters` |
| The code and message for a delete under termination protection | **memory** (was doc; the model says only "the operation fails and the stack remains unchanged") | `termination_protection` |
| Pagination: "1 MB" for stacks, events, resources, change sets; 100 for exports | model; the count of events per page memory | `pagination` |
| *Added 2026-10-11.* A repeated `ClientRequestToken` (same stack and operation; another stack) | memory (the error exists: model) | `client_request_token` |
| `ExecuteChangeSet` with `DisableRollback` omitted: rolls back or not (the model says "Default: True") | model contradicts every tool; **memory** | `execute_change_set_default_rollback` |
| `CreateChangeSet` `CREATE` against an existing stack; no type for a missing stack | model (the rule), memory (the message) | `change_set_type_rules` |
| A duplicate change set name | model (`AlreadyExistsException`) | `change_set_duplicate` |
| An unnamed FIFO queue's generated name ends `.fifo` | memory | `unnamed_fifo_queue` |
| Stack tags on the created resources, and the `aws:cloudformation:*` system tags | model (propagation), memory (system tags) | `stack_tags_propagate` |
| The body of a stack-event notification to `NotificationARNs` | memory | `notification_body` |
| A `TemplateBody` over 51,200 bytes: the code and message | model (the limit), memory (the message) | `template_too_large` |
| `DescribeStackResources` by `PhysicalResourceId`; both arguments | model | `describe_resources_by_physical_id` |
| `GetTemplate` with a change set name, with an unknown one (404 `ChangeSetNotFound`), `TemplateStage` of a SAM stack | model | `get_template_stages` |
| A deleted stack by id: `DeletionTime`, resources, events, template | model | `deleted_stack_by_id` |
| An update that omits a parameter: the default is used, or the update is refused | model | `update_omitted_parameter` |
| `Capabilities` and `ResourceTypes` together; `OnFailure` with `DisableRollback`; body with URL | model | `exclusive_members` |

## 9. Owed to integration

| What | With |
|---|---|
| A queue and its DLQ in one template (SQS-1). *Corrected 2026-10-11: closed in the SQS stage (`cloudformation/dlq_order_apply_test.go`); what remains is the same template through SAM, CDK and Terraform, which the deploy smokes widen* | SQS |
| An SSM parameter's value, and a secret's, changed by an update (CFN-1) | Parameter Store, Secrets Manager |
| A stream declared in a template exists (CFN-2) | Kinesis |
| A custom bus and a rule on it (CFN-3) | EventBridge |
| A queue policy (done: SQS stage), a topic policy and a Lambda permission from a template decide access under enforce (CFN-4) | SNS, Lambda, IAM |
| Stack tags on the resources of every kind that has tags (CFN-50); stack events published to `NotificationARNs` (CFN-64); a rollback trigger's alarm (decision 9) *(added 2026-10-11)* | each service, SNS, CloudWatch |
| An IAM role's `CAPABILITY_IAM` and the role existing in the iam service when a Lambda assumes it (decision 2) *(added)* | IAM |
| An IAM role in a template, if the decision is to map it | IAM |
| A peer's denial during apply under enforce: the failed resource and its reason | IAM |
| Per mapped type, the property table read back from the owning service (CFN-5, CFN-23) | all fourteen |
| A failed resource's reason for each peer's error shape (CFN-18, foundation A1) | all |
| `TemplateURL` and nested children fetched from a staged object | S3 |
| Function code from S3 and from `_local_` | Lambda, S3 |
| `Export`, `Emit`, redeploy into an empty instance, for a stack with every kind | all |
| The four deploy smokes widened from "it deploys" to "it deploys, updates, and deletes clean" | all |
| The stack's resources on the map | the wiring graph |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] CFN-1 to CFN-68 each closed, with the test that failed first named beside it. *Corrected 2026-10-11: was CFN-1 to CFN-39.*
- [ ] No mapped type reports COMPLETE with nothing made. `AWS::Kinesis::Stream`,
      `AWS::Events::EventBus`, `AWS::SNS::TopicPolicy` and
      `AWS::Lambda::Permission` are each functional, or ignored and listed as
      such, or refused. (*Corrected 2026-10-11: `AWS::SQS::QueuePolicy` is
      done, SQS stage.*)
- [ ] The per-type property table is checked in beside the mappers, a test
      holds it against them, and it has no "not established" cell.
- [ ] Decided and written into `SUPPORT.md`: whether a refused create leaves a
      record (CFN-6); whether `AWS::IAM::*` is mapped; whether `Capabilities`
      is required (CFN-19: **decided by the model, yes**); the code for a refused
      operation (CFN-24: **decided, D5**); what an unknown property does (CFN-5);
      what `Tags` on a stack reach (CFN-50: the model says the resources).
      *Added 2026-10-11:* the eleven decisions in the pre-stage review, each
      answered yes, no or changed.
- [ ] Every "not established" in section 2 resolved to F, C or S and tested:
      an explicit empty parameter value, `Rules`, template `Metadata`, `Hooks`,
      `DeletionPolicy: Snapshot`, `UpdateReplacePolicy`, `UpdatePolicy`,
      `CreationPolicy`, AWS-specific and SSM parameter types, and the `GetAtt`
      attribute table. (*Corrected 2026-10-11: `RetainResources`, `DeletionMode`,
      `ChangeSetType=IMPORT`, `IncludeNestedStacks`, `EnableTerminationProtection`
      on create, `TemplateStage`, `ChangeSetName` on GetTemplate,
      `AWS::NotificationARNs` and `AWS::Serverless::LayerVersion` are established,
      CFN-45, 54, 56, 63, 64; they are findings, not unknowns.*)
- [ ] `UpdateTerminationProtection` is F in the ledger. `SetStackPolicy` stays
      C and the console says so if it ever shows one. `CancelUpdateStack` is
      listed as a refusal and says why.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] An update of every mapped kind changes the resource, or the ledger says
      which properties are create-only.
- [ ] All 21 boards implemented from the foundation's shell, or the document
      says which were dropped. Cfn-20 is dropped if CFN-6 removes the record.
- [ ] **The fidelity table (6a) exists and was re-made from rendered boards
      beside rendered pages when the console work finished** (the one above is
      the *before*): every row is "matches", or "differs, deliberately" with the
      reason beside it. No "differs" without a decision. *Added 2026-10-11.*
- [ ] Icons kept: the rail, the list-pane head and the resource header draw
      `awsIcon "cfn"`; each resource row draws its owning service's icon; no
      two-letter mark from a board reaches a page. *(L12)*
- [ ] The five teaching lines are on every setting the console shows: stack
      name, change-set name, template body (limit; *stored, not checked* until
      CFN-47), each parameter (type, value in words, what it does, range and
      *enforced*, neighbour). *(L11)*
- [ ] One wire (`awsQuery`): all 23 operations accepted as spelled (table
      test), a violation in element 2 and 3 of each list refused, and every
      `REQUIRED` response member present. *(L5)*
- [ ] Each asynchronous operation decided and written in `SUPPORT.md` (the L7
      table in section 1): create, update, delete, create change set, execute,
      cancel, rollback, continue rollback. *(L7)*
- [ ] Ids are tokens: stack id, change set id, `ClientRequestToken`,
      `NextToken`, each with its stale and foreign case. *(L6)*
- [ ] The three boards corrected in the working copy (Cfn-03, 09, 14) are
      republished with the canvas.
- [ ] The resource classifier the console uses is the one `Transpile` uses.
- [ ] No statement on a CloudFormation screen is in the "not yet backed" table.
- [ ] The four confirms say only what the code does, including for Retain and
      for a rolled-back stack.
- [ ] `e2e/tests/cloudformation.spec.ts` and `cfn-starter.spec.ts` cover every
      route under `/cfn` with the route gate on.
- [ ] `docs/cloudformation.md`'s three lists are generated from, or tested
      against, `nameProperty` and `ignoredTypes`.
- [ ] Conformance: a scenario for every row in section 8. Zero "answered
      differently". The unverified count is written here: ____ (9 scenarios today; about 33 claims, section 8).
- [ ] Mutation baselines recorded here for the three packages: ____.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files | Days |
|---|---|---|---|
| 1 | Contract and decisions (the eleven in the pre-stage review); the property table | `docs/SUPPORT.md`, `docs/not-built.md`, `docs/cloudformation.md`, a table beside `internal/cfn/resources.go` | 1.5 |
| 2 | Red tests for CFN-1 to CFN-26 and CFN-40 to CFN-68 (the wire table, list elements, required members first: they are cheap and find the rest) | `cloudformation/*_test.go`, `internal/cfn/*_test.go`, `internal/provision/*_test.go` | 2.5 |
| 3 | Types that make nothing; dropped properties (56 kinds) | `internal/cfn/mapper.go`, `mapper_*.go`, `resources.go`, `internal/provision/model.go` | 5 |
| 4 | Update convergence and ordering | `internal/provision/apply.go`, `svc_ssm.go`, `svc_secrets.go`, the other `svc_*.go`, `existing.go`, `destroy.go` | 3 |
| 5 | Stack lifecycle: refused create, delete, importers, events, reasons, ids as tokens, update drops, tokens and operation ids, capabilities, tags | `cloudformation/stacks.go`, `rollback.go`, `deploy_support.go`, `adopt.go`, `stacks_nested.go`, `store.go` | 6 |
| 6 | Describe shapes, `NoEcho`, parameters and defaults, pagination, sizes and list lengths | `cloudformation/stacks.go`, `store.go`, `validate.go`, `internal/cfn/parameters.go`, `intrinsics.go` | 3.5 |
| 7 | Change sets (type, duplicate, members, shape, ids, execute, stale) | `cloudformation/changesets.go`, `internal/cfn/resources.go` (`fingerprint`) | 2 |
| 8 | Section 5 sweep, parity cases, regenerate the audit cases | `cloudformation/rejection_parity_test.go`, `service_test.go`, `validate.go`, `testdata/cases_cloudformation.json` | 1 |
| 9 | Conformance scenarios and claims | `conformance/tests/test_cloudformation.py`, the claims file from foundation B1 | 1.5 |
| 10 | Console, in the 6a build list's order | `internal/console/templates/cfn.html`, `create.html` (`cfn_create`), `handlers_cfn.go`, `client_cfn.go`, `client_cfn_export.go`, `static/editor.js`, `copy.go`, `kit.html` | 7 |
| 11 | Browser tests | `e2e/tests/cloudformation.spec.ts`, `cfn-starter.spec.ts` | 1 |
| 12 | IAM, control plane and per call (D6) | `cloudformation/service.go`, `internal/provision/client.go` | 2 |
| 13 | Ledger, changelog, status; mutation baselines (background) | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` | 0.5 |

The days sum to **37** (16a: steps 1 to 9, 25.5; 16b: steps 10 and 11, 9; 16c:
steps 12 and 13, 2.5), the figure in section 1. The README's 11 and the original
"five or more" are both low by the SQS ratio, which is the point of the review.
A reasonable range is 33 to 42: the property table (step 3) is the large
uncertainty, because 56 kinds have never been read against the resource
reference. *(Added 2026-10-11.)*

Steps 3 and 4 change `internal/provision`, which `doze-aws apply` also uses.
`apply` converges onto what exists on purpose; a stack must not. Where the two
differ (overwriting a parameter's value, adopting a taken name) the difference
is an argument to `Apply`, not a second code path.

**Prior art.** AWS's own resource and property reference is the source for the
property table: each type's page lists its properties, which are create-only
("Update requires: Replacement"), and its `Ref` and `Fn::GetAtt` values, which
is exactly the three columns this stage has to fill. The AWS CLI's `deploy`
command is the consumer of the empty change set's reason and of "No updates are
to be performed", so its behaviour is the test for both. LocalStack orders
CloudFormation resources by dependency, as the SQS document notes; that is the
general answer to CFN-22.
